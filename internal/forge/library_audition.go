package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type audition struct {
	AudioURL    string `json:"audio_url"`
	Format      string `json:"format"`
	Size        int64  `json:"size"`
	Lyrics      string `json:"lyrics"`
	LyricsState string `json:"lyrics_state"`
}

var errAuditionUnavailable = errors.New("Generated audio is unavailable; reload the library and try again")

// Open a registered output under the file lock, then release it before serving
// bytes. An open descriptor survives our atomic replacements without blocking
// builds. os.Root also confines reads when an external process changes symlinks.
// Audition never opens source storage, runs recovery, or updates persisted state.
func (a *App) openAudition(ctx context.Context, id int64, version string, withLyrics bool) (*os.File, audition, string, error) {
	var preview audition
	if err := a.lockFiles(ctx); err != nil {
		return nil, preview, "", err
	}
	defer a.files.Unlock()
	s, err := a.settingsContext(ctx)
	if err != nil {
		return nil, preview, "", err
	}
	source, err := readSource(a.reads.QueryRowContext(ctx, "SELECT "+sourceCols+" FROM sources WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, preview, "", errAuditionUnavailable
	}
	if err != nil {
		return nil, preview, "", err
	}
	if source.Output == "" || !source.OutputPresent || validRelativePath(source.Output) != nil {
		return nil, preview, "", errAuditionUnavailable
	}
	var signature string
	err = a.reads.QueryRowContext(ctx, "SELECT signature FROM managed WHERE path=? AND source_id=? AND kind='audio' AND signature<>''", source.Output, id).Scan(&signature)
	if errors.Is(err, sql.ErrNoRows) || err == nil && version != "" && version != signature {
		return nil, preview, "", errAuditionUnavailable
	}
	if err != nil {
		return nil, preview, "", err
	}
	preview.Format = filepath.Ext(source.Output)
	if preview.Format != ".opus" && preview.Format != ".mp3" {
		return nil, preview, "", errAuditionUnavailable
	}
	root, err := os.OpenRoot(s.Output)
	if err != nil {
		return nil, preview, "", errAuditionUnavailable
	}
	defer root.Close()
	instance, err := a.metaContext(ctx, "instance")
	if err != nil {
		return nil, preview, "", err
	}
	marker, err := root.ReadFile(".musicforge")
	if err != nil || string(marker) != instance {
		return nil, preview, "", errAuditionUnavailable
	}
	file, err := openAuditionFile(root, source.Output)
	if err != nil {
		return nil, preview, "", errAuditionUnavailable
	}
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		file.Close()
		return nil, preview, "", errAuditionUnavailable
	}
	preview.Size = info.Size()
	preview.AudioURL = fmt.Sprintf("/api/library/%d/audition/audio?v=%s", id, signature)
	if withLyrics {
		preview.Lyrics, preview.LyricsState = a.auditionLyrics(ctx, root, source)
	}
	return file, preview, signature, nil
}

func openAuditionFile(root *os.Root, path string) (*os.File, error) {
	before, err := root.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, errAuditionUnavailable
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		file.Close()
		return nil, errAuditionUnavailable
	}
	return file, nil
}

func (a *App) auditionLyrics(ctx context.Context, root *os.Root, source Source) (string, string) {
	rel := lyricsRel(source.Output)
	raw, err := a.metaContext(ctx, lyricsFileKey(rel))
	if errors.Is(err, sql.ErrNoRows) {
		return "", "missing"
	}
	var entry lyricsFile
	if err != nil || json.Unmarshal([]byte(raw), &entry) != nil || entry.Path != rel || entry.ID != source.ID || entry.SourceHash != source.BuiltHash {
		return "", "unavailable"
	}
	file, err := openAuditionFile(root, rel)
	if err != nil {
		if _, statErr := root.Lstat(rel); os.IsNotExist(statErr) {
			return "", "missing"
		}
		return "", "unavailable"
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > maxLyricsBytes {
		return "", "unavailable"
	}
	data, err := io.ReadAll(io.LimitReader(file, maxLyricsBytes+1))
	if err != nil || len(data) > maxLyricsBytes || digest(string(data)) != entry.Signature && (entry.Pending == "" || digest(string(data)) != entry.Pending) {
		return "", "unavailable"
	}
	text, err := normalizeLRC(string(data))
	if err != nil {
		return "", "unavailable"
	}
	return text, "ready"
}

func (a *App) auditionRequest(w http.ResponseWriter, r *http.Request, withLyrics bool) (*os.File, audition, string, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 400, errors.New("invalid source ID"))
		return nil, audition{}, "", false
	}
	version := r.URL.Query().Get("v")
	if !withLyrics && version == "" {
		apiError(w, 400, errors.New("Audio version is required; reload the audition"))
		return nil, audition{}, "", false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	file, preview, signature, err := a.openAudition(ctx, id, version, withLyrics)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errAuditionUnavailable) {
			status = http.StatusConflict
		}
		apiError(w, status, err)
		return nil, preview, "", false
	}
	return file, preview, signature, true
}

func (a *App) libraryAudition(w http.ResponseWriter, r *http.Request) {
	file, preview, _, ok := a.auditionRequest(w, r, true)
	if !ok {
		return
	}
	file.Close()
	respond(w, 200, preview)
}

// Renew the existing server write deadline as bytes are sent, allowing long
// tracks on slow links without allowing a stalled stream to hold a connection.
type auditionWriter struct {
	http.ResponseWriter
	ctx context.Context
}

func (w auditionWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(60 * time.Second))
	return w.ResponseWriter.Write(data)
}

func (a *App) libraryAuditionAudio(w http.ResponseWriter, r *http.Request) {
	file, preview, signature, ok := a.auditionRequest(w, r, false)
	if !ok {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		apiError(w, 409, errAuditionUnavailable)
		return
	}
	contentType := "audio/ogg; codecs=opus"
	if preview.Format == ".mp3" {
		contentType = "audio/mpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", `"`+signature+`"`)
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	http.ServeContent(auditionWriter{w, r.Context()}, r, "audition"+preview.Format, info.ModTime(), file)
}
