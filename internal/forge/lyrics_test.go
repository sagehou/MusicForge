package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLRCValidation(t *testing.T) {
	for _, text := range []string{"\ufeff[ti:Title]\r\n[00:01.23] 歌词\r\n", "[00:01][01:02.123]Repeated", "[123:45:12]Long track"} {
		lrc, err := normalizeLRC(text)
		if err != nil || strings.ContainsAny(lrc, "\r\ufeff") || !strings.HasSuffix(lrc, "\n") {
			t.Fatal("valid LRC rejected or not normalized", lrc, err)
		}
	}
	for _, text := range []string{"", "Only plain lyrics", "<html>error</html>", "[00:99.00]invalid", "[00:01.00][00:02.00] ", "[00:01.00]\x00bad", "[00:01.00]\xff", strings.Repeat("x", maxLyricsBytes+1)} {
		if _, err := normalizeLRC(text); err == nil {
			t.Fatalf("accepted invalid LRC %.80q", text)
		}
	}
}

type lyricsTransport func(*http.Request) (*http.Response, error)

func (f lyricsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockLRCLIB(t *testing.T, a *App, handler http.HandlerFunc) {
	t.Helper()
	a.lyricsHTTP = &http.Client{Timeout: time.Second, Transport: lyricsTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "lrclib.net" || !strings.Contains(r.Header.Get("User-Agent"), "MusicForge/") || r.Header.Get("Accept") != "application/json" {
			t.Error("missing client identity or unexpected lyrics endpoint")
		}
		w := httptest.NewRecorder()
		handler(w, r)
		return w.Result(), nil
	})}
}

func TestLRCLIBMatching(t *testing.T) {
	track := Source{Title: "歌曲", Artist: "Artist & Friend", Album: "Album", Duration: 183.25}
	good := lrclibRecord{Title: track.Title, Artist: track.Artist, Album: track.Album, Duration: 183, Synced: "[00:01.00]Lyrics"}
	wrongArtist, wrongDuration, plain, wrongAlbum := good, good, good, good
	wrongArtist.Artist, wrongDuration.Duration, plain.Synced, wrongAlbum.Album = "Other artist", 200, "", "Live"
	otherLyrics := good
	otherLyrics.Synced = "[00:01.00]Different version"
	wrongInstrumental := wrongAlbum
	wrongInstrumental.Instrumental, wrongInstrumental.Synced = true, ""
	var crowded []lrclibRecord
	for i := 0; i < 8; i++ {
		record := wrongDuration
		record.Duration += float64(i)
		crowded = append(crowded, record)
	}
	crowded = append(crowded, good)
	for _, test := range []struct {
		name   string
		get    *lrclibRecord
		search []lrclibRecord
		state  string
		calls  int
	}{
		{"exact synchronized", &good, nil, "ready", 1},
		{"fallback synchronized", &plain, []lrclibRecord{wrongDuration, wrongArtist, plain, good}, "ready", 2},
		{"ignore another album's instrumental flag", &wrongInstrumental, []lrclibRecord{good}, "ready", 2},
		{"rank matching recordings before candidate limit", nil, crowded, "ready", 2},
		{"review mismatches without accepting plain lyrics", &wrongArtist, []lrclibRecord{wrongArtist, wrongDuration, plain, wrongAlbum}, "needs_review", 2},
		{"ambiguous versions", nil, []lrclibRecord{good, otherLyrics}, "needs_review", 2},
		{"not found", nil, nil, "not_found", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _ := testApp(t)
			calls := 0
			mockLRCLIB(t, a, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Query().Get("track_name") != track.Title || r.URL.Query().Get("artist_name") != track.Artist {
					t.Error("search parameters lost Unicode or special characters")
				}
				if r.URL.Path == "/api/get" {
					if r.URL.Query().Get("duration") != "183.250" || r.URL.Query().Get("album_name") != "Album" {
						t.Error("missing precise duration/album")
					}
					if test.get == nil {
						w.WriteHeader(404)
						return
					}
					respond(w, 200, test.get)
				} else {
					respond(w, 200, test.search)
				}
			})
			text, state, err := a.searchLRCLIB(context.Background(), track)
			if err != nil || state != test.state || calls != test.calls || (text != "") != (state == "ready") {
				t.Fatal("unexpected matching result", state, text, calls, err)
			}
		})
	}
	t.Run("instrumental and rate limiting", func(t *testing.T) {
		a, _ := testApp(t)
		instrumental := good
		instrumental.Instrumental, instrumental.Synced = true, ""
		mockLRCLIB(t, a, func(w http.ResponseWriter, r *http.Request) { respond(w, 200, instrumental) })
		if text, state, err := a.searchLRCLIB(context.Background(), track); err != nil || text != "" || state != "instrumental" {
			t.Fatal("instrumental generated fake lyrics", state, text, err)
		}
		a.lyricsRetryAt = time.Time{}
		calls := 0
		mockLRCLIB(t, a, func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(429)
		})
		if _, _, err := a.searchLRCLIB(context.Background(), track); err == nil || time.Until(a.lyricsRetryAt) < 59*time.Second {
			t.Fatal("rate limit or Retry-After ignored", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := a.searchLRCLIB(ctx, track); !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatal("cancel did not interrupt throttling", calls, err)
		}
	})
	t.Run("invalid response and missing tags", func(t *testing.T) {
		a, _ := testApp(t)
		calls := 0
		mockLRCLIB(t, a, func(w http.ResponseWriter, r *http.Request) {
			calls++
			_, _ = io.WriteString(w, "<html>unavailable</html>")
		})
		if _, _, err := a.searchLRCLIB(context.Background(), Source{Title: "No artist"}); err == nil || calls != 0 {
			t.Fatal("guessed lyrics without tags", err)
		}
		if _, _, err := a.searchLRCLIB(context.Background(), track); err == nil {
			t.Fatal("accepted a non-JSON service error")
		}
	})
}

