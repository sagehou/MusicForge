package forge

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActiveSessionsRenewWithoutChangingTokenOrRevivingRevocations(t *testing.T) {
	for _, method := range []string{"local", "oidc"} {
		t.Run(method, func(t *testing.T) {
			a, _ := testApp(t)
			if _, err := a.db.Exec("INSERT INTO admin(id,username,password) VALUES(1,'admin',x'00')"); err != nil {
				t.Fatal(err)
			}
			issued := httptest.NewRecorder()
			if err := a.newSession(issued, httptest.NewRequest("GET", "/", nil), method); err != nil {
				t.Fatal(err)
			}
			cookie := issued.Result().Cookies()[0]
			send := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", "/api/auth/me", nil)
				r.AddCookie(cookie)
				w := httptest.NewRecorder()
				a.Handler().ServeHTTP(w, r)
				return w
			}
			if w := send(); w.Code != 200 || len(w.Result().Cookies()) != 0 {
				t.Fatal("fresh session was rewritten", w.Code, w.Body.String())
			}
			if _, err := a.db.Exec("UPDATE sessions SET expires=?", time.Now().Add(time.Hour).Unix()); err != nil {
				t.Fatal(err)
			}
			w := send()
			var me map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil || w.Code != 200 || me["authenticated"] != true || me["csrf"] != digest(cookie.Value+":csrf") {
				t.Fatal("renewal changed authentication", w.Code, w.Body.String(), err)
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Value != cookie.Value || !cookies[0].HttpOnly || cookies[0].MaxAge != int(sessionLifetime.Seconds()) {
				t.Fatal("renewal did not preserve the secure session cookie", cookies)
			}
			var expires int64
			if err := a.reads.QueryRow("SELECT expires FROM sessions").Scan(&expires); err != nil || expires < time.Now().Add(6*24*time.Hour).Unix() {
				t.Fatal("session expiry did not advance", expires, err)
			}
			old := session{Token: cookie.Value, Method: method, Expires: time.Now().Add(time.Hour).Unix()}
			if _, err := a.db.Exec("UPDATE sessions SET expires=?", time.Now().Add(-time.Minute).Unix()); err != nil {
				t.Fatal(err)
			}
			w = httptest.NewRecorder()
			if err := a.renewSession(w, httptest.NewRequest("GET", "/", nil), old); !errors.Is(err, sql.ErrNoRows) || len(w.Result().Cookies()) != 0 {
				t.Fatal("renewal resurrected an expired session", err, w.Result().Cookies())
			}
			if _, err := a.db.Exec("DELETE FROM sessions"); err != nil {
				t.Fatal(err)
			}
			w = httptest.NewRecorder()
			if err := a.renewSession(w, httptest.NewRequest("GET", "/", nil), old); !errors.Is(err, sql.ErrNoRows) || len(w.Result().Cookies()) != 0 {
				t.Fatal("renewal resurrected a revoked session", err, w.Result().Cookies())
			}
			if w = send(); !strings.Contains(w.Body.String(), `"authenticated":false`) {
				t.Fatal("revoked session remained authenticated", w.Body.String())
			}
		})
	}
}

func TestDeferredAPIResponsesAreRetryableNotInternalErrors(t *testing.T) {
	w := httptest.NewRecorder()
	apiError(w, 500, later("Waiting for Navidrome scan completion", 10))
	if w.Code != 503 || w.Header().Get("Retry-After") != "10" || !strings.Contains(w.Body.String(), "Waiting for Navidrome") {
		t.Fatal("a normal wait became an internal error", w.Code, w.Header(), w.Body.String())
	}
}

