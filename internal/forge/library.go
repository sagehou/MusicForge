package forge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type boundedLog struct {
	bytes.Buffer
	limit int
	truncated bool
}

func (b *boundedLog) Write(p []byte) (int, error) {
	n := len(p)
	limit := b.limit
	if limit == 0 { limit = 65536 }
	remaining := limit - b.Len()
	if len(p) > remaining { b.truncated = true }
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
func runTool(ctx context.Context, tool string, args ...string) ([]byte, error) {
	return runToolLimited(ctx, nil, tool, args...)
}
func (a *App) mediaTool(ctx context.Context, tool string, args ...string) ([]byte, error) {
	return runToolLimited(ctx, a.sourceSlots, tool, args...)
}
func runToolLimited(ctx context.Context, slots chan struct{}, tool string, args ...string) ([]byte, error) {
	var out boundedLog
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := waitTool(ctx, cmd, slots)
	if err != nil {
		if ctx.Err() != nil { return nil, ctx.Err() }
		return nil, fmt.Errorf("%s failed: %w\n%s", filepath.Base(tool), err, out.String())
	}
	return out.Bytes(), nil
}

func waitTool(ctx context.Context, cmd *exec.Cmd, slots chan struct{}) error {
	if slots != nil {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done(): return ctx.Err()
		}
	}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		if slots != nil { <-slots }
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		if slots != nil { <-slots }
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Do not wait for an unresponsive FUSE syscall to acknowledge SIGKILL.
		_ = cmd.Process.Kill()
		return ctx.Err()
	}
}

