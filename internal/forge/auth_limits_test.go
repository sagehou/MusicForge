package forge

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestClientIPTrustBoundary(t *testing.T) {
	a, _ := testApp(t)
	for _, test := range []struct {
		name, peer, forwarded, want string
		trusted                     []netip.Prefix
	}{
		{"direct ignores spoof", "198.51.100.2:4000", "203.0.113.9", "198.51.100.2", nil},
		{"proxy not configured", "10.0.0.2:4000", "203.0.113.9", "10.0.0.2", nil},
		{"trusted proxy", "10.0.0.2:4000", "203.0.113.9", "203.0.113.9", []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}},
		{"ignore invented leftmost", "10.0.0.2:4000", "203.0.113.8, 198.51.100.2", "198.51.100.2", []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}},
		{"multiple trusted hops", "10.0.0.2:4000", "203.0.113.9, 10.0.0.3", "203.0.113.9", []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}},
		{"IPv6", "[::1]:4000", "2001:db8::7", "2001:db8::7", []netip.Prefix{netip.MustParsePrefix("::1/128")}},
		{"invalid chain falls back", "10.0.0.2:4000", "invalid, 198.51.100.2", "10.0.0.2", []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}},
		{"missing header falls back", "10.0.0.2:4000", "", "10.0.0.2", []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a.cfg.TrustedProxies = test.trusted
			r := httptest.NewRequest("POST", "/api/auth/login", nil)
			r.RemoteAddr = test.peer
			r.Header.Set("X-Forwarded-For", test.forwarded)
			if got := a.clientIP(r); got != test.want {
				t.Fatalf("client IP = %q, want %q", got, test.want)
			}
		})
	}
	for _, invalid := range []string{"proxy", "10.0.0.2", "10.0.0.0/99", "10.0.0.2/32,"} {
		t.Setenv("MUSICFORGE_TRUSTED_PROXIES", invalid)
		if _, err := LoadRuntime(t.TempDir()); err == nil {
			t.Fatal("accepted invalid trusted proxy CIDR", invalid)
		}
	}
	t.Setenv("MUSICFORGE_TRUSTED_PROXIES", "10.0.0.2/32, ::1/128")
	if cfg, err := LoadRuntime(t.TempDir()); err != nil || len(cfg.TrustedProxies) != 2 {
		t.Fatal("valid proxy configuration rejected", cfg, err)
	}
}

func TestAuthenticationLimitsCountFailuresAndSeparateClients(t *testing.T) {
	a, _ := testApp(t)
	a.cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}
	request := func(path, client string, body any) int {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		if path == "/api/auth/oidc/login" {
			r.Method = "GET"
		}
		r.RemoteAddr = "10.0.0.2:4000"
		r.Header.Set("X-Forwarded-For", client)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w.Code
	}
	attacker, admin := "198.51.100.2", "203.0.113.9"
	badSetup := map[string]string{"code": "wrong", "username": "admin", "password": "test-admin-password"}
	for i := 0; i < 10; i++ {
		if got := request("/api/auth/setup", attacker, badSetup); got != 403 {
			t.Fatal("setup attempt", i, got)
		}
	}
	if got := request("/api/auth/setup", attacker, badSetup); got != 429 {
		t.Fatal("setup not limited", got)
	}
	goodSetup := map[string]string{"code": a.bootstrap, "username": "admin", "password": "test-admin-password"}
	if got := request("/api/auth/setup", admin, goodSetup); got != 201 {
		t.Fatal("attacker locked administrator setup", got)
	}
	badLogin := map[string]string{"username": "admin", "password": "wrong-password"}
	goodLogin := map[string]string{"username": "admin", "password": "test-admin-password"}
	for i := 0; i < 10; i++ {
		if got := request("/api/auth/login", attacker, badLogin); got != 401 {
			t.Fatal("login attempt", i, got)
		}
	}
	if got := request("/api/auth/login", "192.0.2.10, "+attacker, goodLogin); got != 429 {
		t.Fatal("spoofed leftmost address bypassed limit", got)
	}
	// Even on one client, successful local logins never exhaust the failure budget.
	for i := 0; i < 12; i++ {
		if got := request("/api/auth/login", admin, goodLogin); got != 200 {
			t.Fatal("successful login counted or attacker locked administrator", i, got)
		}
	}
	for round := 0; round < 2; round++ {
		for i := 0; i < 9; i++ {
			if got := request("/api/auth/login", admin, badLogin); got != 401 {
				t.Fatal("failure budget was not reset", round, i, got)
			}
		}
		if got := request("/api/auth/login", admin, goodLogin); got != 200 {
			t.Fatal("success did not reset failures", got)
		}
	}
	for i := 0; i < 10; i++ {
		if got := request("/api/auth/oidc/login", admin, nil); got != 400 {
			t.Fatal("OIDC budget shares login failures", i, got)
		}
	}
	if got := request("/api/auth/oidc/login", admin, nil); got != 429 {
		t.Fatal("OIDC start not limited", got)
	}
	if got := request("/api/auth/login", admin, goodLogin); got != 200 {
		t.Fatal("OIDC start exhaustion locked local recovery", got)
	}
	// Fixed windows expire independently without rejected requests extending them.
	a.authMu.Lock()
	v := a.loginFailures["login:"+attacker]
	v.Since = time.Now().Add(-16 * time.Minute)
	a.loginFailures["login:"+attacker] = v
	a.authMu.Unlock()
	if got := request("/api/auth/login", attacker, goodLogin); got != 200 {
		t.Fatal("expired window did not recover", got)
	}
}
