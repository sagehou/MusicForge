package forge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// A local, explicitly selected placeholder, never an online search result.
const instrumentalLRC = "[00:00.00]纯音乐，请欣赏\n"

type lyricsTarget struct {
	Settings                 Settings
	Source                   Source
	Signature, FileSignature string
	File                     lyricsFile
	FilePresent              bool
	FileScript               string
	Instrumental             bool
}

func (a *App) captureLyricsTarget(ctx context.Context, id int64) (target lyricsTarget, err error) {
	if err = a.lockFiles(ctx); err != nil {
		return
	}
	defer a.files.Unlock()
	target.Settings, err = a.settingsContext(ctx)
	if err != nil {
		return
	}
	if !target.Settings.Enabled {
		err = errors.New("Enable the library before searching for lyrics")
		return
	}
	if err = a.storageContext(ctx, target.Settings); err != nil {
		return
	}
	if err = a.ensureRecovery(ctx, target.Settings); err != nil {
		return
	}
	target.Source, err = a.source(id)
	if err != nil {
		return
	}
	source := target.Source
	if source.Output == "" || !source.OutputPresent || source.Hash != source.BuiltHash {
		err = errors.New("Artifact missing or waiting for rebuild; search again after conversion")
		return
	}
	err = a.reads.QueryRowContext(ctx, "SELECT signature FROM managed WHERE kind='audio' AND source_id=? AND path=?", id, source.Output).Scan(&target.Signature)
	if err != nil {
		return
	}
	path, pathErr := safePath(target.Settings.Output, source.Output)
	if pathErr != nil {
		err = pathErr
		return
	}
	if info, statErr := os.Lstat(path); statErr != nil || !info.Mode().IsRegular() {
		err = errors.New("Playable artifact is no longer available")
		return
	}
	var text string
	target.File, text, target.FilePresent, err = a.readLyricsFile(ctx, target.Settings, lyricsRel(source.Output))
	if target.FilePresent {
		target.FileSignature = digest(text)
		target.FileScript = lyricsScript(text)
		target.Instrumental = text == instrumentalLRC
	}
	return
}

func (a *App) publishLyricsTarget(ctx context.Context, target lyricsTarget, text, provider string) error {
	if err := a.lockFiles(ctx); err != nil {
		return err
	}
	defer a.files.Unlock()
	settings, err := a.settingsContext(ctx)
	if err != nil {
		return err
	}
	current, err := a.source(target.Source.ID)
	if err != nil {
		return err
	}
	var signature string
	err = a.reads.QueryRowContext(ctx, "SELECT signature FROM managed WHERE path=? AND source_id=? AND kind='audio'", current.Output, current.ID).Scan(&signature)
	if err != nil {
		return err
	}
	if !settings.Enabled || settings.Source != target.Settings.Source || settings.Output != target.Settings.Output || current.Output != target.Source.Output || current.Rel != target.Source.Rel || current.Hash != target.Source.Hash || current.BuiltHash != target.Source.BuiltHash || !current.OutputPresent || signature != target.Signature {
		return errors.New("Artifact or library settings changed during lyrics search; result discarded")
	}
	if err = a.storageContext(ctx, settings); err != nil {
		return err
	}
	path, err := safePath(settings.Output, current.Output)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(path); statErr != nil || !info.Mode().IsRegular() {
		return errors.New("Playable artifact is no longer available")
	}
	_, existing, present, err := a.readLyricsFile(ctx, settings, lyricsRel(current.Output))
	if err != nil {
		return err
	}
	if present != target.FilePresent || present && digest(existing) != target.FileSignature {
		return errors.New("LRC changed during publication; file preserved")
	}
	return a.writeLyricsFile(ctx, settings, current, text, provider)
}

// Live selection tokens are short-lived memory only. Search records are
// separate optional metadata and are rebound to a fresh target on restoration.
type lyricsChoiceSet struct {
	Created        time.Time
	Target         lyricsTarget
	Provider       string
	Query          string
	Searched       int64
	Candidates     []lyricsCandidate
	PreviewIndex   int
	PreviewVersion uint64
	Preview        *lyricsText
}

func (a *App) trimLyricsChoices() {
	for token, set := range a.lyricsChoices {
		if time.Since(set.Created) >= 10*time.Minute {
			delete(a.lyricsChoices, token)
		}
	}
	for {
		bytes, oldest := 0, ""
		for token, set := range a.lyricsChoices {
			if oldest == "" || set.Created.Before(a.lyricsChoices[oldest].Created) {
				oldest = token
			}
			for _, candidate := range set.Candidates {
				if candidate.text != nil {
					bytes += len(candidate.text.Original) + len(candidate.text.Translation) + len(candidate.text.Romanization)
				}
			}
			if set.Preview != nil {
				bytes += len(set.Preview.Original) + len(set.Preview.Translation) + len(set.Preview.Romanization)
			}
		}
		if len(a.lyricsChoices) <= 16 && bytes <= 16*maxLyricsBytes {
			return
		}
		delete(a.lyricsChoices, oldest)
	}
}

