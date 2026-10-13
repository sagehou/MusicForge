package forge

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const repairMaxFailures = 8

type RepairStatus struct {
	State           string  `json:"state"`
	DecodedSamples  int64   `json:"decoded_samples,omitempty"`
	DeclaredSamples int64   `json:"declared_samples,omitempty"`
	PCMVerified     bool    `json:"pcm_verified"`
	Duration        float64 `json:"duration,omitempty"`
	Diagnostics     string  `json:"diagnostics,omitempty"`
}

type repairEntry struct {
	RepairStatus
	hash    string
	rel     string
	created time.Time
	cancel  context.CancelFunc
}

type repairPCM struct {
	Bytes    int64
	Verified bool
	Log      string
}

func (p *flacPCM) copyRepairPCM(dst io.Writer, src io.Reader, report *repairPCM) error {
	h := md5.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(src, p.byteCount()))
	if err != nil {
		return fmt.Errorf("copy repair PCM: %w", err)
	}
	if n == 0 || n%int64(p.Channels*(p.Bits/8)) != 0 {
		return errors.New("repair decoder produced empty or incomplete PCM samples")
	}
	report.Bytes = n
	report.Verified = n == p.byteCount() && bytes.Equal(h.Sum(nil), p.MD5[:])
	report.Log = fmt.Sprintf("Possibly damaged: imported using best-effort decoding\nPCM bytes: %d of %d\nPCM MD5: expected %x, got %x", n, p.byteCount(), p.MD5, h.Sum(nil))
	var extra [1]byte
	if n, err := io.ReadFull(src, extra[:]); n != 0 {
		return errors.New("repair decoder exceeded the declared sample count")
	} else if err != io.EOF {
		return fmt.Errorf("read repair PCM boundary: %w", err)
	}
	return nil
}

func repairDecoderExit(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 1
}

func canRepair(source Source) bool {
	return source.Present && !source.OutputPresent && source.Hash != "" && strings.EqualFold(filepath.Ext(source.Rel), ".flac") && failureCategory(source.Error) == "decode"
}

func (a *App) repairSnapshot(source Source) *RepairStatus {
	a.repairMu.Lock()
	defer a.repairMu.Unlock()
	entry := a.repairs[source.ID]
	if entry == nil || !source.Present || entry.hash != source.Hash || entry.rel != source.Rel {
		return nil
	}
	snapshot := entry.RepairStatus
	return &snapshot
}

func repairArtifactKey(id int64, hash string) string {
	return "repair-artifact:" + strconv.FormatInt(id, 10) + ":" + hash
}

// Optional annotations follow the actual owned audio hash, including after moves
// and older schema-2 writers. Published journals and signatures stay unchanged.
func (a *App) annotateRepairs(ctx context.Context, sources []Source) error {
	if len(sources) == 0 {
		return nil
	}
	byID := make(map[int64]*Source, len(sources))
	for i := range sources {
		byID[sources[i].ID] = &sources[i]
	}
	query := "SELECT m.source_id,m.path,r.value FROM managed m JOIN meta r ON r.key='repair-artifact:'||m.source_id||':'||m.signature WHERE m.kind='audio'"
	var args []any
	if len(sources) == 1 {
		query += " AND m.source_id=?"
		args = append(args, sources[0].ID)
	}
	rows, err := a.reads.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var path, raw string
		if err = rows.Scan(&id, &path, &raw); err != nil {
			return err
		}
		source := byID[id]
		if source == nil || !source.OutputPresent || source.Output != path {
			continue
		}
		source.PossiblyDamaged = true
		if source.Repair == nil {
			var status RepairStatus
			if err := json.Unmarshal([]byte(raw), &status); err != nil {
				status.Diagnostics = "Repair diagnostics unavailable: " + err.Error()
			}
			status.State = "imported"
			source.Repair = &status
		}
	}
	return rows.Err()
}

