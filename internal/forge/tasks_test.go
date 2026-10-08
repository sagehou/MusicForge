package forge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanOwnsOneDurableConversionTask(t *testing.T) {
	a, s := testApp(t)
	for i := 1; i <= 3; i++ {
		makeFLAC(t, a, s, fmt.Sprintf("Artist/Album/%02d.flac", i), fmt.Sprintf("Track %d", i), false)
	}
	id, err := a.enqueue("scan", "scan:task-test", ScanRequest{}, true)
	if err != nil { t.Fatal(err) }
	j, err := a.claim(false)
	if err != nil { t.Fatal(err) }
	if err = a.execute(context.Background(), j); err != nil { t.Fatal(err) }
	if _, err = a.claim(true); err != sql.ErrNoRows { t.Fatal("conversion started before scan completed", err) }
	if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil { t.Fatal(err) }
	list, total, err := a.taskList("all", 1, 0)
	if err != nil || total != 1 || len(list) != 1 || list[0].ID != id || list[0].Counts.Total != 3 || list[0].State != "running" {
		t.Fatalf("scan did not create one queue: %+v %d %v", list, total, err)
	}
	if list[0].Scan == nil || list[0].Scan.Processed != 3 || list[0].Scan.Total != 3 { t.Fatal("scan progress missing", list[0].Scan) }
	if _, err = a.changeTasks("pause", selection{IDs: []int64{id}}); err != nil { t.Fatal(err) }
	if _, err = a.claim(true); err != sql.ErrNoRows { t.Fatal("paused queue claimed", err) }
	cfg, logger, assets := a.cfg, a.logger, a.assets
	a.Close()
	a, err = New(cfg, logger, "test", assets)
	if err != nil { t.Fatal(err) }
	defer a.Close()
	list, total, err = a.taskList("paused", 1, 0)
	if err != nil || total != 1 || list[0].State != "paused" { t.Fatal("pause/grouping lost after restart", list, err) }
	if _, err = a.claim(true); err != sql.ErrNoRows { t.Fatal("restart unpaused queue", err) }
	if _, err = a.changeTasks("resume", selection{IDs: []int64{id}}); err != nil { t.Fatal(err) }
	drain(t, a, true)
	list, total, err = a.taskList("success", 1, 0)
	if err != nil || total != 1 || list[0].Counts.Done != 3 || list[0].Progress != 1 { t.Fatal("queue completion missing", list, err) }
	if _, err = a.changeTasks("delete", selection{IDs: []int64{id}}); err != nil { t.Fatal(err) }
	if _, total, err = a.taskList("all", 100, 0); err != nil || total != 0 { t.Fatal("child history leaked", total, err) }
	for i := 1; i <= 3; i++ {
		if _, err = os.Stat(filepath.Join(s.Output, fmt.Sprintf("Artist/Album/%02d.opus", i))); err != nil { t.Fatal("history deletion removed audio", err) }
	}
	var annotations int
	if err = a.db.QueryRow("SELECT count(*) FROM meta WHERE key LIKE 'task-%'").Scan(&annotations); err != nil || annotations != 0 { t.Fatal("task annotations leaked", annotations, err) }
}

func waitTaskWorker(t *testing.T, a *App, id int64, active bool) {
	t.Helper()
	deadline := time.Now().Add(5*time.Second)
	for time.Now().Before(deadline) {
		a.jobsMu.Lock()
		_, running := a.activeJobs[id]
		a.jobsMu.Unlock()
		var temps int
		if err := a.db.QueryRow("SELECT count(*) FROM managed WHERE kind='temp'").Scan(&temps); err != nil { t.Fatal(err) }
		if running == active && (active && temps > 0 || !active && temps == 0) { return }
		time.Sleep(10*time.Millisecond)
	}
	t.Fatal("worker did not reach expected activity", active)
}

