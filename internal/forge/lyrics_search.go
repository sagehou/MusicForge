package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const lyricsOptionsKey = "lyrics:options"

type lyricsOptions struct {
	AutoSearch bool `json:"auto_search"`
}

type LyricsStatus struct {
	State     string          `json:"state"`
	Output    string          `json:"output"`
	Signature string          `json:"signature"`
	Path      string          `json:"path,omitempty"`
	Provider  string          `json:"provider,omitempty"`
	Error     string          `json:"error,omitempty"`
	Checked   int64           `json:"checked"`
	Attempts  []lyricsAttempt `json:"attempts,omitempty"`
}

type lyricsReport struct {
	State     string `json:"state"`
	Total     int    `json:"total"`
	Processed int    `json:"processed"`
	Found     int    `json:"found"`
	NotFound  int    `json:"not_found"`
	Failed    int    `json:"failed"`
	Review    int    `json:"needs_review"`
	Current   string `json:"current,omitempty"`
	Error     string `json:"error,omitempty"`
	Stopping  bool   `json:"stopping"`
}

type lyricsState struct {
	lyricsMu      sync.Mutex
	lyricsWG      sync.WaitGroup
	lyricsReport  *lyricsReport
	lyricsCancel  context.CancelFunc
	lyricsNext    time.Time
	lyricsRetryAt time.Time
	lyricsHTTP    *http.Client
	lyricsGates   map[string]chan struct{}
	lyricsRetries map[string]time.Time
	lyricsChoices map[string]*lyricsChoiceSet
}

func (a *App) lyricsOptions(ctx context.Context) (lyricsOptions, error) {
	var options lyricsOptions
	raw, err := a.metaContext(ctx, lyricsOptionsKey)
	if errors.Is(err, sql.ErrNoRows) {
		return options, nil // Online lookup is opt-in, including after an upgrade.
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &options)
	}
	return options, err
}

func (a *App) getLyrics(w http.ResponseWriter, r *http.Request) {
	options, err := a.lyricsOptions(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	a.lyricsMu.Lock()
	defer a.lyricsMu.Unlock()
	report := lyricsReport{State: "idle"}
	if a.lyricsReport != nil {
		report = *a.lyricsReport
	}
	respond(w, 200, map[string]any{"options": options, "report": report})
}

func (a *App) putLyricsOptions(w http.ResponseWriter, r *http.Request) {
	var options lyricsOptions
	if !decode(w, r, &options) {
		return
	}
	a.lyricsMu.Lock()
	defer a.lyricsMu.Unlock()
	raw, _ := json.Marshal(options)
	if err := a.setMeta(lyricsOptionsKey, string(raw)); err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, options)
}