// Called under jobsMu; cancelled jobs retain their slot until execution exits.
func (a *App) runningConversions() (int, error) {
	active := []string{"-1"}
	for id := range a.activeJobs {
		active = append(active, strconv.FormatInt(id, 10))
	}
	var count int
	err := a.reads.QueryRow("SELECT count(*) FROM jobs WHERE (state='running' OR id IN (" + strings.Join(active, ",") + ")) AND (kind='convert' OR dedup LIKE 'scan:prepare:%')").Scan(&count)
	return count, err
}

// jobsMu protects the capacity check together with the subsequent queue claim.
func (a *App) claimWithRepairCapacity(conversion bool, concurrency int) (Job, error) {
	a.repairMu.Lock()
	reserved := a.repairRunning
	a.repairMu.Unlock()
	if conversion && reserved {
		running, err := a.runningConversions()
		if err != nil {
			return Job{}, err
		}
		if running >= concurrency-1 {
			return Job{}, sql.ErrNoRows
		}
	}
	return a.claim(conversion)
}

func (a *App) startRepair(source Source, settings Settings) (*RepairStatus, error) {
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	a.repairMu.Lock()
	defer a.repairMu.Unlock()
	if err := a.ioContext.Err(); err != nil {
		return nil, err
	}
	if a.repairRunning {
		return nil, errors.New("A best-effort repair is already running")
	}
	running, err := a.runningConversions()
	if err != nil {
		return nil, err
	}
	if running >= settings.Concurrency {
		return nil, errors.New("Conversion workers are busy; retry the repair shortly")
	}
	delete(a.repairs, source.ID)
	if len(a.repairs) >= repairMaxFailures {
		var oldest int64
		for id, entry := range a.repairs {
			if oldest == 0 || entry.created.Before(a.repairs[oldest].created) {
				oldest = id
			}
		}
		delete(a.repairs, oldest)
	}
	ctx, cancel := context.WithCancel(a.ioContext)
	entry := &repairEntry{RepairStatus: RepairStatus{State: "running"}, hash: source.Hash, rel: source.Rel, created: time.Now(), cancel: cancel}
	a.repairs[source.ID] = entry
	a.repairRunning = true
	a.repairWG.Add(1)
	go func() {
		defer a.repairWG.Done()
		defer cancel()
		result, err := a.makeRepair(ctx, source, settings)
		a.repairMu.Lock()
		defer a.repairMu.Unlock()
		a.repairRunning = false
		if a.repairs[source.ID] != entry || ctx.Err() != nil {
			delete(a.repairs, source.ID)
			return
		}
		if err != nil {
			entry.State, entry.Diagnostics = "failed", err.Error()
			a.logger.Warn("best-effort repair failed", "source", source.Rel, "input_sha256", source.Hash, "detail", err.Error())
		} else {
			delete(a.repairs, source.ID)
			a.logger.Warn("possibly damaged audio imported", "source", source.Rel, "input_sha256", source.Hash, "decoded_samples", result.DecodedSamples, "declared_samples", result.DeclaredSamples, "pcm_verified", result.PCMVerified)
		}
	}()
	snapshot := entry.RepairStatus
	return &snapshot, nil
}

func (a *App) removeRepair(id int64) error {
	a.repairMu.Lock()
	defer a.repairMu.Unlock()
	if entry := a.repairs[id]; entry != nil {
		entry.cancel()
		delete(a.repairs, id)
	}
	return nil
}