func (a *App) requestLyricsCandidates(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 400, errors.New("invalid source ID"))
		return
	}
	var body struct {
		Provider string `json:"provider"`
		Query    string `json:"query"`
		Refresh  bool   `json:"refresh"`
	}
	if !decode(w, r, &body) {
		return
	}
	known := false
	for _, provider := range lyricsProviders {
		known = known || body.Provider == provider
	}
	if !known || len(body.Query) > 2000 || utf8.RuneCountInString(body.Query) > 500 || !utf8.ValidString(body.Query) || strings.ContainsAny(body.Query, "\x00\r\n") {
		apiError(w, 400, errors.New("Invalid lyrics provider or search query"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	stop := context.AfterFunc(a.ioContext, cancel)
	defer stop()
	target, err := a.captureLyricsTarget(ctx, id)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	if strings.TrimSpace(body.Query) == "" && (lyricsTag(target.Source.Title) == "" || lyricsTag(target.Source.Artist) == "") {
		apiError(w, 400, errors.New("Enter a title or artist to search for lyrics"))
		return
	}
	candidates, _, cached, searched, err := a.cachedLyricsCandidates(ctx, target, body.Provider, body.Query, true, body.Refresh)
	if err != nil {
		apiError(w, 502, err)
		return
	}
	respond(w, 200, a.newLyricsChoices(target, body.Provider, body.Query, candidates, cached, searched))
}

type lyricsCandidateGroup struct {
	Provider   string            `json:"provider"`
	Token      string            `json:"token"`
	Candidates []lyricsCandidate `json:"candidates"`
	Cached     bool              `json:"cached"`
	Searched   int64             `json:"searched"`
}

func (a *App) newLyricsChoices(target lyricsTarget, provider, query string, candidates []lyricsCandidate, cached bool, searched int64) lyricsCandidateGroup {
	// JSON must encode an empty result as [], so each provider can finish alone.
	if candidates == nil {
		candidates = []lyricsCandidate{}
	}
	token := randomToken()
	a.lyricsMu.Lock()
	if a.lyricsChoices == nil {
		a.lyricsChoices = map[string]*lyricsChoiceSet{}
	}
	a.lyricsChoices[token] = &lyricsChoiceSet{Created: time.Now(), Target: target, Provider: provider, Query: query,
		Searched: searched, Candidates: candidates, PreviewIndex: -1}
	a.trimLyricsChoices()
	a.lyricsMu.Unlock()
	return lyricsCandidateGroup{Provider: provider, Token: token, Candidates: candidates, Cached: cached, Searched: searched}
}

func (a *App) getLyricsCandidates(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 400, errors.New("invalid source ID"))
		return
	}
	target, err := a.captureLyricsTarget(r.Context(), id)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	records, err := a.restoreLyricsSearches(r.Context(), target)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	query := lyricsSearchQuery(target.Source, "")
	if len(records) > 0 {
		query = records[0].Query
	}
	groups := []lyricsCandidateGroup{}
	for _, provider := range lyricsProviders {
		for _, record := range records {
			if record.Query == query && record.Provider == provider {
				groups = append(groups, a.newLyricsChoices(target, provider, query, record.candidates(target.Source), true, record.Searched))
				break
			}
		}
	}
	// Reuse preview/publication tokens and their conflict checks. Keep the
	// published provider vocabulary: this is a local LRC, not a new service.
	instrumental := a.newLyricsChoices(target, "source", "", []lyricsCandidate{{
		Provider: "source", Title: target.Source.Title, Artist: target.Source.Artist,
		Album: target.Source.Album, Duration: target.Source.Duration,
		text: &lyricsText{Original: instrumentalLRC},
	}}, false, 0)
	respond(w, 200, map[string]any{"query": query, "groups": groups, "instrumental": instrumental})
}

type lyricsChoiceRequest struct {
	Token        string `json:"token"`
	Index        int    `json:"index"`
	Translation  bool   `json:"translation"`
	Romanization bool   `json:"romanization"`
}

