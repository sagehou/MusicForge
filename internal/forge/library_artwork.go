package forge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

func (a *App) libraryArtworkURLs(ctx context.Context, list []Source) error {
	rows, err := a.reads.QueryContext(ctx, "SELECT path,signature FROM managed WHERE kind='cover' AND signature<>''")
	if err != nil {
		return err
	}
	defer rows.Close()
	covers := map[string]string{}
	for rows.Next() {
		var path, signature string
		if err = rows.Scan(&path, &signature); err != nil {
			return err
		}
		covers[filepath.Clean(path)] = signature
	}
	if err = rows.Err(); err != nil {
		return err
	}
	urls := map[string]string{}
	for i := range list {
		cover := filepath.Join(filepath.Dir(list[i].Rel), "cover.jpg")
		if signature := covers[cover]; signature != "" {
			// All tracks using this cover share one browser cache entry.
			if urls[cover] == "" {
				urls[cover] = fmt.Sprintf("/api/library/%d/artwork?v=%s", list[i].ID, digest(signature))
			}
			list[i].ArtworkURL = urls[cover]
		}
	}
	return nil
}

// Serve only registered output covers. Browsing never reads the remote source.
func (a *App) libraryArtwork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 400, errors.New("invalid source ID"))
		return
	}
	var rel string
	err = a.reads.QueryRowContext(r.Context(), "SELECT rel FROM sources WHERE id=?", id).Scan(&rel)
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, 404, errors.New("artwork not found"))
		return
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	cover := filepath.Join(filepath.Dir(rel), "cover.jpg")
	var signature string
	err = a.reads.QueryRowContext(r.Context(), "SELECT signature FROM managed WHERE path=? AND kind='cover' AND signature<>''", cover).Scan(&signature)
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, 404, errors.New("artwork not found"))
		return
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	s, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	path, err := safePath(s.Output, cover)
	if err != nil {
		apiError(w, 404, errors.New("artwork not found"))
		return
	}
	file, err := os.Open(path)
	if err != nil {
		apiError(w, 404, errors.New("artwork not found"))
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 20*1024*1024 {
		apiError(w, 404, errors.New("artwork not found"))
		return
	}
	var header [512]byte
	n, _ := file.Read(header[:])
	if http.DetectContentType(header[:n]) != "image/jpeg" {
		apiError(w, 404, errors.New("artwork not found"))
		return
	}
	if _, err = file.Seek(0, 0); err != nil {
		apiError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("ETag", `"`+digest(fmt.Sprintf("%s:%d:%d", signature, info.Size(), info.ModTime().UnixNano()))+`"`)
	http.ServeContent(w, r, "cover.jpg", info.ModTime(), file)
}
