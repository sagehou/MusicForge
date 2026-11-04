package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestProviderStatusIsPassiveAndManualCheckRespectsCooldown(t *testing.T) {
	a, settings := testApp(t)
	if _, err := a.db.Exec("INSERT INTO sources(id,rel,title,artist,album,duration) VALUES(1,'Artist/Album/1.flac','Song','Artist','Album',180)"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{settings.Source, settings.Output} {
		if err := os.Rename(path, path+"-offline"); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Host != "lrclib.net" || r.URL.Path != "/api/search" || !strings.Contains(r.URL.RawQuery, "Song") {
			t.Error("checked wrong provider or track", r.URL)
		}
		lyricsJSON(w, `[{"id":1,"trackName":"Song","artistName":"Artist","albumName":"Album","duration":180,"syncedLyrics":"[00:01.00]Hello"}]`)
	})
	var beforeMeta, beforeManaged int
	if err := a.db.QueryRow("SELECT count(*) FROM meta").Scan(&beforeMeta); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow("SELECT count(*) FROM managed").Scan(&beforeManaged); err != nil {
		t.Fatal(err)
	}
	status, err := a.lyricsProviderStatus(context.Background())
	if err != nil || len(status) != len(lyricsProviders) || calls != 0 {
		t.Fatal("passive status contacted a provider", err, calls)
	}
	for _, item := range status {
		if item.State != "unknown" || item.Checked != 0 {
			t.Fatal("invented recent search", item)
		}
	}
	check := func(id int) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]int{"source_id": id})
		req := httptest.NewRequest("POST", "/api/lyrics/providers/lrclib/check", strings.NewReader(string(body)))
		req.SetPathValue("provider", "lrclib")
		w := httptest.NewRecorder()
		a.checkLyricsProvider(w, req)
		return w
	}
	w := check(1)
	if w.Code != 200 || calls != 1 {
		t.Fatal("explicit check failed", w.Code, calls, w.Body.String())
	}
	if err = json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	var last lyricsProviderDiagnostic
	for _, item := range status {
		if item.Provider == "lrclib" {
			last = item
		}
	}
	if last.State != "ok" || last.SourceID != 1 || last.Candidates != 1 || last.Checked == 0 {
		t.Fatal("missing search result", last)
	}
	a.lyricsMu.Lock()
	a.lyricsRetryAt = time.Now().Add(time.Minute)
	a.lyricsMu.Unlock()
	w = check(1)
	if w.Code != 200 || calls != 1 {
		t.Fatal("manual check bypassed rate limit", calls, w.Body.String())
	}
	if err = json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	for _, item := range status {
		if item.Provider == "lrclib" && (item.State != "cooldown" || item.Checked != last.Checked || item.RetryAt <= time.Now().Unix()) {
			t.Fatal("cooldown overwrote recent search", item)
		}
	}
	if w = check(999); w.Code != 404 || calls != 1 {
		t.Fatal("checked missing source", w.Code, calls)
	}
	var afterMeta, afterManaged int
	if err = a.db.QueryRow("SELECT count(*) FROM meta").Scan(&afterMeta); err != nil {
		t.Fatal(err)
	}
	if err = a.db.QueryRow("SELECT count(*) FROM managed").Scan(&afterManaged); err != nil {
		t.Fatal(err)
	}
	saved, err := a.settingsContext(context.Background())
	if err != nil || saved != settings || beforeMeta != afterMeta || beforeManaged != afterManaged {
		t.Fatal("diagnostics changed settings or registered artifacts", err)
	}
}

func TestLyricsMatchExplanationsKeepMatchingAndStoredChoices(t *testing.T) {
	source := Source{Title: "Song", Artist: "Artist", Album: "Album", Duration: 180}
	for _, tc := range []struct {
		candidate lyricsCandidate
		reasons   []string
	}{
		{lyricsCandidate{Title: "Song", Artist: "Artist", Album: "Album", Duration: 180}, nil},
		{lyricsCandidate{Title: "Song (Live)", Artist: "Artist", Album: "Album", Duration: 180}, []string{"version"}},
		{lyricsCandidate{Title: "Different", Artist: "Another", Album: "Other", Duration: 210}, []string{"title", "artist", "duration", "album"}},
		{lyricsCandidate{Title: "Song", Artist: "Artist", Album: "Album"}, []string{"duration_unknown"}},
		{lyricsCandidate{Title: "別名", Artist: "別の表記", Album: "Album", Duration: 180, titleAliases: []string{"Song"}, artistAliases: []string{"Artist"}}, nil},
	} {
		candidates := []lyricsCandidate{tc.candidate}
		rankLyricsCandidates(candidates, source)
		before := candidates[0]
		a := &App{}
		result := a.newLyricsChoices(lyricsTarget{Source: source}, "lrclib", "", candidates, false, 0)
		if !slices.Equal(result.Candidates[0].MatchReasons, tc.reasons) {
			t.Errorf("%s reasons = %v, want %v", tc.candidate.Title, result.Candidates[0].MatchReasons, tc.reasons)
		}
		stored := a.lyricsChoices[result.Token].Candidates[0]
		if stored.MatchReasons != nil || stored.HighConfidence != before.HighConfidence || stored.IdentityMatch != before.IdentityMatch || stored.MatchScore != before.MatchScore {
			t.Fatal("display explanation changed stored selection")
		}
	}
}
