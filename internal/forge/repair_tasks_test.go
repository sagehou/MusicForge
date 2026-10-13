package forge

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRepairTaskAPITracksAndStopsRealRepair(t *testing.T) {
	a, s := testApp(t)
	source, _, _ := failedRepairSource(t, a, s, false)
	// Hold publication/staging reservation while exercising the running API.
	a.files.Lock()
	locked := true
	defer func() { if locked { a.files.Unlock() } }()
	if _, err := a.startRepair(source, s); err != nil { t.Fatal(err) }
	history, total, err := a.repairTaskList(context.Background(), "running", 25, 0)
	if err != nil || total != 1 || len(history) != 1 || history[0].Activity.Path != source.Rel {
		t.Fatal("repair not listed immediately", history, total, err)
	}
	id := history[0].ID
	issued := httptest.NewRecorder()
	if err := a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil { t.Fatal(err) }
	cookie := issued.Result().Cookies()[0]
	send := func(method, path string, authenticated, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if authenticated { r.AddCookie(cookie) }
		if csrf { r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf")) }
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct { method, path string; auth, csrf bool; code int }{
		{"GET", "/api/repair-tasks", false, false, 401},
		{"POST", "/api/repair-tasks/"+id+"/stop", false, false, 401},
		{"DELETE", "/api/repair-tasks/"+id, false, false, 401},
		{"GET", "/api/repair-tasks?state=running", true, false, 200},
		{"GET", "/api/repair-tasks?state=invalid", true, false, 400},
		{"POST", "/api/repair-tasks/"+id+"/stop", true, false, 403},
		{"DELETE", "/api/repair-tasks/"+id, true, false, 403},
		{"DELETE", "/api/repair-tasks/"+id, true, true, 409},
		{"POST", "/api/repair-tasks/"+id+"/stop", true, true, 200},
	} {
		if w := send(tc.method, tc.path, tc.auth, tc.csrf); w.Code != tc.code { t.Fatal(tc, w.Code, w.Body.String()) }
	}
	a.files.Unlock()
	locked = false
	a.repairWG.Wait()
	history, total, err = a.repairTaskList(context.Background(), "stopped", 25, 0)
	if err != nil || total != 1 || len(history) != 1 || history[0].Stopping || history[0].Log == "" { t.Fatal(history, total, err) }
	assertNoRepairTemps(t, a)
	if w := send("DELETE", "/api/repair-tasks/"+id, true, true); w.Code != 200 { t.Fatal(w.Code, w.Body.String()) }
	if w := send("GET", "/api/repair-tasks", true, false); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) { t.Fatal(w.Code, w.Body.String()) }
	if w := send("DELETE", "/api/repair-tasks/"+id, true, true); w.Code != 404 { t.Fatal(w.Code, w.Body.String()) }
}

func TestRepairHistoryProgressRestartAndStaleControl(t *testing.T) {
	a, _ := testApp(t)
	for i, state := range []string{"success", "failed", "running"} {
		task := &repairTask{ID: strings.Repeat(string(rune('a'+i)), 64), SourceID: 1, State: state, Created: int64(i+1), Log: "retained diagnostic"}
		if err := a.saveRepairTask(task); err != nil { t.Fatal(err) }
		if state == "running" {
			ctx := context.WithValue(context.Background(), repairTaskKey{}, task)
			a.reportMediaProgress(ctx, 0, Activity{Phase: "read", Path: "Artist/Album/01.flac", ReadBytes: 50, ReadTotalBytes: 100}, .05)
			history, _, err := a.repairTaskList(ctx, "running", 25, 0)
			if err != nil || len(history) != 1 || history[0].Activity.ReadBytes != 50 || history[0].Progress != .05 { t.Fatal(history, err) }
			a.reportMediaProgress(ctx, 0, Activity{Phase: "convert", Percent: 50}, .425)
			// A stale record for this source must never cancel a newer attempt.
			newContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			a.repairs[1] = &repairEntry{task: &repairTask{ID: randomToken()}, cancel: cancel}
			r := httptest.NewRequest("POST", "/", nil)
			r.SetPathValue("id", task.ID)
			w := httptest.NewRecorder()
			a.controlRepairTask(w, r)
			if w.Code != 409 || newContext.Err() != nil { t.Fatal("stale attempt stopped current repair", w.Code) }
		}
	}
	a.Close()
	reopened, err := New(a.cfg, a.logger, "test", a.assets)
	if err != nil { t.Fatal(err) }
	defer reopened.Close()
	history, total, err := reopened.repairTaskList(context.Background(), "all", 1, 0)
	if err != nil || total != 3 || len(history) != 1 || history[0].State != "stopped" || history[0].Progress != .475 || !strings.Contains(history[0].Log, "interrupted by restart") { t.Fatal(history, total, err) }
	history, total, err = reopened.repairTaskList(context.Background(), "all", 1, 1)
	if err != nil || total != 3 || len(history) != 1 || history[0].State != "failed" || history[0].Log != "retained diagnostic" { t.Fatal(history, total, err) }
	for _, state := range []string{"success", "failed"} {
		history, total, err = reopened.repairTaskList(context.Background(), state, 25, 0)
		if err != nil || total != 1 || len(history) != 1 || history[0].Log != "retained diagnostic" { t.Fatal(history, total, err) }
	}
	var jobs, schema int
	_ = reopened.db.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs)
	_ = reopened.db.QueryRow("PRAGMA user_version").Scan(&schema)
	if jobs != 0 || schema != 2 { t.Fatal("history created executable work or changed schema", jobs, schema) }
	// Records remain ordinary JSON optional metadata for older schema-2 readers.
	raw, err := reopened.meta(repairTaskPrefix+strings.Repeat("c", 64))
	if err != nil || !json.Valid([]byte(raw)) { t.Fatal(raw, err) }
}
