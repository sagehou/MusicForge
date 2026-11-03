package forge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type loudnessState struct {
	loudnessMu     sync.Mutex
	loudnessWG     sync.WaitGroup
	loudnessCancel context.CancelFunc
	loudnessReport *loudnessReport
	loudnessBusy   atomic.Bool
}

type loudnessReport struct {
	State     string `json:"state"`
	Total     int    `json:"total"`
	Processed int    `json:"processed"`
	Updated   int    `json:"updated"`
	Skipped   int    `json:"skipped"`
	Failed    int    `json:"failed"`
	Current   string `json:"current,omitempty"`
	Error     string `json:"error,omitempty"`
}

type outputGain struct {
	Version   int    `json:"version"`
	Path      string `json:"path"`
	Signature string `json:"signature"`
	Track     string `json:"track"`
	Album     string `json:"album"`
	Checked   int64  `json:"checked"`
}

// Separate optional metadata leaves the published promotion journal intact.
// Lyric choices are rebound only after the new audio is durable. Before-rename
// crashes retain the old choices; after-rename crashes complete on recovery.
type outputGainJournal struct {
	ID          int64      `json:"id"`
	Previous    string     `json:"previous"`
	Gain        outputGain `json:"gain"`
	SourceHash  string     `json:"source_hash,omitempty"`
	SourceError string     `json:"source_error,omitempty"`
}

func outputGainFor(path, signature string, tags map[string]string) outputGain {
	gain := outputGain{Version: 1, Path: path, Signature: signature, Checked: time.Now().Unix()}
	if filepath.Ext(path) == ".opus" {
		gain.Track, gain.Album = tags["r128_track_gain"], tags["r128_album_gain"]
	} else {
		gain.Track, gain.Album = tags["replaygain_track_gain"], tags["replaygain_album_gain"]
	}
	return gain
}

// Caller holds files. Only signatures are rebound: check time, manual provider,
// instrumental identity, ownership markers and LRC bytes keep their meanings.
func (a *App) recoverOutputGains(ctx context.Context) error {
	rows, err := a.reads.QueryContext(ctx, "SELECT key,value FROM meta WHERE key GLOB 'output-gain-pending:*'")
	if err != nil {
		return err
	}
	type entry struct{ key, raw string }
	var entries []entry
	for rows.Next() {
		var value entry
		if err = rows.Scan(&value.key, &value.raw); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, value := range entries {
		var pending outputGainJournal
		if err = json.Unmarshal([]byte(value.raw), &pending); err != nil {
			return err
		}
		if pending.ID < 1 || pending.Gain.Version != 1 || len(pending.Gain.Signature) != 64 || len(pending.Previous) != 64 {
			return errors.New("Invalid pending loudness metadata")
		}
		var signature string
		err = a.reads.QueryRowContext(ctx, "SELECT signature FROM managed WHERE source_id=? AND path=? AND kind='audio'", pending.ID, pending.Gain.Path).Scan(&signature)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		tx, err := a.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if signature == pending.Gain.Signature {
			raw, _ := json.Marshal(pending.Gain)
			_, err = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "output-gain:"+strconv.FormatInt(pending.ID, 10), string(raw))
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE meta SET value=json_set(value,'$.signature',?) WHERE key=? AND json_valid(value) AND json_extract(value,'$.signature')=? AND json_extract(value,'$.output')=?`, signature, "lyrics-status:"+strconv.FormatInt(pending.ID, 10), pending.Previous, pending.Gain.Path)
			}
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE meta SET value=json_set(value,'$.audio_signature',?) WHERE key=? AND json_valid(value) AND json_extract(value,'$.audio_signature')=? AND json_extract(value,'$.output')=?`, signature, lyricsScriptPrefix+strconv.FormatInt(pending.ID, 10), pending.Previous, pending.Gain.Path)
			}
			if err == nil {
				_, err = tx.ExecContext(ctx, `INSERT INTO meta(key,value) SELECT ?,value FROM meta WHERE key=? ON CONFLICT(key) DO UPDATE SET value=excluded.value`, repairArtifactKey(pending.ID, signature), repairArtifactKey(pending.ID, pending.Previous))
			}
			if err == nil && pending.SourceError != "" {
				_, err = tx.ExecContext(ctx, "UPDATE sources SET error=? WHERE id=? AND hash=? AND output=? AND error=''", pending.SourceError, pending.ID, pending.SourceHash, pending.Gain.Path)
			}
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM meta WHERE key=?", value.key)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return a.recoverOutputTagEdits(ctx)
}

