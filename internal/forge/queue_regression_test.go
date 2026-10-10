package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestScanEntryPointsCoalesceConcurrentRequests(t *testing.T) {
	a, _ := testApp(t)
	type result struct {
		id  int64
		err error
	}
	results := make(chan result, 24)
	for i := 0; i < 24; i++ {
		go func(i int) {
			id, err := a.enqueue("scan", fmt.Sprintf("scan:entry:%d", i), ScanRequest{Dirs: []string{fmt.Sprintf("Artist/%02d", i)}, Verify: i == 0}, true)
			results <- result{id, err}
		}(i)
	}
	var root int64
	for i := 0; i < 24; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if root == 0 {
			root = r.id
		}
		if r.id != root {
			t.Fatal("concurrent entry points created separate tasks", root, r.id)
		}
	}
	j, err := a.claim(false)
	if err != nil || j.ID != root {
		t.Fatal(j, err)
	}
	var request ScanRequest
	if err = json.Unmarshal(j.Args, &request); err != nil || !request.Verify || len(request.Dirs) != 24 {
		t.Fatal("request scopes or verification lost", request, err)
	}
	for i := 0; i < 24; i++ {
		if request.Dirs[i] != fmt.Sprintf("Artist/%02d", i) {
			t.Fatal("scope lost", request)
		}
	}
	if id, err := a.enqueue("scan", "scan:periodic", ScanRequest{}, false); err != nil || id != root {
		t.Fatal("timer did not reuse active task", id, err)
	}
	if _, err = a.meta("scan-followup:" + fmt.Sprint(root)); err != sql.ErrNoRows {
		t.Fatal("timer added follow-up work", err)
	}
	if id, err := a.enqueue("scan", "scan:manual", ScanRequest{Dirs: []string{"Changed/Album"}}, true); err != nil || id != root {
		t.Fatal("running scan did not coalesce manual request", id, err)
	}
	if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil {
		t.Fatal(err)
	}
	j, err = a.claim(false)
	if err != nil || j.ID != root {
		t.Fatal("follow-up created a new task", j, err)
	}
	if err = json.Unmarshal(j.Args, &request); err != nil || !reflect.DeepEqual(request.Dirs, []string{"Changed/Album"}) {
		t.Fatal("follow-up scope lost", request, err)
	}
	if _, total, err := a.taskList("all", 100, 0); err != nil || total != 1 {
		t.Fatal("UI shows more than one task", total, err)
	}
}

