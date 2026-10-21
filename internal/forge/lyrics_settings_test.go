package forge

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLyricsThresholdDefaultsValidationAndLegacyWriters(t *testing.T) {
	a, _ := testApp(t)
	request := lyricsSession(t, a)
	check := func(score int, automatic bool) {
		t.Helper()
		options, err := a.lyricsOptions(context.Background())
		if err != nil || options.MinScore != score || options.AutoSearch != automatic { t.Fatal(options, err) }
	}
	check(96, false)
	for _, value := range []any{-1, 101, 95.5, "90"} {
		if w := request("PUT", "/api/lyrics/options", map[string]any{"auto_search": true, "min_score": value}); w.Code != 400 { t.Fatal(w.Code, w.Body.String()) }
		check(96, false)
	}
	if w := request("PUT", "/api/lyrics/options", map[string]any{"auto_search": true, "min_score": 90}); w.Code != 200 { t.Fatal(w.Code, w.Body.String()) }
	check(90, true)
	if raw, _ := a.meta(lyricsOptionsKey); raw != `{"auto_search":true}` { t.Fatal("published option format changed", raw) }
	// Simulate the exact option writer used by previous compatible releases.
	if err := a.setMeta(lyricsOptionsKey, `{"auto_search":false}`); err != nil { t.Fatal(err) }
	check(90, false)
	if w := request("PUT", "/api/lyrics/options", map[string]any{"auto_search": true}); w.Code != 200 { t.Fatal(w.Code) }
	check(90, true)
	if w := request("DELETE", "/api/lyrics/search", nil); w.Code != 200 { t.Fatal(w.Code) }
	check(90, false)
	for _, score := range []int{0, 100} {
		if w := request("PUT", "/api/lyrics/options", map[string]any{"min_score": score}); w.Code != 200 { t.Fatal(w.Code) }
		check(score, false)
	}
}

func TestLyricsThresholdRetainsIdentityDurationAndConflictGuards(t *testing.T) {
	a, _ := testApp(t)
	source := Source{Title: "Track", Artist: "Artist", Album: "Album", Duration: 183}
	base := lyricsCandidate{Title: "Track", Artist: "Artist", Album: "Other", Duration: 183, text: &lyricsText{Original: "[00:01.00]爱这风云\n"}}
	for _, tc := range []struct { name string; score int; title, artist string; duration float64; accepted bool }{
		{"default", 96, "Track", "Artist", 183, false},
		{"album relaxed", 90, "Track", "Artist", 183, true},
		{"duration relaxed", 80, "Track", "Artist", 188, true},
		{"duration ceiling", 0, "Track", "Artist", 194, false},
		{"wrong title", 0, "Other", "Artist", 183, false},
		{"wrong artist", 0, "Track", "Other", 183, false},
		{"wrong version alias", 0, "Track Live", "Artist", 183, false},
		{"nonfinite duration", 0, "Track", "Artist", math.Inf(1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			candidate.Title, candidate.Artist, candidate.Duration = tc.title, tc.artist, tc.duration
			if tc.name == "wrong version alias" { candidate.titleAliases = []string{"Track"} }
			text, state, err := a.chooseLyrics(context.Background(), source, []lyricsCandidate{candidate}, lyricsBatchOptions{MinScore: tc.score})
			if err != nil || (text != "" && state == "ready") != tc.accepted { t.Fatal(text, state, err) }
		})
	}
	conflict := base
	conflict.text = &lyricsText{Original: "[00:02.00]爱这风云\n"}
	if text, state, err := a.chooseLyrics(context.Background(), source, []lyricsCandidate{base, conflict}, lyricsBatchOptions{}); err != nil || text != "" || state != "needs_review" { t.Fatal(text, state, err) }
}

