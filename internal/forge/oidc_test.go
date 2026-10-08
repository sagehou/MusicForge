package forge

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOIDCBindingAndSingleSubject(t *testing.T) {
	a, s := testApp(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	subject := "allowed-subject"
	nonce := ""
	challenge := ""
	issuer := ""
	var claimsMu sync.Mutex
	encode := func(value any) string { b, _ := json.Marshal(value); return base64.RawURLEncoding.EncodeToString(b) }
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claimsMu.Lock()
		defer claimsMu.Unlock()
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			respond(w, 200, map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/jwks":
			respond(w, 200, map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "test-key", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge {
				t.Error("PKCE verifier mismatch")
			}
			payload := encode(map[string]string{"alg": "RS256", "kid": "test-key", "typ": "JWT"}) + "." + encode(map[string]any{"iss": issuer, "sub": subject, "aud": "test-client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "preferred_username": "admin", "email": "admin@example.test"})
			digest := sha256.Sum256([]byte(payload))
			signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
			if err != nil {
				t.Error(err)
			}
			respond(w, 200, map[string]any{"access_token": "test-access", "token_type": "Bearer", "expires_in": 3600, "id_token": payload + "." + base64.RawURLEncoding.EncodeToString(signature)})
		default:
			w.WriteHeader(404)
		}
	}))
	defer provider.Close()
	claimsMu.Lock()
	issuer = provider.URL
	claimsMu.Unlock()
	a.cfg.PublicURL = "http://musicforge.test"
	s.OIDCIssuer = issuer
	s.OIDCClientID = "test-client"
	s.OIDCSecret = "test-secret"
	if err = a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	localRecorder := httptest.NewRecorder()
	if err = a.newSession(localRecorder, "local"); err != nil {
		t.Fatal(err)
	}
	localCookie := localRecorder.Result().Cookies()[0]
	start := func(bind bool) (*url.URL, *http.Cookie) {
		t.Helper()
		path := "/api/auth/oidc/login"
		method := "GET"
		if bind {
			path = "/api/auth/oidc/bind"
			method = "POST"
		}
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		if bind {
			r.AddCookie(localCookie)
			r.Header.Set("X-CSRF-Token", digest(localCookie.Value+":csrf"))
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		var location string
		if bind {
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			var result map[string]string
			_ = json.Unmarshal(w.Body.Bytes(), &result)
			location = result["url"]
		} else {
			if w.Code != 303 {
				t.Fatal(w.Body.String())
			}
			location = w.Header().Get("Location")
		}
		u, err := url.Parse(location)
		if err != nil {
			t.Fatal(err)
		}
		claimsMu.Lock()
		nonce = u.Query().Get("nonce")
		challenge = u.Query().Get("code_challenge")
		claimsMu.Unlock()
		return u, w.Result().Cookies()[0]
	}
	callback := func(u *url.URL, cookie *http.Cookie, local bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/auth/oidc/callback?code=test-code&state="+url.QueryEscape(u.Query().Get("state")), nil)
		r.AddCookie(cookie)
		if local {
			r.AddCookie(localCookie)
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	u, cookie := start(true)
	if w := callback(u, cookie, true); w.Code != 303 {
		t.Fatal(w.Body.String())
	}
	bound, err := a.settings()
	if err != nil || bound.BoundIssuer != issuer || bound.BoundSubject != subject {
		t.Fatal("binding was not persisted")
	}
	// Public OIDC starts cannot exhaust an authenticated administrator's bind budget.
	a.authMu.Lock()
	a.loginFailures["oidc:192.0.2.1"] = loginLimit{Count: 10, Since: time.Now()}
	a.authMu.Unlock()
	u, cookie = start(true)
	if w := callback(u, cookie, true); w.Code != 303 {
		t.Fatal("public starts prevented authenticated binding", w.Body.String())
	}
	a.authMu.Lock()
	delete(a.loginFailures, "oidc:192.0.2.1")
	a.authMu.Unlock()
	u, cookie = start(false)
	// Authentication must stay responsive while a full library scan owns the file lock.
	a.files.Lock()
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() { response <- callback(u, cookie, false) }()
	var login *httptest.ResponseRecorder
	select {
	case login = <-response:
		a.files.Unlock()
	case <-time.After(5 * time.Second):
		a.files.Unlock()
		<-response
		t.Fatal("OIDC login blocked behind a library operation")
	}
	if login.Code != 303 {
		t.Fatal(login.Body.String())
	}
	a.authMu.Lock()
	_, counted := a.loginFailures["oidc:192.0.2.1"]
	a.authMu.Unlock()
	if counted { t.Fatal("successful OIDC login did not reset its start budget") }
	if w := callback(u, cookie, false); w.Code != 403 {
		t.Fatal("replayed callback accepted")
	}
	claimsMu.Lock()
	subject = "another-user"
	claimsMu.Unlock()
	u, cookie = start(false)
	if w := callback(u, cookie, false); w.Code != 403 {
		t.Fatal("unbound OIDC subject accepted", w.Body.String())
	}
	oldCookie := login.Result().Cookies()[len(login.Result().Cookies())-1]
	access := func(cookie *http.Cookie) int {
		r := httptest.NewRequest("GET", "/api/library", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w.Code
	}
	if access(oldCookie) != 200 {
		t.Fatal("fixture needs an active session for the previous identity")
	}
	// A revocation failure must roll back the new binding as well.
	if _, err = a.db.Exec(`CREATE TRIGGER reject_oidc_revoke BEFORE DELETE ON sessions WHEN OLD.method='oidc' BEGIN SELECT RAISE(ABORT,'injected revocation failure'); END`); err != nil {
		t.Fatal(err)
	}
	u, cookie = start(true)
	if w := callback(u, cookie, true); w.Code != 400 {
		t.Fatal("binding committed despite failed session revocation", w.Code)
	}
	bound, err = a.settings()
	if err != nil || bound.BoundSubject != "allowed-subject" || access(oldCookie) != 200 {
		t.Fatal("failed rebind changed the previous identity", err)
	}
	if _, err = a.db.Exec("DROP TRIGGER reject_oidc_revoke"); err != nil {
		t.Fatal(err)
	}
	u, cookie = start(true)
	if _, err = a.db.Exec("INSERT INTO oidc_flows(state,nonce,verifier,action,session,expires) VALUES('other-flow','nonce','verifier','login','',?)", time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if w := callback(u, cookie, true); w.Code != 303 {
		t.Fatal("replacement identity did not bind", w.Body.String())
	}
	bound, err = a.settings()
	if err != nil || bound.BoundSubject != "another-user" {
		t.Fatal("replacement binding not persisted", err)
	}
	if access(oldCookie) != 401 || access(localCookie) != 200 {
		t.Fatal("rebind must revoke previous OIDC sessions and retain local recovery access")
	}
	var flows int
	if err = a.db.QueryRow("SELECT count(*) FROM oidc_flows").Scan(&flows); err != nil || flows != 0 {
		t.Fatal("old authorization flows survived rebind", flows, err)
	}
	u, cookie = start(false)
	if w := callback(u, cookie, false); w.Code != 303 {
		t.Fatal("new bound identity cannot log in", w.Body.String())
	}
}
