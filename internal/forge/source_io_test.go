package forge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise real process cancellation without requiring a FUSE mount in unit tests.
func TestMain(m *testing.M) {
	args := os.Args[1:]
	if len(args) >= 4 && args[0] == "-source-io" {
		if trace := os.Getenv("MUSICFORGE_TEST_SOURCE_TRACE"); trace != "" {
			if f, err := os.OpenFile(trace, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
				_, _ = fmt.Fprintln(f, args[1]+":"+args[3])
				_ = f.Close()
			}
		}
		var fault struct {
			Operation string `json:"operation"`
			Rel       string `json:"rel"`
			Delay     int    `json:"delay_ms"`
			Pulses    int    `json:"pulses"`
		}
		_ = json.Unmarshal([]byte(os.Getenv("MUSICFORGE_TEST_SOURCE_FAULT")), &fault)
		if args[1] == fault.Operation && args[3] == fault.Rel {
			for i := 0; i < fault.Pulses; i++ {
				time.Sleep(300 * time.Millisecond)
				_ = json.NewEncoder(os.Stdout).Encode(sourceEvent{Read: int64(i + 1)})
			}
			time.Sleep(time.Duration(fault.Delay) * time.Millisecond)
		}
	}
	if RunSourceIO(args) {
		os.Exit(0)
	}
	// Race-enabled helper processes need no one-second exit delay per filesystem call.
	_ = os.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	os.Exit(m.Run())
}

func sourceFault(t *testing.T, operation, rel string, delay, pulses int) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"operation": operation, "rel": rel, "delay_ms": delay, "pulses": pulses})
	t.Setenv("MUSICFORGE_TEST_SOURCE_FAULT", string(raw))
}

func TestSourceHashKeepsWholeFileSignatureAndUsesIdleTimeout(t *testing.T) {
	a, s := testApp(t)
	a.cfg.SourceTimeoutSeconds = 1
	path := makeFLAC(t, a, s, "Artist/Album/01.flac", "Tags are hashed too", true)
	expected, err := fileHash(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	sourceFault(t, "hash", "Artist/Album/01.flac", 0, 5)
	started := time.Now()
	actual, err := a.sourceHash(context.Background(), s.Source, "Artist/Album/01.flac", nil)
	if err != nil || actual != expected || time.Since(started) < time.Second {
		t.Fatalf("slow progressing read changed signature or timed out: %s %s %v", actual, expected, err)
	}
	sourceFault(t, "hash", "Artist/Album/01.flac", 10000, 0)
	started = time.Now()
	if _, err = a.sourceHash(context.Background(), s.Source, "Artist/Album/01.flac", nil); !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 3*time.Second {
		t.Fatalf("hung read was not bounded: %v", err)
	}
	if _, err = a.sourceStat(context.Background(), s.Source, "../escape"); err == nil {
		t.Fatal("helper accepted path traversal")
	}
	if _, err = a.sourceStat(context.Background(), s.Source, "missing.flac"); !os.IsNotExist(err) || !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing files lost filesystem error compatibility", err)
	}
}

