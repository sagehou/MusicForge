package forge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type App struct {
	cfg       Runtime
	db        *sql.DB
	logger    *slog.Logger
	version   string
	assets    fs.FS
	lock      *os.File
	bootstrap string
	wg        sync.WaitGroup
	// ponytail: serialize library mutations; ffmpeg runs outside this lock.
	files         sync.Mutex
	recovered     bool
	authMu        sync.Mutex
	loginFailures map[string]loginLimit
	jobsMu        sync.Mutex
	activeJobs    map[int64]context.CancelFunc
	sourceSlots   chan struct{}
	storageMu     sync.Mutex
	storageCheck  bool
	storageSource string
	storageOutput string
	storageAt     time.Time
	storageError  error
	ioContext     context.Context
	ioCancel      context.CancelFunc
}

type loginLimit struct {
	Count int
	Since time.Time
}
type deferred struct {
	reason  string
	seconds int
}

func (e *deferred) Error() string            { return e.reason }
func later(reason string, seconds int) error { return &deferred{reason, seconds} }

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func digest(value string) string { h := sha256.Sum256([]byte(value)); return hex.EncodeToString(h[:]) }

func New(cfg Runtime, logger *slog.Logger, version string, assets fs.FS) (*App, error) {
	if err := os.MkdirAll(cfg.ConfigDir, 0700); err != nil {
		return nil, err
	}
	// Existing host bind mounts may be 0755; protect SQLite WAL and stored secrets too.
	if err := os.Chmod(cfg.ConfigDir, 0700); err != nil {
		return nil, fmt.Errorf("secure configuration directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(cfg.ConfigDir, "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("another MusicForge process owns /config")
	}
	db, err := openDB(filepath.Join(cfg.ConfigDir, "musicforge.db"))
	if err != nil {
		lock.Close()
		return nil, err
	}
	a := &App{cfg: cfg, db: db, logger: logger, version: version, assets: assets, lock: lock, loginFailures: map[string]loginLimit{}, activeJobs: map[int64]context.CancelFunc{}, sourceSlots: make(chan struct{}, 20)}
	a.ioContext, a.ioCancel = context.WithCancel(context.Background())
	fail := func(err error) (*App, error) { a.Close(); return nil, err }
	if _, err = a.settings(); err == sql.ErrNoRows {
		err = a.saveSettings(DefaultSettings())
	}
	if err != nil {
		return fail(err)
	}
	if _, err = db.Exec("INSERT OR IGNORE INTO meta(key,value) VALUES('instance',?)", randomToken()); err != nil {
		return fail(err)
	}
	if _, err = db.Exec("UPDATE jobs SET state='pending',not_before=0,updated=? WHERE state='running'", time.Now().Unix()); err != nil {
		return fail(err)
	}
	// An older compatible release can explicitly retry stopped primitive jobs.
	// Its pending job state is authoritative over the optional UI annotation.
	if _, err = db.Exec(`DELETE FROM meta WHERE key LIKE 'task-control:%' AND value='stopped' AND EXISTS (
	 SELECT 1 FROM jobs j LEFT JOIN meta m ON m.key='task-member:'||j.id
	 WHERE coalesce(CAST(m.value AS INTEGER),j.id)=CAST(substr(meta.key,14) AS INTEGER)
	 AND j.state IN ('pending','running') AND j.log<>'Stopped by administrator')`); err != nil {
		return fail(err)
	}
	var admins int
	if err = db.QueryRow("SELECT count(*) FROM admin").Scan(&admins); err != nil {
		return fail(err)
	}
	if admins == 0 {
		a.bootstrap = randomToken()
		logger.Warn("first-run setup code", "setup_code", a.bootstrap)
	}
	if err = os.Chmod(filepath.Join(cfg.ConfigDir, "musicforge.db"), 0600); err != nil {
		return fail(err)
	}
	return a, nil
}
func (a *App) Close() {
	if a.ioCancel != nil {
		a.ioCancel()
	}
	if a.db != nil {
		a.db.Close()
	}
	if a.lock != nil {
		a.lock.Close()
	}
}
func (a *App) Wait() { a.wg.Wait() }
func (a *App) Start(ctx context.Context) {
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.scheduler(ctx) }()
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.worker(ctx, false, 0) }()
	for i := 0; i < 16; i++ {
		a.wg.Add(1)
		go func(slot int) { defer a.wg.Done(); a.worker(ctx, true, slot) }(i)
	}
}