func TestPeriodicScanWaitsForEntireQueue(t *testing.T) {
	for _, scenario := range []string{"scan", "running scan", "convert", "finalize", "paused", "move", "delete", "upgrade"} {
		t.Run(scenario, func(t *testing.T) {
			a, s := testApp(t)
			root, err := a.enqueue("scan", "scan:manual", ScanRequest{Dirs: []string{"Artist/Album"}}, true)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "running scan" {
				if _, err = a.claim(false); err != nil {
					t.Fatal(err)
				}
			} else if scenario != "scan" {
				if _, err = a.db.Exec("UPDATE jobs SET state='success' WHERE id=?", root); err != nil {
					t.Fatal(err)
				}
				kind, key := scenario, "member:"+scenario
				var args any = BuildRequest{ID: 1}
				if scenario == "finalize" {
					kind, key, args = "scan", "scan:finalize:"+fmt.Sprint(root), ScanRequest{}
				} else if scenario == "paused" {
					kind = "convert"
				}
				if _, err = a.enqueueTask(kind, key, args, false, root); err != nil {
					t.Fatal(err)
				}
				if scenario == "paused" {
					if _, err = a.changeTasks("pause", selection{IDs: []int64{root}}); err != nil {
						t.Fatal(err)
					}
				}
			}
			var before int
			if err = a.reads.QueryRow("SELECT count(*) FROM jobs").Scan(&before); err != nil {
				t.Fatal(err)
			}
			last := time.Now().Add(-24 * time.Hour)
			for i := 0; i < 5; i++ {
				last, err = a.scheduleScan(s, last)
				if err != nil || time.Since(last) > time.Second {
					t.Fatal("busy interval was not reset", last, err)
				}
			}
			if id, err := a.enqueue("scan", "scan:periodic", ScanRequest{}, false); err != nil || id != root {
				t.Fatal("direct timer enqueue bypassed busy guard", id, err)
			}
			var after, followups int
			if err = a.reads.QueryRow("SELECT count(*) FROM jobs").Scan(&after); err != nil || before != after {
				t.Fatal("timer created a job while queue active", before, after, err)
			}
			if err = a.reads.QueryRow("SELECT count(*) FROM meta WHERE key LIKE 'scan-followup:%'").Scan(&followups); err != nil || followups != 0 {
				t.Fatal("timer created follow-up", followups, err)
			}
			if _, err = a.db.Exec("UPDATE jobs SET state='success'"); err != nil {
				t.Fatal(err)
			}
			last, err = a.scheduleScan(s, last)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.reads.QueryRow("SELECT count(*) FROM jobs").Scan(&after); err != nil || before != after {
				t.Fatal("timer did not wait after completion", before, after, err)
			}
			if _, err = a.scheduleScan(s, time.Now().Add(-24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			if err = a.reads.QueryRow("SELECT count(*) FROM jobs WHERE state='pending' AND dedup='scan:periodic'").Scan(&after); err != nil || after != 1 {
				t.Fatal("idle timer stopped scheduling", after, err)
			}
		})
	}
}

func TestScanFollowupDuringConversionSurvivesRestartAndControl(t *testing.T) {
	for _, action := range []string{"", "pause", "stop"} {
		t.Run("control="+action, func(t *testing.T) {
			a, _ := testApp(t)
			root, err := a.enqueue("scan", "scan:manual", ScanRequest{Dirs: []string{"Original/Album"}}, true)
			if err != nil {
				t.Fatal(err)
			}
			j, err := a.claim(false)
			if err != nil {
				t.Fatal(err)
			}
			child, err := a.enqueueTask("convert", "child:followup", BuildRequest{ID: 1}, false, root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.enqueueTask("scan", "scan:finalize:"+fmt.Sprint(root), ScanRequest{}, false, root); err != nil {
				t.Fatal(err)
			}
			if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil {
				t.Fatal(err)
			}
			for i, request := range []ScanRequest{{Dirs: []string{"Changed/B"}}, {Dirs: []string{"Changed/A"}, Verify: true}} {
				if id, err := a.enqueue("scan", fmt.Sprintf("scan:webhook:%d", i), request, true); err != nil || id != root {
					t.Fatal("conversion stage created a new scan root", id, err)
				}
			}
			if action != "" {
				if _, err = a.changeTasks(action, selection{IDs: []int64{root}}); err != nil {
					t.Fatal(err)
				}
			}
			cfg, logger, assets := a.cfg, a.logger, a.assets
			a.Close()
			a, err = New(cfg, logger, "test", assets)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if action == "stop" {
				j, err = readJob(a.reads.QueryRow("SELECT "+jobCols+" FROM jobs WHERE id=?", child))
				if err != nil {
					t.Fatal(err)
				}
				if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil {
					t.Fatal(err)
				}
				if _, err = a.claim(false); err != sql.ErrNoRows {
					t.Fatal("stopped follow-up restarted", err)
				}
				if _, err = a.meta("scan-followup:" + fmt.Sprint(root)); err != sql.ErrNoRows {
					t.Fatal("stop retained follow-up journal", err)
				}
				return
			}
			if action == "pause" {
				if _, err = a.claim(true); err != sql.ErrNoRows {
					t.Fatal("paused conversion claimed", err)
				}
				if _, err = a.changeTasks("resume", selection{IDs: []int64{root}}); err != nil {
					t.Fatal(err)
				}
			}
			j, err = a.claim(false)
			if err != nil || j.ID != root {
				t.Fatal("late scan did not reuse the same root before conversions", j, err)
			}
			var request ScanRequest
			if err = json.Unmarshal(j.Args, &request); err != nil || !request.Verify || !reflect.DeepEqual(request.Dirs, []string{"Changed/A", "Changed/B"}) {
				t.Fatal("durable scope lost", request, err)
			}
			if _, err = a.claim(true); err != sql.ErrNoRows {
				t.Fatal("conversion started before reindexing", err)
			}
			if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil {
				t.Fatal(err)
			}
			j, err = a.claim(false)
			if err != nil || !strings.HasPrefix(j.Key, "scan:finalize:") {
				t.Fatal("finalizer lost", j, err)
			}
			if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil {
				t.Fatal(err)
			}
			j, err = a.claim(true)
			if err != nil || j.ID != child {
				t.Fatal(j, err)
			}
			if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil {
				t.Fatal(err)
			}
			if _, total, err := a.taskList("success", 100, 0); err != nil || total != 1 {
				t.Fatal("reindex split the queue", total, err)
			}
		})
	}
}

func TestFilteredBulkControlsAcrossPages(t *testing.T) {
	a, s := testApp(t)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 125; i++ {
		if _, err = tx.Exec("INSERT INTO jobs(id,kind,dedup,args,created,updated) VALUES(?,'scan',?,'{}',1,1)", i, fmt.Sprintf("bulk:%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec("INSERT INTO jobs(id,kind,dedup,args,state,created,updated) VALUES(1000,'refresh','protected','{}','success',1,1)"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A worker can still be winding down although its persisted state is terminal.
	a.activeJobs[1000] = func() {}
	defer delete(a.activeJobs, 1000)
	for _, step := range []struct{ action, state string }{{"pause", "pending"}, {"resume", "paused"}, {"stop", "pending"}, {"retry", "stopped"}, {"stop", "pending"}, {"delete", "stopped"}} {
		w := httptest.NewRecorder()
		a.controlJobs(w, httptest.NewRequest("POST", "/api/jobs/control", strings.NewReader(fmt.Sprintf(`{"action":%q,"all":true,"state":%q}`, step.action, step.state))))
		var result struct {
			Changed int `json:"changed"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 202 || result.Changed != 125 {
			t.Fatalf("bulk %s missed later pages: %d %s %v", step.action, w.Code, w.Body.String(), err)
		}
	}
	if n, err := a.changeTasksFiltered("delete", selection{All: true}, "all"); err != nil || n != 0 {
		t.Fatal("bulk history deletion removed active worker", n, err)
	}
	list, total, err := a.taskList("all", 100, 0)
	if err != nil || total != 1 || list[0].ID != 1000 || list[0].CanDelete {
		t.Fatal("active history protection lost", list, total, err)
	}
	if fresh, err := a.settings(); err != nil || fresh.Output != s.Output {
		t.Fatal("bulk control changed library settings", fresh, err)
	}
	for _, body := range []string{`{"action":"delete","all":true}`, `{"action":"stop","all":true,"state":"invalid"}`} {
		w := httptest.NewRecorder()
		a.controlJobs(w, httptest.NewRequest("POST", "/api/jobs/control", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal("invalid bulk scope accepted", w.Code, body)
		}
	}
}

func TestWALReadAPIsStayResponsiveDuringLargeQueueWrite(t *testing.T) {
	a, s := testApp(t)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO admin(id,username,password) VALUES(1,'admin',?)", []byte("unused-in-session-test")); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO jobs(id,kind,dedup,args,state,created,updated) VALUES(1,'scan','scan:large','{}','success',1,1)"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5000; i++ {
		if _, err = tx.Exec("INSERT INTO sources(id,rel,artist,album,title) VALUES(?,?, 'Artist','Album',?)", i, fmt.Sprintf("Artist/Album/%04d.flac", i), fmt.Sprintf("Track %d", i)); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(BuildRequest{ID: int64(i), Profile: s.Encoding})
		state := "success"
		if i == 1 {
			state = "running"
		}
		if _, err = tx.Exec("INSERT INTO jobs(id,kind,dedup,args,state,created,updated) VALUES(?,'convert',?,?,?,1,1)", i+1, fmt.Sprintf("large:%d", i), string(raw), state); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,'1')", taskMemberKey(int64(i+1))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 10001; i <= 10125; i++ {
		if _, err = tx.Exec("INSERT INTO jobs(id,kind,dedup,args,state,created,updated) VALUES(?,'scan',?,'{}','success',1,1)", i, fmt.Sprintf("history:%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	session := httptest.NewRecorder()
	if err = a.newSession(session, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	cookie := session.Result().Cookies()[0]
	tx, err = a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE settings SET data=json_set(data,'$.scan_minutes',999)"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE sources SET title='Uncommitted title' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	handler := a.Handler()
	paths := []string{"/api/auth/me", "/api/settings", "/api/jobs?limit=100", "/api/jobs/1/items?offset=4950", "/api/library", "/api/dashboard"}
	results := make(chan error, len(paths))
	for _, path := range paths {
		go func(path string) {
			started := time.Now()
			r := httptest.NewRequest("GET", path, nil)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 || !json.Valid(w.Body.Bytes()) || time.Since(started) > 2*time.Second {
				results <- fmt.Errorf("API stalled behind writer: %s status=%d duration=%s", path, w.Code, time.Since(started))
				return
			}
			if strings.Contains(w.Body.String(), "Uncommitted title") {
				results <- fmt.Errorf("API exposed uncommitted write: %s", path)
				return
			}
			results <- nil
		}(path)
	}
	for range paths {
		select {
		case err = <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("read API waited for writer transaction")
		}
	}
	if fresh, err := a.settings(); err != nil || fresh.ScanMinutes != s.ScanMinutes {
		t.Fatal("reader did not see committed settings", fresh, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if fresh, err := a.settings(); err != nil || fresh.ScanMinutes != 999 {
		t.Fatal("reader did not observe subsequent commit", fresh, err)
	}
	items, total, err := a.taskItems(1, "all", 50, 4950)
	if err != nil || total != 5001 || len(items) != 50 || items[0].Activity.Title == "" {
		t.Fatal("large detail page lost tags or pagination", len(items), total, err)
	}
	list, total, err := a.taskList("running", 100, 0)
	if err != nil || total != 1 || len(list) != 1 || len(list[0].Current) != 1 || list[0].Current[0].Title != "Uncommitted title" || list[0].Profile == nil {
		t.Fatal("large queue lost active track or profile", list, total, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = a.reads.ExecContext(ctx, "DELETE FROM jobs"); err == nil {
		t.Fatal("API reader can write")
	}
}

func TestLegacyMemberCompletionPreservesPendingScanScope(t *testing.T) {
	a, _ := testApp(t)
	root, err := a.enqueue("scan", "scan:legacy-followup", ScanRequest{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec("UPDATE jobs SET state='success' WHERE id=?", root); err != nil {
		t.Fatal(err)
	}
	child, err := a.enqueueTask("convert", "legacy:child", BuildRequest{ID: 1}, false, root)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := a.enqueue("scan", "scan:new-scope", ScanRequest{Dirs: []string{"Changed/Album"}, Verify: true}, true); err != nil || id != root {
		t.Fatal(id, err)
	}
	// Published workers can finish primitive jobs while the same scan root stays pending.
	if _, err = a.db.Exec("UPDATE jobs SET state='success' WHERE id=?", child); err != nil {
		t.Fatal(err)
	}
	cfg, logger, assets := a.cfg, a.logger, a.assets
	a.Close()
	a, err = New(cfg, logger, "test", assets)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	j, err := a.claim(false)
	if err != nil || j.ID != root {
		t.Fatal("legacy completion stranded the follow-up", j, err)
	}
	var request ScanRequest
	if err = json.Unmarshal(j.Args, &request); err != nil || !request.Verify || !reflect.DeepEqual(request.Dirs, []string{"Changed/Album"}) {
		t.Fatal("legacy round-trip changed the request", request, err)
	}
}

func TestBulkRetryDoesNotCountAlreadyActiveTargets(t *testing.T) {
	a, _ := testApp(t)
	if _, err := a.db.Exec("INSERT INTO jobs(id,kind,dedup,args,state,log,created,updated) VALUES(1,'convert','same-target','{}','failed','Stopped by administrator',1,1),(2,'convert','same-target','{}','pending','',1,1)"); err != nil {
		t.Fatal(err)
	}
	if err := a.setMeta(taskControlKey(1), "stopped"); err != nil {
		t.Fatal(err)
	}
	if n, err := a.changeTasksFiltered("retry", selection{All: true}, "stopped"); err != nil || n != 0 {
		t.Fatal("suppressed retry counted as changed", n, err)
	}
	if value, err := a.meta(taskControlKey(1)); err != nil || value != "stopped" {
		t.Fatal("suppressed retry cleared deliberate stop", value, err)
	}
}

func TestSourceChangeReindexesSameTaskBeforePendingTracks(t *testing.T) {
	a, s := testApp(t)
	rel := "Artist/Album/01.flac"
	makeFLAC(t, a, s, rel, "Original", false)
	root, err := a.enqueue("scan", "scan:source-change", ScanRequest{}, true)
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.claim(false)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.execute(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil {
		t.Fatal(err)
	}
	makeFLAC(t, a, s, rel, "Changed before queued preparation", false)
	j, err = a.claim(true)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.execute(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil {
		t.Fatal(err)
	}
	j, err = a.claim(false)
	if err != nil || j.ID != root {
		t.Fatal("source repair was suppressed or created a different task", j, err)
	}
	if err = a.execute(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	source, err := a.sourceRel(rel)
	if err != nil || source.Title != "Changed before queued preparation" || !source.OutputPresent || source.Hash == "" || source.BuiltHash != source.Hash {
		t.Fatal("source repair did not complete", source, err)
	}
	if _, total, err := a.taskList("all", 100, 0); err != nil || total != 1 {
		t.Fatal("source repair split the task", total, err)
	}
}
