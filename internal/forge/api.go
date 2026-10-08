package forge

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, status int, err error) {
	if response, ok := w.(*apiResponse); ok {
		response.err = err
	}
	respond(w, status, map[string]string{"error": err.Error()})
}

type apiResponse struct {
	http.ResponseWriter
	status int
	err    error
}

func (w *apiResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *apiResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		apiError(w, 400, errors.New("invalid JSON request: "+err.Error()))
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		apiError(w, 400, errors.New("request must contain one JSON object"))
		return false
	}
	return true
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.db.PingContext(r.Context()); err != nil {
			apiError(w, 503, errors.New("database unavailable"))
			return
		}
		respond(w, 200, map[string]string{"status": "ok", "version": a.version})
	})
	mux.HandleFunc("GET /api/auth/me", a.me)
	public := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !a.sameOrigin(r) {
				apiError(w, 403, errors.New("invalid request origin"))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("POST /api/auth/setup", public(a.setup))
	mux.HandleFunc("POST /api/auth/login", public(a.login))
	mux.HandleFunc("POST /api/auth/logout", a.auth(a.logout))
	mux.HandleFunc("GET /api/auth/oidc/login", a.oidcLogin)
	mux.HandleFunc("POST /api/auth/oidc/bind", a.auth(func(w http.ResponseWriter, r *http.Request) { a.oidcStart(w, r, true) }))
	mux.HandleFunc("GET /api/auth/oidc/callback", a.oidcCallback)
	mux.HandleFunc("POST /api/auth/password", a.auth(a.changePassword))
	mux.HandleFunc("GET /api/dashboard", a.auth(a.dashboard))
	mux.HandleFunc("GET /api/library", a.auth(a.library))
	mux.HandleFunc("GET /api/jobs", a.auth(a.jobs))
	mux.HandleFunc("POST /api/jobs/retry", a.auth(a.retry))
	mux.HandleFunc("POST /api/jobs/control", a.auth(a.controlJobs))
	mux.HandleFunc("GET /api/jobs/{id}/items", a.auth(a.jobItems))
	mux.HandleFunc("GET /api/settings", a.auth(a.getSettings))
	mux.HandleFunc("PUT /api/settings", a.auth(a.putSettings))
	mux.HandleFunc("POST /api/library/scan", a.auth(a.requestScan))
	mux.HandleFunc("POST /api/library/rebuild", a.auth(a.rebuild))
	mux.HandleFunc("POST /api/library/delete", a.auth(a.requestDelete))
	mux.HandleFunc("POST /api/navidrome/refresh", a.auth(func(w http.ResponseWriter, r *http.Request) {
		id, err := a.enqueue("refresh", "nav:refresh", map[string]bool{"manual": true}, true)
		a.jobResult(w, id, err)
	}))
	mux.HandleFunc("POST /api/webhook/lidarr", a.webhook)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { apiError(w, 404, errors.New("API route not found")) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		name := strings.TrimPrefix(filepath.Clean(r.URL.Path), "/")
		if name == "." {
			name = "index.html"
		}
		if info, err := fs.Stat(a.assets, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.FileServerFS(a.assets).ServeHTTP(w, r)
			return
		}
		index, err := fs.ReadFile(a.assets, "index.html")
		if err != nil {
			http.Error(w, "Frontend assets unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			response := &apiResponse{ResponseWriter: w}
			started := time.Now()
			defer func() {
				if response.status >= 500 {
					a.logger.Error("API request failed", "method", r.Method, "path", r.URL.Path, "status", response.status, "duration_ms", time.Since(started).Milliseconds(), "error", response.err)
				} else if time.Since(started) >= 2*time.Second {
					a.logger.Warn("slow API request", "method", r.Method, "path", r.URL.Path, "status", response.status, "duration_ms", time.Since(started).Milliseconds())
				}
			}()
			mux.ServeHTTP(response, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *App) oidcLogin(w http.ResponseWriter, r *http.Request) { a.oidcStart(w, r, false) }
func (a *App) jobResult(w http.ResponseWriter, id int64, err error) {
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 202, map[string]int64{"job_id": id})
}

func (a *App) sourcesWithStatus() ([]Source, error) {
	s, err := a.settings()
	if err != nil {
		return nil, err
	}
	list, err := a.allSources()
	if err != nil {
		return nil, err
	}
	for i := range list {
		source := &list[i]
		switch {
		case !source.Present:
			source.Status = "expired"
		case source.Error != "":
			source.Status = "failed"
		case !source.OutputPresent:
			source.Status = "missing"
		case source.BuiltHash != source.Hash:
			source.Status = "changed"
		case source.BuiltProfile != s.Encoding.Fingerprint():
			source.Status = "needs_rebuild"
		default:
			source.Status = "ready"
		}
	}
	return list, nil
}
func (a *App) library(w http.ResponseWriter, r *http.Request) {
	list, err := a.sourcesWithStatus()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, list)
}
func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	list, err := a.sourcesWithStatus()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	sourceCount, outputCount, ready, expired, rebuild, failed := 0, 0, 0, 0, 0, 0
	for _, source := range list {
		if source.Present {
			sourceCount++
		}
		if source.OutputPresent {
			outputCount++
		}
		if source.Status == "ready" {
			ready++
		}
		if source.Status == "expired" && source.OutputPresent {
			expired++
		}
		if source.Status == "needs_rebuild" {
			rebuild++
		}
		if source.Status == "failed" {
			failed++
		}
	}
	s, err := a.settings()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	online := s.Enabled
	checking := false
	reason := "Library is not enabled"
	if s.Enabled {
		online, reason, checking = a.storageStatus(s)
	}
	respond(w, 200, map[string]any{"source_count": sourceCount, "output_count": outputCount, "ready_count": ready, "expired_count": expired, "rebuild_count": rebuild, "failed_count": failed, "online": online, "storage_checking": checking, "storage_message": reason, "codec": s.Encoding.Codec, "enabled": s.Enabled, "version": a.version})
}

func (a *App) jobs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	offset := 0
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 {
		offset = n
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 {
		limit = n
	}
	state := r.URL.Query().Get("state")
	switch state {
	case "", "all", "pending", "running", "success", "failed", "paused", "stopped":
	default:
		apiError(w, 400, errors.New("invalid job state filter"))
		return
	}
	list, total, err := a.taskList(state, limit, offset)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, map[string]any{"jobs": list, "total": total})
}

