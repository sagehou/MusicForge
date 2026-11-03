package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type outputTagOverride struct {
	Version int `json:"version"`
	SourceRoot string `json:"source_root"`
	OutputRoot string `json:"output_root"`
	Rel string `json:"rel"`
	SourceHash string `json:"source_hash"`
	Tags map[string]string `json:"tags"`
}

type outputTagPending struct {
	ID int64 `json:"id"`
	Path string `json:"path"`
	Signature string `json:"signature"`
	Override outputTagOverride `json:"override"`
}

var tagNumber = regexp.MustCompile(`^[1-9][0-9]{0,3}(/[1-9][0-9]{0,3})?$`)

func outputMetadata(original, changes map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range original { result[key] = value }
	for key, value := range changes {
		result[key] = value
		switch key { case "track": result["tracknumber"] = ""; case "disc": result["discnumber"] = ""; case "album_artist": result["albumartist"] = "" }
	}
	return result
}

func (a *App) buildOutputMetadata(ctx context.Context, s Settings, source Source) (map[string]string, error) {
	changes, err := a.outputTagChanges(ctx, s, source, source.Hash)
	return outputMetadata(source.Metadata, changes), err
}

func (a *App) annotateOutputTags(ctx context.Context, s Settings, list []Source) error {
	byID := map[int64]*Source{}
	for i := range list { byID[list[i].ID] = &list[i] }
	rows, err := a.reads.QueryContext(ctx, "SELECT substr(key,13),value FROM meta WHERE key GLOB 'output-tags:*'")
	if err != nil { return err }
	defer rows.Close()
	for rows.Next() {
		var id int64
		var raw string
		var override outputTagOverride
		if err = rows.Scan(&id, &raw); err != nil { return err }
		source := byID[id]
		if source != nil && source.OutputPresent && json.Unmarshal([]byte(raw), &override) == nil && override.Version == 1 && override.SourceRoot == s.Source && override.OutputRoot == s.Output && override.Rel == source.Rel && override.SourceHash == source.BuiltHash { source.OutputTags = override.Tags }
	}
	return rows.Err()
}

func validateOutputTags(tags map[string]string) error {
	if len(tags) == 0 || len(tags) > 6 { return errors.New("Choose at least one output tag to change") }
	for key, value := range tags {
		switch key { case "title", "artist", "album", "album_artist", "track", "disc": default: return errors.New("Unsupported output tag") }
		if !utf8.ValidString(value) || len(value) > 4096 || strings.IndexFunc(value, unicode.IsControl) >= 0 { return errors.New("Output tags must be single-line UTF-8 text, at most 4096 bytes") }
		if value != "" && (key == "track" || key == "disc") {
			if !tagNumber.MatchString(value) { return errors.New("Track and disc must be 1–9999, optionally followed by /total") }
			first, total, ok := strings.Cut(value, "/")
			if ok { n, _ := strconv.Atoi(first); count, _ := strconv.Atoi(total); if n > count { return errors.New("Track or disc exceeds its total") } }
		}
	}
	return nil
}

func (a *App) outputTagChanges(ctx context.Context, s Settings, source Source, hash string) (map[string]string, error) {
	raw, err := a.metaContext(ctx, "output-tags:"+strconv.FormatInt(source.ID, 10))
	if errors.Is(err, sql.ErrNoRows) { return map[string]string{}, nil }
	if err != nil { return nil, err }
	var override outputTagOverride
	if err = json.Unmarshal([]byte(raw), &override); err != nil { return nil, err }
	if override.Version != 1 || override.SourceRoot != s.Source || override.OutputRoot != s.Output || override.Rel != source.Rel || override.SourceHash != hash { return map[string]string{}, nil }
	if err = validateOutputTags(override.Tags); err != nil { return nil, err }
	return override.Tags, nil
}

func (a *App) recoverOutputTagEdits(ctx context.Context) error {
	rows, err := a.reads.QueryContext(ctx, "SELECT key,value FROM meta WHERE key GLOB 'output-tags-pending:*'")
	if err != nil { return err }
	type entry struct { key, raw string }
	entries := []entry{}
	for rows.Next() { var value entry; if err = rows.Scan(&value.key, &value.raw); err != nil { rows.Close(); return err }; entries = append(entries, value) }
	err = rows.Err(); rows.Close()
	if err != nil { return err }
	for _, value := range entries {
		var pending outputTagPending
		if err = json.Unmarshal([]byte(value.raw), &pending); err != nil { return err }
		if pending.ID < 1 || !validDigest(pending.Signature) || pending.Override.Version != 1 { return errors.New("Invalid pending output tags") }
		var signature string
		err = a.reads.QueryRowContext(ctx, "SELECT signature FROM managed WHERE source_id=? AND path=? AND kind='audio'", pending.ID, pending.Path).Scan(&signature)
		if err != nil && !errors.Is(err, sql.ErrNoRows) { return err }
		tx, err := a.db.BeginTx(ctx, nil)
		if err != nil { return err }
		if signature == pending.Signature {
			raw, _ := json.Marshal(pending.Override)
			_, err = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "output-tags:"+strconv.FormatInt(pending.ID, 10), string(raw))
		}
		if err == nil { _, err = tx.ExecContext(ctx, "DELETE FROM meta WHERE key=?", value.key) }
		if err != nil { tx.Rollback(); return err }
		if err = tx.Commit(); err != nil { return err }
	}
	return nil
}

