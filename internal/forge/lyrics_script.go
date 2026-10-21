package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

const lyricsScriptPrefix = "lyrics-script:"

// Derived optional metadata is separate from the published ownership format.
// Older writers leave it intact; changed audio or LRC signatures hide it.
type lyricsScriptRecord struct {
	Version        int    `json:"version"`
	Output         string `json:"output"`
	AudioSignature string `json:"audio_signature"`
	Path           string `json:"path"`
	Signature      string `json:"signature"`
	Script         string `json:"script"`
}

func (a *App) recordLyricsScript(ctx context.Context, source Source, entry lyricsFile, text string) error {
	var signature string
	if err := a.reads.QueryRowContext(ctx, "SELECT signature FROM managed WHERE path=? AND source_id=? AND kind='audio'", source.Output, source.ID).Scan(&signature); err != nil {
		return err
	}
	record := lyricsScriptRecord{Version: 1, Output: source.Output, AudioSignature: signature, Path: entry.Path, Signature: digest(text), Script: lyricsScript(text)}
	raw, _ := json.Marshal(record)
	return a.setMeta(lyricsScriptPrefix+strconv.FormatInt(source.ID, 10), string(raw))
}

func (a *App) annotateLyricsScripts(ctx context.Context, sources []Source) error {
	rows, err := a.reads.QueryContext(ctx, `SELECT key,value FROM meta WHERE key LIKE 'lyrics-file:%'`)
	if err != nil {
		return err
	}
	files := map[string]lyricsFile{}
	for rows.Next() {
		var key, raw string
		if err = rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return err
		}
		var entry lyricsFile
		if json.Unmarshal([]byte(raw), &entry) == nil && key == lyricsFileKey(entry.Path) {
			files[entry.Path] = entry
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = a.reads.QueryContext(ctx, `SELECT s.id,s.output,m.signature,l.value FROM sources s JOIN managed m ON m.source_id=s.id AND m.path=s.output AND m.kind='audio' JOIN meta l ON l.key='lyrics-script:'||s.id WHERE s.output_present=1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	scripts := map[int64]lyricsScriptRecord{}
	for rows.Next() {
		var id int64
		var output, signature, raw string
		if err = rows.Scan(&id, &output, &signature, &raw); err != nil {
			return err
		}
		var record lyricsScriptRecord
		if json.Unmarshal([]byte(raw), &record) != nil {
			continue
		}
		entry, ok := files[record.Path]
		if record.Version == 1 && record.Output == output && record.AudioSignature == signature && record.Path == lyricsRel(output) && ok && entry.ID == id && entry.Pending == "" && record.Signature == entry.Signature && (record.Script == "simplified" || record.Script == "traditional" || record.Script == "unknown") {
			scripts[id] = record
		}
	}
	for i := range sources {
		sources[i].LyricsScript = ""
		if record, ok := scripts[sources[i].ID]; ok && sources[i].OutputPresent && sources[i].Output == record.Output {
			sources[i].LyricsScript = record.Script
		}
	}
	return rows.Err()
}

// Inspect only registered output sidecars. No source storage, tags, audio reads
// or provider requests are needed to backfill existing lyrics.
func (a *App) inspectLyricsScript(ctx context.Context, settings Settings, id int64) (string, error) {
	if err := a.lockFiles(ctx); err != nil {
		return "", err
	}
	defer a.files.Unlock()
	current, err := a.settingsContext(ctx)
	if err != nil {
		return "", err
	}
	if !current.Enabled || current.Output != settings.Output || current.Source != settings.Source {
		return "", errors.New("Artifact or library settings changed during lyrics search; result discarded")
	}
	source, err := a.source(id)
	if err != nil {
		return "", err
	}
	var entry lyricsFile
	var text string
	present := false
	if source.Output != "" && source.OutputPresent {
		entry, text, present, err = a.readLyricsFile(ctx, current, lyricsRel(source.Output))
	}
	if err != nil || !present {
		_, clearErr := a.db.ExecContext(ctx, "DELETE FROM meta WHERE key=?", lyricsScriptPrefix+strconv.FormatInt(id, 10))
		return "", errors.Join(err, clearErr)
	}
	if entry.ID != id {
		return "", errors.New("Invalid lyrics ownership annotation")
	}
	if err = a.recordLyricsScript(ctx, source, entry, text); errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("Playable artifact is no longer available")
	}
	return lyricsScript(text), err
}