func TestRunningTaskPauseAndStopPreservePlayableAudio(t *testing.T) {
	a, s := testApp(t)
	makeFLAC(t, a, s, "Artist/Album/01.flac", "Readable title", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil { t.Fatal(err) }
	drain(t, a, true)
	source, _ := a.sourceRel("Artist/Album/01.flac")
	before, err := os.ReadFile(filepath.Join(s.Output, source.Output))
	if err != nil { t.Fatal(err) }
	s.Encoding = Encoding{"mp3", "vbr", 192, 2}
	if err = a.saveSettings(s); err != nil { t.Fatal(err) }
	id, err := a.queueBuild(source, s.Encoding, false, true)
	if err != nil { t.Fatal(err) }
	tool := filepath.Join(t.TempDir(), "slow-encoder")
	if err = os.WriteFile(tool, []byte("#!/bin/sh\nexec sleep 60\n"), 0700); err != nil { t.Fatal(err) }
	a.cfg.FFmpeg = tool
	var logs bytes.Buffer
	a.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.worker(ctx, true, 0) }()
	defer func() { cancel(); <-done }()
	waitTaskWorker(t, a, id, true)
	items, _, err := a.taskItems(id, "running", 50, 0)
	if err != nil || len(items) != 1 || items[0].Activity.Title != "Readable title" || items[0].Activity.Phase != "convert" { t.Fatal("human progress missing", items, err) }
	if _, err = a.changeTasks("delete", selection{IDs: []int64{id}}); err == nil { t.Fatal("active history could be deleted") }
	if _, err = a.changeTasks("pause", selection{IDs: []int64{id}}); err != nil { t.Fatal(err) }
	waitTaskWorker(t, a, id, false)
	var state string
	var attempts int
	var notBefore int64
	if err = a.db.QueryRow("SELECT state,attempts,not_before FROM jobs WHERE id=?", id).Scan(&state, &attempts, &notBefore); err != nil { t.Fatal(err) }
	if state != "pending" || attempts != 0 || notBefore != heldUntil { t.Fatal("pause consumed retry or lost delay", state, attempts, notBefore) }
	if _, err = a.changeTasks("resume", selection{IDs: []int64{id}}); err != nil { t.Fatal(err) }
	waitTaskWorker(t, a, id, true)
	if _, err = a.changeTasks("stop", selection{IDs: []int64{id}}); err != nil { t.Fatal(err) }
	waitTaskWorker(t, a, id, false)
	cancel(); <-done
	if err = a.db.QueryRow("SELECT attempts FROM jobs WHERE id=?", id).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatal("stop changed actual failure attempts", attempts, err)
	}
	if !strings.Contains(logs.String(), `"title":"Readable title"`) || !strings.Contains(logs.String(), `"phase":"convert"`) { t.Fatal("Docker progress log missing", logs.String()) }
	list, total, err := a.taskList("stopped", 50, 0)
	if err != nil || total != 1 || list[0].State != "stopped" { t.Fatal("stop missing", list, err) }
	after, err := os.ReadFile(filepath.Join(s.Output, source.Output))
	if err != nil || !bytes.Equal(before, after) { t.Fatal("playable output changed on cancellation", err) }
	source, _ = a.source(source.ID)
	if source.Error != "" { t.Fatal("cancellation recorded as source corruption", source.Error) }
	if _, err = a.changeTasks("retry", selection{All: true}); err != nil { t.Fatal(err) }
	if _, err = a.claim(true); err != sql.ErrNoRows { t.Fatal("retry-all restarted an intentionally stopped task", err) }
	if _, err = a.changeTasks("delete", selection{IDs: []int64{id}}); err != nil { t.Fatal(err) }
	if _, err = os.Stat(filepath.Join(s.Output, source.Output)); err != nil { t.Fatal("history cleanup removed audio", err) }
}