type probeData struct {
	Streams []struct {
		Codec       string            `json:"codec_name"`
		Type        string            `json:"codec_type"`
		Duration    string            `json:"duration"`
		Tags        map[string]string `json:"tags"`
		Disposition struct {
			Attached int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

func (a *App) probe(ctx context.Context, path string) (probeData, error) {
	var p probeData
	probeCtx, cancel := context.WithTimeout(ctx, a.sourceTimeout())
	defer cancel()
	// Request only used fields; artwork stream diagnostics can otherwise truncate JSON.
	cmd := exec.CommandContext(probeCtx, a.cfg.FFprobe, "-v", "error", "-show_entries", "stream=codec_name,codec_type,duration:stream_tags:stream_disposition=attached_pic:format=duration:format_tags", "-of", "json", path)
	out := boundedLog{limit: 4*1024*1024}
	var stderr boundedLog
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := waitTool(probeCtx, cmd, a.sourceSlots)
	if err != nil {
		if probeCtx.Err() != nil { return p, probeCtx.Err() }
		return p, fmt.Errorf("ffprobe failed: %w\n%s", err, stderr.String())
	}
	if out.truncated {
		return p, errors.New("FLAC metadata exceeds the 4 MiB limit")
	}
	err = json.Unmarshal(out.Bytes(), &p)
	if err != nil {
		return p, err
	}
	return p, nil
}
func (p probeData) duration() float64 {
	v, _ := strconv.ParseFloat(p.Format.Duration, 64)
	if v == 0 {
		for _, s := range p.Streams {
			if s.Type == "audio" {
				v, _ = strconv.ParseFloat(s.Duration, 64)
				break
			}
		}
	}
	return v
}
func (p probeData) tags() map[string]string {
	tags := map[string]string{}
	for _, s := range p.Streams {
		if s.Type == "audio" {
			for k, v := range s.Tags {
				tags[strings.ToLower(k)] = v
			}
		}
	}
	for k, v := range p.Format.Tags {
		tags[strings.ToLower(k)] = v
	}
	return tags
}
func fileHash(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	b := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(b)
		if n > 0 {
			_, _ = h.Write(b[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func tagNumber(value string) int {
	value = strings.Split(value, "/")[0]
	v, _ := strconv.Atoi(value)
	return v
}
func outputRel(rel, codec string) string {
	return strings.TrimSuffix(rel, filepath.Ext(rel)) + "." + codec
}
func sameStat(info fs.FileInfo, s Source) bool {
	return info.Size() == s.Size && info.ModTime().UnixNano() == s.Mtime
}
func scoped(rel string, dirs []string) bool {
	if len(dirs) == 0 {
		return true
	}
	for _, dir := range dirs {
		if containsPath(dir, rel) {
			return true
		}
	}
	return false
}

func (a *App) scan(ctx context.Context, r ScanRequest) error {
	jobID, _ := ctx.Value(taskJobKey{}).(int64)
	if err := a.lockFiles(ctx); err != nil { return err }
	defer a.files.Unlock()
	s, err := a.settings()
	if err != nil {
		return err
	}
	a.reportProgress(jobID, Activity{Phase: "storage", Path: s.Source}, 0)
	if err = a.storageContext(ctx, s); err != nil {
		return err
	}
	if err = a.ensureRecovery(ctx, s); err != nil {
		return err
	}
	files := map[string]fs.FileInfo{}
	lastReport := time.Time{}
	dirs := r.Dirs
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	for _, dir := range dirs {
		err = a.sourceIO(ctx, "walk", s.Source, dir, func(e sourceEvent) error {
			if e.Info != nil {
				files[e.Info.Rel] = *e.Info
			}
			if time.Since(lastReport) >= time.Second {
				a.reportProgress(jobID, Activity{Phase: "discover", Path: e.Path, Processed: len(files)}, 0)
				lastReport = time.Now()
			}
			return nil
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return later("Scan incomplete; no deletions applied: "+err.Error(), 30)
		}
	}
	existing, err := a.allSources()
	if err != nil {
		return err
	}
	byRel := map[string]Source{}
	for _, source := range existing {
		byRel[source.Rel] = source
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	quiet := false
	invalid := 0
	unavailable := 0
	for index, rel := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		info := files[rel]
		source, found := byRel[rel]
		a.reportProgress(jobID, Activity{Phase: "scan", Path: rel, Artist: source.Artist, Album: source.Album, Title: source.Title, Processed: index, Total: len(paths)}, float64(index)/float64(len(paths)))
		changed := !found || !sameStat(info, source) || r.Verify || strings.HasPrefix(source.Error, sourceReadErrorPrefix)
		if changed && time.Since(info.ModTime()) < 30*time.Second {
			quiet = true
			continue
		}
		if changed {
			activity := Activity{Phase: "read", Path: rel, Artist: source.Artist, Album: source.Album, Title: source.Title, Processed: index, Total: len(paths), ReadTotalBytes: info.Size()}
			a.reportProgress(jobID, activity, float64(index)/float64(len(paths)))
			lastRead := time.Time{}
			hash, err := a.sourceHash(ctx, s.Source, rel, func(read, total int64) {
				if time.Since(lastRead) < time.Second && read != total {
					return
				}
				activity.ReadBytes, activity.ReadTotalBytes = read, total
				a.reportProgress(jobID, activity, float64(index)/float64(len(paths)))
				if read > 0 { lastRead = time.Now() }
			})
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err = a.indexSourceReadError(rel, info, err); err != nil {
					return err
				}
				unavailable++
				continue
			}
			current, err := a.sourceStat(ctx, s.Source, rel)
			if err != nil {
				if ctx.Err() != nil { return ctx.Err() }
				if err = a.indexSourceReadError(rel, info, err); err != nil { return err }
				unavailable++
				continue
			}
			activity.Phase = "probe"
			a.reportProgress(jobID, activity, float64(index)/float64(len(paths)))
			p, err := a.probe(ctx, current.Path)
			audio := false
			for _, stream := range p.Streams {
				if stream.Type == "audio" && stream.Codec == "flac" {
					audio = true
				}
			}
			duration := p.duration()
			if err == nil && (!audio || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0)) {
				err = fmt.Errorf("not FLAC audio: %s", rel)
			}
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if errors.Is(err, context.DeadlineExceeded) {
					if err = a.indexSourceReadError(rel, info, err); err != nil { return err }
					unavailable++
					continue
				}
				if err = a.indexSourceError(rel, info, err); err != nil {
					return err
				}
				invalid++
				continue
			}
			after, err := a.sourceStat(ctx, s.Source, rel)
			if err != nil {
				if ctx.Err() != nil { return ctx.Err() }
				if err = a.indexSourceReadError(rel, info, err); err != nil { return err }
				unavailable++
				continue
			}
			if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
				quiet = true
				continue
			}
			tags := p.tags()
			metadata, _ := json.Marshal(tags)
			if !found {
				// A missing byte-identical source is a rename. Existing duplicates keep their own IDs.
				for _, old := range existing {
					if old.Hash == hash && old.Hash != "" && files[old.Rel] == nil {
						// A scoped scan must confirm absence outside its enumerated directories.
						if _, err = a.sourceStat(ctx, s.Source, old.Rel); err == nil {
							continue
						} else if !os.IsNotExist(err) {
							return err
						}
						var taken int
						if err = a.db.QueryRow("SELECT count(*) FROM sources WHERE id=? AND rel=?", old.ID, old.Rel).Scan(&taken); err != nil {
							return err
						}
						if taken == 1 {
							source = old
							found = true
							break
						}
					}
				}
			}
			if found {
				_, err = a.db.Exec("UPDATE sources SET rel=?,hash=?,size=?,mtime=?,artist=?,album=?,title=?,track=?,disc=?,duration=?,metadata=?,present=1,error='' WHERE id=?", rel, hash, info.Size(), info.ModTime().UnixNano(), tags["artist"], tags["album"], tags["title"], tagNumber(tags["track"]), tagNumber(tags["disc"]), p.duration(), string(metadata), source.ID)
			} else {
				var result sql.Result
				result, err = a.db.Exec("INSERT INTO sources(rel,hash,size,mtime,artist,album,title,track,disc,duration,metadata) VALUES(?,?,?,?,?,?,?,?,?,?,?)", rel, hash, info.Size(), info.ModTime().UnixNano(), tags["artist"], tags["album"], tags["title"], tagNumber(tags["track"]), tagNumber(tags["disc"]), p.duration(), string(metadata))
				if err == nil {
					source.ID, err = result.LastInsertId()
				}
			}
			if err != nil {
				return err
			}
		} else {
			if _, err = a.db.Exec("UPDATE sources SET present=1 WHERE id=?", source.ID); err != nil {
				return err
			}
		}
		source, err = a.source(source.ID)
		if err != nil {
			return err
		}
		if changed {
			a.reportProgress(jobID, Activity{Phase: "scan", Path: rel, Artist: source.Artist, Album: source.Album, Title: source.Title, Processed: index, Total: len(paths)}, float64(index)/float64(len(paths)))
		}
		present := false
		if source.Output != "" {
			out, err := safePath(s.Output, source.Output)
			if err != nil {
				return err
			}
			fi, err := os.Stat(out)
			present = err == nil && fi.Mode().IsRegular()
			if err != nil && !os.IsNotExist(err) {
				return later("Output storage unavailable: "+err.Error(), 30)
			}
		}
		if _, err = a.db.Exec("UPDATE sources SET output_present=? WHERE id=?", present, source.ID); err != nil {
			return err
		}
		source.OutputPresent = present
		if source.Hash == "" {
			if source.Error != "" {
				invalid++
			}
			continue
		}
		moved := present && source.BuiltHash == source.Hash && source.Output != outputRel(source.Rel, strings.TrimPrefix(filepath.Ext(source.Output), "."))
		if moved {
			if _, err = a.queueBuildTask(source, s.Encoding, true, false, jobID); err != nil {
				return err
			}
		} else if !present || source.BuiltHash != source.Hash {
			if _, err = a.queueBuildTask(source, s.Encoding, false, false, jobID); err != nil {
				return err
			}
		}
	}
	if quiet {
		return later("Waiting for source files to be unchanged for 30 seconds", 10)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Only a complete scan of accessible storage may expire sources.
	if err = a.storageContext(ctx, s); err != nil {
		return err
	}
	all, err := a.allSources()
	if err != nil {
		return err
	}
	for _, source := range all {
		if invalid == 0 && unavailable == 0 && scoped(source.Rel, r.Dirs) && files[source.Rel] == nil {
			if _, err = a.db.Exec("UPDATE sources SET present=0 WHERE id=?", source.ID); err != nil {
				return err
			}
		}
	}
	albums := map[string][]Source{}
	for _, source := range all {
		if files[source.Rel] != nil && source.Hash != "" && source.Error == "" && scoped(source.Rel, r.Dirs) {
			dir := filepath.Dir(source.Rel)
			albums[dir] = append(albums[dir], source)
		}
	}
	for dir, tracks := range albums {
		if err := ctx.Err(); err != nil {
			return err
		}
		a.reportProgress(jobID, Activity{Phase: "artwork", Path: dir, Processed: len(paths), Total: len(paths)}, .99)
		if err = a.artwork(ctx, s, dir, tracks); err != nil {
			return err
		}
	}
	a.reportProgress(jobID, Activity{Phase: "indexed", Processed: len(paths), Total: len(paths), Percent: 100}, 1)
	if unavailable > 0 {
		return fmt.Errorf("scan found %d unavailable source files and %d invalid FLAC files; healthy tracks queued, no deletions applied; see Library errors", unavailable, invalid)
	}
	if invalid > 0 {
		return fmt.Errorf("scan found %d invalid FLAC files; healthy tracks processed, no deletions applied; see Library errors", invalid)
	}
	return nil
}

func (a *App) indexSourceError(rel string, info fs.FileInfo, cause error) error {
	// Keep playable output and display metadata, but invalidate the current build target.
	_, err := a.db.Exec("INSERT INTO sources(rel,size,mtime,present,error) VALUES(?,?,?,1,?) ON CONFLICT(rel) DO UPDATE SET hash='',size=excluded.size,mtime=excluded.mtime,present=1,error=excluded.error", rel, info.Size(), info.ModTime().UnixNano(), cause.Error())
	return err
}

const sourceReadErrorPrefix = "Source read unavailable: "

func (a *App) indexSourceReadError(rel string, info fs.FileInfo, cause error) error {
	// A remote-read failure does not invalidate the last known hash or playable audio.
	message := sourceReadErrorPrefix + cause.Error()
	_, err := a.db.Exec("INSERT INTO sources(rel,size,mtime,present,error) VALUES(?,?,?,1,?) ON CONFLICT(rel) DO UPDATE SET present=1,error=excluded.error", rel, info.Size(), info.ModTime().UnixNano(), message)
	a.logger.Warn("source read failed", "path", rel, "error", cause)
	return err
}

func (a *App) queueBuild(source Source, profile Encoding, move, manual bool) (int64, error) {
	return a.queueBuildTask(source, profile, move, manual, 0)
}

func (a *App) queueBuildTask(source Source, profile Encoding, move, manual bool, task int64) (int64, error) {
	kind := "convert"
	if move {
		kind = "move"
	}
	key := fmt.Sprintf("%s:%d:%s:%s:%s", kind, source.ID, source.Hash, profile.Fingerprint(), digest(source.Rel))
	return a.enqueueTask(kind, key, BuildRequest{source.ID, source.Hash, profile, move}, manual, task)
}

func (a *App) owned(rel string, id int64) error {
	var owner int64
	var kind string
	err := a.db.QueryRow("SELECT source_id,kind FROM managed WHERE path=?", rel).Scan(&owner, &kind)
	if err != nil {
		return fmt.Errorf("refusing unregistered output %q: %w", rel, err)
	}
	if kind != "audio" || owner != id {
		return fmt.Errorf("output %q is owned by another source", rel)
	}
	return nil
}
func (a *App) targetAvailable(s Settings, rel string, id int64) error {
	path, err := safePath(s.Output, rel)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		var owner int64
		err = a.db.QueryRow("SELECT source_id FROM managed WHERE path=?", rel).Scan(&owner)
		if err == nil && owner != id {
			return errors.New("target reserved by another source")
		}
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("output target is not a regular file")
	}
	return a.owned(rel, id)
}

type promotion struct {
	ID           int64  `json:"id"`
	Temp         string `json:"temp"`
	Target       string `json:"target"`
	Old          string `json:"old"`
	Hash         string `json:"hash"`
	Profile      string `json:"profile"`
	ArtifactHash string `json:"artifact_hash"`
}

func (a *App) finalize(s Settings, p promotion) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO managed(path,source_id,kind,signature) VALUES(?,?,'audio',?) ON CONFLICT(path) DO UPDATE SET source_id=excluded.source_id,kind='audio',signature=excluded.signature", p.Target, p.ID, p.ArtifactHash); err != nil {
		return err
	}
	if p.Temp != p.Target {
		if _, err = tx.Exec("DELETE FROM managed WHERE path=? AND kind='temp'", p.Temp); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE sources SET output=?,built_hash=?,built_profile=?,output_present=1,error='' WHERE id=?", p.Target, p.Hash, p.Profile, p.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = a.dirty(p.Target); err != nil {
		return err
	}
	if p.Old != "" && p.Old != p.Target {
		if err = a.removeOwned(s, p.Old, p.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	_, err = a.db.Exec("DELETE FROM meta WHERE key=?", "promotion:"+strconv.FormatInt(p.ID, 10))
	return err
}

func (a *App) recoverPromotions(ctx context.Context, s Settings) error {
	rows, err := a.db.Query("SELECT value FROM meta WHERE key LIKE 'promotion:%'")
	if err != nil {
		return err
	}
	journals := []string{}
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			rows.Close()
			return err
		}
		journals = append(journals, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, value := range journals {
		var p promotion
		if err = json.Unmarshal([]byte(value), &p); err != nil {
			return err
		}
		path, err := safePath(s.Output, p.Target)
		if err != nil {
			return err
		}
		hash, err := fileHash(ctx, path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && hash == p.ArtifactHash {
			if err = a.finalize(s, p); err != nil {
				return err
			}
		} else {
			_, err = a.db.Exec("DELETE FROM meta WHERE key=?", "promotion:"+strconv.FormatInt(p.ID, 10))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) recoverFiles(ctx context.Context, s Settings) error {
	if err := a.recoverPromotions(ctx, s); err != nil {
		return err
	}
	if err := a.recoverDeletions(s); err != nil {
		return err
	}
	rows, err := a.db.Query("SELECT path FROM managed WHERE kind='temp'")
	if err != nil {
		return err
	}
	temps := []string{}
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			rows.Close()
			return err
		}
		temps = append(temps, path)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, rel := range temps {
		path, err := safePath(s.Output, rel)
		if err != nil {
			return err
		}
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if _, err = a.db.Exec("DELETE FROM managed WHERE path=? AND kind='temp'", rel); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) build(ctx context.Context, j Job, r BuildRequest) error {
	if err := a.lockFiles(ctx); err != nil { return err }
	s, err := a.settings()
	if err != nil {
		a.files.Unlock()
		return err
	}
	if err = a.storageContext(ctx, s); err != nil {
		a.files.Unlock()
		return err
	}
	if err = a.ensureRecovery(ctx, s); err != nil {
		a.files.Unlock()
		return err
	}
	if err = a.recoverPromotions(ctx, s); err != nil {
		a.files.Unlock()
		return err
	}
	source, err := a.source(r.ID)
	if err != nil {
		a.files.Unlock()
		return err
	}
	if !source.Present || source.Hash != r.Hash || (!r.Move && s.Encoding.Fingerprint() != r.Profile.Fingerprint()) {
		a.files.Unlock()
		return nil
	}
	info, err := a.sourceStat(ctx, s.Source, source.Rel)
	in := info.Path
	if err != nil {
		a.files.Unlock()
		if os.IsNotExist(err) {
			_, _ = a.enqueue("scan", "scan:periodic", ScanRequest{}, false)
			return nil
		}
		if ctx.Err() != nil { return ctx.Err() }
		return a.buildError(ctx, s, source, err)
	}
	if !sameStat(info, source) || time.Since(info.ModTime()) < 30*time.Second {
		a.files.Unlock()
		_, _ = a.enqueue("scan", "scan:periodic", ScanRequest{}, false)
		return later("Source changed or is still being written", 10)
	}
	target := outputRel(source.Rel, r.Profile.Codec)
	if r.Move {
		target = outputRel(source.Rel, strings.TrimPrefix(filepath.Ext(source.Output), "."))
	}
	if !r.Move && source.OutputPresent && source.Output == target && source.BuiltHash == r.Hash && source.BuiltProfile == r.Profile.Fingerprint() {
		path, pathErr := safePath(s.Output, target)
		if pathErr != nil {
			a.files.Unlock()
			return pathErr
		}
		if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
			a.files.Unlock()
			return nil
		} else if statErr != nil && !os.IsNotExist(statErr) {
			a.files.Unlock()
			return later("Output storage unavailable: "+statErr.Error(), 30)
		}
	}
	activity := Activity{Phase: j.Kind, Path: source.Rel, Artist: source.Artist, Album: source.Album, Title: source.Title}
	a.reportProgress(j.ID, activity, 0)
	if err = a.targetAvailable(s, target, source.ID); err != nil {
		a.files.Unlock()
		return err
	}
	out, err := safePath(s.Output, target)
	if err != nil {
		a.files.Unlock()
		return err
	}
	if err = os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		a.files.Unlock()
		return err
	}
	if r.Move {
		if err := ctx.Err(); err != nil {
			a.files.Unlock()
			return err
		}
		if source.Output == target {
			a.files.Unlock()
			return nil
		}
		if err = a.owned(source.Output, source.ID); err != nil {
			a.files.Unlock()
			return err
		}
		old, err := safePath(s.Output, source.Output)
		if err != nil {
			a.files.Unlock()
			return err
		}
		hash, err := fileHash(ctx, old)
		if err != nil {
			a.files.Unlock()
			return err
		}
		p := promotion{source.ID, source.Output, target, source.Output, source.BuiltHash, source.BuiltProfile, hash}
		b, _ := json.Marshal(p)
		if err = a.setMeta("promotion:"+strconv.FormatInt(source.ID, 10), string(b)); err == nil {
			err = os.Rename(old, out)
		}
		if err == nil {
			err = syncDirectory(filepath.Dir(out))
		}
		if err == nil && filepath.Dir(old) != filepath.Dir(out) {
			err = syncDirectory(filepath.Dir(old))
		}
		if err == nil {
			err = a.finalize(s, p)
		}
		a.files.Unlock()
		return err
	}
	// Register a random temporary name before creating it; only owned temps are cleaned.
	tempRel := filepath.Join(filepath.Dir(target), ".musicforge-"+randomToken()+".part")
	temp, err := safePath(s.Output, tempRel)
	if err != nil {
		a.files.Unlock()
		return err
	}
	if _, err = a.db.Exec("INSERT INTO managed(path,source_id,kind) VALUES(?,?,'temp')", tempRel, source.ID); err != nil {
		a.files.Unlock()
		return err
	}
	a.files.Unlock()
	defer func() {
		// The random registered temp is exclusive to this build. Cleanup must not
		// wait for an unrelated remote scan to release the library mutation lock.
		_ = os.Remove(temp)
		_, _ = a.db.Exec("DELETE FROM managed WHERE path=? AND kind='temp'", tempRel)
	}()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-n", "-xerror", "-err_detect", "crccheck+explode", "-i", in, "-map", "0:a:0", "-map_metadata", "0", "-vn", "-sn", "-dn"}
	keys := make([]string, 0, len(source.Metadata))
	for key := range source.Metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-metadata", key+"="+source.Metadata[key])
	}
	args = append(args, r.Profile.Args()...)
	args = append(args, temp)
	if err = a.encode(ctx, j.ID, source.Duration, activity, args); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return a.buildError(ctx, s, source, err)
	}
	activity.Phase, activity.Percent = "validate", 100
	a.reportProgress(j.ID, activity, .9)
	if err = a.validateArtifact(ctx, temp, r.Profile, source.Duration); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return a.buildError(ctx, s, source, err)
	}
	file, err := os.Open(temp)
	if err != nil {
		return err
	}
	err = file.Sync()
	file.Close()
	if err != nil {
		return err
	}
	hash, err := fileHash(ctx, temp)
	if err != nil {
		return err
	}
	if err := a.lockFiles(ctx); err != nil { return err }
	defer a.files.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err = a.storageContext(ctx, s); err != nil {
		return err
	}
	latest, err := a.source(source.ID)
	if err != nil {
		return err
	}
	settings, err := a.settings()
	if err != nil {
		return err
	}
	after, err := a.sourceStat(ctx, s.Source, source.Rel)
	if err != nil || !sameStat(after, source) || latest.Rel != source.Rel || latest.Hash != source.Hash {
		_, _ = a.enqueue("scan", "scan:periodic", ScanRequest{}, false)
		return later("Source changed during conversion; temporary artifact discarded", 10)
	}
	if settings.Encoding.Fingerprint() != r.Profile.Fingerprint() {
		return nil
	}
	if err = a.targetAvailable(s, target, source.ID); err != nil {
		return err
	}
	p := promotion{source.ID, tempRel, target, latest.Output, source.Hash, r.Profile.Fingerprint(), hash}
	b, _ := json.Marshal(p)
	if err = a.setMeta("promotion:"+strconv.FormatInt(source.ID, 10), string(b)); err != nil {
		return err
	}
	if err = os.Rename(temp, out); err != nil {
		return err
	}
	if err = syncDirectory(filepath.Dir(out)); err != nil {
		return err
	}
	if err = a.finalize(s, p); err != nil {
		return err
	}
	_, _ = a.db.Exec("UPDATE jobs SET progress=.95 WHERE id=?", j.ID)
	return nil
}

func (a *App) encode(ctx context.Context, id int64, duration float64, activity Activity, args []string) error {
	encodeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stalled := time.AfterFunc(a.sourceTimeout(), cancel)
	defer stalled.Stop()
	select {
	case a.sourceSlots <- struct{}{}:
	case <-encodeCtx.Done():
		if ctx.Err() != nil { return ctx.Err() }
		return fmt.Errorf("media workers are still blocked: %w", context.DeadlineExceeded)
	}
	args = append([]string{"-progress", "pipe:1", "-nostats"}, args...)
	cmd := exec.CommandContext(encodeCtx, a.cfg.FFmpeg, args...)
	cmd.WaitDelay = 2 * time.Second
	var stderr boundedLog
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err == nil { err = cmd.Start() }
	if err != nil { <-a.sourceSlots; return err }
	defer stdout.Close()
	values := make(chan float64)
	done := make(chan error, 1)
	go func() {
		defer func() { <-a.sourceSlots }()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			key, value, found := strings.Cut(scanner.Text(), "=")
			if !found || key != "out_time_us" { continue }
			microseconds, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(microseconds) || math.IsInf(microseconds, 0) { continue }
			select {
			case values <- microseconds:
			case <-encodeCtx.Done():
			}
			if encodeCtx.Err() != nil { break }
		}
		close(values)
		if scanner.Err() != nil { cancel() }
		if err := cmd.Wait(); err != nil {
			done <- fmt.Errorf("%s failed: %w\n%s", filepath.Base(a.cfg.FFmpeg), err, stderr.String())
			return
		}
		done <- scanner.Err()
	}()
	last := time.Time{}
	advanced := -1.0
	for {
		select {
		case microseconds, ok := <-values:
			if !ok { values = nil; continue }
			if microseconds > advanced {
				advanced = microseconds
				stalled.Reset(a.sourceTimeout())
			}
			if duration <= 0 || time.Since(last) < time.Second { continue }
			activity.Percent = math.Max(0, math.Min(100, microseconds/duration/10000))
			a.reportProgress(id, activity, activity.Percent/100*.85)
			last = time.Now()
		case err := <-done:
			if ctx.Err() != nil { return ctx.Err() }
			if encodeCtx.Err() != nil { return fmt.Errorf("encoding stalled at %s: %w", activity.Path, context.DeadlineExceeded) }
			return err
		case <-encodeCtx.Done():
			_ = cmd.Process.Kill()
			if ctx.Err() != nil { return ctx.Err() }
			return fmt.Errorf("encoding stalled at %s (no progress for %s): %w", activity.Path, a.sourceTimeout(), context.DeadlineExceeded)
		}
	}
}

func (a *App) recordSourceError(id int64, err error) {
	_, _ = a.db.Exec("UPDATE sources SET error=? WHERE id=?", err.Error(), id)
}
func (a *App) buildError(ctx context.Context, s Settings, source Source, err error) error {
	if ctx.Err() != nil { return ctx.Err() }
	if storageErr := a.storageContext(ctx, s); storageErr != nil {
		return storageErr
	}
	info, statErr := a.sourceStat(ctx, s.Source, source.Rel)
	if os.IsNotExist(statErr) || statErr == nil && !sameStat(info, source) {
		_, _ = a.enqueue("scan", "scan:periodic", ScanRequest{}, false)
		return later("Source changed during conversion; temporary artifact discarded", 10)
	}
	a.recordSourceError(source.ID, err)
	return err
}
func (a *App) validateArtifact(ctx context.Context, path string, e Encoding, duration float64) error {
	p, err := a.probe(ctx, path)
	if err != nil {
		return err
	}
	audio := 0
	for _, stream := range p.Streams {
		if stream.Type == "video" {
			return errors.New("artifact unexpectedly contains artwork")
		}
		if stream.Type == "audio" {
			audio++
			if stream.Codec != e.Codec {
				return errors.New("artifact codec mismatch")
			}
		}
	}
	if audio != 1 || p.duration() <= 0 || math.Abs(p.duration()-duration) > math.Max(.5, duration*.02) {
		return errors.New("artifact duration or audio stream validation failed")
	}
	_, err = runTool(ctx, a.cfg.FFmpeg, "-nostdin", "-v", "error", "-xerror", "-err_detect", "crccheck+explode", "-i", path, "-map", "0:a:0", "-f", "null", "-")
	return err
}

type deletion struct {
	ID  int64  `json:"id"`
	Rel string `json:"rel"`
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (a *App) recoverDeletions(s Settings) error {
	rows, err := a.db.Query("SELECT value FROM meta WHERE key LIKE 'deletion:%'")
	if err != nil {
		return err
	}
	var pending []deletion
	for rows.Next() {
		var raw string
		var entry deletion
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal([]byte(raw), &entry)
		}
		if err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, entry)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, entry := range pending {
		if err = a.removeOwned(s, entry.Rel, entry.ID); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) removeOwned(s Settings, rel string, id int64) error {
	key := "deletion:" + strconv.FormatInt(id, 10) + ":" + digest(rel)
	raw, err := a.meta(key)
	journaled := err == nil
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if journaled {
		var entry deletion
		if err = json.Unmarshal([]byte(raw), &entry); err != nil {
			return err
		}
		if entry.ID != id || entry.Rel != rel {
			return errors.New("deletion journal identity mismatch")
		}
	}
	ownershipErr := a.owned(rel, id)
	if ownershipErr != nil && !(journaled && errors.Is(ownershipErr, sql.ErrNoRows)) {
		return ownershipErr
	}
	path, err := safePath(s.Output, rel)
	if err != nil {
		return err
	}
	if !journaled {
		b, err := json.Marshal(deletion{id, rel})
		if err != nil {
			return err
		}
		if err = a.setMeta(key, string(b)); err != nil {
			return err
		}
	}
	if ownershipErr != nil {
		// A completed registry removal only authorizes recovery of an absent file.
		if _, err = os.Lstat(path); !os.IsNotExist(err) {
			return errors.New("deletion recovery found an unregistered replacement")
		}
	} else if err = os.Remove(path); err == nil {
		if err = syncDirectory(filepath.Dir(path)); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM managed WHERE path=? AND source_id=? AND kind='audio'", rel, id); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE sources SET output='',output_present=0,built_hash='',built_profile='' WHERE id=? AND output=?", id, rel); err != nil {
		return err
	}
	if _, err = tx.Exec(markDirtySQL, filepath.ToSlash(filepath.Dir(rel)), time.Now().UnixNano()); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM meta WHERE key=?", key); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.cleanAlbum(s, filepath.Dir(rel))
	return nil
}

func (a *App) cleanAlbum(s Settings, dir string) {
	var count int
	query := "SELECT count(*) FROM managed WHERE kind='audio' AND substr(path,1,?)=?"
	args := []any{len(dir) + 1, dir + string(filepath.Separator)}
	if dir == "." {
		query = "SELECT count(*) FROM managed WHERE kind='audio' AND instr(path,'/')=0"
		args = nil
	}
	if a.db.QueryRow(query, args...).Scan(&count) != nil || count > 0 {
		return
	}
	cover := filepath.Join(dir, "cover.jpg")
	var kind string
	if a.db.QueryRow("SELECT kind FROM managed WHERE path=?", cover).Scan(&kind) == nil && kind == "cover" {
		if path, err := safePath(s.Output, cover); err == nil {
			if err = os.Remove(path); err == nil || os.IsNotExist(err) {
				_, _ = a.db.Exec("DELETE FROM managed WHERE path=? AND kind='cover'", cover)
			}
		}
	}
	for dir != "." && dir != "" {
		path, err := safePath(s.Output, dir)
		if err != nil || os.Remove(path) != nil {
			break
		}
		dir = filepath.Dir(dir)
	}
}
func (a *App) deleteExpired(ids []int64) error {
	return a.deleteExpiredContext(context.Background(), ids)
}
func (a *App) deleteExpiredContext(ctx context.Context, ids []int64) error {
	if err := a.lockFiles(ctx); err != nil { return err }
	defer a.files.Unlock()
	s, err := a.settings()
	if err != nil {
		return err
	}
	if err = a.storageContext(ctx, s); err != nil {
		return err
	}
	if err = a.ensureRecovery(ctx, s); err != nil {
		return err
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		source, err := a.source(id)
		if err != nil {
			return err
		}
		if source.Present || source.Output == "" {
			continue
		}
		if _, err = a.sourceStat(ctx, s.Source, source.Rel); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err = a.removeOwned(s, source.Output, id); err != nil {
			return err
		}
		if _, err = a.db.Exec("UPDATE sources SET output='',output_present=0,built_hash='',built_profile='' WHERE id=?", id); err != nil {
			return err
		}
	}
	return nil
}

type artworkMemo struct {
	Album string `json:"album"`
	Rel   string `json:"rel"`
	Hash  string `json:"hash"`
}

func (a *App) embeddedArtwork(ctx context.Context, s Settings, dir string, tracks []Source) (string, string, error) {
	sort.Slice(tracks, func(i, j int) bool {
		if tracks[i].Disc != tracks[j].Disc {
			return tracks[i].Disc < tracks[j].Disc
		}
		if tracks[i].Track != tracks[j].Track {
			return tracks[i].Track < tracks[j].Track
		}
		return tracks[i].Rel < tracks[j].Rel
	})
	snapshot := make([]struct{ Rel, Hash string }, len(tracks))
	for i, track := range tracks {
		snapshot[i] = struct{ Rel, Hash string }{track.Rel, track.Hash}
	}
	b, _ := json.Marshal(snapshot)
	album := digest(string(b))
	key := "artwork:" + digest(dir)
	memo := artworkMemo{}
	value, err := a.meta(key)
	if err != nil && err != sql.ErrNoRows {
		return "", "", err
	}
	cached := err == nil && json.Unmarshal([]byte(value), &memo) == nil && memo.Album == album
	if !cached {
		memo = artworkMemo{Album: album}
		for _, track := range tracks {
			info, err := a.sourceStat(ctx, s.Source, track.Rel)
			if err != nil { return "", "", err }
			p, err := a.probe(ctx, info.Path)
			if err != nil {
				return "", "", err
			}
			for _, stream := range p.Streams {
				if stream.Type == "video" && stream.Disposition.Attached == 1 {
					memo.Rel = track.Rel
					memo.Hash = track.Hash
					break
				}
			}
			if memo.Rel != "" {
				break
			}
		}
		b, _ = json.Marshal(memo)
		if err = a.setMeta(key, string(b)); err != nil {
			return "", "", err
		}
	}
	if memo.Rel == "" {
		return "", "", nil
	}
	info, err := a.sourceStat(ctx, s.Source, memo.Rel)
	return info.Path, "embedded:" + memo.Hash, err
}

func (a *App) artwork(ctx context.Context, s Settings, dir string, tracks []Source) error {
	var input, signature string
	embedded := false
	for _, name := range []string{"cover.jpg", "folder.jpg", "Cover.jpg", "Folder.jpg", "cover.png", "folder.png", "cover.jpeg", "folder.jpeg"} {
		rel := filepath.Join(dir, name)
		info, err := a.sourceStat(ctx, s.Source, rel)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("artwork is not a regular file")
		}
		hash, err := a.sourceHash(ctx, s.Source, rel, nil)
		if err != nil { return err }
		input = info.Path
		signature = "external:" + hash
		break
	}
	if input == "" {
		var err error
		input, signature, err = a.embeddedArtwork(ctx, s, dir, tracks)
		if err != nil {
			return err
		}
		embedded = input != ""
	}
	target := filepath.Join(dir, "cover.jpg")
	var kind, oldSig string
	registered := a.db.QueryRow("SELECT kind,signature FROM managed WHERE path=?", target).Scan(&kind, &oldSig) == nil
	out, err := safePath(s.Output, target)
	if err != nil {
		return err
	}
	if input == "" {
		if registered && kind == "cover" {
			if err = os.Remove(out); err != nil && !os.IsNotExist(err) {
				return err
			}
			_, err = a.db.Exec("DELETE FROM managed WHERE path=? AND kind='cover'", target)
			if err == nil {
				err = a.dirty(target)
			}
			return err
		}
		return nil
	}
	if registered && kind != "cover" {
		return errors.New("cover path owned by another artifact")
	}
	if info, err := os.Lstat(out); err == nil {
		if !info.Mode().IsRegular() || !registered {
			return errors.New("unregistered artwork path conflict")
		}
		if oldSig == signature {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	rel := filepath.Join(dir, ".musicforge-"+randomToken()+".part")
	temp, err := safePath(s.Output, rel)
	if err != nil {
		return err
	}
	if _, err = a.db.Exec("INSERT INTO managed(path,kind) VALUES(?,'temp')", rel); err != nil {
		return err
	}
	defer func() { os.Remove(temp); _, _ = a.db.Exec("DELETE FROM managed WHERE path=? AND kind='temp'", rel) }()
	args := []string{"-nostdin", "-v", "error", "-n", "-i", input}
	if embedded {
		args = append(args, "-map", "0:v:0")
	}
	args = append(args, "-frames:v", "1", "-c:v", "mjpeg", "-f", "image2", temp)
	artCtx, cancel := context.WithTimeout(ctx, a.sourceTimeout())
	defer cancel()
	if _, err = a.mediaTool(artCtx, a.cfg.FFmpeg, args...); err != nil { return err }
	file, err := os.OpenFile(temp, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	// Record ownership before promotion so a crash can safely regenerate the cover.
	if _, err = a.db.Exec("INSERT INTO managed(path,kind,signature) VALUES(?,'cover','') ON CONFLICT(path) DO UPDATE SET signature=''", target); err != nil {
		return err
	}
	if err = os.Rename(temp, out); err != nil {
		return err
	}
	if err = syncDirectory(filepath.Dir(out)); err != nil {
		return err
	}
	if _, err = a.db.Exec("UPDATE managed SET signature=? WHERE path=?", signature, target); err != nil {
		return err
	}
	return a.dirty(target)
}
