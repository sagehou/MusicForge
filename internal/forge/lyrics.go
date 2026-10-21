package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxLyricsBytes = 1024 * 1024
const lyricsFilePrefix = "lyrics-file:"

var lrcTimestamp = regexp.MustCompile(`^\[[0-9]{1,4}:[0-5][0-9](?:[.:][0-9]{1,3})?\]`)

func normalizeLRC(text string) (string, error) {
	if len(text) > maxLyricsBytes || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return "", errors.New("LRC must be UTF-8 and no larger than 1 MiB")
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if len(text)+1 > maxLyricsBytes {
		return "", errors.New("LRC must be UTF-8 and no larger than 1 MiB")
	}
	for _, line := range strings.Split(text, "\n") {
		timed := false
		for {
			stamp := lrcTimestamp.FindString(line)
			if stamp == "" {
				break
			}
			timed, line = true, line[len(stamp):]
		}
		if timed && strings.TrimSpace(line) != "" {
			return text + "\n", nil
		}
	}
	return "", errors.New("No synchronized LRC lines found")
}

func lyricsRel(audio string) string    { return outputRel(audio, "lrc") }
func lyricsFileKey(path string) string { return lyricsFilePrefix + digest(filepath.ToSlash(path)) }

// Optional ownership annotations leave published managed kinds and journals
// unchanged. Pending accepts either side of an interrupted atomic publication.
type lyricsFile struct {
	Path       string `json:"path"`
	ID         int64  `json:"source_id"`
	SourceHash string `json:"source_hash"`
	Signature  string `json:"signature"`
	Pending    string `json:"pending_signature,omitempty"`
	Provider   string `json:"provider"`
}

func (a *App) readLyricsFile(ctx context.Context, s Settings, rel string) (lyricsFile, string, bool, error) {
	entry := lyricsFile{Path: rel}
	raw, err := a.metaContext(ctx, lyricsFileKey(rel))
	registered := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return entry, "", false, err
	}
	if registered {
		if err = json.Unmarshal([]byte(raw), &entry); err != nil || entry.Path != rel || filepath.Ext(rel) != ".lrc" {
			return entry, "", false, errors.New("Invalid lyrics ownership annotation")
		}
	}
	path, err := safePath(s.Output, rel)
	if err != nil {
		return entry, "", false, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return entry, "", false, nil
	}
	if err != nil {
		return entry, "", false, err
	}
	if !registered || !info.Mode().IsRegular() || info.Size() > maxLyricsBytes {
		return entry, "", false, errors.New("LRC path conflict; unregistered or modified files are preserved")
	}
	file, err := os.Open(path)
	if err != nil {
		return entry, "", false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxLyricsBytes+1))
	if err != nil {
		return entry, "", false, err
	}
	hash := digest(string(data))
	if len(data) > maxLyricsBytes || (hash != entry.Signature && (entry.Pending == "" || hash != entry.Pending)) {
		return entry, "", false, errors.New("LRC path conflict; unregistered or modified files are preserved")
	}
	return entry, string(data), true, nil
}

// Caller holds files. Never publish to a path without its registered audio.
func (a *App) writeLyricsFile(ctx context.Context, s Settings, source Source, text, provider string) error {
	if err := a.owned(source.Output, source.ID); err != nil {
		return err
	}
	text, err := normalizeLRC(text)
	if err != nil {
		return err
	}
	rel := lyricsRel(source.Output)
	old, content, present, err := a.readLyricsFile(ctx, s, rel)
	if err != nil {
		return err
	}
	hash := digest(text)
	var sourceHash string
	if err = a.reads.QueryRowContext(ctx, "SELECT built_hash FROM sources WHERE id=? AND output=?", source.ID, source.Output).Scan(&sourceHash); err != nil {
		return err
	}
	entry := lyricsFile{Path: rel, ID: source.ID, SourceHash: sourceHash, Signature: hash, Provider: provider}
	if present && content == text {
		raw, _ := json.Marshal(entry)
		if err = a.setMeta(lyricsFileKey(rel), string(raw)); err != nil { return err }
		return a.recordLyricsScript(ctx, source, entry, text)
	}
	path, err := safePath(s.Output, rel)
	if err != nil {
		return err
	}
	tempRel := filepath.Join(filepath.Dir(rel), ".musicforge-"+randomToken()+".part")
	temp, err := safePath(s.Output, tempRel)
	if err != nil {
		return err
	}
	if _, err = a.db.Exec("INSERT INTO managed(path,source_id,kind) VALUES(?,?,'temp')", tempRel, source.ID); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(temp)
		_, _ = a.db.Exec("DELETE FROM managed WHERE path=? AND kind='temp'", tempRel)
	}()
	file, err := os.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(text)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	previous, readErr := a.meta(lyricsFileKey(rel))
	if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
		return readErr
	}
	pending := entry
	pending.Signature, pending.Pending = old.Signature, hash
	raw, _ := json.Marshal(pending)
	if err = a.setMeta(lyricsFileKey(rel), string(raw)); err != nil {
		return err
	}
	// Recheck ownership after writing the temporary file. A new destination uses
	// link(2) so a concurrently created user file cannot be overwritten.
	_, current, exists, checkErr := a.readLyricsFile(ctx, s, rel)
	if checkErr != nil || exists != present || current != content {
		err = errors.New("LRC changed during publication; file preserved")
	} else if present {
		err = os.Rename(temp, path)
	} else {
		err = os.Link(temp, path)
	}
	if err != nil {
		if errors.Is(readErr, sql.ErrNoRows) {
			_, _ = a.db.Exec("DELETE FROM meta WHERE key=?", lyricsFileKey(rel))
		} else {
			_ = a.setMeta(lyricsFileKey(rel), previous)
		}
		return err
	}
	if err = syncDirectory(filepath.Dir(path)); err != nil {
		return err // Pending ownership still recognizes the published bytes.
	}
	raw, _ = json.Marshal(entry)
	if err = a.setMeta(lyricsFileKey(rel), string(raw)); err != nil {
		return err
	}
	if err = a.dirty(rel); err != nil { return err }
	return a.recordLyricsScript(ctx, source, entry, text)
}

