package forge

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestMaintenanceSnapshotsAreIndexOnlyAndStopInterruptedReports(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	request := lyricsSession(t, a)
	for _, kind := range []string{"loudness", "tags", "fingerprints", "storage", "undo"} {
		if err := a.setMeta("maintenance:"+kind, `{"state":"running","total":3,"processed":1,"updated":1,"failed":0,"skipped":0,"current":"old.opus","waiting_reason":"capacity"}`); err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range []string{s.Source, s.Output} {
		if err := os.Rename(root, root+".offline"); err != nil {
			t.Fatal(err)
		}
	}
	a.lyricsMu.Lock()
	a.lyricsReport = &lyricsReport{State: "running", Total: 2, Processed: 1, Current: "current.opus", Skipped: 8}
	a.lyricsRetries = map[string]time.Time{"kuwo": time.Now().Add(time.Minute)}
	a.netease = &neteaseSessionState{gate: make(chan struct{}, 1), retryAt: time.Now().Add(30 * time.Minute)}
	a.lyricsMu.Unlock()
	w := request("GET", "/api/maintenance/progress", nil)
	var progress struct {
		Reports   []maintenanceProgress
		Cooldowns []struct {
			Provider string
			RetryAt  int64 `json:"retry_at"`
		}
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &progress) != nil || len(progress.Reports) != 6 || len(progress.Cooldowns) != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, report := range progress.Reports {
		if report.Kind == "lyrics" {
			if report.State != "running" || report.Current != "current.opus" || report.Skipped != 8 {
				t.Fatal(report)
			}
		} else if report.State != "stopped" || report.Current != "" || report.WaitingReason != "" {
			t.Fatal("interrupted report claims to be active", report)
		}
	}
	if progress.Cooldowns[0].Provider != "netease" || progress.Cooldowns[1].Provider != "kuwo" || progress.Cooldowns[1].RetryAt <= time.Now().Unix() {
		t.Fatal(progress.Cooldowns)
	}
	if raw, _ := a.meta("maintenance:tags"); raw != `{"state":"running","total":3,"processed":1,"updated":1,"failed":0,"skipped":0,"current":"old.opus","waiting_reason":"capacity"}` {
		t.Fatal("polling wrote persisted state", raw)
	}
	// A fresh in-process report and its actual capacity wait remain visible.
	a.loudnessMu.Lock()
	a.loudnessReport = &loudnessReport{State: "running", Total: 2}
	a.loudnessMu.Unlock()
	a.maintenanceWaiting("loudness", "schedule")
	if report, err := a.loudnessSnapshot(context.Background()); err != nil || report.WaitingReason != "schedule" {
		t.Fatal(report, err)
	}
	a.maintenanceWaiting("loudness", "")
}

func TestFailureGroupsRetryOnlyMatchingItemsAndPreserveControls(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	request := lyricsSession(t, a)
	root, err := a.enqueue("scan", "failure-root", ScanRequest{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec("UPDATE jobs SET state='success' WHERE id=?", root); err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	logs := []string{"Audio decoding failed\ninvalid frame", "Audio conversion failed\npermission denied", "Audio decoding failed\nsecond", "Audio decoding failed\nthird", "Stopped by administrator", "Audio decoding failed\nbusy worker", "Audio decoding failed\nduplicate active work"}
	for i, log := range logs {
		id, err := a.enqueue("convert", "failure-item:"+string(rune('a'+i)), BuildRequest{ID: source.ID}, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.db.Exec("UPDATE jobs SET state='failed',attempts=3,log=? WHERE id=?", log, id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if _, err := a.db.Exec("INSERT INTO jobs(kind,dedup,args,state,created,updated) SELECT kind,dedup,args,'pending',created,updated FROM jobs WHERE id=?", ids[6]); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids[:2] {
		if err := a.setMeta(taskMemberKey(id), strconv.FormatInt(root, 10)); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.setMeta(taskControlKey(ids[2]), "paused"); err != nil {
		t.Fatal(err)
	}
	if err := a.setMeta(taskControlKey(ids[3]), "stopped"); err != nil {
		t.Fatal(err)
	}
	a.jobsMu.Lock()
	a.activeJobs[ids[5]] = func() {}
	a.jobsMu.Unlock()
	status := LyricsStatus{State: "failed", Output: source.Output, Signature: digest("registered audio fixture"), Checked: 5, Error: "Lyrics providers unavailable; retry later", Attempts: []lyricsAttempt{{Provider: "kuwo", State: "failed", Error: "kuwo lyrics service is cooling down; retry later"}}}
	raw, _ := json.Marshal(status)
	if err := a.setMeta("lyrics-status:1", string(raw)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{s.Source, s.Output} {
		if err := os.Rename(path, path+".offline"); err != nil {
			t.Fatal(err)
		}
	}
	w := request("GET", "/api/maintenance/failures", nil)
	var groups []maintenanceFailureGroup
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &groups) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	byReason := map[string]maintenanceFailureGroup{}
	for _, group := range groups {
		byReason[group.Reason] = group
	}
	if group := byReason["decode"]; group.Jobs != 5 || group.RetryableJobs != 1 {
		t.Fatal("controls not respected in grouping", groups)
	}
	if group := byReason["rate_limit"]; group.Lyrics != 1 || len(group.LyricIDs) != 1 || group.LyricIDs[0] != 1 {
		t.Fatal("current lyric failure missing", groups)
	}
	for _, body := range []map[string]any{{"action": "stop", "all": true, "failure": "decode"}, {"action": "retry", "all": true, "failure": "unknown"}, {"action": "retry", "all": true, "failure": "decode", "include_repairs": true}} {
		if w = request("POST", "/api/jobs/control", body); w.Code != 400 {
			t.Fatal("accepted invalid retry filter", w.Code, w.Body.String())
		}
	}
	w = request("POST", "/api/jobs/control", map[string]any{"action": "retry", "all": true, "failure": "decode"})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	for i, id := range ids {
		var state, log string
		var attempts int
		if err := a.reads.QueryRow("SELECT state,attempts,log FROM jobs WHERE id=?", id).Scan(&state, &attempts, &log); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if state != "pending" || attempts != 0 || log != "" {
				t.Fatal("matching item not retried", state, attempts, log)
			}
		} else if state != "failed" || attempts != 3 || log != logs[i] {
			t.Fatal("unrelated failure changed", i, state, attempts, log)
		}
	}
	for i, control := range map[int]string{2: "paused", 3: "stopped"} {
		if raw, _ := a.meta(taskControlKey(ids[i])); raw != control {
			t.Fatal("control lost", raw)
		}
	}
	a.jobsMu.Lock()
	delete(a.activeJobs, ids[5])
	a.jobsMu.Unlock()
	// Stale failures cannot retry lyrics for a rebuilt audio artifact.
	if _, err := a.db.Exec("UPDATE managed SET signature='new' WHERE source_id=1 AND kind='audio'"); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "/api/maintenance/failures", nil)
	if json.Unmarshal(w.Body.Bytes(), &groups) != nil {
		t.Fatal(w.Body.String())
	}
	for _, group := range groups {
		if group.Lyrics != 0 {
			t.Fatal("stale lyric failure remains actionable", group)
		}
	}
}

func TestMaintenanceFailureReasons(t *testing.T) {
	for message, want := range map[string]string{
		"Audio conversion failed\npermission denied":                    "permission",
		"ffmpeg failed: no space left on device":                        "space",
		"Source mount offline":                                          "storage",
		"migu lyrics service returned HTTP 429; retry later":            "rate_limit",
		"kuwo lyrics request failed; check connectivity or retry later": "network",
		"Audio decoding failed\nbad frame":                              "decode",
		"Source read unavailable: timeout":                              "read",
		"Output validation failed: corrupt stream":                      "validate",
		"unrecognized failure":                                          "other",
	} {
		if got := maintenanceFailureReason(message); got != want {
			t.Fatal(message, got, want)
		}
	}
}