func pause(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (a *App) scheduler(ctx context.Context) {
	var lastScan time.Time
	for ctx.Err() == nil {
		s, err := a.settings()
		if err != nil {
			a.logger.Error("settings read failed", "error", err)
		} else if s.Enabled {
			if lastScan.IsZero() || time.Since(lastScan) >= time.Duration(s.ScanMinutes)*time.Minute {
				if _, err = a.enqueue("scan", "scan:periodic", ScanRequest{}, false); err == nil {
					lastScan = time.Now()
				}
			}
			var dirty, active int
			var latest int64
			if err = a.db.QueryRow("SELECT count(*),coalesce(max(updated),0) FROM dirty_dirs").Scan(&dirty, &latest); err == nil && dirty > 0 && time.Since(time.Unix(0, latest)) >= 5*time.Second {
				_ = a.db.QueryRow("SELECT count(*) FROM jobs WHERE state IN ('pending','running') AND kind IN ('scan','convert','move','delete')").Scan(&active)
				if active == 0 && s.NavURL != "" {
					_, _ = a.enqueue("refresh", "nav:refresh", map[string]bool{"manual": false}, false)
				}
			}
		}
		_, _ = a.db.Exec("DELETE FROM sessions WHERE expires<?", time.Now().Unix())
		_, _ = a.db.Exec("DELETE FROM oidc_flows WHERE expires<?", time.Now().Unix())
		if !pause(ctx, 5*time.Second) {
			return
		}
	}
}

func (a *App) worker(ctx context.Context, conversion bool, slot int) {
	for ctx.Err() == nil {
		s, err := a.settings()
		if err != nil || !s.Enabled || (conversion && slot >= s.Concurrency) {
			if !pause(ctx, time.Second) {
				return
			}
			continue
		}
		a.jobsMu.Lock()
		j, err := a.claim(conversion)
		jobCtx, cancel := context.WithCancel(ctx)
		if err == nil {
			a.activeJobs[j.ID] = cancel
		}
		a.jobsMu.Unlock()
		if err == sql.ErrNoRows {
			cancel()
			if !pause(ctx, time.Second) {
				return
			}
			continue
		}
		if err != nil {
			cancel()
			a.logger.Error("job claim failed", "error", err)
			if !pause(ctx, time.Second) {
				return
			}
			continue
		}
		a.logger.Info("job started", "task", a.taskID(j.ID), "job", j.ID, "kind", j.Kind)
		err = a.execute(jobCtx, j)
		interrupted := jobCtx.Err() != nil
		cancel()
		state := "success"
		attempts := j.Attempts
		wait := 0
		message := ""
		if interrupted && err == nil {
			err = context.Canceled
		}
		if err != nil {
			message = err.Error()
			var d *deferred
			if interrupted {
				state = "pending"
				message = "Interrupted; resumed from the beginning without consuming an attempt"
			} else if errors.As(err, &d) {
				state = "pending"
				wait = d.seconds
			} else {
				attempts++
				state = "failed"
				if attempts < 3 {
					state = "pending"
					wait = 10 * attempts * attempts
				}
			}
		}
		progress := 1.0
		if state != "success" {
			progress = 0
		}
		for {
			a.jobsMu.Lock()
			updateErr := a.completeJob(j, state, attempts, progress, message, wait)
			if updateErr == nil {
				delete(a.activeJobs, j.ID)
			}
			a.jobsMu.Unlock()
			if updateErr == nil {
				break
			}
			a.logger.Error("job state persistence failed", "job", j.ID, "error", updateErr)
			if !pause(ctx, time.Second) {
				return
			}
		}
		if actual, readErr := readJob(a.db.QueryRow("SELECT "+jobCols+" FROM jobs WHERE id=?", j.ID)); readErr == nil {
			state, attempts, message = actual.State, actual.Attempts, actual.Log
		}
		a.logger.Info("job finished", "task", a.taskID(j.ID), "job", j.ID, "kind", j.Kind, "state", state, "attempts", attempts, "detail", message)
	}
}

func (a *App) execute(ctx context.Context, j Job) error {
	switch j.Kind {
	case "scan":
		var r ScanRequest
		if err := json.Unmarshal(j.Args, &r); err != nil {
			return err
		}
		return a.scan(context.WithValue(ctx, taskJobKey{}, j.ID), r)
	case "convert", "move":
		var r BuildRequest
		if err := json.Unmarshal(j.Args, &r); err != nil {
			return err
		}
		return a.build(ctx, j, r)
	case "delete":
		var r struct {
			IDs []int64 `json:"ids"`
		}
		if err := json.Unmarshal(j.Args, &r); err != nil {
			return err
		}
		return a.deleteExpiredContext(ctx, r.IDs)
	case "upgrade":
		var r UpgradeRequest
		if err := json.Unmarshal(j.Args, &r); err != nil {
			return err
		}
		return a.finishUpgrade(ctx, r)
	case "refresh":
		return a.refresh(ctx)
	default:
		return fmt.Errorf("unknown job kind %q", j.Kind)
	}
}

func (a *App) meta(key string) (string, error) {
	var value string
	err := a.db.QueryRow("SELECT value FROM meta WHERE key=?", key).Scan(&value)
	return value, err
}
func (a *App) setMeta(key, value string) error {
	_, err := a.db.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}
func (a *App) dirty(rel string) error {
	dir := filepath.ToSlash(filepath.Dir(rel))
	_, err := a.db.Exec(markDirtySQL, dir, time.Now().UnixNano())
	return err
}

func (a *App) storageContext(ctx context.Context, s Settings) error {
	source, err := a.sourceStat(ctx, s.Source, ".")
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return later("Source storage offline: "+err.Error(), 30)
	}
	if !source.IsDir() || source.Identity == "" {
		return later("Source storage offline: library root is not an identifiable directory", 30)
	}
	expected, err := a.meta("source_root")
	if err != nil || source.Identity != expected {
		return later("Source mount changed; verify the mount and save Settings to acknowledge it", 30)
	}
	id, err := a.meta("instance")
	if err != nil {
		return err
	}
	output, err := os.ReadFile(filepath.Join(s.Output, ".musicforge"))
	if err != nil || string(output) != id {
		return later("Output storage offline or ownership marker missing", 30)
	}
	path, err := safePath(s.Output, ".")
	if err != nil {
		return later("Output storage offline: "+err.Error(), 30)
	}
	f, err := os.CreateTemp(path, ".musicforge-access-")
	if err != nil {
		return later("Output storage is not writable: "+err.Error(), 30)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}

// Dashboard polling reads the last background check, never the remote mount.
func (a *App) storageStatus(s Settings) (bool, string, bool) {
	a.storageMu.Lock()
	defer a.storageMu.Unlock()
	if a.storageSource != s.Source || a.storageOutput != s.Output {
		a.storageAt = time.Time{}
	}
	if !a.storageCheck && (a.storageAt.IsZero() || time.Since(a.storageAt) >= 5*time.Second) {
		a.storageCheck = true
		go func() {
			err := a.storageContext(a.ioContext, s)
			a.storageMu.Lock()
			defer a.storageMu.Unlock()
			a.storageSource, a.storageOutput = s.Source, s.Output
			a.storageAt, a.storageError, a.storageCheck = time.Now(), err, false
		}()
	}
	if a.storageAt.IsZero() || a.storageCheck && time.Since(a.storageAt) > 15*time.Second {
		return false, "Checking library storage", true
	}
	if a.storageError != nil {
		return false, a.storageError.Error(), false
	}
	return true, "", false
}

func (a *App) initializeStorage(s Settings) error {
	return a.initializeStorageContext(context.Background(), s)
}
func (a *App) initializeStorageContext(ctx context.Context, s Settings) error {
	source, err := a.sourceStat(ctx, s.Source, ".")
	if err != nil {
		return err
	}
	if !source.IsDir() || source.Identity == "" {
		return errors.New("library root is not an identifiable directory")
	}
	sourceReal := source.Path
	outputReal, err := filepath.EvalSymlinks(s.Output)
	if err != nil {
		return err
	}
	if containsPath(sourceReal, outputReal) || containsPath(outputReal, sourceReal) {
		return errors.New("resolved library roots overlap")
	}
	configReal, err := filepath.EvalSymlinks(a.cfg.ConfigDir)
	if err != nil {
		return err
	}
	if containsPath(sourceReal, configReal) || containsPath(outputReal, configReal) || containsPath(configReal, outputReal) || containsPath(configReal, sourceReal) {
		return errors.New("library and configuration paths must not overlap")
	}
	id, err := a.meta("instance")
	if err != nil {
		return err
	}
	marker := filepath.Join(s.Output, ".musicforge")
	b, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		entries, err := os.ReadDir(s.Output)
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return errors.New("initial output directory must be empty")
		}
		if err = os.WriteFile(marker, []byte(id), 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if string(b) != id {
		return errors.New("output directory belongs to another MusicForge instance")
	}
	return a.setMeta("source_root", source.Identity)
}

func (a *App) ensureRecovery(ctx context.Context, s Settings) error {
	if a.recovered {
		return nil
	}
	if err := a.recoverFiles(ctx, s); err != nil {
		return err
	}
	a.recovered = true
	return nil
}
