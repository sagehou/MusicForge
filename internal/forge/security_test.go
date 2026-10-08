package forge

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOIDCReconfigurationRequiresLocalLoginAndRevokesSessions(t *testing.T) {
	a, s := testApp(t)
	s.OIDCIssuer = "https://issuer.test/"
	s.OIDCClientID = "client"
	s.OIDCSecret = "secret"
	s.BoundIssuer = s.OIDCIssuer
	s.BoundSubject = "subject"
	a.cfg.PublicURL = "https://musicforge.test"
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	oidc := httptest.NewRecorder()
	if err := a.newSession(oidc, "oidc"); err != nil {
		t.Fatal(err)
	}
	local := httptest.NewRecorder()
	if err := a.newSession(local, "local"); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"enabled": s.Enabled, "source": s.Source, "output": s.Output, "encoding": s.Encoding, "concurrency": s.Concurrency, "scan_minutes": s.ScanMinutes, "nav_library": s.NavLibrary, "oidc_issuer": s.OIDCIssuer, "oidc_client_id": s.OIDCClientID, "unbind_oidc": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		recorder *httptest.ResponseRecorder
		code     int
	}{{oidc, 403}, {local, 200}} {
		cookie := test.recorder.Result().Cookies()[0]
		req := httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != test.code {
			t.Fatalf("want %d, got %d: %s", test.code, w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest("GET", "/api/library", nil)
	req.AddCookie(oidc.Result().Cookies()[0])
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("unbound identity kept active session", w.Code)
	}
	latest, err := a.settings()
	if err != nil || latest.BoundSubject != "" {
		t.Fatal("binding not cleared", err)
	}
}

func TestResolvedConfigurationOverlapAndURLBoundaries(t *testing.T) {
	a, s := testApp(t)
	alias := filepath.Join(t.TempDir(), "config-alias")
	if err := os.Symlink(a.cfg.ConfigDir, alias); err != nil {
		t.Fatal(err)
	}
	a.cfg.ConfigDir = alias
	s.Output = filepath.Join(alias, "output")
	if err := os.MkdirAll(s.Output, 0755); err != nil {
		t.Fatal(err)
	}
	if err := a.initializeStorage(s); err == nil {
		t.Fatal("accepted resolved config overlap")
	}
	for _, value := range []string{"https://user:secret@example.test", "https://example.test/?token=secret", "https://example.test/#fragment"} {
		t.Setenv("MUSICFORGE_PUBLIC_URL", value)
		if _, err := LoadRuntime(t.TempDir()); err == nil {
			t.Fatal("accepted unsafe public origin", value)
		}
		normal := DefaultSettings()
		normal.OIDCIssuer = value
		normal.OIDCClientID = "client"
		normal.OIDCSecret = "secret"
		if err := normal.Validate(); err == nil {
			t.Fatal("accepted unsafe integration URL", value)
		}
	}
}

func TestBusyLibraryDoesNotTrapSettingsRequest(t *testing.T) {
	a, s := testApp(t)
	local := httptest.NewRecorder()
	if err := a.newSession(local, "local"); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(body))
	cookie := local.Result().Cookies()[0]
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
	w := httptest.NewRecorder()
	done := make(chan struct{})
	a.files.Lock()
	go func() { a.Handler().ServeHTTP(w, req); close(done) }()
	select {
	case <-done:
		a.files.Unlock()
	case <-time.After(time.Second):
		a.files.Unlock()
		<-done
		t.Fatal("Settings waited indefinitely behind a library operation")
	}
	if w.Code != 409 {
		t.Fatal("busy Settings request did not preserve unsaved form", w.Code, w.Body.String())
	}
}

func TestStartupSecuresExistingConfigurationDirectory(t *testing.T) {
	a, _ := testApp(t)
	cfg, logger, assets := a.cfg, a.logger, a.assets
	a.Close()
	// Typical host-side mkdir creates 0755; existing directories bypass MkdirAll's mode.
	if err := os.Chmod(cfg.ConfigDir, 0755); err != nil {
		t.Fatal(err)
	}
	restored, err := New(cfg, logger, "test", assets)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err = restored.setMeta("secret-fixture", "not-for-other-host-users"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cfg.ConfigDir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("configuration directory must protect database, WAL and config secrets: %v / %v", info, err)
	}
}

func TestPasswordChangeRevokesExistingSessionsAndRejectsOldPassword(t *testing.T) {
	a, _ := testApp(t)
	send := func(path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		if cookie != nil {
			r.AddCookie(cookie)
			r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	setup := send("/api/auth/setup", map[string]string{"code": a.bootstrap, "username": "admin", "password": "old-test-password"}, nil)
	if setup.Code != 201 {
		t.Fatal(setup.Body.String())
	}
	old := setup.Result().Cookies()[0]
	oidc := httptest.NewRecorder()
	if err := a.newSession(oidc, "oidc"); err != nil {
		t.Fatal(err)
	}
	changed := send("/api/auth/password", map[string]string{"current": "old-test-password", "password": "new-test-password"}, old)
	if changed.Code != 200 {
		t.Fatal(changed.Body.String())
	}
	for _, cookie := range []*http.Cookie{old, oidc.Result().Cookies()[0]} {
		r := httptest.NewRequest("GET", "/api/library", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("password change kept an old session alive", w.Code)
		}
	}
	if w := send("/api/auth/login", map[string]string{"username": "admin", "password": "old-test-password"}, nil); w.Code != 401 {
		t.Fatal("old password still logs in", w.Code)
	}
	if w := send("/api/auth/login", map[string]string{"username": "admin", "password": "new-test-password"}, nil); w.Code != 200 {
		t.Fatal("new password cannot log in", w.Code)
	}
}
