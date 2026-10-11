package forge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionReadFailureDoesNotInvalidateLocalOrOIDCLogin(t *testing.T) {
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
			var logs bytes.Buffer
			a.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			send := func(path string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", path, nil)
				r.AddCookie(cookie)
				w := httptest.NewRecorder()
				a.Handler().ServeHTTP(w, r)
				return w
			}
			// Fault only the session lookup; admin and Settings remain readable.
			if _, err := a.db.Exec("ALTER TABLE sessions RENAME TO unavailable_sessions"); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/api/auth/me", "/api/library"} {
				w := send(path)
				if w.Code != 503 || w.Header().Get("Retry-After") != "1" || strings.Contains(w.Body.String(), `"authenticated":false`) || len(w.Result().Cookies()) != 0 {
					t.Fatal("lookup failure was treated as logout", path, w.Code, w.Body.String())
				}
			}
			if !strings.Contains(logs.String(), "no such table: sessions") || !strings.Contains(logs.String(), "db_read_in_use") {
				t.Fatal("API failure lost its cause or database context", logs.String())
			}
			if _, err := a.db.Exec("ALTER TABLE unavailable_sessions RENAME TO sessions"); err != nil {
				t.Fatal(err)
			}
			w := send("/api/auth/me")
			var me struct {
				Authenticated bool `json:"authenticated"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil || w.Code != 200 || !me.Authenticated {
				t.Fatal("same session did not recover", w.Code, w.Body.String(), err)
			}
			if _, err := a.db.Exec("UPDATE sessions SET expires=0"); err != nil {
				t.Fatal(err)
			}
			if w = send("/api/library"); w.Code != 401 {
				t.Fatal("expired session was accepted", w.Code)
			}
		})
	}
}

func TestUnavailableLoginDatabaseDoesNotConsumeCredentialAttempts(t *testing.T) {
	a, _ := testApp(t)
	if _, err := a.db.Exec("ALTER TABLE admin RENAME TO unavailable_admin"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"valid-password"}`))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 503 || len(a.loginFailures) != 0 {
			t.Fatal("database outage counted as invalid credentials", w.Code, a.loginFailures)
		}
	}
}

func TestCancelledReadsReleaseConnectionPoolAndTaskControlWait(t *testing.T) {
	a, _ := testApp(t)
	var connections []*sql.Conn
	for i := 0; i < 4; i++ {
		connection, err := a.reads.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
	}
	release := func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}
	defer release()
	checks := []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := a.settingsContext(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := a.sourcesWithStatusContext(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, _, err := a.taskListContext(ctx, "all", 100, 0)
			return err
		},
		func(ctx context.Context) error {
			_, _, err := a.taskItemsContext(ctx, 1, "all", 50, 0)
			return err
		},
		func(ctx context.Context) error {
			_, err := a.metaContext(ctx, "instance")
			return err
		},
	}
	for index, check := range checks {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		done := make(chan error, 1)
		go func() { done <- check(ctx) }()
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("read lost cancellation", index, err)
			}
		case <-time.After(time.Second):
			release()
			cancel()
			<-done
			t.Fatal("abandoned read remained queued for a connection", index)
		}
		cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	r := httptest.NewRequest("GET", "/api/library", nil).WithContext(ctx)
	r.AddCookie(&http.Cookie{Name: "musicforge_session", Value: "test-session"})
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	cancel()
	if w.Code != 503 {
		t.Fatal("connection exhaustion became an authentication failure", w.Code)
	}
	release()
	if _, err := a.enqueue("scan", "scan:cancel-control-wait", ScanRequest{}, true); err != nil {
		t.Fatal(err)
	}
	a.jobsMu.Lock()
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, _, err := a.taskListContext(ctx, "all", 100, 0)
	cancel()
	a.jobsMu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("task read ignored cancellation while waiting for queue control", err)
	}
	if err = a.reads.Ping(); err != nil {
		t.Fatal("cancelled reads prevented subsequent recovery", err)
	}
}

type sqliteBusyFixture int

func (e sqliteBusyFixture) Error() string { return fmt.Sprintf("SQLite fault %d", e) }
func (e sqliteBusyFixture) Code() int     { return int(e) }

func TestAPIBusyErrorsAreRetryableAndRetainOriginalDiagnostic(t *testing.T) {
	for _, code := range []int{5, 6, 5 | 2<<8} {
		w := httptest.NewRecorder()
		response := &apiResponse{ResponseWriter: w}
		cause := fmt.Errorf("source query: %w", sqliteBusyFixture(code))
		apiError(response, 500, cause)
		if w.Code != 503 || response.err != cause || !strings.Contains(w.Body.String(), "Database is busy; please retry") {
			t.Fatal("busy error lost retry status or original diagnostic", code, w.Code, response.err)
		}
	}
}