type loudnessMeasurement struct{ Integrated, Peak float64 }
type measuredOutput struct {
	Source          Source
	Settings        Settings
	Signature       string
	Tags            map[string]string
	Duration        float64
	Loudness        loudnessMeasurement
	ResultSignature *string
}

var integratedLoudness = regexp.MustCompile(`(?m)^\s*I:\s+(-?[0-9]+(?:\.[0-9]+)?)\s+LUFS`)
var samplePeak = regexp.MustCompile(`(?m)^\s*Peak:\s+(-?[0-9]+(?:\.[0-9]+)?)\s+dBFS`)

func parseLoudness(log string) (loudnessMeasurement, error) {
	var value loudnessMeasurement
	i, p := integratedLoudness.FindAllStringSubmatch(log, -1), samplePeak.FindAllStringSubmatch(log, -1)
	if len(i) == 0 || len(p) == 0 {
		return value, errors.New("Audio has no measurable integrated loudness or sample peak")
	}
	value.Integrated, _ = strconv.ParseFloat(i[len(i)-1][1], 64)
	value.Peak, _ = strconv.ParseFloat(p[len(p)-1][1], 64)
	if value.Integrated <= -70 || value.Integrated > 10 || value.Peak > 10 {
		return value, errors.New("Audio has no measurable integrated loudness or sample peak")
	}
	return value, nil
}

func gainTags(codec string, track, album loudnessMeasurement) map[string]string {
	if codec == "opus" {
		return map[string]string{"r128_track_gain": strconv.FormatInt(int64(math.Round((-23-track.Integrated)*256)), 10),
			"r128_album_gain": strconv.FormatInt(int64(math.Round((-23-album.Integrated)*256)), 10)}
	}
	return map[string]string{"replaygain_track_gain": fmt.Sprintf("%.2f dB", -18-track.Integrated),
		"replaygain_album_gain": fmt.Sprintf("%.2f dB", -18-album.Integrated),
		"replaygain_track_peak": fmt.Sprintf("%.8f", math.Pow(10, track.Peak/20)),
		"replaygain_album_peak": fmt.Sprintf("%.8f", math.Pow(10, album.Peak/20))}
}

func albumKey(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) >= 3 {
		return strings.Join(parts[:2], "/")
	}
	return filepath.ToSlash(filepath.Dir(rel))
}