type tagPreviewTrack struct {
	ID int64 `json:"id"`
	Path string `json:"path"`
	Before map[string]string `json:"before"`
	After map[string]string `json:"after"`
	Source Source `json:"-"`
	Signature string `json:"-"`
}
type tagPreview struct {
	Token string `json:"token"`
	Tracks []tagPreviewTrack `json:"tracks"`
	Settings Settings `json:"-"`
	Created time.Time `json:"-"`
}

func (a *App) previewOutputTags(w http.ResponseWriter, r *http.Request) {
	var body struct { IDs []int64 `json:"ids"`; Tags map[string]string `json:"tags"` }
	if !decode(w, r, &body) { return }
	if err := validateOutputTags(body.Tags); err != nil { apiError(w, 400, err); return }
	if len(body.IDs) < 1 || len(body.IDs) > 1000 { apiError(w, 400, errors.New("Select between 1 and 1000 tracks")); return }
	if err := a.lockFiles(r.Context()); err != nil { apiError(w, 503, err); return }
	defer a.files.Unlock()
	s, err := a.settingsContext(r.Context())
	if err != nil { apiError(w, 500, err); return }
	if !s.Enabled { apiError(w, 409, errors.New("library is disabled")); return }
	preview := tagPreview{Token: randomToken(), Settings: s, Created: time.Now(), Tracks: []tagPreviewTrack{}}
	seen := map[int64]bool{}
	for _, id := range body.IDs {
		if seen[id] { continue }; seen[id] = true
		source, readErr := a.source(id)
		if readErr != nil || !source.OutputPresent || source.Output == "" { apiError(w, 409, errors.New("Every selected track must have a registered output")); return }
		var signature string
		if readErr = a.reads.QueryRowContext(r.Context(), "SELECT signature FROM managed WHERE source_id=? AND path=? AND kind='audio'", id, source.Output).Scan(&signature); readErr != nil || !validDigest(signature) { apiError(w, 409, errAuditionUnavailable); return }
		before, readErr := a.outputTagChanges(r.Context(), s, source, source.BuiltHash)
		if readErr != nil { apiError(w, 500, readErr); return }
		snapshot := map[string]string{}
		for key := range body.Tags { snapshot[key] = source.Metadata[key]; if value, ok := before[key]; ok { snapshot[key] = value } }
		preview.Tracks = append(preview.Tracks, tagPreviewTrack{ID: id, Path: source.Output, Before: snapshot, After: body.Tags, Source: source, Signature: signature})
	}
	a.toolMu.Lock()
	if a.tagPreviews == nil { a.tagPreviews = map[string]tagPreview{} }
	for key, entry := range a.tagPreviews { if time.Since(entry.Created) >= 10*time.Minute { delete(a.tagPreviews, key) } }
	if len(a.tagPreviews) >= 8 { a.toolMu.Unlock(); apiError(w, 429, errors.New("Too many tag previews; retry in ten minutes")); return }
	a.tagPreviews[preview.Token] = preview
	a.toolMu.Unlock()
	respond(w, 200, preview)
}

func (a *App) startOutputTags(w http.ResponseWriter, r *http.Request) {
	var body struct { Token string `json:"token"` }
	if !decode(w, r, &body) { return }
	a.toolMu.Lock()
	preview, ok := a.tagPreviews[body.Token]
	if ok { delete(a.tagPreviews, body.Token) }
	a.toolMu.Unlock()
	if !ok || time.Since(preview.Created) >= 10*time.Minute { apiError(w, 409, errors.New("Tag preview expired; preview the changes again")); return }
	err := a.runLibraryTool("tags", len(preview.Tracks), func(ctx context.Context) {
		for _, entry := range preview.Tracks {
			if ctx.Err() != nil { break }
			a.toolCurrent("tags", entry.Path)
			err := a.waitOutputMaintenanceCapacity(ctx)
			updated := false
			if err == nil { updated, err = a.editOutputTags(ctx, preview.Settings, entry) }
			if ctx.Err() != nil { break }
			a.toolProgress("tags", entry.Source, updated, err)
		}
	})
	if err != nil { apiError(w, 409, err); return }
	respond(w, 202, map[string]bool{"ok": true})
}

func (a *App) editOutputTags(ctx context.Context, settings Settings, entry tagPreviewTrack) (bool, error) {
	staged, track, err := a.stageOutput(ctx, entry.ID, entry.Signature)
	if err != nil { return false, err }
	if track.Settings.Source != settings.Source || track.Settings.Output != settings.Output || track.Source.Rel != entry.Source.Rel || track.Source.BuiltHash != entry.Source.BuiltHash || track.Source.Hash != entry.Source.Hash { staged.Close(); return false, errAuditionUnavailable }
	probe, err := a.probe(ctx, staged.Path)
	staged.Close()
	if err != nil { return false, err }
	track.Tags, track.Duration = probe.tags(), probe.duration()
	changes, err := a.outputTagChanges(ctx, settings, track.Source, track.Source.BuiltHash)
	if err != nil { return false, err }
	for key, value := range entry.After { changes[key] = value }
	override := outputTagOverride{Version: 1, SourceRoot: settings.Source, OutputRoot: settings.Output, Rel: track.Source.Rel, SourceHash: track.Source.BuiltHash, Tags: changes}
	return a.remuxOutputTags(ctx, track, entry.After, true, &override)
}
