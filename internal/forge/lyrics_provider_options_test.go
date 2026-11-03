package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLyricsProvidersValidatePersistAndSurviveLegacyWriters(t *testing.T) {
	a, _ := testApp(t)
	ctx := context.Background()
	request := lyricsSession(t, a)
	check := func(app *App, automatic bool, score int, providers []string) {
		t.Helper()
		options, err := app.lyricsOptions(ctx)
		if err != nil || options.AutoSearch != automatic || options.MinScore != score || options.Providers == nil || !slices.Equal(options.Providers, providers) {
			t.Fatal(options, err)
		}
	}
	check(a, false, 96, lyricsProviders)
	order := []string{"petitlyrics", "lrclib", "migu"}
	if w := request("PUT", "/api/lyrics/options", map[string]any{"auto_search": true, "min_score": 90, "providers": order}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	check(a, true, 90, order)
	if raw, _ := a.meta(lyricsOptionsKey); raw != `{"auto_search":true}` {
		t.Fatal("published options format changed", raw)
	}
	for _, invalid := range []any{[]string{"qq", "qq"}, []string{"source"}, []string{"unknown"}, []string{"QQ"}, "qq", []int{1}} {
		if w := request("PUT", "/api/lyrics/options", map[string]any{"auto_search": false, "min_score": 100, "providers": invalid}); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
		check(a, true, 90, order)
	}
	if w := request("PUT", "/api/lyrics/options", map[string]any{"auto_search": true, "min_score": 98}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	check(a, true, 98, order)
	if err := a.setMeta(lyricsOptionsKey, `{"auto_search":false}`); err != nil {
		t.Fatal(err)
	}
	check(a, false, 98, order)
	if w := request("DELETE", "/api/lyrics/search", nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	check(a, false, 98, order)
	cfg, logger, assets := a.cfg, a.logger, a.assets
	a.Close()
	reopened, err := New(cfg, logger, "test", assets)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	check(reopened, false, 98, order)
	request = lyricsSession(t, reopened)
	if w := request("PUT", "/api/lyrics/options", map[string]any{"providers": []string{}}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	check(reopened, false, 98, []string{})
	if w := request("PUT", "/api/lyrics/options", map[string]any{"providers": nil}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	check(reopened, false, 98, []string{})
	for _, invalid := range []string{`{"version":2,"providers":[]}`, `{"version":1}`, `{"version":1,"providers":null}`, `{"version":1,"providers":["qq","qq"]}`, `{"version":1,"providers":["other"]}`, `invalid`} {
		if err := reopened.setMeta(lyricsProvidersKey, invalid); err != nil {
			t.Fatal(err)
		}
		if _, err := reopened.lyricsOptions(ctx); err == nil {
			t.Fatal("invalid saved policy silently enabled sources", invalid)
		}
	}
}

func TestDisabledOnlineLyricsRetainSourceAndInstrumentalChoices(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	request := lyricsSession(t, a)
	if w := request("PUT", "/api/lyrics/options", map[string]any{"providers": []string{}}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		t.Error("disabled online sources made a request", r.URL)
		w.WriteHeader(500)
	})
	if text, provider, state, attempts, err := a.searchOnlineLyrics(context.Background(), lyricsTarget{}, lyricsBatchOptions{}); err != nil || text != "" || provider != "" || state != "not_found" || len(attempts) != 0 {
		t.Fatal(text, provider, state, attempts, err)
	}
	if err := os.WriteFile(filepath.Join(s.Source, "Album/01.lrc"), []byte("[00:01.00]日本語の歌\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/lyrics/search", map[string]any{"all": true}); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if report := waitLyrics(t, a); report.Found != 1 || report.Failed != 0 {
		t.Fatal(report)
	}
	token, index := previewInstrumentalLyrics(t, a, request)
	if w := request("PUT", "/api/library/1/lyrics", map[string]any{"token": token, "index": index}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if data, err := os.ReadFile(filepath.Join(s.Output, lyricsRel(source.Output))); err != nil || string(data) != instrumentalLRC {
		t.Fatal(string(data), err)
	}
}

func TestLyricsProviderBatchSnapshotsSettings(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	request := lyricsSession(t, a)
	if w := request("PUT", "/api/lyrics/options", map[string]any{"providers": []string{"lrclib", "qq"}}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	target, err := a.captureLyricsTarget(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	candidate := lyricsCandidate{Provider: "qq", Title: "Track", Artist: "Artist", Album: "Album", Duration: 183, text: &lyricsText{Original: "[00:01.00]Saved choice\n"}}
	if err := a.storeLyricsSearch(context.Background(), target, "qq", "", []lyricsCandidate{candidate}, false, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "lrclib.net" {
			t.Error("unexpected provider", r.URL)
		}
		if r.URL.Path == "/api/get" {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
			}
			w.WriteHeader(404)
			return
		}
		lyricsJSON(w, `[]`)
	})
	if _, started := a.startLyrics(s, []Source{source}, lyricsBatchOptions{}, false); !started {
		t.Fatal("batch did not start")
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("batch did not query its first provider")
	}
	if w := request("PUT", "/api/lyrics/options", map[string]any{"providers": []string{}}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	close(release)
	if report := waitLyrics(t, a); report.Found != 1 || report.Failed != 0 {
		t.Fatal("settings changed an active batch", report)
	}
	raw, err := a.meta("lyrics-status:1")
	var status LyricsStatus
	if err != nil || json.Unmarshal([]byte(raw), &status) != nil || status.Provider != "qq" || len(status.Attempts) != 2 || status.Attempts[0].Provider != "lrclib" || status.Attempts[1].Provider != "qq" {
		t.Fatal(raw, err)
	}
	if order, err := a.lyricsProviderOrder(context.Background()); err != nil || order == nil || len(order) != 0 {
		t.Fatal("new batch settings were lost", order, err)
	}
}

func TestDisabledLyricsSourcesHideCachedCandidatesAndRejectRequests(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	ctx := context.Background()
	target, err := a.captureLyricsTarget(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i, provider := range []string{"lrclib", "petitlyrics", "qq"} {
		query := "Enabled query"
		if provider == "qq" {
			query = "Disabled latest query"
		}
		if err := a.storeLyricsSearch(ctx, target, provider, query, []lyricsCandidate{{Provider: provider, remoteID: "123", Title: "Track", Artist: "Artist", Duration: 183}}, false, time.Now().Unix()-int64(3-i)); err != nil {
			t.Fatal(err)
		}
	}
	stale := a.newLyricsChoices(target, "qq", "", []lyricsCandidate{{Provider: "qq", remoteID: "123"}}, false, 0)
	request := lyricsSession(t, a)
	if w := request("PUT", "/api/lyrics/options", map[string]any{"providers": []string{"petitlyrics", "lrclib"}}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := request("GET", "/api/library/1/lyrics/candidates", nil)
	var restored struct {
		Query  string                 `json:"query"`
		Groups []lyricsCandidateGroup `json:"groups"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &restored) != nil || restored.Query != "Enabled query" || len(restored.Groups) != 2 || restored.Groups[0].Provider != "petitlyrics" || restored.Groups[1].Provider != "lrclib" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, root := range []string{s.Source, s.Output} {
		if err := os.Rename(root, root+"-offline"); err != nil {
			t.Fatal(err)
		}
	}
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		t.Error("disabled provider made a request", r.URL)
		w.WriteHeader(500)
	})
	for _, body := range []map[string]any{{"provider": "qq", "query": "Track"}, {"provider": "qq", "query": "Track", "refresh": true}} {
		if w := request("POST", "/api/library/1/lyrics/candidates", body); w.Code != 409 || !strings.Contains(w.Body.String(), "Lyrics provider is disabled in settings") {
			t.Fatal("disabled search touched offline storage", w.Code, w.Body.String())
		}
	}
	if w := request("POST", "/api/library/1/lyrics/preview", map[string]any{"token": stale.Token, "index": 0}); w.Code != 409 {
		t.Fatal("stale preview used a disabled provider", w.Code, w.Body.String())
	}
}
