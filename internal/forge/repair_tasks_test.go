package forge

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairTaskAPITracksAndStopsRealRepair(t *testing.T) {
	a, s := testApp(t)
	source, _, _ := failedRepairSource(t, a, s, false)
	// Hold publication/staging reservation while exercising the running API.
	a.files.Lock()
	locked := true
	defer func() {
		if locked {
			a.files.Unlock()
		}
	}()
	if _, err := a.startRepair(source, s); err != nil {
		t.Fatal(err)
	}
	history, total, err := a.repairTaskList(context.Background(), "running", 25, 0)
	if err != nil || total != 1 || len(history) != 1 || history[0].Activity.Path != source.Rel {
		t.Fatal("repair not listed immediately", history, total, err)
	}
	id := history[0].ID
	issued := httptest.NewRecorder()
	if err := a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	cookie := issued.Result().Cookies()[0]
	send := func(method, path string, authenticated, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if authenticated {
			r.AddCookie(cookie)
		}
		if csrf {
			r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		method, path string
		auth, csrf   bool
		code         int
	}{
		{"GET", "/api/repair-tasks", false, false, 401},
		{"POST", "/api/repair-tasks/" + id + "/stop", false, false, 401},
		{"DELETE", "/api/repair-tasks/" + id, false, false, 401},
		{"GET", "/api/repair-tasks?state=running", true, false, 200},
		{"GET", "/api/repair-tasks?state=invalid", true, false, 400},
		{"POST", "/api/repair-tasks/" + id + "/stop", true, false, 403},
		{"DELETE", "/api/repair-tasks/" + id, true, false, 403},
		{"DELETE", "/api/repair-tasks/" + id, true, true, 409},
		{"POST", "/api/repair-tasks/" + id + "/stop", true, true, 200},
	} {
		if w := send(tc.method, tc.path, tc.auth, tc.csrf); w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	a.files.Unlock()
	locked = false
	a.repairWG.Wait()
	history, total, err = a.repairTaskList(context.Background(), "stopped", 25, 0)
	if err != nil || total != 1 || len(history) != 1 || history[0].Stopping || history[0].Log == "" {
		t.Fatal(history, total, err)
	}
	assertNoRepairTemps(t, a)
	if w := send("DELETE", "/api/repair-tasks/"+id, true, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("GET", "/api/repair-tasks", true, false); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("DELETE", "/api/repair-tasks/"+id, true, true); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestRepairHistoryProgressRestartAndStaleControl(t *testing.T) {
	a, _ := testApp(t)
	for i, state := range []string{"success", "failed", "running"} {
		task := &repairTask{ID: strings.Repeat(string(rune('a'+i)), 64), SourceID: 1, State: state, Created: int64(i + 1), Log: "retained diagnostic"}
		if err := a.saveRepairTask(task); err != nil {
			t.Fatal(err)
		}
		if state == "running" {
			ctx := context.WithValue(context.Background(), repairTaskKey{}, task)
			a.reportMediaProgress(ctx, 0, Activity{Phase: "read", Path: "Artist/Album/01.flac", ReadBytes: 50, ReadTotalBytes: 100}, .05)
			history, _, err := a.repairTaskList(ctx, "running", 25, 0)
			if err != nil || len(history) != 1 || history[0].Activity.ReadBytes != 50 || history[0].Progress != .05 {
				t.Fatal(history, err)
			}
			a.reportMediaProgress(ctx, 0, Activity{Phase: "convert", Percent: 50}, .425)
			// A stale record for this source must never cancel a newer attempt.
			newContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			a.repairs[1] = &repairEntry{task: &repairTask{ID: randomToken()}, cancel: cancel}
			r := httptest.NewRequest("POST", "/", nil)
			r.SetPathValue("id", task.ID)
			w := httptest.NewRecorder()
			a.controlRepairTask(w, r)
			if w.Code != 409 || newContext.Err() != nil {
				t.Fatal("stale attempt stopped current repair", w.Code)
			}
		}
	}
	a.Close()
	reopened, err := New(a.cfg, a.logger, "test", a.assets)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	history, total, err := reopened.repairTaskList(context.Background(), "all", 1, 0)
	if err != nil || total != 3 || len(history) != 1 || history[0].State != "stopped" || history[0].Progress != .475 || !strings.Contains(history[0].Log, "interrupted by restart") {
		t.Fatal(history, total, err)
	}
	history, total, err = reopened.repairTaskList(context.Background(), "all", 1, 1)
	if err != nil || total != 3 || len(history) != 1 || history[0].State != "failed" || history[0].Log != "retained diagnostic" {
		t.Fatal(history, total, err)
	}
	for _, state := range []string{"success", "failed"} {
		history, total, err = reopened.repairTaskList(context.Background(), state, 25, 0)
		if err != nil || total != 1 || len(history) != 1 || history[0].Log != "retained diagnostic" {
			t.Fatal(history, total, err)
		}
	}
	var jobs, schema int
	_ = reopened.db.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs)
	_ = reopened.db.QueryRow("PRAGMA user_version").Scan(&schema)
	if jobs != 0 || schema != 2 {
		t.Fatal("history created executable work or changed schema", jobs, schema)
	}
	// Records remain ordinary JSON optional metadata for older schema-2 readers.
	raw, err := reopened.meta(repairTaskPrefix + strings.Repeat("c", 64))
	if err != nil || !json.Valid([]byte(raw)) {
		t.Fatal(raw, err)
	}
}

func TestUnifiedTasksPaginateAndControlGroupedRepairHistory(t *testing.T) {
	a, s := testApp(t)
	first, err := a.enqueue("scan", "scan:first", ScanRequest{}, false)
	if err != nil { t.Fatal(err) }
	last, err := a.enqueue("scan", "scan:last", ScanRequest{}, false)
	if err != nil { t.Fatal(err) }
	if _, err = a.db.Exec("UPDATE jobs SET state='success',created=CASE id WHEN ? THEN 100 ELSE 400 END", first); err != nil { t.Fatal(err) }
	root := strings.Repeat("a", 64)
	for i, state := range []string{"failed", "success", "success"} {
		id := strings.Repeat(string(rune('a'+i)), 64)
		task := &repairTask{ID: id, SourceID: int64(i+1), State: state, Created: int64(200+i*50), Updated: int64(200+i*50), Profile: s.Encoding, Activity: Activity{Path: "Artist/Album/01.flac", Phase: "convert"}}
		if state == "success" { task.Progress = 1 }
		if err := a.saveRepairTask(task); err != nil { t.Fatal(err) }
		if i < 2 { if err := a.setMeta(repairMemberPrefix+id, root); err != nil { t.Fatal(err) } }
	}
	if err := a.setMeta("repair-artifact:1:retained", "retained"); err != nil { t.Fatal(err) }
	page, total, err := a.unifiedTaskList(context.Background(), "all", 2, 0)
	if err != nil || total != 4 || len(page) != 2 || page[0].ID != last || page[1].RepairID != strings.Repeat("c", 64) { t.Fatal(page, total, err) }
	page, total, err = a.unifiedTaskList(context.Background(), "all", 2, 2)
	if err != nil || total != 4 || len(page) != 2 || page[0].RepairID != root || page[0].Counts.Total != 2 || page[0].Counts.Done != 1 || page[0].Counts.Failed != 1 || !page[0].CanDelete || page[1].ID != first { t.Fatal(page, total, err) }
	page, total, err = a.unifiedTaskList(context.Background(), "failed", 2, 0)
	if err != nil || total != 1 || len(page) != 1 || page[0].RepairID != root { t.Fatal(page, total, err) }
	page, total, err = a.unifiedTaskList(context.Background(), "failed", 2, 20)
	if err != nil || total != 1 || len(page) != 0 { t.Fatal(page, total, err) }
	if count, err := a.changeRepairGroups(context.Background(), "retry", nil, true, "failed"); err != nil || count != 0 { t.Fatal("strict retries consented to repair", count, err) }
	if count, err := a.changeRepairGroups(context.Background(), "delete", nil, true, "failed"); err != nil || count != 1 { t.Fatal(count, err) }
	history, total, err := a.repairTaskList(context.Background(), "all", 25, 0)
	if err != nil || total != 1 || len(history) != 1 || history[0].ID != strings.Repeat("c", 64) { t.Fatal(history, total, err) }
	if value, err := a.meta("repair-artifact:1:retained"); err != nil || value != "retained" { t.Fatal("history deletion removed artifact warning", value, err) }
}

func TestRepairBatchAPIWaitsForCapacitySkipsUnsafeTracksAndStopsWholeQueue(t *testing.T) {
	a, s := testApp(t)
	for i, rel := range []string{"Artist/Album/01.flac", "Artist/Album/02.flac", "Artist/Album/03.mp3", "Artist/Album/04.flac"} {
		output := i == 3
		if _, err := a.db.Exec("INSERT INTO sources(id,rel,hash,error,output_present) VALUES(?,?,?,?,?)", i+1, rel, strings.Repeat("a", 64), "Audio decoding failed", output); err != nil { t.Fatal(err) }
	}
	occupied, err := a.enqueue("convert", "convert:occupied", BuildRequest{ID: 1, Profile: s.Encoding}, false)
	if err != nil { t.Fatal(err) }
	if _, err = a.db.Exec("UPDATE jobs SET state='running' WHERE id=?", occupied); err != nil { t.Fatal(err) }
	issued := httptest.NewRecorder()
	if err = a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil { t.Fatal(err) }
	cookie := issued.Result().Cookies()[0]
	send := func(method, path, body string, auth, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if auth { r.AddCookie(cookie) }
		if csrf { r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf")) }
		w := httptest.NewRecorder(); a.Handler().ServeHTTP(w, r); return w
	}
	body := `{"tracks":[{"id":1,"hash":"`+strings.Repeat("a",64)+`"},{"id":2,"hash":"`+strings.Repeat("a",64)+`"},{"id":3,"hash":"`+strings.Repeat("a",64)+`"},{"id":4,"hash":"`+strings.Repeat("a",64)+`"},{"id":2,"hash":"stale"},{"id":999,"hash":"missing"}]}`
	for _, tc := range []struct { auth, csrf bool; code int }{{false,false,401},{true,false,403},{true,true,202}} {
		w := send("POST", "/api/library/repair", body, tc.auth, tc.csrf)
		if w.Code != tc.code { t.Fatal(w.Code, w.Body.String()) }
		if tc.code == 202 && (!strings.Contains(w.Body.String(), `"queued":2`) || !strings.Contains(w.Body.String(), `"skipped":4`)) { t.Fatal(w.Body.String()) }
	}
	page, total, err := a.unifiedTaskList(context.Background(), "pending", 100, 0)
	if err != nil || total != 1 || len(page) != 1 || page[0].Counts.Pending != 2 || page[0].CanDelete { t.Fatal(page, total, err) }
	group := page[0].RepairID
	if w := send("POST", "/api/jobs/control", `{"action":"retry","repair_ids":["`+group+`"]}`, true, true); w.Code != 202 || !strings.Contains(w.Body.String(), `"changed":0`) { t.Fatal(w.Code, w.Body.String()) }
	if w := send("POST", "/api/jobs/control", `{"action":"stop","repair_ids":["invalid"]}`, true, true); w.Code != 400 { t.Fatal(w.Code) }
	if w := send("POST", "/api/jobs/control", `{"action":"delete","repair_ids":["`+group+`"]}`, true, true); w.Code != 202 || !strings.Contains(w.Body.String(), `"changed":0`) { t.Fatal(w.Code, w.Body.String()) }
	if w := send("POST", "/api/jobs/control", `{"action":"stop","all":true,"state":"pending","include_repairs":true}`, true, true); w.Code != 202 || !strings.Contains(w.Body.String(), `"changed":1`) { t.Fatal(w.Code, w.Body.String()) }
	a.repairWG.Wait()
	history, total, err := a.repairTaskListGroup(context.Background(), "stopped", 50, 0, group)
	if err != nil || total != 2 || len(history) != 2 { t.Fatal(history, total, err) }
	assertNoRepairTemps(t, a)
	if _, _, err := a.taskItemsContext(context.Background(), occupied, "all", 50, 0); err != nil { t.Fatal(err) }
	if w := send("GET", "/api/jobs?include_repairs=true&state=stopped", "", true, false); w.Code != 200 || !strings.Contains(w.Body.String(), `"repair_id":"`+group+`"`) { t.Fatal(w.Code, w.Body.String()) }
	if w := send("POST", "/api/jobs/control", `{"action":"delete","repair_ids":["`+group+`"]}`, true, true); w.Code != 202 || !strings.Contains(w.Body.String(), `"changed":1`) { t.Fatal(w.Code, w.Body.String()) }
	if _, total, err = a.repairTaskList(context.Background(), "all", 50, 0); err != nil || total != 0 { t.Fatal(total, err) }
}

func TestRepairBatchContinuesAfterFailureAndImportsTheNextTrack(t *testing.T) {
	a, s := testApp(t)
	source, path, _ := failedRepairSource(t, a, s, false)
	before, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	result, err := a.db.Exec("INSERT INTO sources(rel,hash,error) VALUES(?,?,?)", "Artist/Album/missing.flac", source.Hash, "Audio decoding failed")
	if err != nil { t.Fatal(err) }
	id, _ := result.LastInsertId()
	missing, _ := a.source(id)
	group, err := a.startRepairs([]Source{missing, source}, s, true)
	if err != nil { t.Fatal(err) }
	a.repairWG.Wait()
	page, total, err := a.unifiedTaskList(context.Background(), "failed", 100, 0)
	if err != nil || total != 1 || len(page) != 1 || page[0].RepairID != group || page[0].Counts.Done != 1 || page[0].Counts.Failed != 1 || !page[0].CanDelete { t.Fatal(page, total, err) }
	current, _ := a.source(source.ID)
	if !current.OutputPresent { t.Fatal("one failed repair stopped the remaining queue") }
	if _, err = os.Stat(filepath.Join(s.Output, current.Output)); err != nil { t.Fatal(err) }
	if err = a.annotateRepairs(context.Background(), []Source{current}); err != nil { t.Fatal(err) }
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) { t.Fatal("repair changed the source", err) }
	assertNoRepairTemps(t, a)
	a.Close()
	reopened, err := New(a.cfg, a.logger, "test", a.assets)
	if err != nil { t.Fatal(err) }
	defer reopened.Close()
	page, total, err = reopened.unifiedTaskList(context.Background(), "failed", 100, 0)
	if err != nil || total != 1 || len(page) != 1 || page[0].RepairID != group || page[0].Counts.Total != 2 { t.Fatal("restart lost batch history", page, total, err) }
}