func (a *App) annotateOutputGain(ctx context.Context, sources []Source) error {
	byID := map[int64]*Source{}
	for i := range sources {
		byID[sources[i].ID] = &sources[i]
	}
	rows, err := a.reads.QueryContext(ctx, `SELECT m.source_id,g.value,m.signature FROM meta g JOIN managed m
	 ON g.key='output-gain:'||m.source_id AND m.kind='audio' WHERE g.key GLOB 'output-gain:*'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var raw, signature string
		var gain outputGain
		if err = rows.Scan(&id, &raw, &signature); err != nil {
			return err
		}
		source := byID[id]
		if source != nil && source.OutputPresent && json.Unmarshal([]byte(raw), &gain) == nil && gain.Version == 1 && gain.Path == source.Output && gain.Signature == signature && gain.Track != "" && gain.Album != "" {
			source.OutputGain = &gain
		}
	}
	return rows.Err()
}

// Copy a versioned open descriptor; concurrent atomic replacements cannot alter
// this snapshot. The shared staging budget bounds local disk use as well.
func (a *App) stageOutput(ctx context.Context, id int64, version string) (*stagedSource, measuredOutput, error) {
	var measured measuredOutput
	file, preview, signature, err := a.openAudition(ctx, id, version, false)
	if err != nil {
		return nil, measured, err
	}
	defer file.Close()
	if err = a.lockFiles(ctx); err != nil {
		return nil, measured, err
	}
	measured.Source, err = a.source(id)
	if err == nil {
		measured.Settings, err = a.settingsContext(ctx)
	}
	measured.Signature = signature
	a.files.Unlock()
	if err != nil {
		return nil, measured, err
	}
	if err = a.reserveStaging(ctx, preview.Size, 0, Activity{}); err != nil {
		return nil, measured, err
	}
	staged := &stagedSource{Path: filepath.Join(a.stagingDir, ".musicforge-"+randomToken()+".source"), size: preview.Size, app: a}
	output, err := os.OpenFile(staged.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		staged.Close()
		return nil, measured, err
	}
	hash := sha256.New()
	buffer := make([]byte, 128*1024)
	size := int64(0)
	for err == nil {
		if err = ctx.Err(); err != nil {
			break
		}
		var n int
		var readErr error
		n, readErr = file.Read(buffer)
		if n > 0 {
			_, err = output.Write(buffer[:n])
			_, _ = hash.Write(buffer[:n])
			size += int64(n)
			if size > preview.Size {
				err = errAuditionUnavailable
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			err = readErr
		}
	}
	err = errors.Join(err, output.Close())
	if err == nil && (size != preview.Size || hex.EncodeToString(hash.Sum(nil)) != signature) {
		err = errAuditionUnavailable
	}
	if err != nil {
		staged.Close()
		return nil, measured, err
	}
	return staged, measured, nil
}

func (a *App) analyzeAlbum(ctx context.Context, sources []Source) ([]measuredOutput, loudnessMeasurement, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var log boundedLog
	analyzer := exec.CommandContext(ctx, a.cfg.FFmpeg, "-nostdin", "-hide_banner", "-v", "info", "-f", "f32le", "-ar", "48000", "-ac", "2", "-i", "pipe:0", "-af", "ebur128=framelog=verbose:peak=sample", "-f", "null", "-")
	analyzer.Stderr = &log
	analyzer.WaitDelay = 2 * time.Second
	input, err := analyzer.StdinPipe()
	if err != nil {
		return nil, loudnessMeasurement{}, err
	}
	if err = analyzer.Start(); err != nil {
		input.Close()
		return nil, loudnessMeasurement{}, err
	}
	var tracks []measuredOutput
	for _, source := range sources {
		a.loudnessMu.Lock()
		if a.loudnessReport != nil {
			a.loudnessReport.Current = source.Rel
		}
		a.loudnessMu.Unlock()
		var staged *stagedSource
		var track measuredOutput
		staged, track, err = a.stageOutput(ctx, source.ID, "")
		if err != nil {
			break
		}
		var probe probeData
		probe, err = a.probe(ctx, staged.Path)
		if err == nil {
			track.Tags, track.Duration = probe.tags(), probe.duration()
			var analysis []byte
			analysis, err = a.mediaTool(ctx, a.cfg.FFmpeg, "-nostdin", "-hide_banner", "-v", "info", "-xerror", "-i", staged.Path, "-map", "0:a:0", "-af", "aformat=sample_rates=48000:channel_layouts=stereo,ebur128=framelog=verbose:peak=sample", "-f", "null", "-")
			if err == nil {
				track.Loudness, err = parseLoudness(string(analysis))
			}
		}
		if err == nil {
			var decoderLog boundedLog
			decoder := exec.CommandContext(ctx, a.cfg.FFmpeg, "-nostdin", "-v", "error", "-xerror", "-i", staged.Path, "-map", "0:a:0", "-ar", "48000", "-ac", "2", "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1")
			decoder.Stdout, decoder.Stderr = input, &decoderLog
			err = waitTool(ctx, decoder, a.sourceSlots)
			if err != nil && ctx.Err() != nil {
				err = ctx.Err()
			} else if err != nil {
				err = fmt.Errorf("Album loudness decode failed: %w\n%s", err, decoderLog.String())
			}
		}
		staged.Close()
		if err != nil {
			break
		}
		tracks = append(tracks, track)
	}
	_ = input.Close()
	if err != nil {
		cancel()
		_ = analyzer.Wait()
		return nil, loudnessMeasurement{}, err
	}
	if err = analyzer.Wait(); err != nil {
		return nil, loudnessMeasurement{}, fmt.Errorf("Album loudness analysis failed: %w\n%s", err, log.String())
	}
	album, err := parseLoudness(log.String())
	return tracks, album, err
}

func (a *App) writeOutputGain(ctx context.Context, track measuredOutput, tags map[string]string) (bool, error) {
	return a.remuxOutputTags(ctx, track, tags, false, nil)
}

func (a *App) remuxOutputTags(ctx context.Context, track measuredOutput, tags map[string]string, replace bool, override *outputTagOverride) (bool, error) {
	missing := map[string]string{}
	for key, value := range tags {
		if replace && track.Tags[key] != value || !replace && strings.TrimSpace(track.Tags[key]) == "" {
			missing[key] = value
		}
	}
	if len(missing) == 0 {
		if err := a.lockFiles(ctx); err != nil {
			return false, err
		}
		defer a.files.Unlock()
		var signature string
		err := a.reads.QueryRowContext(ctx, "SELECT signature FROM managed WHERE path=? AND source_id=? AND kind='audio'", track.Source.Output, track.Source.ID).Scan(&signature)
		if err != nil || signature != track.Signature {
			return false, errAuditionUnavailable
		}
		settings, err := a.settingsContext(ctx)
		if err != nil {
			return false, err
		}
		source, err := a.source(track.Source.ID)
		if err != nil {
			return false, err
		}
		if !settings.Enabled || settings.Source != track.Settings.Source || settings.Output != track.Settings.Output || source.Rel != track.Source.Rel || source.Output != track.Source.Output || source.BuiltHash != track.Source.BuiltHash || source.Hash != track.Source.Hash {
			return false, errAuditionUnavailable
		}
		if override != nil {
			if track.ResultSignature != nil {
				*track.ResultSignature = signature
			}
			if len(override.Tags) == 0 {
				return false, a.restoreOptionalMeta("output-tags:"+strconv.FormatInt(track.Source.ID, 10), "")
			}
			raw, _ := json.Marshal(override)
			return false, a.setMeta("output-tags:"+strconv.FormatInt(track.Source.ID, 10), string(raw))
		}
		gain := outputGainFor(track.Source.Output, signature, track.Tags)
		raw, _ := json.Marshal(gain)
		return false, a.setMeta("output-gain:"+strconv.FormatInt(track.Source.ID, 10), string(raw))
	}
	staged, current, err := a.stageOutput(ctx, track.Source.ID, track.Signature)
	if err != nil {
		return false, err
	}
	defer staged.Close()
	if current.Source.Rel != track.Source.Rel || current.Source.Output != track.Source.Output || current.Source.BuiltHash != track.Source.BuiltHash {
		return false, errAuditionUnavailable
	}
	if err = a.lockFiles(ctx); err != nil {
		return false, err
	}
	if err = a.ensureRecovery(ctx, track.Settings); err != nil {
		a.files.Unlock()
		return false, err
	}
	tempRel := filepath.Join(filepath.Dir(track.Source.Output), ".musicforge-"+randomToken()+".part")
	temp, err := safePath(track.Settings.Output, tempRel)
	if err == nil {
		_, err = a.db.Exec("INSERT INTO managed(path,source_id,kind) VALUES(?,?,'temp')", tempRel, track.Source.ID)
	}
	a.files.Unlock()
	if err != nil {
		return false, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temp)
			_, _ = a.db.Exec("DELETE FROM managed WHERE path=? AND kind='temp'", tempRel)
		}
	}()
	codec := strings.TrimPrefix(filepath.Ext(track.Source.Output), ".")
	args := []string{"-nostdin", "-v", "error", "-n", "-xerror", "-i", staged.Path, "-map", "0:a:0", "-map_metadata", "0", "-c:a", "copy"}
	if replace {
		for key, alias := range map[string]string{"track": "tracknumber", "disc": "discnumber", "album_artist": "albumartist"} {
			if _, ok := tags[key]; ok {
				args = append(args, "-metadata", alias+"=")
				if codec == "opus" {
					args = append(args, "-metadata:s:a:0", alias+"=")
				}
			}
		}
	}
	keys := []string{}
	for key := range missing {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-metadata", key+"="+missing[key])
		if codec == "opus" {
			args = append(args, "-metadata:s:a:0", key+"="+missing[key])
		}
	}
	args = append(args, "-f", codec, temp)
	if _, err = a.mediaTool(ctx, a.cfg.FFmpeg, args...); err != nil {
		return false, err
	}
	if err = a.validateArtifact(ctx, temp, Encoding{Codec: codec}, track.Duration); err != nil {
		return false, err
	}
	probe, err := a.probe(ctx, temp)
	if err != nil {
		return false, err
	}
	actualTags := probe.tags()
	for key, value := range missing {
		if actualTags[key] != value {
			return false, fmt.Errorf("Output tag %s did not survive remuxing", key)
		}
	}
	hash, err := fileHash(ctx, temp)
	if err != nil {
		return false, err
	}
	if track.ResultSignature != nil {
		*track.ResultSignature = hash
	}
	file, err := os.Open(temp)
	if err != nil {
		return false, err
	}
	err = errors.Join(file.Sync(), file.Close())
	if err != nil {
		return false, err
	}
	if err = a.lockFiles(ctx); err != nil {
		return false, err
	}
	defer a.files.Unlock()
	s, err := a.settingsContext(ctx)
	if err != nil {
		return false, err
	}
	source, err := a.source(track.Source.ID)
	if err != nil {
		return false, err
	}
	var signature string
	err = a.reads.QueryRowContext(ctx, "SELECT signature FROM managed WHERE path=? AND source_id=? AND kind='audio'", source.Output, source.ID).Scan(&signature)
	if err != nil || !s.Enabled || s.Source != track.Settings.Source || s.Output != track.Settings.Output || source.Rel != track.Source.Rel || source.Output != track.Source.Output || source.Hash != track.Source.Hash || source.BuiltHash != track.Source.BuiltHash || signature != track.Signature {
		return false, errAuditionUnavailable
	}
	if err = a.verifyOutputStorage(ctx, s); err != nil {
		return false, err
	}
	out, err := safePath(s.Output, source.Output)
	if err != nil {
		return false, err
	}
	existingHash, err := fileHash(ctx, out)
	if err != nil || existingHash != track.Signature {
		return false, errAuditionUnavailable
	}
	p := promotion{ID: source.ID, Temp: tempRel, Target: source.Output, Old: source.Output, Hash: source.BuiltHash, Profile: source.BuiltProfile, ArtifactHash: hash}
	journal, _ := json.Marshal(p)
	gain := outputGainFor(source.Output, hash, actualTags)
	pending, _ := json.Marshal(outputGainJournal{ID: source.ID, Previous: track.Signature, Gain: gain, SourceHash: source.Hash, SourceError: source.Error})
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "promotion:"+strconv.FormatInt(source.ID, 10), string(journal)); err != nil {
		return false, err
	}
	if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "output-gain-pending:"+strconv.FormatInt(source.ID, 10), string(pending)); err != nil {
		return false, err
	}
	if override != nil {
		raw, _ := json.Marshal(outputTagPending{ID: source.ID, Path: source.Output, Signature: hash, Override: *override})
		if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "output-tags-pending:"+strconv.FormatInt(source.ID, 10), string(raw)); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	keep = true
	if err = os.Rename(temp, out); err != nil {
		return false, err
	}
	if err = syncDirectory(filepath.Dir(out)); err != nil {
		return false, err
	}
	if err = a.finalize(s, p); err != nil {
		return false, err
	}
	if err = a.recoverOutputGains(ctx); err != nil {
		return false, err
	}
	keep = false
	return true, nil
}

func (a *App) getLoudness(w http.ResponseWriter, r *http.Request) {
	a.loudnessMu.Lock()
	defer a.loudnessMu.Unlock()
	if a.loudnessReport == nil {
		var saved loudnessReport
		raw, err := a.metaContext(r.Context(), "maintenance:loudness")
		if err == nil && json.Unmarshal([]byte(raw), &saved) == nil {
			if saved.State == "running" {
				saved.State, saved.Current = "stopped", ""
			}
			a.loudnessReport = &saved
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			apiError(w, 500, err)
			return
		}
	}
	if a.loudnessReport == nil {
		respond(w, 200, loudnessReport{State: "idle"})
		return
	}
	respond(w, 200, a.loudnessReport)
}

func (a *App) saveLoudnessReport() {
	raw, _ := json.Marshal(a.loudnessReport)
	if err := a.setMeta("maintenance:loudness", string(raw)); err != nil {
		a.logger.Warn("loudness report save failed", "error", err)
	}
}

func (a *App) startLoudness(w http.ResponseWriter, r *http.Request) {
	var body selection
	if !decode(w, r, &body) {
		return
	}
	s, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if !s.Enabled {
		apiError(w, 409, errors.New("library is disabled"))
		return
	}
	list, err := a.sourcesWithStatusContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	selected := map[int64]bool{}
	for _, id := range body.IDs {
		selected[id] = true
	}
	wanted := map[string]bool{}
	count, skipped := 0, 0
	for _, source := range list {
		if (!body.All && !selected[source.ID]) || !source.OutputPresent || source.Output == "" {
			continue
		}
		if source.OutputGain != nil && source.OutputGain.Track != "" && source.OutputGain.Album != "" {
			skipped++
			continue
		}
		wanted[albumKey(source.Rel)] = true
		count++
	}
	groups := map[string][]Source{}
	for _, source := range list {
		if wanted[albumKey(source.Rel)] && source.OutputPresent && source.Output != "" {
			groups[albumKey(source.Rel)] = append(groups[albumKey(source.Rel)], source)
		}
	}
	for _, group := range groups {
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Disc != group[j].Disc {
				return group[i].Disc < group[j].Disc
			}
			if group[i].Track != group[j].Track {
				return group[i].Track < group[j].Track
			}
			return group[i].Rel < group[j].Rel
		})
	}
	a.jobsMu.Lock()
	a.repairMu.Lock()
	repairing := a.repairRunning
	a.repairMu.Unlock()
	a.loudnessMu.Lock()
	if repairing || a.loudnessBusy.Load() || a.toolBusy.Load() || a.ioContext.Err() != nil {
		a.loudnessMu.Unlock()
		a.jobsMu.Unlock()
		apiError(w, 409, errors.New("Another output maintenance operation is running"))
		return
	}
	report := &loudnessReport{State: "running", Total: count, Skipped: skipped}
	if count == 0 {
		report.State = "complete"
	}
	a.loudnessReport = report
	a.saveLoudnessReport()
	if count == 0 {
		a.loudnessMu.Unlock()
		a.jobsMu.Unlock()
		respond(w, 200, report)
		return
	}
	ctx, cancel := context.WithCancel(a.ioContext)
	a.loudnessCancel = cancel
	a.loudnessBusy.Store(true)
	a.loudnessWG.Add(1)
	a.loudnessMu.Unlock()
	a.jobsMu.Unlock()
	go func() {
		defer a.loudnessWG.Done()
		defer cancel()
		keys := []string{}
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if ctx.Err() != nil {
				break
			}
			capacityErr := a.waitOutputMaintenanceCapacity(ctx)
			var tracks []measuredOutput
			var album loudnessMeasurement
			if capacityErr == nil {
				tracks, album, capacityErr = a.analyzeAlbum(ctx, groups[key])
			}
			// Reuse any consistent existing album gain for its codec. Missing tags
			// join that album's manual choice; conflicting choices require review.
			existing := map[string]string{}
			if capacityErr == nil {
				for _, track := range tracks {
					for _, name := range []string{"replaygain_album_gain", "r128_album_gain"} {
						if value := track.Tags[name]; value != "" {
							if previous := existing[name]; previous != "" && previous != value {
								capacityErr = errors.New("Conflicting existing album gain tags; review the album before filling")
							}
							existing[name] = value
						}
					}
				}
			}
			if capacityErr != nil {
				if ctx.Err() != nil {
					break
				}
				a.loudnessMu.Lock()
				for _, source := range groups[key] {
					if (body.All || selected[source.ID]) && (source.OutputGain == nil || source.OutputGain.Track == "" || source.OutputGain.Album == "") {
						a.loudnessReport.Processed++
						a.loudnessReport.Failed++
					}
				}
				a.loudnessReport.Error = capacityErr.Error()
				a.saveLoudnessReport()
				a.loudnessMu.Unlock()
				continue
			}
			for _, track := range tracks {
				if ctx.Err() != nil {
					break
				}
				if !body.All && !selected[track.Source.ID] {
					continue
				}
				original := Source{}
				for _, source := range groups[key] {
					if source.ID == track.Source.ID {
						original = source
						break
					}
				}
				if original.OutputGain != nil && original.OutputGain.Track != "" && original.OutputGain.Album != "" {
					continue
				}
				a.loudnessMu.Lock()
				a.loudnessReport.Current = track.Source.Rel
				a.loudnessMu.Unlock()
				tags := gainTags(strings.TrimPrefix(filepath.Ext(track.Source.Output), "."), track.Loudness, album)
				for name, value := range existing {
					if _, ok := tags[name]; ok {
						tags[name] = value
					}
				}
				updated, err := a.writeOutputGain(ctx, track, tags)
				if err != nil && ctx.Err() != nil {
					break
				}
				a.loudnessMu.Lock()
				a.loudnessReport.Processed++
				if err != nil {
					a.loudnessReport.Failed++
					a.loudnessReport.Error = err.Error()
				} else if updated {
					a.loudnessReport.Updated++
				} else {
					a.loudnessReport.Skipped++
				}
				a.saveLoudnessReport()
				a.loudnessMu.Unlock()
			}
		}
		a.loudnessMu.Lock()
		a.loudnessReport.State, a.loudnessReport.Current = "complete", ""
		if ctx.Err() != nil {
			a.loudnessReport.State = "stopped"
		}
		a.saveLoudnessReport()
		a.loudnessBusy.Store(false)
		a.loudnessCancel = nil
		a.loudnessMu.Unlock()
	}()
	respond(w, 202, map[string]bool{"started": true})
}

func (a *App) stopLoudness(w http.ResponseWriter, r *http.Request) {
	a.loudnessMu.Lock()
	defer a.loudnessMu.Unlock()
	if a.loudnessCancel != nil {
		a.loudnessCancel()
	}
	respond(w, 200, map[string]bool{"ok": true})
}
