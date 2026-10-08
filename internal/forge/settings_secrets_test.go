package forge

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSettingsSecretClearingAndLidarrValidation(t *testing.T) {
	a, s := testApp(t)
	a.cfg.PublicURL = "https://musicforge.test"
	s.NavURL, s.NavUser, s.NavPassword = "https://navidrome.test", "admin", "nav-secret"
	s.OIDCIssuer, s.OIDCClientID, s.OIDCSecret = "https://issuer.test", "client", "oidc-secret"
	s.BoundIssuer, s.BoundSubject = s.OIDCIssuer, "subject"
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	local := httptest.NewRecorder()
	if err := a.newSession(local, httptest.NewRequest("GET", a.cfg.PublicURL+"/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	localCookie := local.Result().Cookies()[0]
	send := func(changes map[string]any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		body := map[string]any{}
		raw, _ := json.Marshal(s)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		body["nav_password"], body["oidc_secret"] = "", ""
		for key, value := range changes {
			body[key] = value
		}
		raw, _ = json.Marshal(body)
		r := httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(raw))
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	for _, changes := range []map[string]any{
		{"lidarr_prefix": "music/flac"},
		{"clear_nav_password": true},
		{"clear_oidc_secret": true},
	} {
		if w := send(changes, localCookie); w.Code != 400 {
			t.Fatal("invalid configuration was saved", changes, w.Code, w.Body.String())
		}
	}
	if w := send(map[string]any{"lidarr_prefix": "/lidarr/flac"}, localCookie); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	// Disabling an integration preserves credentials unless deletion is explicit.
	if w := send(map[string]any{"nav_url": "", "oidc_issuer": ""}, localCookie); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	latest, err := a.settings()
	if err != nil || latest.NavPassword != s.NavPassword || latest.OIDCSecret != s.OIDCSecret {
		t.Fatal("blank values unexpectedly erased stored credentials", latest, err)
	}
	// Restore the enabled fixture, with an active OIDC session and authorization flow.
	if err = a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	oidc := httptest.NewRecorder()
	if err = a.newSession(oidc, httptest.NewRequest("GET", a.cfg.PublicURL+"/", nil), "oidc"); err != nil {
		t.Fatal(err)
	}
	oidcCookie := oidc.Result().Cookies()[0]
	if _, err = a.db.Exec("INSERT INTO oidc_flows(state,nonce,verifier,action,session,expires) VALUES('clear-test','nonce','verifier','login','',?)", time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	clear := map[string]any{"nav_url": "", "oidc_issuer": "", "clear_nav_password": true, "clear_oidc_secret": true}
	if w := send(clear, oidcCookie); w.Code != 403 {
		t.Fatal("OIDC session could delete its own authentication configuration", w.Code)
	}
	latest, err = a.settings()
	if err != nil || latest.OIDCSecret != s.OIDCSecret || latest.NavPassword != s.NavPassword {
		t.Fatal("rejected save changed secrets", err)
	}
	if w := send(clear, localCookie); w.Code != 200 {
		t.Fatal("explicit deletion failed", w.Code, w.Body.String())
	}
	latest, err = a.settings()
	if err != nil || latest.NavPassword != "" || latest.OIDCSecret != "" || latest.BoundSubject != "" {
		t.Fatal("explicit deletion left credentials or binding behind", latest, err)
	}
	var flows int
	if err = a.db.QueryRow("SELECT count(*) FROM oidc_flows").Scan(&flows); err != nil || flows != 0 {
		t.Fatal("cleared OIDC configuration retained flows", flows, err)
	}
	for _, test := range []struct {
		cookie *http.Cookie
		status int
	}{{oidcCookie, 401}, {localCookie, 200}} {
		r := httptest.NewRequest("GET", "/api/settings", nil)
		r.AddCookie(test.cookie)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatal("credential clearing revoked incorrect sessions", w.Code, test.status)
		}
		if w.Code == 200 {
			var response struct {
				Configured map[string]bool `json:"configured"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Configured["nav_password"] || response.Configured["oidc_secret"] {
				t.Fatal("UI still reports configured credentials", response, err)
			}
		}
	}
}