func TestRemoteReadFailurePastThirtyTracksKeepsHealthyQueueAndStopsAfterThreeAttempts(t *testing.T) {
	a, s := testApp(t)
	a.cfg.SourceTimeoutSeconds = 1
	removed := makeFLAC(t, a, s, "Removed/01.flac", "Keep playable", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	expired, _ := a.sourceRel("Removed/01.flac")
	playable, err := os.ReadFile(filepath.Join(s.Output, expired.Output))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	first := makeFLAC(t, a, s, "Artist/Album/01.flac", "Remote track", false)
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 40; i++ {
		path := filepath.Join(s.Source, fmt.Sprintf("Artist/Album/%02d.flac", i))
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Minute)
		if err = os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	sourceFault(t, "stage", "Artist/Album/36.flac", 10000, 0)
	id, err := a.enqueue("scan", "scan:remote-test", ScanRequest{}, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	conversionsDone := make(chan struct{})
	go func() { defer close(done); a.worker(ctx, false, 0) }()
	go func() { defer close(conversionsDone); a.worker(ctx, true, 0) }()
	defer func() { cancel(); <-done; <-conversionsDone }()
	deadline := time.Now().Add(60 * time.Second)
	for {
		j, err := readJob(a.db.QueryRow("SELECT "+jobCols+" FROM jobs WHERE dedup LIKE 'scan:prepare:%' AND json_extract(args,'$.dirs[0]')='Artist/Album/36.flac' ORDER BY id DESC LIMIT 1"))
		if err == sql.ErrNoRows {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if j.State == "failed" && j.Attempts == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("read retries did not terminate: %+v", j)
		}
		if j.State == "pending" && j.Attempts > 0 {
			_, _ = a.db.Exec("UPDATE jobs SET not_before=0 WHERE id=?", j.ID)
		}
		if _, _, err = a.taskList("all", 100, 0); err != nil {
			t.Fatal("queue API failed during indexing", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	retained, _ := a.source(expired.ID)
	if !retained.Present {
		t.Fatal("incomplete remote scan expired an unseen source")
	}
	after, _ := os.ReadFile(filepath.Join(s.Output, expired.Output))
	if !bytes.Equal(after, playable) {
		t.Fatal("incomplete scan damaged playable output")
	}
	bad, err := a.sourceRel("Artist/Album/36.flac")
	if err != nil || !strings.HasPrefix(bad.Error, sourceReadErrorPrefix) {
		t.Fatal("missing readable source error", bad, err)
	}
	var queued int
	for {
		if err = a.db.QueryRow("SELECT count(*) FROM jobs j JOIN meta m ON m.key='task-member:'||j.id WHERE m.value=? AND j.dedup LIKE 'scan:prepare:%' AND j.state='success'", fmt.Sprint(id)).Scan(&queued); err != nil {
			t.Fatal(err)
		}
		if queued == 39 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("one unavailable track blocked healthy tracks", queued)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	<-conversionsDone
	sourceFault(t, "stage", "Artist/Album/36.flac", 0, 0)
	if _, err = a.changeTasks("retry", selection{IDs: []int64{id}}); err != nil {
		t.Fatal(err)
	}
	j, err := a.claim(true)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.execute(context.Background(), j); err != nil {
		t.Fatal("recovered source was not retried", err)
	}
	if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil {
		t.Fatal(err)
	}
	bad, _ = a.source(bad.ID)
	if bad.Error != "" || bad.Hash == "" {
		t.Fatal("source error did not clear after recovery", bad)
	}
}

func TestHungScanLeavesAPIResponsiveAndCanPause(t *testing.T) {
	a, s := testApp(t)
	makeFLAC(t, a, s, "Artist/Album/01.flac", "Remote track", false)
	sourceFault(t, "stage", "Artist/Album/01.flac", 10000, 0)
	id, err := a.enqueue("scan", "scan:pause-remote", ScanRequest{}, true)
	if err != nil {
		t.Fatal(err)
	}
	session := httptest.NewRecorder()
	if err = a.newSession(session, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	cookie := session.Result().Cookies()[0]
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	conversionsDone := make(chan struct{})
	go func() { defer close(done); a.worker(ctx, false, 0) }()
	go func() { defer close(conversionsDone); a.worker(ctx, true, 0) }()
	defer func() { cancel(); <-done; <-conversionsDone }()
	deadline := time.Now().Add(5 * time.Second)
	var readingID int64
	for {
		items, _, _ := a.taskItems(id, "running", 50, 0)
		for _, item := range items {
			if item.Activity.Phase == "read" {
				readingID = item.ID
			}
		}
		if readingID != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan did not reach source read")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, path := range []string{"/api/jobs", "/api/library", "/api/dashboard", "/healthz"} {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		started := time.Now()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 200 || time.Since(started) > time.Second {
			t.Fatal("API waited for the hung source", path, w.Code, w.Body.String())
		}
	}
	if _, err = a.changeTasks("pause", selection{IDs: []int64{id}}); err != nil {
		t.Fatal(err)
	}
	waitTaskWorker(t, a, readingID, false)
	var attempts int
	var notBefore int64
	if err = a.db.QueryRow("SELECT attempts,not_before FROM jobs WHERE id=?", readingID).Scan(&attempts, &notBefore); err != nil || attempts != 0 || notBefore != heldUntil {
		t.Fatal("pause consumed an attempt or did not persist", attempts, notBefore, err)
	}
}

func TestDashboardDoesNotWaitForRemoteRootStat(t *testing.T) {
	a, _ := testApp(t)
	a.cfg.SourceTimeoutSeconds = 1
	sourceFault(t, "stat", ".", 10000, 0)
	started := time.Now()
	w := httptest.NewRecorder()
	a.dashboard(w, httptest.NewRequest("GET", "/api/dashboard", nil))
	if w.Code != 200 || time.Since(started) > 500*time.Millisecond || !strings.Contains(w.Body.String(), `"storage_checking":true`) {
		t.Fatal("dashboard blocked on the mount", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		w = httptest.NewRecorder()
		a.dashboard(w, httptest.NewRequest("GET", "/api/dashboard", nil))
		if strings.Contains(w.Body.String(), "Source storage offline:") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background storage timeout missing", w.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProbeDoesNotTruncateLargeTags(t *testing.T) {
	a, s := testApp(t)
	path := makeFLAC(t, a, s, "Album/01.flac", "Title", false)
	large := strings.Repeat("x", 90000)
	output := filepath.Join(t.TempDir(), "tagged.flac")
	if _, err := runTool(context.Background(), a.cfg.FFmpeg, "-v", "error", "-i", path, "-c:a", "copy", "-metadata", "comment="+large, output); err != nil {
		t.Fatal(err)
	}
	p, err := a.probe(context.Background(), output)
	if err != nil || p.tags()["comment"] != large {
		t.Fatal("large but valid tags truncated probe JSON", err)
	}
}

func TestStalledEncoderKeepsExistingArtifact(t *testing.T) {
	a, s := testApp(t)
	makeFLAC(t, a, s, "Album/01.flac", "Playable", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	source, _ := a.sourceRel("Album/01.flac")
	before, _ := os.ReadFile(filepath.Join(s.Output, source.Output))
	s.Encoding = Encoding{"mp3", "vbr", 192, 2}
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	a.cfg.SourceTimeoutSeconds = 1
	tool := filepath.Join(t.TempDir(), "stalled-ffmpeg")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexec sleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	a.cfg.FFmpeg = tool
	if _, err := a.queueBuild(source, s.Encoding, false, true); err != nil {
		t.Fatal(err)
	}
	j, err := a.claim(true)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err = a.execute(context.Background(), j); !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 3*time.Second {
		t.Fatal("encoder stall not bounded", err)
	}
	after, _ := os.ReadFile(filepath.Join(s.Output, source.Output))
	if !bytes.Equal(before, after) {
		t.Fatal("stalled encoder replaced playable output")
	}
	var temps int
	if err = a.db.QueryRow("SELECT count(*) FROM managed WHERE kind='temp'").Scan(&temps); err != nil || temps != 0 {
		t.Fatal("temporary artifact leaked", temps, err)
	}
}

func TestCancelledWorkDoesNotWaitForAnotherScanMutationLock(t *testing.T) {
	a, _ := testApp(t)
	for _, kind := range []string{"scan", "convert", "delete", "upgrade"} {
		t.Run(kind, func(t *testing.T) {
			a.files.Lock()
			defer a.files.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- a.execute(ctx, Job{Kind: kind, Args: json.RawMessage(`{}`)}) }()
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancelled worker did not exit", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled worker still waits for a remote scan")
			}
		})
	}
}

func TestSourceTimeoutRuntimeValidation(t *testing.T) {
	cfg, err := LoadRuntime(t.TempDir())
	if err != nil || cfg.SourceTimeoutSeconds != 120 {
		t.Fatal("missing timeout default", cfg, err)
	}
	for _, value := range []string{"0", "-1", "3601", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("MUSICFORGE_SOURCE_TIMEOUT_SECONDS", value)
			if _, err := LoadRuntime(t.TempDir()); err == nil {
				t.Fatal("invalid source timeout accepted", value)
			}
		})
	}
	t.Setenv("MUSICFORGE_SOURCE_TIMEOUT_SECONDS", "300")
	if cfg, err = LoadRuntime(t.TempDir()); err != nil || cfg.SourceTimeoutSeconds != 300 {
		t.Fatal("timeout override ignored", cfg, err)
	}
}