func (a *App) lyricsChoice(w http.ResponseWriter, r *http.Request) (lyricsChoiceRequest, lyricsChoiceSet, bool) {
	var body lyricsChoiceRequest
	if !decode(w, r, &body) {
		return body, lyricsChoiceSet{}, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	a.lyricsMu.Lock()
	defer a.lyricsMu.Unlock()
	a.trimLyricsChoices()
	set := a.lyricsChoices[body.Token]
	if err != nil || id < 1 || len(body.Token) > 128 || set == nil || set.Target.Source.ID != id || body.Index < 0 || body.Index >= len(set.Candidates) {
		apiError(w, 409, errors.New("Lyrics candidates expired; search again"))
		return body, lyricsChoiceSet{}, false
	}
	snapshot := *set
	snapshot.Candidates = append([]lyricsCandidate(nil), set.Candidates...)
	return body, snapshot, true
}

func (a *App) previewLyricsCandidate(w http.ResponseWriter, r *http.Request) {
	body, set, ok := a.lyricsChoice(w, r)
	if !ok {
		return
	}
	a.lyricsMu.Lock()
	current := a.lyricsChoices[body.Token]
	if current == nil {
		a.lyricsMu.Unlock()
		apiError(w, 409, errors.New("Lyrics candidates expired; search again"))
		return
	}
	current.PreviewVersion++
	version := current.PreviewVersion
	current.Preview, current.PreviewIndex = nil, -1
	a.lyricsMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	stop := context.AfterFunc(a.ioContext, cancel)
	defer stop()
	text, err := a.candidateLyrics(ctx, set.Candidates[body.Index])
	if ctx.Err() != nil {
		apiError(w, 502, ctx.Err())
		return
	}
	if err != nil && text.diagnostic == nil {
		apiError(w, 502, err)
		return
	}
	a.lyricsMu.Lock()
	a.trimLyricsChoices()
	current = a.lyricsChoices[body.Token]
	if current == nil {
		a.lyricsMu.Unlock()
		apiError(w, 409, errors.New("Lyrics candidates expired; search again"))
		return
	}
	if current.PreviewVersion != version {
		a.lyricsMu.Unlock()
		apiError(w, 409, errors.New("Lyrics preview changed; select the candidate again"))
		return
	}
	if text.diagnostic != nil {
		a.lyricsMu.Unlock()
		// A diagnostic is a successful inspection, not a usable/savable preview.
		// Keep it out of the choice set and the published version-1 search cache.
		respond(w, 200, map[string]any{"original": "", "diagnostic": text.diagnostic})
		return
	}
	if text.Original == "" {
		a.lyricsMu.Unlock()
		apiError(w, 422, errors.New("No synchronized LRC lines found"))
		return
	}
	current.PreviewIndex, current.Preview = body.Index, &text
	current.Candidates[body.Index].text = &text
	candidates := append([]lyricsCandidate(nil), current.Candidates...)
	a.trimLyricsChoices()
	a.lyricsMu.Unlock()
	// Keep downloaded previews with their search so reopening never downloads
	// the same pending result again. Publication still requires explicit preview.
	if set.Provider != "source" {
		if err = a.storeLyricsSearch(ctx, set.Target, set.Provider, set.Query, candidates, false, set.Searched); err != nil {
			apiError(w, 500, err)
			return
		}
	}
	respond(w, 200, map[string]any{"original": text.Original, "translation": text.Translation,
		"romanization": text.Romanization, "script": lyricsScript(text.Original), "translation_script": lyricsScript(text.Translation)})
}

func (a *App) applyLyricsCandidate(w http.ResponseWriter, r *http.Request) {
	body, set, ok := a.lyricsChoice(w, r)
	if !ok {
		return
	}
	if set.Preview == nil || set.PreviewIndex != body.Index {
		apiError(w, 409, errors.New("Preview the lyrics candidate before saving"))
		return
	}
	text, err := combineLyrics(*set.Preview, body.Translation, body.Romanization)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	candidate := set.Candidates[body.Index]
	if err = a.publishLyricsTarget(r.Context(), set.Target, text, candidate.Provider); err != nil {
		apiError(w, 409, err)
		return
	}
	a.discardLyricsSearches(set.Target.Source.ID)
	status := LyricsStatus{State: "ready", Output: set.Target.Source.Output, Signature: set.Target.Signature,
		Path: lyricsRel(set.Target.Source.Output), Provider: candidate.Provider, Checked: time.Now().Unix(), Instrumental: text == instrumentalLRC}
	raw, _ := json.Marshal(status)
	if err = a.setMeta("lyrics-status:"+strconv.FormatInt(set.Target.Source.ID, 10), string(raw)); err != nil {
		apiError(w, 500, err)
		return
	}
	a.lyricsMu.Lock()
	delete(a.lyricsChoices, body.Token)
	a.lyricsMu.Unlock()
	respond(w, 200, status)
}
