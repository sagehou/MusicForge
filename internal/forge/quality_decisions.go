package forge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type qualityDecision struct {
	Version int    `json:"version"`
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	State   string `json:"state"`
	Reason  string `json:"reason"`
	Binding string `json:"binding"`
	Updated int64  `json:"updated"`
}

func qualityKind(kind string) bool {
	switch kind {
	case "metadata", "duration", "numbering", "duplicates", "failed", "damaged", "lyrics", "covers":
		return true
	}
	return false
}

// Bind decisions to indexed facts, not check times or transient job progress.
func qualityBinding(s Settings, source Source) string {
	state := ""
	if source.Lyrics != nil {
		state = source.Lyrics.State
	}
	raw, _ := json.Marshal([]any{s.Source, s.Output, source.Rel, source.Hash, source.BuiltHash, source.Output, source.Present, source.OutputPresent, source.Artist, source.Album, source.Title, source.Track, source.Disc, source.Metadata, source.Duration, source.Error, source.PossiblyDamaged, source.ArtworkURL, state})
	return digest(string(raw))
}

func (a *App) qualityDecisions(ctx context.Context, s Settings, sources []Source) ([]qualityDecision, error) {
	bindings := map[int64]string{}
	for _, source := range sources {
		bindings[source.ID] = qualityBinding(s, source)
	}
	rows, err := a.reads.QueryContext(ctx, "SELECT value FROM meta WHERE key GLOB 'quality-decision:*'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []qualityDecision{}
	for rows.Next() {
		var raw string
		var decision qualityDecision
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &decision) != nil || decision.Version != 1 {
			return nil, errors.New("Invalid quality decision")
		}
		if bindings[decision.ID] == decision.Binding {
			result = append(result, decision)
		}
	}
	return result, rows.Err()
}

func (a *App) getQualityDecisions(w http.ResponseWriter, r *http.Request) {
	s, err := a.settingsContext(r.Context())
	var sources []Source
	if err == nil {
		sources, err = a.sourcesWithStatusContext(r.Context())
	}
	if err == nil {
		err = a.libraryArtworkURLs(r.Context(), sources)
	}
	if err == nil {
		err = a.annotateLyrics(r.Context(), sources)
	}
	var decisions []qualityDecision
	if err == nil {
		decisions, err = a.qualityDecisions(r.Context(), s, sources)
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, decisions)
}

func (a *App) saveQualityDecisions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []int64 `json:"ids"`
		Kind   string  `json:"kind"`
		State  string  `json:"state"`
		Reason string  `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if !qualityKind(body.Kind) || len(body.IDs) == 0 || len(body.IDs) > 1000 || (body.State != "open" && body.State != "todo" && body.State != "ignored") || !utf8.ValidString(body.Reason) || len(body.Reason) > 1000 || strings.ContainsRune(body.Reason, 0) || body.State == "ignored" && body.Reason == "" {
		apiError(w, 400, errors.New("Choose up to 1000 issues and a valid decision; ignoring requires a reason of at most 1000 bytes"))
		return
	}
	if err := a.lockFiles(r.Context()); err != nil {
		apiError(w, 503, err)
		return
	}
	defer a.files.Unlock()
	s, err := a.settingsContext(r.Context())
	var sources []Source
	if err == nil {
		sources, err = a.sourcesWithStatusContext(r.Context())
	}
	if err == nil {
		err = a.libraryArtworkURLs(r.Context(), sources)
	}
	if err == nil {
		err = a.annotateLyrics(r.Context(), sources)
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	byID := map[int64]Source{}
	for _, source := range sources {
		byID[source.ID] = source
	}
	for _, id := range body.IDs {
		if _, ok := byID[id]; !ok {
			apiError(w, 409, errors.New("Selected issue is no longer indexed"))
			return
		}
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	defer tx.Rollback()
	for _, id := range body.IDs {
		key := "quality-decision:" + body.Kind + ":" + strconv.FormatInt(id, 10)
		if body.State == "open" {
			_, err = tx.ExecContext(r.Context(), "DELETE FROM meta WHERE key=?", key)
		} else {
			decision := qualityDecision{Version: 1, ID: id, Kind: body.Kind, State: body.State, Reason: body.Reason, Binding: qualityBinding(s, byID[id]), Updated: time.Now().Unix()}
			raw, _ := json.Marshal(decision)
			_, err = tx.ExecContext(r.Context(), "INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(raw))
		}
		if err != nil {
			apiError(w, 500, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, map[string]int{"updated": len(body.IDs)})
}