func TestRefreshAPIQueuesWithoutWaitingForNavidrome(t *testing.T) {
	a, s := testApp(t)
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		respond(w, 200, map[string]any{"subsonic-response": map[string]any{"status": "ok", "scanStatus": map[string]bool{"scanning": true}}})
	}))
	defer remote.Close()
	issued := httptest.NewRecorder()
	if err := a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	cookie := issued.Result().Cookies()[0]
	send := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/navidrome/refresh", strings.NewReader("{}"))
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	if w := send(); w.Code != 409 {
		t.Fatal("unconfigured integration accepted work", w.Code)
	}
	s.NavURL, s.NavUser, s.NavPassword = remote.URL, "admin", "secret"
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	w := send()
	if w.Code != 202 || calls.Load() != 0 || w.Header().Get("X-Request-ID") == "" {
		t.Fatal("API waited for the remote scan", w.Code, calls.Load(), w.Header())
	}
	var job struct {
		ID int64 `json:"job_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("UPDATE jobs SET log=?,not_before=? WHERE id=?", "Waiting for Navidrome scan completion", time.Now().Add(time.Minute).Unix(), job.ID); err != nil {
		t.Fatal(err)
	}
	tasks, _, err := a.taskList("all", 100, 0)
	if err != nil || len(tasks) != 1 || tasks[0].State != "pending" || tasks[0].WaitingReason == "" || tasks[0].RetryAt == 0 {
		t.Fatal("waiting work lost its readable state", tasks, err)
	}
}

func TestGroupedDeferredTaskReportsWaitingWithoutChangingState(t *testing.T) {
	a, _ := testApp(t)
	root, err := a.enqueue("scan", "scan:waiting-group", ScanRequest{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec("UPDATE jobs SET state='success' WHERE id=?", root); err != nil {
		t.Fatal(err)
	}
	child, err := a.enqueueTask("convert", "waiting-group:convert", BuildRequest{ID: 1}, false, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec("UPDATE jobs SET log=?,not_before=? WHERE id=?", "Waiting for stable source", time.Now().Add(time.Minute).Unix(), child); err != nil {
		t.Fatal(err)
	}
	tasks, _, err := a.taskList("all", 100, 0)
	if err != nil || len(tasks) != 1 || tasks[0].State != "running" || tasks[0].WaitingReason != "Waiting for stable source" || tasks[0].RetryAt == 0 {
		t.Fatal("grouped task omitted deferred reason", tasks, err)
	}
	if _, err = a.changeTasks("pause", selection{IDs: []int64{root}}); err != nil {
		t.Fatal(err)
	}
	tasks, _, err = a.taskList("all", 100, 0)
	if err != nil || len(tasks) != 1 || tasks[0].State != "paused" || tasks[0].WaitingReason != "" {
		t.Fatal("administrative pause looked like a retry wait", tasks, err)
	}
	if _, err = a.changeTasks("resume", selection{IDs: []int64{root}}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec("UPDATE jobs SET state='running',log=?,not_before=? WHERE id=?", "Waiting for stable source", time.Now().Add(time.Minute).Unix(), child); err != nil {
		t.Fatal(err)
	}
	tasks, _, err = a.taskList("all", 100, 0)
	if err != nil || len(tasks) != 1 || tasks[0].WaitingReason != "" {
		t.Fatal("executing task looked like a retry wait", tasks, err)
	}
}

func TestLibraryArtworkIsAuthenticatedRegisteredAndOutputOnly(t *testing.T) {
	a, s := testApp(t)
	result, err := a.db.Exec("INSERT INTO sources(rel,artist,album,title,duration) VALUES('Artist/Album/01.flac','Artist','Album','Track',123)")
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	cover := filepath.Join("Artist", "Album", "cover.jpg")
	path := filepath.Join(s.Output, cover)
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	writeCover(t, path)
	if _, err = a.db.Exec("INSERT INTO managed(path,kind,signature) VALUES(?,'cover','external:test')", cover); err != nil {
		t.Fatal(err)
	}
	issued := httptest.NewRecorder()
	if err = a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	cookie := issued.Result().Cookies()[0]
	send := func(url, etag string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", url, nil)
		if authenticated {
			r.AddCookie(cookie)
		}
		if etag != "" {
			r.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	w := send("/api/library", "", true)
	var sources []Source
	if err = json.Unmarshal(w.Body.Bytes(), &sources); err != nil || w.Code != 200 || len(sources) != 1 || sources[0].ID != id || sources[0].ArtworkURL == "" {
		t.Fatal("library omitted existing artwork", w.Code, w.Body.String(), err)
	}
	url := sources[0].ArtworkURL
	if w = send(url, "", false); w.Code != 401 {
		t.Fatal("anonymous artwork access", w.Code)
	}
	// The source mount is deliberately unavailable; browsing uses only outputs.
	s.Source = filepath.Join(t.TempDir(), "unavailable")
	if err = a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	w = send(url, "", true)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || w.Body.Len() == 0 || !strings.Contains(w.Header().Get("Cache-Control"), "private") {
		t.Fatal("output artwork did not render", w.Code, w.Header())
	}
	if cached := send(url, w.Header().Get("ETag"), true); cached.Code != 304 {
		t.Fatal("artwork cache did not revalidate", cached.Code)
	}
	if _, err = a.db.Exec("DELETE FROM managed WHERE path=?", cover); err != nil {
		t.Fatal(err)
	}
	if w = send(url, "", true); w.Code != 404 {
		t.Fatal("unregistered file was served", w.Code)
	}
	if _, err = a.db.Exec("INSERT INTO managed(path,kind,signature) VALUES(?,'cover','external:test')", cover); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jpg")
	writeCover(t, outside)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if w = send(url, "", true); w.Code != 404 {
		t.Fatal("artwork symlink escaped the output root", w.Code)
	}
}