func (a *App) repairTargetAvailable(settings Settings, source Source, target string) error {
	for _, rel := range []string{source.Output, target} {
		if rel == "" {
			continue
		}
		path, err := safePath(settings.Output, rel)
		if err != nil {
			return err
		}
		if _, err = os.Lstat(path); err == nil {
			return errors.New("Existing output is preserved; best-effort repair cannot replace it")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return a.targetAvailable(settings, target, source.ID)
}

func (a *App) reserveRepairTemp(ctx context.Context, settings Settings, source Source, target string) (string, string, error) {
	if err := a.lockFiles(ctx); err != nil {
		return "", "", err
	}
	defer a.files.Unlock()
	if err := a.storageContext(ctx, settings); err != nil {
		return "", "", err
	}
	if err := a.ensureRecovery(ctx, settings); err != nil {
		return "", "", err
	}
	if err := a.recoverPromotions(ctx, settings); err != nil {
		return "", "", err
	}
	if err := a.repairTargetAvailable(settings, source, target); err != nil {
		return "", "", err
	}
	tempRel := filepath.Join(filepath.Dir(target), ".musicforge-"+randomToken()+".part")
	temp, err := safePath(settings.Output, tempRel)
	if err != nil {
		return "", "", err
	}
	if err = os.MkdirAll(filepath.Dir(temp), 0755); err != nil {
		return "", "", err
	}
	if _, err = a.db.Exec("INSERT INTO managed(path,source_id,kind) VALUES(?,?,'temp')", tempRel, source.ID); err != nil {
		return "", "", err
	}
	return tempRel, temp, nil
}

func (a *App) makeRepair(ctx context.Context, source Source, settings Settings) (RepairStatus, error) {
	fail := func(err error) (RepairStatus, error) { return RepairStatus{}, err }
	target := outputRel(source.Rel, settings.Encoding.Codec)
	tempRel, path, err := a.reserveRepairTemp(ctx, settings, source, target)
	if err != nil {
		return fail(err)
	}
	defer func() {
		_ = os.Remove(path)
		_, _ = a.db.Exec("DELETE FROM managed WHERE path=? AND kind='temp'", tempRel)
	}()
	info, err := a.sourceStat(ctx, settings.Source, source.Rel)
	if err != nil {
		return fail(err)
	}
	if !sameStat(info, source) {
		return fail(errors.New("Source changed; scan it again before repairing"))
	}
	staged, err := a.stageSource(ctx, settings, source, 0, Activity{Phase: "read", Path: source.Rel})
	if err != nil {
		return fail(err)
	}
	defer staged.Close()
	if staged.Hash != source.Hash {
		return fail(errors.New("Source changed; scan it again before repairing"))
	}
	pcm, err := readFLACPCM(staged.Path)
	if err != nil {
		return fail(err)
	}
	if pcm == nil {
		return fail(errors.New("Repair requires native FLAC with known samples and a PCM checksum"))
	}
	args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-n", "-xerror"}, pcm.inputArgs()...)
	args = append(args, "-i", "pipe:0", "-map", "0:a:0", "-vn", "-sn", "-dn")
	keys := make([]string, 0, len(source.Metadata))
	for key := range source.Metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-metadata", key+"="+source.Metadata[key])
	}
	args = append(args, settings.Encoding.Args()...)
	args = append(args, path)
	var report repairPCM
	if err = a.encodeMediaAttempt(ctx, 0, source.Duration, Activity{Phase: "convert", Path: source.Rel}, args, staged.Path, pcm, &report); err != nil {
		return fail(err)
	}
	samples := report.Bytes / int64(pcm.Channels*(pcm.Bits/8))
	duration := float64(samples) / float64(pcm.Rate)
	if err = a.validateArtifact(ctx, path, settings.Encoding, duration); err != nil {
		return fail(fmt.Errorf("Repair output validation failed: %w", err))
	}
	file, err := os.Open(path)
	if err != nil {
		return fail(err)
	}
	err = file.Sync()
	file.Close()
	if err != nil {
		return fail(err)
	}
	artifactHash, err := fileHash(ctx, path)
	if err != nil {
		return fail(err)
	}
	if err = a.lockFiles(ctx); err != nil {
		return fail(err)
	}
	defer a.files.Unlock()
	currentSettings, err := a.settings()
	if err != nil {
		return fail(err)
	}
	if !currentSettings.Enabled || currentSettings.Encoding.Fingerprint() != settings.Encoding.Fingerprint() {
		return fail(errors.New("Settings changed; retry the repair with the current encoding"))
	}
	if err = a.storageContext(ctx, settings); err != nil {
		return fail(err)
	}
	info, err = a.sourceStat(ctx, settings.Source, source.Rel)
	if err != nil || !sameStat(info, source) {
		return fail(errors.New("Source changed; scan it again before repairing"))
	}
	current, err := a.source(source.ID)
	if err != nil {
		return fail(err)
	}
	if !current.Present || current.Hash != source.Hash || current.Rel != source.Rel {
		return fail(errors.New("Source changed; scan it again before repairing"))
	}
	if err = ctx.Err(); err != nil {
		return fail(err)
	}
	if err = a.repairTargetAvailable(settings, current, target); err != nil {
		return fail(err)
	}
	result := RepairStatus{State: "imported", DecodedSamples: samples, DeclaredSamples: pcm.Samples, PCMVerified: report.Verified, Duration: duration, Diagnostics: report.Log + "\nOriginal strict conversion failure:\n" + source.Error}
	p := promotion{source.ID, tempRel, target, current.Output, source.Hash, settings.Encoding.Fingerprint(), artifactHash}
	annotation, _ := json.Marshal(result)
	journal, _ := json.Marshal(p)
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", repairArtifactKey(source.ID, artifactHash), string(annotation)); err != nil {
		return fail(err)
	}
	if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "promotion:"+strconv.FormatInt(source.ID, 10), string(journal)); err != nil {
		return fail(err)
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	out, err := safePath(settings.Output, target)
	if err != nil {
		return fail(err)
	}
	if err = os.Rename(path, out); err != nil {
		return fail(err)
	}
	if err = syncDirectory(filepath.Dir(out)); err != nil {
		return fail(err)
	}
	if err = a.finalize(settings, p); err != nil {
		return fail(err)
	}
	prefix := filepath.Dir(source.Rel) + string(filepath.Separator)
	if filepath.Dir(source.Rel) == "." {
		prefix = ""
	}
	tracks, err := a.sourcesWhere(" WHERE present=1 AND error='' AND substr(rel,1,length(?))=? AND instr(substr(rel,length(?)+1),?)=0", prefix, prefix, prefix, string(filepath.Separator))
	if err == nil {
		err = a.artwork(context.WithValue(ctx, stagedSourceKey{}, staged), settings, filepath.Dir(source.Rel), tracks)
	}
	if err != nil {
		a.logger.Warn("repaired audio imported but artwork update failed", "source", source.Rel, "error", err)
	}
	return result, nil
}