func (a *App) removeLyricsFile(ctx context.Context, s Settings, rel string) error {
	_, _, present, err := a.readLyricsFile(ctx, s, rel)
	if err != nil {
		return err
	}
	if present {
		path, err := safePath(s.Output, rel)
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err = syncDirectory(filepath.Dir(path)); err != nil {
			return err
		}
	}
	// Mark dirty before releasing ownership, so an interrupted deletion can retry.
	if err = a.dirty(rel); err != nil {
		return err
	}
	_, err = a.db.Exec("DELETE FROM meta WHERE key=?", lyricsFileKey(rel))
	return err
}

// Reconcile sidecars after our own or an older compatible worker's audio move
// or deletion. Unknown/edited files retain their annotation and are never removed.
// Caller holds files; this only reads the local output and database.
func (a *App) syncLyricsFile(ctx context.Context, s Settings, entry lyricsFile) error {
	if entry.Path == "" || filepath.Ext(entry.Path) != ".lrc" {
		return errors.New("Invalid lyrics ownership annotation")
	}
	var current Source
	err := a.reads.QueryRowContext(ctx, `SELECT source_id,path FROM managed WHERE kind='audio' AND path IN (?,?) LIMIT 1`, outputRel(entry.Path, "opus"), outputRel(entry.Path, "mp3")).Scan(&current.ID, &current.Output)
	if err == nil {
		if entry.ID != current.ID {
			entry.ID = current.ID
			raw, _ := json.Marshal(entry)
			if err = a.setMeta(lyricsFileKey(entry.Path), string(raw)); err != nil {
				return err
			}
		}
		// Also recover a crash after publication but before marking the directory
		// dirty. This runs once per storage recovery, not on library polling.
		return a.dirty(entry.Path)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	err = a.reads.QueryRowContext(ctx, `SELECT m.source_id,m.path FROM managed m JOIN sources s ON s.id=m.source_id AND s.output=m.path WHERE m.kind='audio' AND m.source_id=? AND s.built_hash=?`, entry.ID, entry.SourceHash).Scan(&current.ID, &current.Output)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		_, text, present, readErr := a.readLyricsFile(ctx, s, entry.Path)
		if readErr != nil {
			return readErr
		}
		if present {
			if err = a.writeLyricsFile(ctx, s, current, text, entry.Provider); err != nil {
				return err
			}
		}
	}
	if err = a.removeLyricsFile(ctx, s, entry.Path); err == nil {
		a.cleanAlbum(s, filepath.Dir(entry.Path))
	}
	return err
}

func (a *App) syncLyricsAfterDelete(s Settings, audio string) {
	raw, err := a.meta(lyricsFileKey(lyricsRel(audio)))
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	var entry lyricsFile
	if err == nil {
		err = json.Unmarshal([]byte(raw), &entry)
	}
	if err == nil && entry.Path != lyricsRel(audio) {
		err = errors.New("Lyrics ownership key mismatch")
	}
	if err == nil {
		err = a.syncLyricsFile(context.Background(), s, entry)
	}
	if err != nil {
		a.logger.Warn("lyrics lifecycle update failed", "path", audio, "error", err)
	}
}

func (a *App) reconcileLyrics(ctx context.Context, s Settings) {
	rows, err := a.reads.QueryContext(ctx, "SELECT key,value FROM meta WHERE key LIKE 'lyrics-file:%'")
	if err != nil {
		a.logger.Warn("lyrics ownership read failed", "error", err)
		return
	}
	var entries []lyricsFile
	for rows.Next() {
		var key, raw string
		var entry lyricsFile
		if err = rows.Scan(&key, &raw); err == nil {
			err = json.Unmarshal([]byte(raw), &entry)
		}
		if err == nil && key != lyricsFileKey(entry.Path) {
			err = errors.New("Lyrics ownership key mismatch")
		}
		if err != nil {
			break
		}
		entries = append(entries, entry)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		a.logger.Warn("lyrics ownership read failed", "error", err)
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		if err = a.syncLyricsFile(ctx, s, entry); err != nil {
			a.logger.Warn("lyrics reconciliation failed", "path", entry.Path, "error", err)
		}
	}
}

func (a *App) sourceLyrics(ctx context.Context, s Settings, source Source) (string, error) {
	if !source.Present {
		return "", nil
	}
	for _, rel := range []string{lyricsRel(source.Rel), outputRel(source.Rel, "LRC")} {
		var text string
		err := a.sourceIO(ctx, "lyrics", s.Source, rel, func(e sourceEvent) error {
			if !e.Complete {
				text = e.Text
			}
			return nil
		})
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("Read source LRC: %w", err)
		}
		return normalizeLRC(text)
	}
	return "", nil
}
