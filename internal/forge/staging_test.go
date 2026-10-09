package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestTagsPrecedeFullReadAndEncodingReusesOneStagedCopy(t *testing.T) {
	a, s := testApp(t)
	rel := "歌手/专辑/01.flac"
	path := makeFLAC(t, a, s, rel, "Visible before download", true)
	expected, err := fileHash(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "source-reads")
	t.Setenv("MUSICFORGE_TEST_SOURCE_TRACE", trace)
	sourceFault(t, "stage", rel, 10000, 0)
	id, err := a.enqueue("scan", "scan:single-read", ScanRequest{}, true)
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.claim(false)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.execute(context.Background(), j); err != nil {
		t.Fatal("metadata scan tried to stage audio", err)
	}
	if err = a.completeJob(j, "success", 0, 1, "", 0); err != nil {
		t.Fatal(err)
	}
	source, err := a.sourceRel(rel)
	if err != nil || source.Hash != "" || source.Title != "Visible before download" || source.Size < 1 || source.Mtime == 0 {
		t.Fatal("tag-only index is not usable before full download", source, err)
	}
	items, _, err := a.taskItems(id, "pending", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if strings.HasPrefix(item.Key, prepareScanPrefix) {
			var request ScanRequest
			if err = json.Unmarshal(item.Args, &request); err != nil || !request.Verify || len(request.Dirs) != 1 || request.Dirs[0] != rel {
				t.Fatal("preparation is not an old-reader-compatible verification scan", item)
			}
		}
	}
	sourceFault(t, "stage", rel, 0, 0)
	// Repeated indexing shares the original verification, including while it runs.
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal("repeated metadata scan could not share preparation", err)
	}
	var preparedID int64
	if err = a.db.QueryRow("SELECT id FROM jobs WHERE dedup LIKE 'scan:prepare:%' AND state='pending'").Scan(&preparedID); err != nil {
		t.Fatal(err)
	}
	_, _ = a.db.Exec("UPDATE jobs SET state='running' WHERE id=?", preparedID)
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.meta("scan-followup:" + strconv.FormatInt(preparedID, 10)); err != sql.ErrNoRows {
		t.Fatal("identical preparation was scheduled for a second full read", err)
	}
	_, _ = a.db.Exec("UPDATE jobs SET state='pending' WHERE id=?", preparedID)
	// ffmpeg may decode/encode scratch files, never the mounted source audio.
	tool := filepath.Join(t.TempDir(), "local-input-only")
	script := "#!/bin/sh\nprevious=\nfor argument do\nif [ \"$previous\" = -i ]; then\ncase \"$argument\" in\n\"$MUSICFORGE_TEST_SOURCE_ROOT\"/*) exit 41;;\nesac\nfi\nprevious=$argument\ndone\nexec ffmpeg \"$@\"\n"
	if err = os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSICFORGE_TEST_SOURCE_ROOT", s.Source)
	a.cfg.FFmpeg = tool
	drain(t, a, true)
	source, err = a.sourceRel(rel)
	if err != nil || source.Hash != expected || source.BuiltHash != expected || !source.OutputPresent {
		t.Fatal("staged build changed whole-file hash semantics", source, err)
	}
	if _, err = os.Stat(filepath.Join(s.Output, "歌手/专辑/cover.jpg")); err != nil {
		t.Fatal("embedded cover did not use staged audio", err)
	}
	reads, err := os.ReadFile(trace)
	if err != nil || strings.Count(string(reads), "stage:"+rel+"\n") != 1 || strings.Contains(string(reads), "hash:"+rel+"\n") {
		t.Fatal("successful build performed multiple complete source reads", string(reads), err)
	}
	entries, err := os.ReadDir(a.stagingDir)
	if err != nil || len(entries) != 0 || a.stagingBytes != 0 {
		t.Fatal("staging leaked after success", entries, a.stagingBytes, err)
	}
}

func TestStagingBoundRejectsBeforeReadingAndCleansInterruptedCopies(t *testing.T) {
	a, s := testApp(t)
	rel := "Album/01.flac"
	makeFLAC(t, a, s, rel, "Bounded local space", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	source, _ := a.sourceRel(rel)
	a.cfg.StagingMaxBytes = source.Size - 1
	if _, err := a.stageSource(context.Background(), s, source, 0, Activity{}); err == nil || !strings.Contains(err.Error(), "MUSICFORGE_STAGING_MAX_BYTES") {
		t.Fatal("oversized source was not rejected before downloading", err)
	}
	a.cfg.StagingMaxBytes = source.Size
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.stageSource(ctx, s, source, 0, Activity{}); err == nil {
		t.Fatal("cancelled staging succeeded")
	}
	entries, err := os.ReadDir(a.stagingDir)
	if err != nil || len(entries) != 0 || a.stagingBytes != 0 {
		t.Fatal("interrupted staging retained a file or reservation", entries, a.stagingBytes, err)
	}
}