func repairID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 400, errors.New("invalid source ID"))
		return 0, false
	}
	return id, true
}

func (a *App) requestRepair(w http.ResponseWriter, r *http.Request) {
	id, ok := repairID(w, r)
	if !ok {
		return
	}
	var body struct {
		Hash string `json:"hash"`
	}
	if !decode(w, r, &body) {
		return
	}
	source, err := a.source(id)
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, 404, errors.New("source not found"))
		return
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if body.Hash == "" || body.Hash != source.Hash {
		apiError(w, 409, errors.New("Source changed; scan it again before repairing"))
		return
	}
	if !canRepair(source) {
		apiError(w, 409, errors.New("Only failed native FLAC tracks without an existing output can be repaired"))
		return
	}
	settings, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if !settings.Enabled {
		apiError(w, 409, errors.New("library is disabled"))
		return
	}
	status, err := a.startRepair(source, settings)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	respond(w, 202, status)
}

func (a *App) deleteRepair(w http.ResponseWriter, r *http.Request) {
	id, ok := repairID(w, r)
	if !ok {
		return
	}
	if err := a.removeRepair(id); err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, map[string]bool{"removed": true})
}

// Only in-flight operations are transient. Imported warnings live in optional
// artifact annotations, not Settings, job arguments or promotion journals.
type repairState struct {
	repairMu      sync.Mutex
	repairWG      sync.WaitGroup
	repairs       map[int64]*repairEntry
	repairRunning bool
}