func seedLyricsArtifact(t *testing.T, a *App, s Settings) Source {
	t.Helper()
	for _, root := range []string{s.Source, s.Output} {
		if err := os.MkdirAll(filepath.Join(root, "Album"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.db.Exec(`INSERT INTO sources(id,rel,hash,built_hash,artist,album,title,duration,output,output_present) VALUES(1,'Album/01.flac','source-hash','source-hash','Artist','Album','Track',183,'Album/01.opus',1)`); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Output, "Album/01.opus"), []byte("registered audio fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("INSERT INTO managed(path,source_id,kind,signature) VALUES('Album/01.opus',1,'audio',?)", digest("registered audio fixture")); err != nil {
		t.Fatal(err)
	}
	source, err := a.source(1)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func waitLyrics(t *testing.T, a *App) lyricsReport {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		a.lyricsMu.Lock()
		running := a.lyricsCancel != nil
		var report lyricsReport
		if a.lyricsReport != nil {
			report = *a.lyricsReport
		}
		a.lyricsMu.Unlock()
		if !running {
			return report
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("lyrics work did not finish")
	return lyricsReport{}
}

func TestLyricsSourceUpdatesAndArtifactLifecycle(t *testing.T) {
	a, s := testApp(t)
	ctx := context.Background()
	makeFLAC(t, a, s, "Artist/Album/01.flac", "Track", false)
	local := filepath.Join(s.Source, "Artist/Album/01.lrc")
	if err := os.WriteFile(local, []byte("\ufeff[00:01.00]本地歌词\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.scan(ctx, ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	source, _ := a.sourceRel("Artist/Album/01.flac")
	var jobsBefore int
	if err := a.db.QueryRow("SELECT count(*) FROM jobs").Scan(&jobsBefore); err != nil {
		t.Fatal(err)
	}
	before, err := fileHash(ctx, filepath.Join(s.Output, source.Output))
	if err != nil {
		t.Fatal(err)
	}
	settingsBefore, _ := json.Marshal(s)
	mockLRCLIB(t, a, func(w http.ResponseWriter, r *http.Request) { t.Error("local LRC should not query the internet") })
	if !a.startLyrics(s, []Source{source}, false, false) {
		t.Fatal("lyrics did not start")
	}
	if report := waitLyrics(t, a); report.Found != 1 || report.Failed != 0 {
		t.Fatal(report)
	}
	output := filepath.Join(s.Output, "Artist/Album/01.lrc")
	if data, err := os.ReadFile(output); err != nil || string(data) != "[00:01.00]本地歌词\n" {
		t.Fatal(string(data), err)
	}
	if err := os.WriteFile(local, []byte("[00:01.00]更新歌词\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !a.startLyrics(s, []Source{source}, false, false) {
		t.Fatal("update did not start")
	}
	if report := waitLyrics(t, a); report.Found != 1 {
		t.Fatal(report)
	}
	if data, _ := os.ReadFile(output); string(data) != "[00:01.00]更新歌词\n" {
		t.Fatal("source lyrics did not update")
	}
	if after, _ := fileHash(ctx, filepath.Join(s.Output, source.Output)); after != before {
		t.Fatal("lyrics update re-encoded audio")
	}
	var jobsAfter int
	if err := a.db.QueryRow("SELECT count(*) FROM jobs").Scan(&jobsAfter); err != nil || jobsAfter != jobsBefore {
		t.Fatal("lyrics update queued audio work", jobsBefore, jobsAfter, err)
	}
	settingsAfter, _ := a.settings()
	rawSettings, _ := json.Marshal(settingsAfter)
	if !bytes.Equal(settingsBefore, rawSettings) {
		t.Fatal("lyrics changed published Settings JSON")
	}
	if err = a.annotateLyrics(ctx, []Source{source}); err != nil {
		t.Fatal(err)
	}
	// Move through the real scanner/queue and its unchanged promotion journal.
	if err = os.MkdirAll(filepath.Join(s.Source, "Moved/Album"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{"flac", "lrc"} {
		if err = os.Rename(filepath.Join(s.Source, "Artist/Album/01."+ext), filepath.Join(s.Source, "Moved/Album/02."+ext)); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.scan(ctx, ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	moved, err := a.sourceRel("Moved/Album/02.flac")
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(s.Output, "Moved/Album/02.lrc")); err != nil || string(data) != "[00:01.00]更新歌词\n" {
		t.Fatal("lyrics did not follow audio move", err)
	}
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("old lyrics survived a completed move", err)
	}
	if after, _ := fileHash(ctx, filepath.Join(s.Output, moved.Output)); after != before {
		t.Fatal("rename re-encoded audio")
	}
	s.Encoding.Codec = "mp3"
	if err = a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	if _, err = a.queueBuild(moved, s.Encoding, false, true); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	if _, err = os.Stat(filepath.Join(s.Output, "Moved/Album/02.lrc")); err != nil {
		t.Fatal("codec switch removed shared LRC", err)
	}
	if err = os.Remove(filepath.Join(s.Source, moved.Rel)); err != nil {
		t.Fatal(err)
	}
	if err = a.scan(ctx, ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.Output, "Moved/Album/02.lrc")); err != nil {
		t.Fatal("ordinary source deletion removed lyrics", err)
	}
	if err = a.deleteExpired([]int64{moved.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.Output, "Moved/Album/02.lrc")); !os.IsNotExist(err) {
		t.Fatal("artifact deletion retained managed lyrics", err)
	}
	var version, unexpected int
	_ = a.db.QueryRow("PRAGMA user_version").Scan(&version)
	_ = a.db.QueryRow("SELECT count(*) FROM managed WHERE kind NOT IN ('audio','cover','temp')").Scan(&unexpected)
	if version != 2 || unexpected != 0 {
		t.Fatal("lyrics changed persistence baseline", version, unexpected)
	}
}

func TestLyricsOwnershipAndInterruptedPublication(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	ctx := context.Background()
	local := filepath.Join(s.Source, "Album/01.lrc")
	output := filepath.Join(s.Output, "Album/01.lrc")
	if err := os.WriteFile(local, []byte("[00:01.00]Local"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("user lyrics"), 0644); err != nil {
		t.Fatal(err)
	}
	if status := a.maintainLyrics(ctx, s, source.ID, false); status.State != "failed" {
		t.Fatal("adopted an unregistered file", status)
	}
	if data, _ := os.ReadFile(output); string(data) != "user lyrics" {
		t.Fatal("user file overwritten")
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	if status := a.maintainLyrics(ctx, s, source.ID, false); status.State != "ready" {
		t.Fatal(status)
	}
	// A crash between rename and the final annotation update recognizes the new hash.
	entry, _, _, err := a.readLyricsFile(ctx, s, "Album/01.lrc")
	if err != nil {
		t.Fatal(err)
	}
	published := "[00:02.00]Published before crash\n"
	entry.Pending = digest(published)
	raw, _ := json.Marshal(entry)
	if err = a.setMeta(lyricsFileKey(entry.Path), string(raw)); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output, []byte(published), 0644); err != nil {
		t.Fatal(err)
	}
	if _, data, present, err := a.readLyricsFile(ctx, s, entry.Path); err != nil || !present || data != published {
		t.Fatal("lost interrupted publication", err)
	}
	if err = os.WriteFile(output, []byte("hand edited"), 0644); err != nil {
		t.Fatal(err)
	}
	if status := a.maintainLyrics(ctx, s, source.ID, true); status.State != "failed" {
		t.Fatal("overwrote edited LRC", status)
	}
	if err = a.removeOwned(s, source.Output, source.ID); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(output); string(data) != "hand edited" {
		t.Fatal("deleting audio deleted edited lyrics")
	}
}

func TestLyricsRecoveryAfterOlderWorkerMoveAndDeletion(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(s.Source, "Album/01.lrc"), []byte("[00:01.00]Kept across versions"), 0644); err != nil {
		t.Fatal(err)
	}
	if status := a.maintainLyrics(ctx, s, source.ID, false); status.State != "ready" {
		t.Fatal(status)
	}
	// An older release moves its ordinary audio and registry without interpreting
	// any lyrics annotation. The candidate must reconcile that same output volume.
	if err := os.MkdirAll(filepath.Join(s.Output, "Moved"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(s.Output, source.Output), filepath.Join(s.Output, "Moved/02.opus")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("UPDATE managed SET path='Moved/02.opus' WHERE source_id=1 AND kind='audio'"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("UPDATE sources SET rel='Moved/02.flac',output='Moved/02.opus' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := a.recoverFiles(ctx, s); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(s.Output, "Moved/02.lrc")); err != nil || string(data) != "[00:01.00]Kept across versions\n" {
		t.Fatal("older move lost LRC", err)
	}
	if _, err := os.Stat(filepath.Join(s.Output, "Album/01.lrc")); !os.IsNotExist(err) {
		t.Fatal("older move retained stale LRC", err)
	}
	if err := os.Remove(filepath.Join(s.Output, "Moved/02.opus")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("DELETE FROM managed WHERE source_id=1 AND kind='audio'"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("UPDATE sources SET output='',output_present=0,built_hash='' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := a.recoverFiles(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Output, "Moved/02.lrc")); !os.IsNotExist(err) {
		t.Fatal("older artifact deletion retained LRC", err)
	}
}

func TestLyricsSearchRejectsChangedArtifactAndStopsWithoutBlockingAPI(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	mockLRCLIB(t, a, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		respond(w, 200, lrclibRecord{Title: source.Title, Artist: source.Artist, Album: source.Album, Duration: source.Duration, Synced: "[00:01.00]Network"})
	})
	if !a.startLyrics(s, []Source{source}, false, false) {
		t.Fatal("did not start")
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("search did not reach provider")
	}
	if !a.files.TryLock() {
		t.Fatal("network search held the library mutation lock")
	}
	if _, err := a.db.Exec("UPDATE managed SET signature='new-audio' WHERE path=?", source.Output); err != nil {
		a.files.Unlock()
		t.Fatal(err)
	}
	a.files.Unlock()
	close(release)
	if report := waitLyrics(t, a); report.Failed != 1 {
		t.Fatal("published against a changed artifact", report)
	}
	if _, err := os.Stat(filepath.Join(s.Output, "Album/01.lrc")); !os.IsNotExist(err) {
		t.Fatal("stale lyrics published", err)
	}
	// A source-side stalled LRC read is cancellable too, without an audio retry.
	sourceFault(t, "lyrics", "Album/01.lrc", 10000, 0)
	if !a.startLyrics(s, []Source{source}, false, false) {
		t.Fatal("second search did not start")
	}
	issued := httptest.NewRecorder()
	if err := a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	cookie := issued.Result().Cookies()[0]
	for _, endpoint := range []string{"/api/lyrics", "/api/library", "/healthz"} {
		r := httptest.NewRequest("GET", endpoint, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("read API failed during lyrics work", endpoint, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("DELETE", "/api/lyrics/search", nil)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if report := waitLyrics(t, a); report.State != "stopped" || calls.Load() != 1 {
		t.Fatal("stop did not cancel the pending source read", report)
	}
	a.lyricsNext = time.Time{}
	a.scheduleLyrics(s)
	if options, err := a.lyricsOptions(context.Background()); err != nil || options.AutoSearch {
		t.Fatal("stop did not persistently disable automatic filling", options, err)
	}
	if a.startLyrics(s, []Source{source}, false, true) {
		t.Fatal("a previously selected automatic batch restarted after stop")
	}
}

func TestLyricsAPIAuthenticationAndAutomaticOptIn(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	if err := os.WriteFile(filepath.Join(s.Source, "Album/01.LRC"), []byte("[00:01.00]Uppercase sidecar"), 0644); err != nil {
		t.Fatal(err)
	}
	a.scheduleLyrics(s)
	if a.lyricsReport != nil {
		t.Fatal("online maintenance enabled without opting in")
	}
	issued := httptest.NewRecorder()
	if err := a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	cookie := issued.Result().Cookies()[0]
	for _, route := range []struct{ method, path, body string }{
		{"POST", "/api/lyrics/search", `{"all":true}`},
		{"PUT", "/api/lyrics/options", `{"auto_search":true}`},
		{"DELETE", "/api/lyrics/search", ""},
		{"POST", "/api/library/1/lyrics/candidates", `{"provider":"lrclib","query":"Track Artist"}`},
		{"POST", "/api/library/1/lyrics/preview", `{"token":"invalid","index":0}`},
		{"PUT", "/api/library/1/lyrics", `{"token":"invalid","index":0}`},
	} {
		for _, authenticated := range []bool{false, true} {
			r := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
			if authenticated {
				r.AddCookie(cookie)
			}
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != 401 && w.Code != 403 {
				t.Fatal("mutation accepted without auth/CSRF", route, w.Code)
			}
		}
	}
	if err := a.setMeta(lyricsOptionsKey, `{"auto_search":true}`); err != nil {
		t.Fatal(err)
	}
	a.lyricsNext = time.Time{}
	a.scheduleLyrics(s)
	if report := waitLyrics(t, a); report.Found != 1 {
		t.Fatal(report)
	}
	list := []Source{source}
	if err := a.annotateLyrics(context.Background(), list); err != nil || list[0].Lyrics == nil || list[0].Lyrics.Provider != "source" {
		t.Fatal("missing library status", list, err)
	}
	if _, err := a.db.Exec("UPDATE managed SET signature='rebuilt' WHERE source_id=1"); err != nil {
		t.Fatal(err)
	}
	list = []Source{source}
	if err := a.annotateLyrics(context.Background(), list); err != nil || list[0].Lyrics != nil {
		t.Fatal("stale status survived rebuild", list, err)
	}
	var stored string
	if err := a.db.QueryRow("SELECT value FROM meta WHERE key=?", "lyrics-status:"+strconv.FormatInt(source.ID, 10)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
}
