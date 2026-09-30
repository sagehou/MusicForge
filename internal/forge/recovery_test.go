package forge

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"testing/fstest"
	"time"
)

func TestPromotionAndRestartRecovery(t *testing.T) {
	a, s := testApp(t)
	sourcePath := makeFLAC(t, a, s, "Album/01.flac", "Track", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	source, _ := a.sourceRel("Album/01.flac")
	profile := Encoding{"mp3", "vbr", 192, 2}
	s.Encoding = profile
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	tempRel := "Album/.musicforge-recovery.part"
	target := "Album/01.mp3"
	temp := filepath.Join(s.Output, tempRel)
	args := []string{"-nostdin", "-v", "error", "-i", sourcePath, "-map", "0:a:0", "-vn"}
	args = append(args, profile.Args()...)
	args = append(args, temp)
	if _, err := runTool(context.Background(), a.cfg.FFmpeg, args...); err != nil {
		t.Fatal(err)
	}
	if err := a.validateArtifact(context.Background(), temp, profile, source.Duration); err != nil {
		t.Fatal(err)
	}
	hash, err := fileHash(context.Background(), temp)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.db.Exec("INSERT INTO managed(path,source_id,kind) VALUES(?,?,'temp')", tempRel, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := promotion{source.ID, tempRel, target, source.Output, source.Hash, profile.Fingerprint(), hash}
	b, _ := json.Marshal(p)
	if err = a.setMeta("promotion:"+strconv.FormatInt(source.ID, 10), string(b)); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(temp, filepath.Join(s.Output, target)); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after the atomic rename, before the SQLite update.
	_, err = a.db.Exec("INSERT INTO managed(path,kind) VALUES('Album/.musicforge-owned.part','temp')")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(s.Output, "Album/.musicforge-owned.part"), []byte("owned"), 0600)
	foreign := filepath.Join(s.Output, "Album/foreign.part")
	_ = os.WriteFile(foreign, []byte("foreign"), 0600)
	jobID, err := a.queueBuild(source, profile, false, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.db.Exec("UPDATE jobs SET state='running',attempts=2 WHERE id=?", jobID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := a.cfg
	a.Close()
	restored, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), "test", fstest.MapFS{"index.html": {Data: []byte("test")}})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var state string
	var attempts int
	if err = restored.db.QueryRow("SELECT state,attempts FROM jobs WHERE id=?", jobID).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || attempts != 2 {
		t.Fatal("interruption consumed attempt or lost queued job")
	}
	if err = restored.ensureRecovery(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	recovered, err := restored.source(source.ID)
	if err != nil || recovered.Output != target || recovered.BuiltProfile != profile.Fingerprint() {
		t.Fatal("promotion was not reconciled")
	}
	if _, err = os.Stat(filepath.Join(s.Output, source.Output)); !os.IsNotExist(err) {
		t.Fatal("old codec not removed after recovery")
	}
	if _, err = os.Stat(filepath.Join(s.Output, "Album/.musicforge-owned.part")); !os.IsNotExist(err) {
		t.Fatal("owned temp not cleaned")
	}
	if foreignBytes, readErr := os.ReadFile(foreign); readErr != nil || string(foreignBytes) != "foreign" {
		t.Fatal("foreign temp was touched")
	}
	// Cleanup replay must be idempotent after an unlink already persisted.
	if err = restored.setMeta("promotion:"+strconv.FormatInt(source.ID, 10), string(b)); err != nil {
		t.Fatal(err)
	}
	if err = restored.recoverPromotions(context.Background(), s); err != nil {
		t.Fatal("cleanup replay is not idempotent:", err)
	}
}

func TestRetryBudgetAndInterruption(t *testing.T) {
	a, _ := testApp(t)
	id, err := a.enqueue("unknown", "budget-test", map[string]bool{}, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.worker(ctx, false, 0) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		var attempts int
		if err = a.db.QueryRow("SELECT state,attempts FROM jobs WHERE id=?", id).Scan(&state, &attempts); err != nil {
			t.Fatal(err)
		}
		if state == "failed" {
			if attempts != 3 {
				t.Fatalf("got %d attempts", attempts)
			}
			return
		}
		_, _ = a.db.Exec("UPDATE jobs SET not_before=0 WHERE id=? AND state='pending'", id)
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not stop at three failures")
}
