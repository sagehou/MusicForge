package forge

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime"
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

const repairMaxBytes = 32 * 1024 * 1024
const repairMaxPreviews = 8

// Repair previews are disposable, not published artifacts or schema-2 jobs.
type RepairPreview struct {
	State           string  `json:"state"`
	Filename        string  `json:"filename,omitempty"`
	DownloadURL     string  `json:"download_url,omitempty"`
	DecodedSamples  int64   `json:"decoded_samples,omitempty"`
	DeclaredSamples int64   `json:"declared_samples,omitempty"`
	PCMVerified     bool    `json:"pcm_verified"`
	Duration        float64 `json:"duration,omitempty"`
	Diagnostics     string  `json:"diagnostics,omitempty"`
}

type repairEntry struct {
	RepairPreview
	hash    string
	rel     string
	path    string
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
	report.Log = fmt.Sprintf("Possibly damaged: best-effort repair preview only\nPCM bytes: %d of %d\nPCM MD5: expected %x, got %x", n, p.byteCount(), p.MD5, h.Sum(nil))
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

func (a *App) initializeRepairs() error {
	a.repairDir = filepath.Join(a.cfg.ConfigDir, "repair-previews")
	if err := os.Mkdir(a.repairDir, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(a.repairDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("repair-previews must be a local directory, without a symlink")
	}
	if err = os.Chmod(a.repairDir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(a.repairDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".musicforge-repair-") && !entry.IsDir() {
			if err = os.Remove(filepath.Join(a.repairDir, entry.Name())); err != nil {
				return err
			}
		}
	}
	a.repairs = make(map[int64]*repairEntry)
	return nil
}

func canRepair(source Source) bool {
	return source.Present && source.Hash != "" && strings.EqualFold(filepath.Ext(source.Rel), ".flac") && failureCategory(source.Error) == "decode"
}

func (a *App) repairSnapshot(source Source) *RepairPreview {
	a.repairMu.Lock()
	defer a.repairMu.Unlock()
	entry := a.repairs[source.ID]
	if entry == nil || !source.Present || entry.hash != source.Hash || entry.rel != source.Rel {
		return nil
	}
	snapshot := entry.RepairPreview
	return &snapshot
}

// jobsMu protects the capacity check together with the subsequent queue claim.
func (a *App) claimWithRepairCapacity(conversion bool, concurrency int) (Job, error) {
	a.repairMu.Lock()
	reserved := a.repairRunning
	a.repairMu.Unlock()
	if conversion && reserved {
		var running int
		if err := a.reads.QueryRow("SELECT count(*) FROM jobs WHERE state='running' AND (kind='convert' OR dedup LIKE 'scan:prepare:%')").Scan(&running); err != nil {
			return Job{}, err
		}
		if running >= concurrency-1 {
			return Job{}, sql.ErrNoRows
		}
	}
	return a.claim(conversion)
}

func (a *App) startRepair(source Source, settings Settings) (*RepairPreview, error) {
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	a.repairMu.Lock()
	defer a.repairMu.Unlock()
	if err := a.ioContext.Err(); err != nil {
		return nil, err
	}
	if a.repairRunning {
		return nil, errors.New("A repair preview is already running")
	}
	var running int
	if err := a.reads.QueryRow("SELECT count(*) FROM jobs WHERE state='running' AND (kind='convert' OR dedup LIKE 'scan:prepare:%')").Scan(&running); err != nil {
		return nil, err
	}
	if running >= settings.Concurrency {
		return nil, errors.New("Conversion workers are busy; retry the repair shortly")
	}
	if old := a.repairs[source.ID]; old != nil {
		if old.State == "ready" && old.hash == source.Hash && old.rel == source.Rel {
			snapshot := old.RepairPreview
			return &snapshot, nil
		}
		if old.path != "" {
			if err := os.Remove(old.path); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
		}
		delete(a.repairs, source.ID)
	}
	if len(a.repairs) >= repairMaxPreviews {
		var oldest int64
		for id, entry := range a.repairs {
			if oldest == 0 || entry.created.Before(a.repairs[oldest].created) {
				oldest = id
			}
		}
		if path := a.repairs[oldest].path; path != "" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
		}
		delete(a.repairs, oldest)
	}
	ctx, cancel := context.WithCancel(a.ioContext)
	entry := &repairEntry{RepairPreview: RepairPreview{State: "running"}, hash: source.Hash, rel: source.Rel, created: time.Now(), cancel: cancel}
	a.repairs[source.ID] = entry
	a.repairRunning = true
	a.repairWG.Add(1)
	go func() {
		defer a.repairWG.Done()
		defer cancel()
		result, path, err := a.makeRepair(ctx, source, settings)
		a.repairMu.Lock()
		defer a.repairMu.Unlock()
		a.repairRunning = false
		if a.repairs[source.ID] != entry || ctx.Err() != nil {
			if path != "" {
				_ = os.Remove(path)
			}
			return
		}
		if err != nil {
			entry.State, entry.Diagnostics = "failed", err.Error()
			a.logger.Warn("repair preview failed", "source", source.Rel, "input_sha256", source.Hash, "detail", err.Error())
		} else {
			entry.RepairPreview, entry.path = result, path
			a.logger.Warn("possibly damaged repair preview ready", "source", source.Rel, "input_sha256", source.Hash, "decoded_samples", result.DecodedSamples, "declared_samples", result.DeclaredSamples, "pcm_verified", result.PCMVerified)
		}
	}()
	snapshot := entry.RepairPreview
	return &snapshot, nil
}

func (a *App) removeRepair(id int64) error {
	a.repairMu.Lock()
	defer a.repairMu.Unlock()
	if entry := a.repairs[id]; entry != nil {
		if entry.path != "" {
			if err := os.Remove(entry.path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		entry.cancel()
		delete(a.repairs, id)
	}
	return nil
}

func (a *App) makeRepair(ctx context.Context, source Source, settings Settings) (RepairPreview, string, error) {
	fail := func(err error) (RepairPreview, string, error) { return RepairPreview{}, "", err }
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
	file, err := os.CreateTemp(a.repairDir, ".musicforge-repair-*.part")
	if err != nil {
		return fail(err)
	}
	path := file.Name()
	_ = file.Close()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()
	args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-xerror"}, pcm.inputArgs()...)
	args = append(args, "-i", "pipe:0", "-map", "0:a:0", "-vn", "-sn", "-dn")
	keys := make([]string, 0, len(source.Metadata))
	for key := range source.Metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-metadata", key+"="+source.Metadata[key])
	}
	args = append(args, "-metadata", "MUSICFORGE_RECOVERY=possibly_damaged")
	args = append(args, settings.Encoding.Args()...)
	args = append(args, "-fs", strconv.Itoa(repairMaxBytes), path)
	var report repairPCM
	if err = a.encodeMediaAttempt(ctx, 0, source.Duration, Activity{Phase: "convert", Path: source.Rel}, args, staged.Path, pcm, &report); err != nil {
		return fail(err)
	}
	outputInfo, err := os.Stat(path)
	if err != nil {
		return fail(err)
	}
	if outputInfo.Size() < 1 || outputInfo.Size() > repairMaxBytes {
		return fail(errors.New("Repair preview exceeds the download size limit"))
	}
	samples := report.Bytes / int64(pcm.Channels*(pcm.Bits/8))
	duration := float64(samples) / float64(pcm.Rate)
	if err = a.validateArtifact(ctx, path, settings.Encoding, duration); err != nil {
		return fail(fmt.Errorf("Repair output validation failed: %w", err))
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
	keep = true
	filename := strings.TrimSuffix(filepath.Base(source.Rel), filepath.Ext(source.Rel)) + ".possibly-damaged." + settings.Encoding.Codec
	return RepairPreview{State: "ready", Filename: filename, DownloadURL: fmt.Sprintf("/api/library/%d/repair/download", source.ID), DecodedSamples: samples, DeclaredSamples: pcm.Samples, PCMVerified: report.Verified, Duration: duration, Diagnostics: report.Log}, path, nil
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
	var body struct { Hash string `json:"hash"` }
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
		apiError(w, 409, errors.New("Only failed native FLAC tracks can be repaired"))
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
	preview, err := a.startRepair(source, settings)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	respond(w, 202, preview)
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

func (a *App) downloadRepair(w http.ResponseWriter, r *http.Request) {
	id, ok := repairID(w, r)
	if !ok {
		return
	}
	source, err := a.source(id)
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, 404, errors.New("Repair preview is unavailable; generate it again"))
		return
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	a.repairMu.Lock()
	entry := a.repairs[id]
	if entry == nil || entry.State != "ready" || !source.Present || entry.hash != source.Hash || entry.rel != source.Rel {
		a.repairMu.Unlock()
		apiError(w, 404, errors.New("Repair preview is unavailable; generate it again"))
		return
	}
	file, err := os.Open(entry.path)
	filename := entry.Filename
	a.repairMu.Unlock()
	if err != nil {
		apiError(w, 404, errors.New("Repair preview is unavailable; generate it again"))
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > repairMaxBytes {
		apiError(w, 404, errors.New("Repair preview is unavailable; generate it again"))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

// Keep repair state separate from the schema-2 queue and file-recovery journals.
type repairState struct {
	repairMu  sync.Mutex
	repairWG  sync.WaitGroup
	repairDir string
	repairs   map[int64]*repairEntry
	repairRunning bool
}
