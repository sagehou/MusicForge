package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestScanRequestsSurviveCoalescingAndRestart(t *testing.T) {
	a, _ := testApp(t)
	id, err := a.enqueue("scan", "scan:test", ScanRequest{Dirs: []string{"A"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.enqueue("scan", "scan:test", ScanRequest{Dirs: []string{"B"}, Verify: true}, true); err != nil {
		t.Fatal(err)
	}
	j, err := a.claim(false)
	if err != nil {
		t.Fatal(err)
	}
	var request ScanRequest
	if err = json.Unmarshal(j.Args, &request); err != nil {
		t.Fatal(err)
	}
	if !request.Verify || !reflect.DeepEqual(request.Dirs, []string{"A", "B"}) {
		t.Fatalf("lost pending scope: %+v", request)
	}
	if _, err = a.enqueue("scan", "scan:test", ScanRequest{Dirs: []string{"C"}}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = a.enqueue("scan", "scan:test", ScanRequest{Verify: true}, true); err != nil {
		t.Fatal(err)
	}
	cfg, logger, assets := a.cfg, a.logger, a.assets
	a.Close()
	restored, err := New(cfg, logger, "test", assets)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	j, err = restored.claim(false)
	if err != nil {
		t.Fatal(err)
	}
	if j.ID != id {
		t.Fatal("restart lost active job")
	}
	if err = restored.completeJob(j, "success", 0, 1, "", 0); err != nil {
		t.Fatal(err)
	}
	followup, err := restored.claim(false)
	if err != nil {
		t.Fatal("running scan lost followup", err)
	}
	if err = json.Unmarshal(followup.Args, &request); err != nil {
		t.Fatal(err)
	}
	if !request.Verify || len(request.Dirs) != 0 {
		t.Fatalf("full verification lost: %+v", request)
	}
	if err = restored.completeJob(followup, "success", 0, 1, "", 0); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = restored.db.QueryRow("SELECT count(*) FROM meta WHERE key LIKE 'scan-followup:%'").Scan(&count); err != nil || count != 0 {
		t.Fatal("followup journal leaked", count, err)
	}
}

func TestNavidromeRefreshRetainsConcurrentDirtyChange(t *testing.T) {
	a, s := testApp(t)
	if err := os.MkdirAll(filepath.Join(s.Output, "Album"), 0755); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "startScan") {
			// Force the same wall-clock timestamp; a monotonic stamp must still change.
			var stamp int64
			if err := a.db.QueryRow("SELECT updated FROM dirty_dirs WHERE path='Album'").Scan(&stamp); err != nil {
				t.Error(err)
			}
			if _, err := a.db.Exec(markDirtySQL, "Album", stamp); err != nil {
				t.Error(err)
			}
		}
		respond(w, 200, map[string]any{"subsonic-response": map[string]any{"status": "ok", "type": "navidrome", "serverVersion": "0.64.2"}})
	}))
	defer server.Close()
	s.NavURL = server.URL
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.dirty("Album/track.opus"); err != nil {
		t.Fatal(err)
	}
	if err := a.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.db.QueryRow("SELECT count(*) FROM dirty_dirs WHERE path='Album'").Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent refresh was lost", count, err)
	}
}