type selection struct {
	IDs []int64 `json:"ids"`
	All bool    `json:"all"`
}

func (a *App) retry(w http.ResponseWriter, r *http.Request) {
	var body selection
	if !decode(w, r, &body) {
		return
	}
	if !body.All && len(body.IDs) == 0 {
		apiError(w, 400, errors.New("select failed jobs"))
		return
	}
	if len(body.IDs) > 10000 {
		apiError(w, 400, errors.New("too many selected jobs"))
		return
	}
	n, err := a.changeTasks("retry", body)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	respond(w, 202, map[string]int{"retried": n})
}

func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := a.settings()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	configured := map[string]bool{"webhook": s.WebhookHash != "", "nav_password": s.NavPassword != "", "oidc_secret": s.OIDCSecret != ""}
	s.WebhookHash = ""
	s.NavPassword = ""
	s.OIDCSecret = ""
	respond(w, 200, map[string]any{"settings": s, "configured": configured, "public_url": a.cfg.PublicURL, "allowed_origins": a.cfg.origins()})
}
func (a *App) putSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Settings
		WebhookSecret    string `json:"webhook_secret"`
		ClearWebhook     bool   `json:"clear_webhook"`
		ClearNavPassword bool   `json:"clear_nav_password"`
		ClearOIDCSecret  bool   `json:"clear_oidc_secret"`
		UnbindOIDC       bool   `json:"unbind_oidc"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !a.files.TryLock() {
		apiError(w, 409, errors.New("library file operation in progress; try saving Settings again shortly"))
		return
	}
	defer a.files.Unlock()
	a.authMu.Lock()
	defer a.authMu.Unlock()
	old, err := a.settings()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	s := body.Settings
	s.Source = filepath.Clean(s.Source)
	s.Output = filepath.Clean(s.Output)
	s.NavURL = strings.TrimRight(s.NavURL, "/")
	s.WebhookHash = old.WebhookHash
	if body.ClearWebhook {
		s.WebhookHash = ""
	}
	if body.WebhookSecret != "" {
		if len(body.WebhookSecret) < 24 {
			apiError(w, 400, errors.New("webhook secret must contain at least 24 characters"))
			return
		}
		s.WebhookHash = digest(body.WebhookSecret)
	}
	if body.ClearNavPassword {
		s.NavPassword = ""
	} else if s.NavPassword == "" {
		s.NavPassword = old.NavPassword
	}
	if body.ClearOIDCSecret {
		s.OIDCSecret = ""
	} else if s.OIDCSecret == "" {
		s.OIDCSecret = old.OIDCSecret
	}
	s.BoundIssuer = old.BoundIssuer
	s.BoundSubject = old.BoundSubject
	s.BoundUsername = old.BoundUsername
	s.BoundEmail = old.BoundEmail
	oidcChanged := body.UnbindOIDC || body.ClearOIDCSecret || s.OIDCIssuer != old.OIDCIssuer || s.OIDCClientID != old.OIDCClientID || s.OIDCSecret != old.OIDCSecret
	if oidcChanged {
		current, sessionErr := a.session(r)
		if sessionErr != nil || current.Method != "local" {
			apiError(w, 403, errors.New("local login required to change OIDC settings"))
			return
		}
		s.BoundIssuer = ""
		s.BoundSubject = ""
		s.BoundUsername = ""
		s.BoundEmail = ""
	}
	if body.ClearNavPassword && s.NavURL != "" {
		apiError(w, 400, errors.New("disable Navidrome before clearing its password"))
		return
	}
	if body.ClearOIDCSecret && s.OIDCIssuer != "" {
		apiError(w, 400, errors.New("disable OIDC before clearing its client secret"))
		return
	}
	if err = s.Validate(); err != nil {
		apiError(w, 400, err)
		return
	}
	if s.OIDCIssuer != "" && a.cfg.PublicURL == "" {
		apiError(w, 400, errors.New("set MUSICFORGE_PUBLIC_URL before enabling OIDC"))
		return
	}
	if s.Source != old.Source || s.Output != old.Output {
		var count int
		if err = a.db.QueryRow("SELECT (SELECT count(*) FROM sources)+(SELECT count(*) FROM managed)+(SELECT count(*) FROM jobs WHERE state IN ('pending','running'))").Scan(&count); err != nil {
			apiError(w, 500, err)
			return
		}
		if count > 0 {
			apiError(w, 409, errors.New("library roots cannot change after indexing; relocate host mounts while keeping container paths"))
			return
		}
	}
	if s.Enabled {
		storageCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err = a.initializeStorageContext(storageCtx, s); err != nil {
			apiError(w, 400, err)
			return
		}
	}
	if oidcChanged {
		err = a.saveOIDCSettings(s)
	} else {
		err = a.saveSettings(s)
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if s.Enabled && !old.Enabled {
		_, _ = a.enqueue("scan", "scan:periodic", ScanRequest{}, false)
	}
	respond(w, 200, map[string]bool{"ok": true, "encoding_changed": s.Encoding.Fingerprint() != old.Encoding.Fingerprint()})
}
func (a *App) requestScan(w http.ResponseWriter, r *http.Request) {
	var body ScanRequest
	if !decode(w, r, &body) {
		return
	}
	s, err := a.settings()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if !s.Enabled {
		apiError(w, 409, errors.New("enable the library in Settings first"))
		return
	}
	for _, dir := range body.Dirs {
		if err = validRelativePath(dir); err != nil {
			apiError(w, 400, err)
			return
		}
	}
	key := "scan:manual:" + strconv.FormatBool(body.Verify)
	id, err := a.enqueue("scan", key, body, true)
	a.jobResult(w, id, err)
}
func (a *App) rebuild(w http.ResponseWriter, r *http.Request) {
	var body selection
	if !decode(w, r, &body) {
		return
	}
	s, err := a.settings()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if !s.Enabled {
		apiError(w, 409, errors.New("library is disabled"))
		return
	}
	list, err := a.allSources()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	selected := map[int64]bool{}
	for _, id := range body.IDs {
		selected[id] = true
	}
	count := 0
	task := int64(0)
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	for _, source := range list {
		if !source.Present || source.Hash == "" || (!body.All && !selected[source.ID]) {
			continue
		}
		if source.BuiltHash == source.Hash && source.BuiltProfile == s.Encoding.Fingerprint() && source.OutputPresent && source.Output == outputRel(source.Rel, s.Encoding.Codec) {
			continue
		}
		var id int64
		if id, err = a.queueBuildTask(source, s.Encoding, false, true, task); err != nil {
			apiError(w, 500, err)
			return
		}
		if task == 0 {
			task = a.taskID(id)
			if err = a.setMeta(taskMemberKey(id), strconv.FormatInt(task, 10)); err != nil {
				apiError(w, 500, err)
				return
			}
		}
		count++
	}
	respond(w, 202, map[string]any{"queued": count, "job_id": task})
}
func (a *App) requestDelete(w http.ResponseWriter, r *http.Request) {
	var body selection
	if !decode(w, r, &body) {
		return
	}
	list, err := a.allSources()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	selected := map[int64]bool{}
	for _, id := range body.IDs {
		selected[id] = true
	}
	ids := []int64{}
	for _, source := range list {
		if !source.Present && source.Output != "" && (body.All || selected[source.ID]) {
			ids = append(ids, source.ID)
		}
	}
	if len(ids) == 0 {
		respond(w, 200, map[string]int{"deleted": 0})
		return
	}
	key := fmt.Sprintf("delete:%s", randomToken())
	id, err := a.enqueue("delete", key, map[string]any{"ids": ids}, true)
	a.jobResult(w, id, err)
}
func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	a.authMu.Lock()
	defer a.authMu.Unlock()
	session, err := a.session(r)
	if err != nil || session.Method != "local" {
		apiError(w, 403, errors.New("local login required"))
		return
	}
	if !a.verifyPassword(body.Current) {
		apiError(w, 403, errors.New("current password is incorrect"))
		return
	}
	if err = a.ResetPassword(body.Password); err != nil {
		apiError(w, 400, err)
		return
	}
	if err = a.newSession(w, r, "local"); err != nil {
		apiError(w, 500, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func constantEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
