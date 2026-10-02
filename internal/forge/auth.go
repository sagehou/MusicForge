package forge

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
)

func passwordHash(password string) ([]byte, error) {
	if len(password) < 12 || len(password) > 72 {
		return nil, errors.New("password must contain 12–72 bytes")
	}
	return bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
}
func (a *App) ResetPassword(password string) error {
	hash, err := passwordHash(password)
	if err != nil {
		return err
	}
	tx, err := a.db.Begin()
 if err != nil { return err }
 defer tx.Rollback()
 result, err := tx.Exec("UPDATE admin SET password=? WHERE id=1", hash)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("no administrator exists; use web setup")
	}
	if _, err = tx.Exec("DELETE FROM sessions"); err != nil { return err }
 return tx.Commit()
}

type session struct {
	Token  string
	Method string
}

func (a *App) session(r *http.Request) (session, error) {
	cookie, err := r.Cookie("musicforge_session")
	if err != nil {
		return session{}, err
	}
	var method string
	err = a.db.QueryRow("SELECT method FROM sessions WHERE token=? AND expires>?", digest(cookie.Value), time.Now().Unix()).Scan(&method)
	return session{cookie.Value, method}, err
}
func (a *App) secureCookie() bool { return strings.HasPrefix(a.cfg.PublicURL, "https://") }
func (a *App) newSession(w http.ResponseWriter, method string) error {
	token := randomToken()
	_, err := a.db.Exec("INSERT INTO sessions(token,method,expires) VALUES(?,?,?)", digest(token), method, time.Now().Add(7*24*time.Hour).Unix())
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "musicforge_session", Value: token, Path: "/", HttpOnly: true, Secure: a.secureCookie(), SameSite: http.SameSiteLaxMode, MaxAge: 7 * 24 * 3600})
	return nil
}
func (a *App) origin(r *http.Request) string {
	if a.cfg.PublicURL != "" {
		return a.cfg.PublicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
func (a *App) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || origin == a.origin(r)
}
func (a *App) limited(r *http.Request) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	a.authMu.Lock()
	defer a.authMu.Unlock()
	now := time.Now()
	for key, v := range a.loginFailures {
		if now.Sub(v.Since) > 15*time.Minute {
			delete(a.loginFailures, key)
		}
	}
	v := a.loginFailures[ip]
	if v.Since.IsZero() {
		v.Since = now
	}
	v.Count++
	a.loginFailures[ip] = v
	return v.Count > 10
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	var count int
	if err := a.db.QueryRow("SELECT count(*) FROM admin").Scan(&count); err != nil {
		apiError(w, 500, err)
		return
	}
	s, err := a.settings()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	result := map[string]any{"initialized": count > 0, "authenticated": false, "oidc": s.OIDCIssuer != "" && s.BoundSubject != "", "version": a.version}
	if session, err := a.session(r); err == nil {
		var username string
		if err = a.db.QueryRow("SELECT username FROM admin WHERE id=1").Scan(&username); err != nil {
			apiError(w, 500, err)
			return
		}
		result["authenticated"] = true
		result["username"] = username
		result["method"] = session.Method
		result["csrf"] = digest(session.Token + ":csrf")
	}
	respond(w, 200, result)
}
func (a *App) setup(w http.ResponseWriter, r *http.Request) {
	if a.limited(r) {
		apiError(w, 429, errors.New("too many attempts; wait 15 minutes"))
		return
	}
	var body struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if a.bootstrap == "" || subtle.ConstantTimeCompare([]byte(body.Code), []byte(a.bootstrap)) != 1 {
		apiError(w, 403, errors.New("invalid or expired setup code"))
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if len(body.Username) < 1 || len(body.Username) > 100 {
		apiError(w, 400, errors.New("username must contain 1–100 bytes"))
		return
	}
	hash, err := passwordHash(body.Password)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	if _, err = a.db.Exec("INSERT INTO admin(id,username,password) VALUES(1,?,?)", body.Username, hash); err != nil {
		apiError(w, 409, errors.New("setup already completed"))
		return
	}
	a.bootstrap = ""
	if err = a.newSession(w, "local"); err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 201, map[string]bool{"ok": true})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if a.limited(r) {
		apiError(w, 429, errors.New("too many attempts; wait 15 minutes"))
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	var username string
	var hash []byte
	err := a.db.QueryRow("SELECT username,password FROM admin WHERE id=1").Scan(&username, &hash)
	if err != nil || bcrypt.CompareHashAndPassword(hash, []byte(body.Password)) != nil || subtle.ConstantTimeCompare([]byte(username), []byte(body.Username)) != 1 {
		apiError(w, 401, errors.New("invalid username or password"))
		return
	}
	if err = a.newSession(w, "local"); err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if session, err := a.session(r); err == nil {
		_, _ = a.db.Exec("DELETE FROM sessions WHERE token=?", digest(session.Token))
	}
	http.SetCookie(w, &http.Cookie{Name: "musicforge_session", Value: "", Path: "/", HttpOnly: true, Secure: a.secureCookie(), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	respond(w, 200, map[string]bool{"ok": true})
}

func (a *App) oidcConfig(ctx context.Context) (*oidc.Provider, oauth2.Config, Settings, error) {
	s, err := a.settings()
	if err != nil {
		return nil, oauth2.Config{}, s, err
	}
	if s.OIDCIssuer == "" || a.cfg.PublicURL == "" {
		return nil, oauth2.Config{}, s, errors.New("configure OIDC and MUSICFORGE_PUBLIC_URL first")
	}
	provider, err := oidc.NewProvider(ctx, s.OIDCIssuer)
	if err != nil {
		return nil, oauth2.Config{}, s, errors.New("OIDC discovery failed; verify issuer and connectivity")
	}
	config := oauth2.Config{ClientID: s.OIDCClientID, ClientSecret: s.OIDCSecret, Endpoint: provider.Endpoint(), RedirectURL: a.cfg.PublicURL + "/api/auth/oidc/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}
	return provider, config, s, nil
}
func (a *App) oidcStart(w http.ResponseWriter, r *http.Request, bind bool) {
	if a.limited(r) {
		apiError(w, 429, errors.New("too many login attempts"))
		return
	}
	sessionToken := ""
	action := "login"
	if bind {
		session, err := a.session(r)
		if err != nil || session.Method != "local" {
			apiError(w, 403, errors.New("use local administrator login to bind OIDC"))
			return
		}
		sessionToken = digest(session.Token)
		action = "bind"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	_, config, s, err := a.oidcConfig(ctx)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	if !bind && s.BoundSubject == "" {
		apiError(w, 403, errors.New("OIDC identity is not bound"))
		return
	}
	state, nonce, verifier := randomToken(), randomToken(), oauth2.GenerateVerifier()
	_, err = a.db.Exec("INSERT INTO oidc_flows(state,nonce,verifier,action,session,expires) VALUES(?,?,?,?,?,?)", digest(state), nonce, verifier, action, sessionToken, time.Now().Add(10*time.Minute).Unix())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "musicforge_oidc", Value: state, Path: "/api/auth/oidc", HttpOnly: true, Secure: a.secureCookie(), SameSite: http.SameSiteLaxMode, MaxAge: 600})
	location := config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	if bind {
		respond(w, 200, map[string]string{"url": location})
	} else {
		http.Redirect(w, r, location, http.StatusSeeOther)
	}
}
func (a *App) oidcCallback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("musicforge_oidc")
	state := r.URL.Query().Get("state")
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		apiError(w, 403, errors.New("invalid OIDC state"))
		return
	}
	var nonce, verifier, action, sessionHash string
	err = a.db.QueryRow("DELETE FROM oidc_flows WHERE state=? AND expires>? RETURNING nonce,verifier,action,session", digest(state), time.Now().Unix()).Scan(&nonce, &verifier, &action, &sessionHash)
	if err != nil {
		apiError(w, 403, errors.New("expired or already used OIDC flow"))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "musicforge_oidc", Value: "", Path: "/api/auth/oidc", HttpOnly: true, Secure: a.secureCookie(), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	provider, config, s, err := a.oidcConfig(ctx)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	token, err := config.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		apiError(w, 401, errors.New("OIDC code exchange failed"))
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		apiError(w, 401, errors.New("OIDC provider returned no ID token"))
		return
	}
	id, err := provider.Verifier(&oidc.Config{ClientID: config.ClientID}).Verify(ctx, raw)
	if err != nil || id.Nonce != nonce {
		apiError(w, 401, errors.New("OIDC token validation failed"))
		return
	}
	var claims struct {
		Username string `json:"preferred_username"`
		Email    string `json:"email"`
	}
	if err = id.Claims(&claims); err != nil {
		apiError(w, 401, errors.New("invalid OIDC claims"))
		return
	}
	if action == "bind" {
		session, err := a.session(r)
		if err != nil || session.Method != "local" || digest(session.Token) != sessionHash {
			apiError(w, 403, errors.New("local administrator session expired"))
			return
		}
		a.authMu.Lock()
		latest, err := a.settings()
		if err == nil && (latest.OIDCIssuer != s.OIDCIssuer || latest.OIDCClientID != s.OIDCClientID || latest.OIDCSecret != s.OIDCSecret) {
			err = errors.New("OIDC settings changed during binding")
		}
		if err == nil {
			latest.BoundIssuer = id.Issuer
			latest.BoundSubject = id.Subject
			latest.BoundUsername = claims.Username
			latest.BoundEmail = claims.Email
			err = a.saveSettings(latest)
		}
		a.authMu.Unlock()
		if err != nil {
			apiError(w, 400, err)
			return
		}
		http.Redirect(w, r, "/settings?bound=1", http.StatusSeeOther)
		return
	}
	// Serialize binding/configuration changes with session issuance.
 a.authMu.Lock()
 defer a.authMu.Unlock()
 latest, err := a.settings()
 if err != nil || latest.OIDCIssuer != s.OIDCIssuer || latest.OIDCClientID != s.OIDCClientID || latest.OIDCSecret != s.OIDCSecret || id.Issuer != latest.BoundIssuer || id.Subject != latest.BoundSubject {
		apiError(w, 403, errors.New("this OIDC identity is not the administrator"))
		return
	}
	if err = a.newSession(w, "oidc"); err != nil {
		apiError(w, 500, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := a.session(r)
		if err != nil {
			apiError(w, 401, errors.New("login required"))
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			csrf := r.Header.Get("X-CSRF-Token")
			expected := digest(session.Token + ":csrf")
			if !a.sameOrigin(r) || subtle.ConstantTimeCompare([]byte(csrf), []byte(expected)) != 1 {
				apiError(w, 403, errors.New("invalid request origin or CSRF token"))
				return
			}
		}
		next(w, r)
	}
}

func (a *App) verifyPassword(password string) bool {
	var hash []byte
	if a.db.QueryRow("SELECT password FROM admin WHERE id=1").Scan(&hash) != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
}