func TestNativeLidarrDownloadUpgradeAndStrictPayload(t *testing.T) {
	a, s := testApp(t)
	s.LidarrPrefix = "/lidarr/flac"
	s.WebhookHash = digest("integration-secret")
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	old := makeAudioSource(t, a, s, "Album/old.mp3", "Old", "libmp3lame", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	previous, _ := a.sourceRel("Album/old.mp3")
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	makeAudioSource(t, a, s, "Album/new.m4a", "New", "aac", false)
	payload := `{"eventType":"Download","isUpgrade":true,"artist":{"name":"Artist"},"trackFiles":[{"path":"/lidarr/flac/Album/new.m4a","quality":{"quality":{"name":"AAC"}}}],"deletedFiles":[{"path":"/lidarr/flac/Album/old.mp3"}]}`
	send := func(body string, basic bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/webhook/lidarr", strings.NewReader(body))
		if basic {
			r.SetBasicAuth("musicforge", "integration-secret")
		} else {
			r.Header.Set("Authorization", "Bearer integration-secret")
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	if w := send(payload+` {}`, false); w.Code != 400 {
		t.Fatal("accepted trailing JSON", w.Code)
	}
	if w := send(payload, true); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	if w := send(payload, false); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	scan, err := a.claim(false)
	if err != nil || scan.Kind != "scan" {
		t.Fatal("missing import scan", err)
	}
	if err = a.execute(context.Background(), scan); err != nil {
		t.Fatal(err)
	}
	if err = a.completeJob(scan, "success", 0, 1, "", 0); err != nil {
		t.Fatal(err)
	}
	upgrade, err := a.claim(false)
	if err != nil || upgrade.Kind != "upgrade" {
		t.Fatal("missing upgrade", err)
	}
	if err = a.execute(context.Background(), upgrade); err == nil {
		t.Fatal("cleanup before validated replacement")
	}
	if _, err = os.Stat(filepath.Join(s.Output, previous.Output)); err != nil {
		t.Fatal("playable output lost", err)
	}
	drain(t, a, true)
	if err = a.execute(context.Background(), upgrade); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.Output, previous.Output)); !os.IsNotExist(err) {
		t.Fatal("explicit upgrade did not remove old output", err)
	}
}

func TestNavidromeAsynchronousRefreshKeepsJournalUntilSuccess(t *testing.T) {
	a, s := testApp(t)
	var scanning atomic.Bool
	scanning.Store(true)
	var starts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "startScan") {
			starts.Add(1)
			scanning.Store(true)
		}
		running := scanning.Load() && starts.Load() > 0
		respond(w, 200, map[string]any{"subsonic-response": map[string]any{"status": "ok", "type": "navidrome", "serverVersion": "0.64.2", "scanStatus": map[string]any{"scanning": running}}})
	}))
	defer server.Close()
	s.NavURL = server.URL
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.dirty("Album/track.opus"); err != nil {
		t.Fatal(err)
	}
	if err := a.refresh(context.Background()); err == nil {
		t.Fatal("accepted asynchronous scan as complete")
	}
	cfg, logger, assets := a.cfg, a.logger, a.assets
	a.Close()
	restored, err := New(cfg, logger, "test", assets)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err = restored.refresh(context.Background()); err == nil {
		t.Fatal("lost asynchronous journal after restart")
	}
	if starts.Load() != 1 {
		t.Fatal("restarted an in-flight remote scan", starts.Load())
	}
	scanning.Store(false)
	if err = restored.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = restored.db.QueryRow("SELECT count(*) FROM dirty_dirs").Scan(&count); err != nil || count != 0 {
		t.Fatal("completion did not acknowledge changes", count, err)
	}
}

func TestJobsFilterAppliesBeforePagination(t *testing.T) {
	a, _ := testApp(t)
	failed, err := a.enqueue("scan", "older-failure", ScanRequest{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec("UPDATE jobs SET state='failed' WHERE id=?", failed); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 105; i++ {
		if _, err = a.db.Exec("INSERT INTO jobs(kind,dedup,args,state,created,updated) VALUES('scan',?,'{}','success',0,0)", i); err != nil {
			t.Fatal(err)
		}
	}
	session := httptest.NewRecorder()
	if err = a.newSession(session, httptest.NewRequest("GET", a.cfg.PublicURL+"/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/jobs?state=failed&limit=1", nil)
	req.AddCookie(session.Result().Cookies()[0])
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	var result struct {
		Jobs  []Job `json:"jobs"`
		Total int   `json:"total"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || result.Total != 1 || len(result.Jobs) != 1 || result.Jobs[0].ID != failed {
		t.Fatal("pagination hid older failure", w.Code, w.Body.String())
	}
}
