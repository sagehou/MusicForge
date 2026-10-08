package forge

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRuntimeAllowedOrigins(t *testing.T) {
	t.Setenv("MUSICFORGE_PUBLIC_URL", "https://MusicForge.test:443/")
	t.Setenv("MUSICFORGE_ALLOWED_ORIGINS", " https://HOME.test/, http://192.0.2.7:8787,https://musicforge.test,https://home.test,https://[2001:db8:0::1]:8443 ")
	cfg, err := LoadRuntime(t.TempDir())
	want := []string{"https://musicforge.test", "https://home.test", "http://192.0.2.7:8787", "https://[2001:db8::1]:8443"}
	if err != nil || !slices.Equal(cfg.origins(), want) {
		t.Fatal("origin normalization/deduplication failed", cfg, err)
	}
	for _, value := range []string{
		"*", "https://*.test", "home.test", "//home.test", "ftp://home.test", "https://home.test/music",
		"https://user:secret@home.test", "https://home.test?", "https://home.test#", "https://home.test?token=value",
		"https://home.test,", "https://home.test:0", "https://home.test:65536", "https://home.test:",
		"https://[invalid]", "https://[fe80::1%25eth0]", "http://musicforge.test", "http://musicforge.test:443", "http://musicforge.test:8787",
	} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("MUSICFORGE_ALLOWED_ORIGINS", value)
			if _, err := LoadRuntime(t.TempDir()); err == nil {
				t.Fatal("accepted invalid or ambiguous origin", value)
			}
		})
	}
	// config.json remains readable by older versions; the new field is optional.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"public_url":"https://main.test","allowed_origins":["https://file.test/"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSICFORGE_PUBLIC_URL", "")
	if err := os.Unsetenv("MUSICFORGE_ALLOWED_ORIGINS"); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadRuntime(dir)
	if err != nil || !slices.Equal(cfg.origins(), []string{"https://main.test", "https://file.test"}) {
		t.Fatal("file configuration rejected", cfg, err)
	}
	t.Setenv("MUSICFORGE_ALLOWED_ORIGINS", "http://localhost:8787")
	cfg, err = LoadRuntime(dir)
	if err != nil || !slices.Equal(cfg.origins(), []string{"https://main.test", "http://localhost:8787"}) {
		t.Fatal("environment did not override file origins", cfg, err)
	}
	t.Setenv("MUSICFORGE_ALLOWED_ORIGINS", "")
	cfg, err = LoadRuntime(dir)
	if err != nil || !slices.Equal(cfg.origins(), []string{"https://main.test"}) {
		t.Fatal("empty environment did not clear additional origins", cfg, err)
	}
}

func TestMultipleOriginsLocalAuthenticationAndCookies(t *testing.T) {
	a, _ := testApp(t)
	a.cfg.PublicURL = "https://musicforge.test"
	a.cfg.AllowedOrigins = []string{"https://home.test", "http://192.0.2.7:8787"}
	send := func(origin, path, method string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, origin+path, bytes.NewReader(raw))
		// TLS terminates at a reverse proxy that preserves Host.
		r.TLS = nil
		r.Header.Set("Origin", origin)
		if cookie != nil {
			r.AddCookie(cookie)
			r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	credentials := map[string]string{"username": "admin", "password": "test-admin-password", "code": a.bootstrap}
	setup := send("https://home.test", "/api/auth/setup", "POST", credentials, nil)
	if setup.Code != 201 {
		t.Fatal("setup on additional origin failed", setup.Code, setup.Body.String())
	}
	cookie := setup.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("setup cookie lost its security or host-only scope", cookie)
	}
	for _, test := range []struct {
		host, origin string
		want         int
	}{
		{"musicforge.test", "https://musicforge.test", 204},
		{"home.test", "https://home.test", 204},
		{"HOME.test:443", "https://home.test", 204},
		{"192.0.2.7:8787", "http://192.0.2.7:8787", 204},
		{"home.test", "https://musicforge.test", 403},
		{"home.test", "http://home.test", 403},
		{"home.test", "https://home.test.evil", 403},
		{"home.test", "null", 403},
		{"unconfigured.test", "https://unconfigured.test", 403},
		{"unconfigured.test", "https://home.test", 403},
		{"home.test", "", 204},
	} {
		r := httptest.NewRequest("POST", "http://"+test.host+"/api/library/scan", nil)
		r.Header.Set("Origin", test.origin)
		r.Header.Set("X-Forwarded-Host", "home.test")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.auth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })(w, r)
		if w.Code != test.want {
			t.Fatal("origin/host trust boundary failed", test.host, test.origin, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "https://home.test/api/library/scan", nil)
	r.Header.Set("Origin", "https://home.test")
	r.Header.Set("X-CSRF-Token", "wrong")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("additional origin bypassed CSRF", w.Code)
	}
	settings := send("https://home.test", "/api/settings", "GET", nil, cookie)
	var response struct {
		PublicURL      string   `json:"public_url"`
		AllowedOrigins []string `json:"allowed_origins"`
	}
	if settings.Code != 200 || json.Unmarshal(settings.Body.Bytes(), &response) != nil || response.PublicURL != a.cfg.PublicURL || !slices.Equal(response.AllowedOrigins, a.cfg.origins()) {
		t.Fatal("Settings did not expose read-only access configuration", settings.Body.String())
	}
	delete(credentials, "code")
	for _, origin := range a.cfg.origins() {
		login := send(origin, "/api/auth/login", "POST", credentials, nil)
		if login.Code != 200 {
			t.Fatal("local login failed", origin, login.Body.String())
		}
		cookie = login.Result().Cookies()[0]
		if cookie.Secure != (origin != "http://192.0.2.7:8787") || cookie.Domain != "" || !cookie.HttpOnly {
			t.Fatal("login cookie does not match the accessed origin", origin, cookie)
		}
	}
	// Password rotation and logout must use the current HTTP alias, not the HTTPS primary.
	changed := send("http://192.0.2.7:8787", "/api/auth/password", "POST", map[string]string{"current": credentials["password"], "password": "changed-admin-password"}, cookie)
	if changed.Code != 200 || changed.Result().Cookies()[0].Secure {
		t.Fatal("password rotation returned an unusable cookie", changed.Code, changed.Body.String())
	}
	cookie = changed.Result().Cookies()[0]
	logout := send("http://192.0.2.7:8787", "/api/auth/logout", "POST", nil, cookie)
	if logout.Code != 200 || logout.Result().Cookies()[0].Secure || logout.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout did not clear the current origin's cookie", logout.Code, logout.Body.String())
	}
	if revoked := send("http://192.0.2.7:8787", "/api/settings", "GET", nil, cookie); revoked.Code != 401 {
		t.Fatal("logout left an active session", revoked.Code)
	}
}
