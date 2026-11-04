package forge

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
)

const lyricsBatchKey = "lyrics:batch"

type lyricsBatchTrack struct {
	ID int64 `json:"id"`
	Path string `json:"path"`
}

// Optional schema-2 metadata; older workers retain it and ignore the batch UI.
type lyricsBatch struct {
	Version   int                `json:"version"`
	Source    string             `json:"source"`
	Output    string             `json:"output"`
	Options   lyricsBatchOptions `json:"options"`
	Remaining []lyricsBatchTrack            `json:"remaining"`
	Report    lyricsReport       `json:"report"`
}

// Caller holds lyricsMu, except during startup before workers are started.
func (a *App) saveLyricsBatch() error {
	batch := a.lyricsBatch
	batch.Report.Remaining = len(batch.Remaining)
	batch.Report.Resumable = batch.Report.State != "running" && len(batch.Remaining) > 0
	raw, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	return a.setMeta(lyricsBatchKey, string(raw))
}

func (a *App) restoreLyricsBatch() error {
	raw, err := a.meta(lyricsBatchKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var batch lyricsBatch
	if err = json.Unmarshal([]byte(raw), &batch); err != nil {
		return err
	}
	if batch.Version != 1 {
		return errors.New("Unsupported lyrics batch version")
	}
	if batch.Report.State == "running" {
		batch.Report.State = "stopped"
	}
	batch.Report.Current, batch.Report.Stopping = "", false
	batch.Report.Remaining = len(batch.Remaining)
	batch.Report.Resumable = len(batch.Remaining) > 0
	a.lyricsBatch, a.lyricsReport = &batch, &batch.Report
	return nil
}

func (a *App) resumeLyrics(w http.ResponseWriter, r *http.Request) {
	a.lyricsMu.Lock()
	defer a.lyricsMu.Unlock()
	batch := a.lyricsBatch
	if batch == nil || len(batch.Remaining) == 0 || a.lyricsCancel != nil {
		apiError(w, 409, errors.New("No stopped lyrics batch to continue"))
		return
	}
	s, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if !s.Enabled || s.Source != batch.Source || s.Output != batch.Output {
		apiError(w, 409, errors.New("Restore the batch's library paths and enable the library before continuing"))
		return
	}
	all, err := a.sourcesWhereContext(r.Context(), " WHERE output<>'' AND output_present=1")
	if err != nil {
		apiError(w, 500, err)
		return
	}
	byID := make(map[int64]Source, len(all))
	for _, source := range all {
		byID[source.ID] = source
	}
	sources := make([]Source, 0, len(batch.Remaining))
	for _, track := range batch.Remaining {
		if source, ok := byID[track.ID]; ok && source.Rel == track.Path {
			sources = append(sources, source)
		}
	}
	selected, started := a.startLyricsLocked(s, sources, batch.Options, false, batch)
	if !started {
		apiError(w, 409, errors.New("Could not continue lyrics batch; check settings and configuration storage"))
		return
	}
	respond(w, 202, map[string]int{"selected": selected})
}
