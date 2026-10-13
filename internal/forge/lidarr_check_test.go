package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLidarrComparisonCountsPathsNotTracksOrOutputs(t *testing.T) {
	s := Settings{Source: "/source", LidarrPrefix: "/music"}
	sources := []Source{
		{ID: 1, Rel: "Artist/Album/01.flac", Present: true, OutputPresent: true},
		{ID: 2, Rel: "Artist/Album/02.flac", Present: true, Error: "Audio decoding failed"},
		{ID: 3, Rel: "Artist/Album/03.flac", Present: false, OutputPresent: true},
		{ID: 4, Rel: "Artist/Album/Extra.mp3", Present: true},
		{ID: 5, Rel: "歌手/专辑/未匹配.flac", Present: true},
	}
	files := []lidarrFile{
		{Path: "/music/Artist/Album/01.flac"}, {Path: "/music/Artist/Album/01.flac"},
		{Path: "/music/Artist/Album/02.flac"}, {Path: "/music/Artist/Album/03.flac"},
		{Path: "/music/Artist/Album/04.flac"}, {Path: "/music/Artist/Album/05.dsf"},
		{Path: "/music-other/Artist/Album/01.flac"},
	}
	r, err := compareLidarrFiles(s, sources, files, []lidarrFile{{Path: "/music/歌手/专辑/未匹配.flac"}})
	if err != nil || r.LidarrCount != 5 || r.SourceCount != 4 || r.Matched != 2 || r.OnlyLidarr != 3 || r.OnlyMusicForge != 2 || r.Unmapped != 1 || r.OutsideScope != 1 {
		t.Fatal("counts did not use unique in-scope paths", r, err)
	}
	if r.LidarrCount != r.Matched+r.OnlyLidarr || r.SourceCount != r.Matched+r.OnlyMusicForge {
		t.Fatal("counts do not reconcile", r)
	}
	kinds := map[string]int{}
	for _, row := range r.Differences {
		kinds[row.Kind]++
		if row.ScanDir != "" && (row.Kind != "expired" && row.Kind != "not_indexed" || row.ScanDir != "Artist/Album") {
			t.Fatal("unexpected rescan target", row)
		}
	}
	for _, kind := range []string{"failed", "expired", "not_indexed", "unsupported", "not_in_lidarr", "unmapped", "outside_scope"} {
		if kinds[kind] != 1 {
			t.Fatal("missing explanation", kind, r)
		}
	}
	if _, err := compareLidarrFiles(s, sources, []lidarrFile{{Path: "relative.flac"}}, nil); err == nil {
		t.Fatal("incomplete API data looked like missing files")
	}
}

func TestLidarrComparisonUsesReadOnlyScopedAPIAndRejectsPartialResults(t *testing.T) {
	a, s := testApp(t)
	s.LidarrPrefix = "/music"
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("X-Api-Key") != "test-only-key" {
			t.Error("comparison wrote to Lidarr or omitted API authentication")
		}
		switch r.URL.RequestURI() {
		case "/base/api/v1/artist":
			ioJSON(w, `[{"id":1},{"id":2}]`)
		case "/base/api/v1/trackfile?artistId=1":
			ioJSON(w, `[{"path":"/music/Artist/Album/01.flac"}]`)
		case "/base/api/v1/trackfile?artistId=2":
			if fail.Load() {
				http.Error(w, "private upstream message", 500)
				return
			}
			ioJSON(w, `[{"path":"/music/歌手/专辑/02.mp3"}]`)
		case "/base/api/v1/trackfile?unmapped=true":
			ioJSON(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := lidarrConnection{URL: server.URL + "/base", APIKey: "test-only-key"}
	progress := 0
	r, err := a.compareLidarr(context.Background(), c, s, func(done, total int) {
		progress = done
		if total != 3 {
			t.Error(total)
		}
	})
	if err != nil || r.OnlyLidarr != 2 || progress != 3 {
		t.Fatal(r, err, progress)
	}
	var count int
	if err := a.db.QueryRow("SELECT count(*) FROM jobs").Scan(&count); err != nil || count != 0 {
		t.Fatal("comparison queued media work", count, err)
	}
	fail.Store(true)
	r, err = a.compareLidarr(context.Background(), c, s, func(int, int) {})
	if err == nil || len(r.Differences) != 0 || strings.Contains(err.Error(), "private upstream message") {
		t.Fatal("partial results or remote details leaked", r, err)
	}
}

func ioJSON(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(text))
}

func TestLidarrGETNeverFollowsRedirectsOrAcceptsNullLists(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/redirect") {
			http.Redirect(w, r, target.URL, http.StatusFound)
			return
		}
		ioJSON(w, `null`)
	}))
	defer server.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, endpoint := range []string{"redirect", "null"} {
		var files []lidarrFile
		if err := lidarrGET(context.Background(), client, lidarrConnection{URL: server.URL, APIKey: "test-only-key"}, endpoint, &files); err == nil {
			t.Fatal("accepted", endpoint)
		}
	}
	if leaked.Load() {
		t.Fatal("API key redirected to another server")
	}
}

func TestLidarrConnectionAndAsyncCheckAuthenticationAndDeduplication(t *testing.T) {
	a, s := testApp(t)
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.HasSuffix(r.URL.Path, "/artist") {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		ioJSON(w, `[]`)
	}))
	defer server.Close()
	defer func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}()
	issued := httptest.NewRecorder()
	if err := a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	cookie := issued.Result().Cookies()[0]
	send := func(method, path string, body any, auth, csrf bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if auth {
			r.AddCookie(cookie)
		}
		if csrf {
			r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	body := lidarrConnection{URL: server.URL, APIKey: "test-secret-never-returned"}
	if w := send("PUT", "/api/lidarr/connection", body, false, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := send("PUT", "/api/lidarr/connection", body, true, false); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := send("PUT", "/api/lidarr/connection", body, true, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	if w := send("GET", "/api/lidarr/connection", nil, true, false); w.Code != 200 || strings.Contains(w.Body.String(), body.APIKey) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("PUT", "/api/lidarr/connection", lidarrConnection{URL: body.URL}, true, true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	saved, _ := a.lidarrConnection(context.Background())
	if saved != body {
		t.Fatal("legacy settings write or blank key lost connection", saved.URL)
	}
	if w := send("PUT", "/api/lidarr/connection", lidarrConnection{URL: body.URL + "/changed"}, true, true); w.Code != 400 {
		t.Fatal("key would be sent to changed destination", w.Code)
	}
	if w := send("POST", "/api/lidarr/check", nil, true, false); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := send("POST", "/api/lidarr/check", nil, true, true); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("comparison did not start")
	}
	if w := send("POST", "/api/lidarr/check", nil, true, true); w.Code != 202 || calls.Load() != 1 {
		t.Fatal("duplicate check started remote requests", w.Code, calls.Load())
	}
	if w := send("GET", "/api/lidarr/check", nil, true, false); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"running"`) {
		t.Fatal("polling blocked behind remote I/O", w.Code, w.Body.String())
	}
	close(gate)
	a.lidarrWG.Wait()
	if w := send("GET", "/api/lidarr/check", nil, true, false); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"complete"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("PUT", "/api/lidarr/connection", lidarrConnection{}, true, true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	saved, _ = a.lidarrConnection(context.Background())
	if saved.URL != "" || saved.APIKey != "" {
		t.Fatal("disconnect retained credentials")
	}
}