func TestTaskFiltersAndPaginationCountQueues(t *testing.T) {
	a, _ := testApp(t)
	id, err := a.enqueue("scan", "scan:large-task", ScanRequest{}, true)
	if err != nil { t.Fatal(err) }
	if _, err = a.db.Exec("UPDATE jobs SET state='success' WHERE id=?", id); err != nil { t.Fatal(err) }
	for i := 0; i < 110; i++ {
		child, err := a.enqueueTask("convert", fmt.Sprintf("large:%d", i), BuildRequest{ID: int64(i+1)}, false, id)
		if err != nil { t.Fatal(err) }
		if _, err = a.db.Exec("UPDATE jobs SET state='failed',attempts=3,log='bad source' WHERE id=?", child); err != nil { t.Fatal(err) }
	}
	list, total, err := a.taskList("failed", 1, 0)
	if err != nil || total != 1 || len(list) != 1 || list[0].Counts.Failed != 110 { t.Fatal("queue aggregation/filtering broken", list, total, err) }
	items, total, err := a.taskItems(id, "failed", 50, 100)
	if err != nil || total != 110 || len(items) != 10 || items[0].Log != "bad source" { t.Fatal("failed item pagination broken", total, len(items), err) }
	if _, err = a.changeTasks("retry", selection{IDs: []int64{id}}); err != nil { t.Fatal(err) }
	list, total, err = a.taskList("running", 1, 0)
	if err != nil || total != 1 || list[0].Counts.Pending != 110 || list[0].Counts.Failed != 0 { t.Fatal("retry missed queue members", list, total, err) }
}

func TestLegacyWriterCanRetryStoppedJobs(t *testing.T) {
	a, _ := testApp(t)
	id, err := a.enqueue("scan", "legacy-writer-retry", ScanRequest{}, true)
	if err != nil { t.Fatal(err) }
	if _, err = a.changeTasks("stop", selection{IDs: []int64{id}}); err != nil { t.Fatal(err) }
	// This is the published schema-2 retry write: no task annotation support.
	if _, err = a.db.Exec("UPDATE jobs SET state='pending',attempts=0,not_before=0,log='',updated=? WHERE state='failed' AND id=?", time.Now().Unix(), id); err != nil { t.Fatal(err) }
	cfg, logger, assets := a.cfg, a.logger, a.assets
	a.Close()
	a, err = New(cfg, logger, "test", assets)
	if err != nil { t.Fatal(err) }
	defer a.Close()
	j, err := a.claim(false)
	if err != nil || j.ID != id { t.Fatal("legacy retry lost", j, err) }
	if err = a.execute(context.Background(), j); err != nil { t.Fatal(err) }
	if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil { t.Fatal(err) }
	list, total, err := a.taskList("success", 100, 0)
	if err != nil || total != 1 || list[0].ID != id { t.Fatal("stale annotation overrode legacy write", list, err) }
}

func TestManualRebuildCreatesOneQueue(t *testing.T) {
	a, s := testApp(t)
	for i := 1; i <= 2; i++ { makeFLAC(t, a, s, fmt.Sprintf("Artist/Album/%02d.flac", i), fmt.Sprintf("Track %d", i), false) }
	if err := a.scan(context.Background(), ScanRequest{}); err != nil { t.Fatal(err) }
	drain(t, a, true)
	if _, err := a.db.Exec("DELETE FROM jobs"); err != nil { t.Fatal(err) }
	if _, err := a.db.Exec("DELETE FROM meta WHERE key LIKE 'task-%'"); err != nil { t.Fatal(err) }
	s.Encoding = Encoding{"mp3", "vbr", 192, 2}
	if err := a.saveSettings(s); err != nil { t.Fatal(err) }
	w := httptest.NewRecorder()
	a.rebuild(w, httptest.NewRequest("POST", "/api/library/rebuild", strings.NewReader(`{"all":true}`)))
	if w.Code != 202 { t.Fatal(w.Code, w.Body.String()) }
	var result struct { Queued int `json:"queued"`; ID int64 `json:"job_id"` }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Queued != 2 || result.ID == 0 { t.Fatal("rebuild response", result, err) }
	list, total, err := a.taskList("all", 100, 0)
	if err != nil || total != 1 || list[0].Counts.Total != 2 || list[0].ID != result.ID { t.Fatal("manual rebuild split into tracks", list, err) }
	if _, err = a.changeTasks("pause", selection{IDs: []int64{result.ID}}); err != nil { t.Fatal(err) }
	if _, err = a.claim(true); err != sql.ErrNoRows { t.Fatal("rebuild queue was not paused as one task", err) }
}
