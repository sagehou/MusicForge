package forge

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

type lyricsProviderDiagnostic struct {
	Provider   string `json:"provider"`
	Enabled    bool   `json:"enabled"`
	State      string `json:"state"`
	Checked    int64  `json:"checked"`
	Latency    int64  `json:"latency_ms"`
	RetryAt    int64  `json:"retry_at,omitempty"`
	Candidates int    `json:"candidates"`
	SourceID   int64  `json:"source_id,omitempty"`
	Title      string `json:"title,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (a *App) lyricsProviderCooldowns() map[string]time.Time {
	a.lyricsMu.Lock()
	until := map[string]time.Time{}
	for _, provider := range lyricsProviders {
		until[provider] = a.lyricsRetries[provider]
	}
	until["lrclib"] = a.lyricsRetryAt
	netease := a.netease
	a.lyricsMu.Unlock()
	// A status read never waits for provider I/O or reads credentials from disk.
	if netease != nil {
		select {
		case netease.gate <- struct{}{}:
			if netease.retryAt.After(until["netease"]) {
				until["netease"] = netease.retryAt
			}
			<-netease.gate
		default:
		}
	}
	return until
}

func (a *App) recordProviderDiagnostic(ctx context.Context, provider string, source Source, started time.Time, candidates int, err error, cooling bool) {
	a.lyricsMu.Lock()
	defer a.lyricsMu.Unlock()
	a.providerChecking[provider]--
	if errors.Is(ctx.Err(), context.Canceled) || cooling && err != nil {
		return
	}
	if a.providerDiagnostics == nil {
		a.providerDiagnostics = map[string]lyricsProviderDiagnostic{}
	}
	status := lyricsProviderDiagnostic{Provider: provider, State: "ok", Checked: time.Now().Unix(), Latency: time.Since(started).Milliseconds(), Candidates: candidates, SourceID: source.ID, Title: source.Title, Artist: source.Artist}
	if err != nil {
		status.State, status.Error = "failed", err.Error()
	}
	a.providerDiagnostics[provider] = status
}

func (a *App) lyricsProviderStatus(ctx context.Context) ([]lyricsProviderDiagnostic, error) {
	enabled, err := a.lyricsProviderOrder(ctx)
	if err != nil {
		return nil, err
	}
	cooldowns := a.lyricsProviderCooldowns()
	a.lyricsMu.Lock()
	defer a.lyricsMu.Unlock()
	result := []lyricsProviderDiagnostic{}
	for _, provider := range lyricsProviders {
		status := a.providerDiagnostics[provider]
		status.Provider, status.Enabled = provider, slices.Contains(enabled, provider)
		if status.State == "" {
			status.State = "unknown"
		}
		if until := cooldowns[provider]; until.After(time.Now().Add(2 * time.Second)) {
			status.State, status.RetryAt = "cooldown", until.Unix()
		}
		if a.providerChecking[provider] > 0 {
			status.State = "checking"
		}
		result = append(result, status)
	}
	return result, nil
}

func (a *App) getLyricsProviderStatus(w http.ResponseWriter, r *http.Request) {
	status, err := a.lyricsProviderStatus(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, status)
}

func (a *App) checkLyricsProvider(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if !slices.Contains(lyricsProviders, provider) {
		apiError(w, 400, errors.New("Unknown lyrics provider"))
		return
	}
	var body struct {
		SourceID int64 `json:"source_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.SourceID <= 0 {
		apiError(w, 400, errors.New("invalid source ID"))
		return
	}
	source, err := readSource(a.reads.QueryRowContext(r.Context(), "SELECT "+sourceCols+" FROM sources WHERE id=?", body.SourceID))
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, 404, errors.New("Library track is no longer available"))
		return
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if err = validLyricsSearch(source); err != nil {
		apiError(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	_, _, _ = a.providerCandidates(ctx, provider, source, "", true)
	status, err := a.lyricsProviderStatus(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, status)
}

// Display only: choices and persisted search records retain the original
// candidate and matching rules. Alias-aware identity comes from the same matcher.
func lyricsMatchReasons(candidate lyricsCandidate, source Source) []string {
	reasons := []string{}
	title, artist := candidateIdentity(candidate, source)
	versions := func(value string) string {
		return strings.ToLower(strings.Join(lyricsVersionWords.FindAllString(normalizeLyricsMetadata(value), -1), "/"))
	}
	if !title {
		if versions(candidate.Title) != versions(source.Title) {
			reasons = append(reasons, "version")
		} else {
			reasons = append(reasons, "title")
		}
	}
	if !artist {
		reasons = append(reasons, "artist")
	}
	if candidate.DurationDelta == nil {
		reasons = append(reasons, "duration_unknown")
	} else if *candidate.DurationDelta > 10 {
		reasons = append(reasons, "duration")
	}
	if source.Album != "" && !sameLyricsTag(candidate.Album, source.Album) {
		reasons = append(reasons, "album")
	}
	return reasons
}
