package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type playlistItem struct {
	Path     string `json:"path"`
	Original string `json:"original"`
}
type savedPlaylist struct {
	Version    int            `json:"version"`
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	SourceRoot string         `json:"source_root,omitempty"`
	OutputRoot string         `json:"output_root,omitempty"`
	Items      []playlistItem `json:"items"`
}
type playlistInput struct {
	Name string  `json:"name"`
	Text string  `json:"text"`
	Base string  `json:"base"`
	IDs  []int64 `json:"ids,omitempty"`
}
type playlistResult struct {
	ID      string         `json:"id,omitempty"`
	Name    string         `json:"name"`
	Content string         `json:"content"`
	Ready   int            `json:"ready"`
	Missing []playlistItem `json:"missing"`
}

func playlistPath(s Settings, base, input string) string {
	path := strings.ReplaceAll(input, "\\", "/")
	if strings.ContainsAny(path, "\x00\r\n") {
		return ""
	}
	if strings.HasPrefix(path, "file:") {
		u, err := url.Parse(path)
		if err != nil || u.Host != "" && u.Host != "localhost" || u.RawQuery != "" || u.Fragment != "" {
			return ""
		}
		path = u.Path
	} else if strings.Contains(path, "://") {
		return ""
	}
	if filepath.IsAbs(path) {
		for _, root := range []string{s.Source, s.LidarrPrefix} {
			if root == "" {
				continue
			}
			if containsPath(root, filepath.Clean(path)) {
				rel, err := filepath.Rel(root, path)
				if err == nil && rel != "." {
					return filepath.ToSlash(rel)
				}
			}
		}
		return ""
	}
	path = filepath.Clean(filepath.Join(base, path))
	if validRelativePath(path) != nil || path == "." || strings.Contains(path, ":") {
		return ""
	}
	return filepath.ToSlash(path)
}

func parsePlaylist(s Settings, input playlistInput, sources []Source) (savedPlaylist, error) {
	playlist := savedPlaylist{Version: 1, Name: strings.TrimSpace(input.Name), SourceRoot: s.Source, OutputRoot: s.Output, Items: []playlistItem{}}
	if playlist.Name == "" || len(playlist.Name) > 160 || strings.IndexFunc(playlist.Name, unicode.IsControl) >= 0 {
		return playlist, errors.New("Enter a playlist name, at most 160 bytes")
	}
	if !utf8.ValidString(input.Text) || len(input.Text) > 4*1024*1024 || len(input.IDs) > 10000 || validRelativePath(input.Base) != nil {
		return playlist, errors.New("Playlist must be UTF-8 M3U, at most 4 MiB and 10000 entries, with a relative base directory")
	}
	byID := map[int64]Source{}
	for _, source := range sources {
		byID[source.ID] = source
	}
	if len(input.IDs) > 0 {
		for _, id := range input.IDs {
			source, ok := byID[id]
			if !ok {
				return playlist, errors.New("Selected playlist track is no longer indexed")
			}
			playlist.Items = append(playlist.Items, playlistItem{Path: source.Rel, Original: source.Rel})
		}
		return playlist, nil
	}
	for _, line := range strings.Split(strings.TrimPrefix(input.Text, "\ufeff"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(playlist.Items) >= 10000 {
			return playlist, errors.New("Playlist exceeds 10000 entries")
		}
		playlist.Items = append(playlist.Items, playlistItem{Path: playlistPath(s, input.Base, line), Original: line})
	}
	if len(playlist.Items) == 0 {
		return playlist, errors.New("Playlist has no track paths")
	}
	return playlist, nil
}

func mapPlaylist(playlist savedPlaylist, sources []Source) playlistResult {
	byPath := map[string]Source{}
	for _, source := range sources {
		byPath[filepath.ToSlash(source.Rel)] = source
	}
	result := playlistResult{ID: playlist.ID, Name: playlist.Name, Missing: []playlistItem{}}
	var content strings.Builder
	content.WriteString("#EXTM3U\n")
	for _, item := range playlist.Items {
		source, ok := byPath[item.Path]
		if !ok || !source.OutputPresent || source.Output == "" || validRelativePath(source.Output) != nil || strings.ContainsAny(source.Output, "\r\n\x00") {
			result.Missing = append(result.Missing, item)
			continue
		}
		content.WriteString("./" + filepath.ToSlash(source.Output) + "\n")
		result.Ready++
	}
	result.Content = content.String()
	return result
}

func (a *App) previewPlaylist(w http.ResponseWriter, r *http.Request) {
	var input playlistInput
	if !decode(w, r, &input, 5*1024*1024) {
		return
	}
	if err := a.lockFiles(r.Context()); err != nil {
		apiError(w, 503, err)
		return
	}
	defer a.files.Unlock()
	s, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	sources, err := a.sourcesWhereContext(r.Context(), "")
	if err != nil {
		apiError(w, 500, err)
		return
	}
	playlist, err := parsePlaylist(s, input, sources)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	if r.URL.Path == "/api/playlists" {
		list, readErr := a.savedPlaylists(r.Context(), s)
		if readErr != nil {
			apiError(w, 500, readErr)
			return
		}
		if len(list) >= 50 {
			apiError(w, 409, errors.New("Remove a saved playlist before saving another (limit 50)"))
			return
		}
		playlist.ID = randomToken()
		raw, _ := json.Marshal(playlist)
		if err = a.setMeta("playlist:"+playlist.ID, string(raw)); err != nil {
			apiError(w, 500, err)
			return
		}
	}
	respond(w, 200, mapPlaylist(playlist, sources))
}

func (a *App) savedPlaylists(ctx context.Context, s Settings) ([]savedPlaylist, error) {
	rows, err := a.reads.QueryContext(ctx, "SELECT value FROM meta WHERE key GLOB 'playlist:*' ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []savedPlaylist{}
	for rows.Next() {
		var raw string
		var playlist savedPlaylist
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &playlist); err != nil {
			return nil, err
		}
		if playlist.Version != 1 {
			return nil, errors.New("Unsupported saved playlist version")
		}
		if playlist.SourceRoot == s.Source && playlist.OutputRoot == s.Output {
			list = append(list, playlist)
		}
	}
	return list, rows.Err()
}

func (a *App) listPlaylists(w http.ResponseWriter, r *http.Request) {
	s, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	list, err := a.savedPlaylists(r.Context(), s)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	items := []map[string]any{}
	for _, playlist := range list {
		items = append(items, map[string]any{"id": playlist.ID, "name": playlist.Name, "count": len(playlist.Items)})
	}
	respond(w, 200, items)
}

func (a *App) exportPlaylist(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("playlist")
	if !validDigest(id) {
		apiError(w, 400, errors.New("Invalid playlist ID"))
		return
	}
	raw, err := a.metaContext(r.Context(), "playlist:"+id)
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, 404, errors.New("Playlist not found"))
		return
	}
	var playlist savedPlaylist
	if err == nil {
		err = json.Unmarshal([]byte(raw), &playlist)
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
	if playlist.Version != 1 || playlist.SourceRoot != s.Source || playlist.OutputRoot != s.Output {
		apiError(w, 409, errors.New("Playlist belongs to different library roots"))
		return
	}
	if r.Method == "DELETE" {
		if _, err = a.db.ExecContext(r.Context(), "DELETE FROM meta WHERE key=?", "playlist:"+id); err != nil {
			apiError(w, 500, err)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
		return
	}
	sources, err := a.sourcesWhereContext(r.Context(), "")
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, mapPlaylist(playlist, sources))
}