func (a *App) requestLyrics(w http.ResponseWriter, r *http.Request) {
	var body struct {
		selection
		Refresh bool `json:"refresh"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.IDs) > 10000 || (!body.All && len(body.IDs) == 0) {
		apiError(w, 400, errors.New("Select between 1 and 10000 tracks, or all artifacts"))
		return
	}
	ids := map[int64]bool{}
	for _, id := range body.IDs {
		if id < 1 {
			apiError(w, 400, errors.New("Library record IDs must be positive"))
			return
		}
		ids[id] = true
	}
	s, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if !s.Enabled {
		apiError(w, 409, errors.New("Enable the library before searching for lyrics"))
		return
	}
	all, err := a.allSources()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	var chosen []Source
	for _, source := range all {
		if (body.All || ids[source.ID]) && source.Output != "" && source.OutputPresent {
			chosen = append(chosen, source)
		}
	}
	if len(chosen) == 0 {
		apiError(w, 409, errors.New("No playable artifacts selected"))
		return
	}
	if !a.startLyrics(s, chosen, body.Refresh, false) {
		apiError(w, 409, errors.New("Lyrics search already running or application is stopping"))
		return
	}
	respond(w, 202, map[string]int{"selected": len(chosen)})
}

func (a *App) stopLyrics(w http.ResponseWriter, r *http.Request) {
	a.lyricsMu.Lock()
	defer a.lyricsMu.Unlock()
	// Stopping must also prevent the automatic scheduler from immediately
	// restarting unfinished tracks, including after a container restart.
	if err := a.setMeta(lyricsOptionsKey, `{"auto_search":false}`); err != nil {
		apiError(w, 500, err)
		return
	}
	if a.lyricsCancel != nil {
		a.lyricsReport.Stopping = true
		a.lyricsCancel()
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (a *App) annotateLyrics(ctx context.Context, sources []Source) error {
	rows, err := a.reads.QueryContext(ctx, `SELECT s.id,s.output,m.signature,l.value FROM sources s JOIN managed m ON m.source_id=s.id AND m.path=s.output AND m.kind='audio' JOIN meta l ON l.key='lyrics-status:'||s.id WHERE s.output_present=1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	statuses := map[int64]LyricsStatus{}
	for rows.Next() {
		var id int64
		var output, signature, raw string
		var status LyricsStatus
		if err = rows.Scan(&id, &output, &signature, &raw); err != nil {
			return err
		}
		if json.Unmarshal([]byte(raw), &status) == nil && status.Output == output && status.Signature == signature {
			statuses[id] = status
		}
	}
	for i := range sources {
		if status, ok := statuses[sources[i].ID]; ok && sources[i].Output == status.Output && sources[i].OutputPresent {
			copy := status
			sources[i].Lyrics = &copy
		}
	}
	return rows.Err()
}

func (a *App) scheduleLyrics(s Settings) {
	a.lyricsMu.Lock()
	if a.lyricsCancel != nil || time.Now().Before(a.lyricsNext) {
		a.lyricsMu.Unlock()
		return
	}
	a.lyricsNext = time.Now().Add(30 * time.Second)
	a.lyricsMu.Unlock()
	options, err := a.lyricsOptions(a.ioContext)
	if err != nil || !options.AutoSearch {
		return
	}
	sources, err := a.allSources()
	if err != nil || a.annotateLyrics(a.ioContext, sources) != nil {
		return
	}
	var chosen []Source
	for _, source := range sources {
		if source.Output == "" || !source.OutputPresent {
			continue
		}
		status := source.Lyrics
		if status == nil || (status.State == "failed" && time.Since(time.Unix(status.Checked, 0)) >= time.Hour) ||
			(status.State == "not_found" || status.State == "instrumental" || status.State == "needs_review") && time.Since(time.Unix(status.Checked, 0)) >= 7*24*time.Hour {
			chosen = append(chosen, source)
		}
	}
	if len(chosen) > 0 {
		a.startLyrics(s, chosen, false, true)
	}
}

func (a *App) startLyrics(s Settings, sources []Source, refresh, automatic bool) bool {
	a.lyricsMu.Lock()
	defer a.lyricsMu.Unlock()
	if a.lyricsCancel != nil || a.ioContext.Err() != nil {
		return false
	}
	if automatic {
		// Stop/options writes share this lock: a previously selected automatic
		// batch must not start after the administrator disables filling.
		options, err := a.lyricsOptions(a.ioContext)
		if err != nil || !options.AutoSearch {
			return false
		}
	}
	ctx, cancel := context.WithCancel(a.ioContext)
	report := &lyricsReport{State: "running", Total: len(sources)}
	a.lyricsReport, a.lyricsCancel = report, cancel
	a.lyricsWG.Add(1)
	go func() {
		defer a.lyricsWG.Done()
		defer cancel()
		for _, source := range sources {
			if ctx.Err() != nil {
				break
			}
			a.lyricsMu.Lock()
			report.Current = source.Rel
			a.lyricsMu.Unlock()
			status := a.maintainLyrics(ctx, s, source.ID, refresh)
			if ctx.Err() != nil {
				break
			}
			status.Checked = time.Now().Unix()
			raw, _ := json.Marshal(status)
			if err := a.setMeta("lyrics-status:"+strconv.FormatInt(source.ID, 10), string(raw)); err != nil {
				status.State, status.Error = "failed", err.Error()
			}
			a.lyricsMu.Lock()
			report.Processed++
			switch status.State {
			case "ready":
				report.Found++
			case "not_found", "instrumental":
				report.NotFound++
			case "needs_review":
				report.Review++
			default:
				report.Failed++
				report.Error = status.Error
			}
			a.lyricsMu.Unlock()
			a.logger.Info("lyrics checked", "source", source.Rel, "state", status.State, "provider", status.Provider, "error", status.Error)
		}
		a.lyricsMu.Lock()
		defer a.lyricsMu.Unlock()
		report.State, report.Current = "complete", ""
		if ctx.Err() != nil {
			report.State = "stopped"
		}
		a.lyricsCancel = nil
	}()
	return true
}

func (a *App) maintainLyrics(ctx context.Context, s Settings, id int64, refresh bool) (status LyricsStatus) {
	status.State = "failed"
	target, err := a.captureLyricsTarget(ctx, id)
	status.Output, status.Signature = target.Source.Output, target.Signature
	if target.FilePresent {
		status.Path, status.Provider = target.File.Path, target.File.Provider
	}
	if err == nil && (target.Settings.Source != s.Source || target.Settings.Output != s.Output) {
		err = errors.New("Artifact or library settings changed during lyrics search; result discarded")
	}
	if err == nil {
		var text string
		text, err = a.sourceLyrics(ctx, s, target.Source)
		provider := "source"
		if err == nil && text == "" && target.FilePresent && !refresh && target.File.SourceHash == target.Source.BuiltHash {
			status.State = "ready"
			return
		}
		if err == nil && text == "" {
			text, provider, status.State, status.Attempts, err = a.searchOnlineLyrics(ctx, target.Source)
			if text == "" && err == nil {
				if target.FilePresent {
					if target.File.SourceHash == target.Source.BuiltHash {
						status.State = "ready"
					}
					status.Error = "No synchronized match found; existing LRC retained"
				}
				return
			}
		}
		if err == nil {
			err = a.publishLyricsTarget(ctx, target, text, provider)
		}
		if err == nil {
			status.State, status.Path, status.Provider = "ready", lyricsRel(target.Source.Output), provider
		}
	}
	if err != nil {
		status.State, status.Error = "failed", err.Error()
	}
	return
}

type lrclibRecord struct {
	Title        string  `json:"trackName"`
	Artist       string  `json:"artistName"`
	Album        string  `json:"albumName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	Synced       string  `json:"syncedLyrics"`
}

func sameLyricsTag(a, b string) bool {
	return lyricsTag(a) != "" && lyricsTag(a) == lyricsTag(b)
}

func matchingLyrics(record lrclibRecord, source Source) bool {
	return candidateMatches(lyricsCandidate{Title: record.Title, Artist: record.Artist, Album: record.Album, Duration: record.Duration}, source)
}

func (a *App) searchLRCLIB(ctx context.Context, source Source) (string, string, error) {
	if err := validLyricsSearch(source); err != nil {
		return "", "", err
	}
	candidates, instrumental, err := a.providerCandidates(ctx, "lrclib", source, "", false)
	if err != nil {
		return "", "", err
	}
	if instrumental {
		return "", "instrumental", nil
	}
	return a.chooseLyrics(ctx, source, candidates)
}

func (a *App) lyricsGET(ctx context.Context, endpoint string, target any) (int, error) {
	return a.lyricsRequest(ctx, "lrclib", "GET", "https://lrclib.net"+endpoint, nil, target)
}