func TestLyricsInspectionOnlyReadsOutputAndPreservesMatchingStatus(t *testing.T) {
	a, settings := testApp(t)
	source := seedLyricsArtifact(t, a, settings)
	ctx := context.Background()
	if err := a.writeLyricsFile(ctx, settings, source, "[00:01.00]愛這風雲", "source"); err != nil { t.Fatal(err) }
	key := lyricsScriptPrefix+strconv.FormatInt(source.ID, 10)
	if _, err := a.db.Exec("DELETE FROM meta WHERE key=?", key); err != nil { t.Fatal(err) }
	status := LyricsStatus{State: "needs_review", Output: source.Output, Signature: "audio", Checked: 123}
	raw, _ := json.Marshal(status)
	if err := a.setMeta("lyrics-status:1", string(raw)); err != nil { t.Fatal(err) }
	if err := os.Rename(settings.Source, settings.Source+"-offline"); err != nil { t.Fatal(err) }
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) { t.Error("inspection contacted a provider", r.URL); w.WriteHeader(500) })
	request := lyricsSession(t, a)
	if w := request("POST", "/api/lyrics/search", map[string]any{"all": true, "inspect_only": true}); w.Code != 202 { t.Fatal(w.Code, w.Body.String()) }
	if report := waitLyrics(t, a); report.Mode != "inspect" || report.Found != 1 || report.Failed != 0 { t.Fatal(report) }
	if current, _ := a.meta("lyrics-status:1"); current != string(raw) { t.Fatal("inspection changed matching status", current) }
	rows := []Source{source}
	if err := a.annotateLyrics(ctx, rows); err != nil || rows[0].LyricsScript != "traditional" { t.Fatal(rows, err) }
	if _, err := a.db.Exec("UPDATE managed SET signature='rebuilt' WHERE kind='audio'"); err != nil { t.Fatal(err) }
	if err := a.annotateLyrics(ctx, rows); err != nil || rows[0].LyricsScript != "" { t.Fatal("stale script survived audio rebuild", rows, err) }
	if err := os.WriteFile(filepath.Join(settings.Output, lyricsRel(source.Output)), []byte("[00:01.00]手动改过爱\n"), 0644); err != nil { t.Fatal(err) }
	if _, err := a.inspectLyricsScript(ctx, settings, source.ID); err == nil { t.Fatal("edited file was accepted") }
	if _, err := a.meta(key); err == nil { t.Fatal("failed inspection kept stale script") }
}

func TestTraditionalReplacementBypassesSourceAndReusesSearchAtNewThreshold(t *testing.T) {
	a, settings := testApp(t)
	source := seedLyricsArtifact(t, a, settings)
	ctx := context.Background()
	traditional := "[00:01.00]愛這風雲\n"
	if err := os.WriteFile(filepath.Join(settings.Source, "Album/01.lrc"), []byte(traditional), 0644); err != nil { t.Fatal(err) }
	if err := a.writeLyricsFile(ctx, settings, source, traditional, "source"); err != nil { t.Fatal(err) }
	lrclibCalls := 0
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host == "lrclib.net" { lrclibCalls++; if r.URL.Path == "/api/get" { w.WriteHeader(404); return }; lyricsJSON(w, `[{"trackName":"Track","artistName":"Artist","albumName":"Other","duration":183,"syncedLyrics":"[00:01.00]爱这风云"}]`); return }
		w.WriteHeader(503)
	})
	run := func() lyricsReport {
		t.Helper()
		if !a.startLyrics(settings, []Source{source}, lyricsBatchOptions{ReplaceTraditional: true}, false) { t.Fatal("batch did not start") }
		return waitLyrics(t, a)
	}
	if report := run(); report.Replaced != 0 || report.Unchanged != 1 { t.Fatal(report) }
	_, text, present, err := a.readLyricsFile(ctx, settings, lyricsRel(source.Output))
	if err != nil || !present || text != traditional { t.Fatal("unmatched replacement lost original", text, err) }
	calls := lrclibCalls
	request := lyricsSession(t, a)
	if w := request("PUT", "/api/lyrics/options", map[string]any{"min_score": 90}); w.Code != 200 { t.Fatal(w.Code) }
	if report := run(); report.Replaced != 1 || report.Unchanged != 0 { t.Fatal(report) }
	if lrclibCalls != calls { t.Fatal("new threshold unnecessarily repeated a cached search", calls, lrclibCalls) }
	entry, text, present, err := a.readLyricsFile(ctx, settings, lyricsRel(source.Output))
	if err != nil || !present || lyricsScript(text) != "simplified" || entry.Provider != "lrclib" { t.Fatal(entry, text, err) }
	if report := run(); report.Replaced != 0 || report.Unchanged != 1 { t.Fatal("already simplified track was searched again", report) }
	if data, err := os.ReadFile(filepath.Join(settings.Source, "Album/01.lrc")); err != nil || string(data) != traditional { t.Fatal("replacement changed source LRC", err) }
}
