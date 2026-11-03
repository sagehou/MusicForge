package forge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func testUndo(t *testing.T, a *App, operation string, items ...int) libraryToolReport {
	t.Helper()
	request := lyricsSession(t, a)
	w := request("POST", "/api/maintenance/operations/"+operation+"/undo", map[string]any{"items": items})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.toolWG.Wait()
	w = request("GET", "/api/maintenance/undo", nil)
	var report libraryToolReport
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &report) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return report
}

func TestQualityDecisionsAreIndexOnlyAndReopenWhenFactsChange(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	request := lyricsSession(t, a)
	for _, path := range []string{s.Source, s.Output} {
		if err := os.Rename(path, path+".offline"); err != nil {
			t.Fatal(err)
		}
	}
	body := map[string]any{"ids": []int64{source.ID}, "kind": "duration", "state": "ignored", "reason": ""}
	if w := request("PUT", "/api/maintenance/issues", body); w.Code != 400 {
		t.Fatal("ignored without reason", w.Code)
	}
	body["reason"] = "An intentional short introduction"
	if w := request("PUT", "/api/maintenance/issues", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := request("GET", "/api/maintenance/issues", nil)
	var decisions []qualityDecision
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &decisions) != nil || len(decisions) != 1 || decisions[0].Reason != body["reason"] {
		t.Fatal(w.Code, w.Body.String())
	}
	prior, _ := a.meta("quality-decision:duration:1")
	body["ids"], body["state"] = []int64{source.ID, 999}, "open"
	if w = request("PUT", "/api/maintenance/issues", body); w.Code != 409 {
		t.Fatal("accepted missing issue", w.Code)
	}
	if raw, _ := a.meta("quality-decision:duration:1"); raw != prior {
		t.Fatal("partial issue decision was committed")
	}
	if _, err := a.db.Exec("UPDATE sources SET title='New indexed title' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "/api/maintenance/issues", nil)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal("stale decision remained active", w.Code, w.Body.String())
	}
}

func TestLyricsOperationSurvivesRestartAndUndoRestoresStatus(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	ctx := context.Background()
	original := "[00:01.00]Original\n"
	if err := a.writeLyricsFile(ctx, s, source, original, "petitlyrics"); err != nil {
		t.Fatal(err)
	}
	status := LyricsStatus{State: "ready", Output: source.Output, Signature: digest("registered audio fixture"), Path: lyricsRel(source.Output), Provider: "petitlyrics", Checked: 77}
	raw, _ := json.Marshal(status)
	if err := a.setMeta("lyrics-status:1", string(raw)); err != nil {
		t.Fatal(err)
	}
	priorStatus, _ := a.meta("lyrics-status:1")
	priorScript, _ := a.meta("lyrics-script:1")
	request := lyricsSession(t, a)
	w := request("GET", "/api/library/1/lyrics/edit", nil)
	var edit struct{ Token string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &edit) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("PUT", "/api/library/1/lyrics/edit", map[string]any{"token": edit.Token, "text": "[00:02.00]Changed"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	operations, err := a.operations(ctx, s)
	if err != nil || len(operations) != 1 || operations[0].Items[0].State != "applied" {
		t.Fatal(operations, err)
	}
	operation := operations[0]
	w = request("GET", "/api/maintenance/operations/"+operation.ID+"/snapshot?item=0&side=before", nil)
	var snapshot struct{ Text string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &snapshot) != nil || snapshot.Text != original {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("GET", "/api/maintenance/operations/"+operation.ID+"/snapshot?item=5&side=before", nil); w.Code != 404 {
		t.Fatal("invalid snapshot index accepted")
	}
	if err = os.Rename(s.Source, s.Source+".offline"); err != nil {
		t.Fatal(err)
	}
	a.Close()
	reopened, err := New(a.cfg, a.logger, "test", a.assets)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if report := testUndo(t, reopened, operation.ID, 0); report.Failed != 0 || report.Updated != 1 {
		t.Fatal(report)
	}
	entry, text, present, err := reopened.readLyricsFile(ctx, s, lyricsRel(source.Output))
	if err != nil || !present || text != original || entry.Provider != "petitlyrics" {
		t.Fatal(entry, text, present, err)
	}
	if value, _ := reopened.meta("lyrics-status:1"); value != priorStatus {
		t.Fatal("undo changed previous status or checked time", value)
	}
	if value, _ := reopened.meta("lyrics-script:1"); value != priorScript {
		t.Fatal("undo changed previous script annotation", value)
	}
	w = lyricsSession(t, reopened)("POST", "/api/maintenance/operations/"+operation.ID+"/undo", map[string]any{"items": []int{0}})
	if w.Code != 409 {
		t.Fatal("repeated undo accepted", w.Code)
	}
}

func TestLyricsUndoConflictsPreserveCurrentFiles(t *testing.T) {
	for _, conflict := range []string{"content", "identity", "snapshot", "choice"} {
		t.Run(conflict, func(t *testing.T) {
			a, s := testApp(t)
			source := seedLyricsArtifact(t, a, s)
			ctx := context.Background()
			if err := a.writeLyricsFile(ctx, s, source, "[00:01]Before", "source"); err != nil {
				t.Fatal(err)
			}
			target, err := a.captureLyricsTarget(ctx, source.ID, true)
			if err != nil {
				t.Fatal(err)
			}
			target.RecordHistory = true
			if err = a.publishLyricsTarget(ctx, target, "[00:02]After\n", "source"); err != nil {
				t.Fatal(err)
			}
			operations, _ := a.operations(ctx, s)
			operation := operations[0]
			path := filepath.Join(s.Output, lyricsRel(source.Output))
			switch conflict {
			case "content":
				err = os.WriteFile(path, []byte("[00:03]External\n"), 0644)
			case "identity":
				_, err = a.db.Exec("UPDATE sources SET hash='new-source' WHERE id=1")
			case "snapshot":
				err = os.Remove(filepath.Join(a.cfg.ConfigDir, "edit-history", operation.Items[0].BeforeHash+".lyrics"))
			case "choice":
				err = a.writeLyricsFile(ctx, s, source, "[00:02]After\n", "qq")
			}
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			if report := testUndo(t, a, operation.ID, 0); report.Failed != 1 || report.Updated != 0 {
				t.Fatal(report)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("conflict replaced current lyrics")
			}
		})
	}
}

func TestBatchTagUndoKeepsConflictsAndRestoresCorrectionPolicy(t *testing.T) {
	a, s := testApp(t)
	ctx := context.Background()
	for _, rel := range []string{"Album/01.flac", "Album/02.flac"} {
		makeFLAC(t, a, s, rel, "Original", false)
	}
	if err := a.scan(ctx, ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	one, _ := a.sourceRel("Album/01.flac")
	two, _ := a.sourceRel("Album/02.flac")
	request := lyricsSession(t, a)
	apply := func(ids []int64, tags map[string]string) {
		t.Helper()
		w := request("POST", "/api/library/tags/preview", map[string]any{"ids": ids, "tags": tags})
		var preview tagPreview
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		if w = request("POST", "/api/maintenance/tags", map[string]string{"token": preview.Token}); w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		a.toolWG.Wait()
		if a.toolReports["tags"].Failed != 0 {
			t.Fatal(a.toolReports["tags"])
		}
	}
	apply([]int64{one.ID}, map[string]string{"artist": "Prior correction"})
	var signature string
	if err := a.reads.QueryRow("SELECT signature FROM managed WHERE source_id=? AND kind='audio'", one.ID).Scan(&signature); err != nil {
		t.Fatal(err)
	}
	if err := a.writeLyricsFile(ctx, s, one, "[00:01]Manual", "source"); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(LyricsStatus{State: "ready", Output: one.Output, Signature: signature, Checked: 77})
	if err := a.setMeta("lyrics-status:"+strconv.FormatInt(one.ID, 10), string(raw)); err != nil {
		t.Fatal(err)
	}
	apply([]int64{one.ID, two.ID}, map[string]string{"title": "Batch correction"})
	operations, err := a.operations(ctx, s)
	if err != nil || len(operations[0].Items) != 2 {
		t.Fatal(operations, err)
	}
	operation := operations[0]
	if _, err = a.db.Exec("UPDATE sources SET hash='changed' WHERE id=?", two.ID); err != nil {
		t.Fatal(err)
	}
	if report := testUndo(t, a, operation.ID, 0, 1); report.Updated != 1 || report.Failed != 1 {
		t.Fatal(report)
	}
	changes, err := a.outputTagChanges(ctx, s, one, one.BuiltHash)
	if err != nil || len(changes) != 1 || changes["artist"] != "Prior correction" {
		t.Fatal("previous correction policy lost", changes, err)
	}
	probe, err := a.probe(ctx, filepath.Join(s.Output, one.Output))
	if err != nil || probe.tags()["title"] != "Original" || probe.tags()["artist"] != "Prior correction" {
		t.Fatal(probe.tags(), err)
	}
	probe, err = a.probe(ctx, filepath.Join(s.Output, two.Output))
	if err != nil || probe.tags()["title"] != "Batch correction" {
		t.Fatal("conflicting output changed", probe.tags(), err)
	}
	sources, err := a.sourcesWithStatus()
	if err == nil {
		err = a.annotateLyrics(ctx, sources)
	}
	if err != nil || sources[0].Lyrics == nil || sources[0].Lyrics.Checked != 77 {
		t.Fatal("undo invalidated ready lyrics", sources, err)
	}
	if _, err = a.db.Exec("UPDATE sources SET hash=? WHERE id=?", two.Hash, two.ID); err != nil {
		t.Fatal(err)
	}
	if report := testUndo(t, a, operation.ID, 1); report.Updated != 1 || report.Failed != 0 {
		t.Fatal(report)
	}
	if _, err = a.meta("output-tags:" + strconv.FormatInt(two.ID, 10)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("undo retained an empty correction instead of restoring source policy", err)
	}
}

func TestCoverUndoRestoresSnapshotsAndOverridePolicy(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	request := lyricsSession(t, a)
	ctx := context.Background()
	upload := func(value uint8) {
		t.Helper()
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				img.Set(x, y, color.RGBA{R: value, A: 255})
			}
		}
		var buffer bytes.Buffer
		if err := jpeg.Encode(&buffer, img, nil); err != nil {
			t.Fatal(err)
		}
		w := request("GET", "/api/library/1/artwork/edit", nil)
		var edit struct{ Token string }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &edit) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		w = request("PUT", "/api/library/1/artwork/edit", map[string]string{"token": edit.Token, "image": base64.StdEncoding.EncodeToString(buffer.Bytes())})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	upload(40)
	firstOverride, _ := a.meta(artworkOverrideKey("Album/cover.jpg"))
	path := filepath.Join(s.Output, "Album/cover.jpg")
	first, _ := os.ReadFile(path)
	upload(200)
	operations, err := a.operations(ctx, s)
	if err != nil || len(operations) != 2 {
		t.Fatal(operations, err)
	}
	latest := operations[0]
	// An override blob evicted by normal cleanup is restored from the audit snapshot.
	oldPath := filepath.Join(a.cfg.ConfigDir, "artwork-overrides", latest.Items[0].PriorOverrideHash+".jpg")
	if err = os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(a.cfg.ConfigDir, "edit-history", latest.Items[0].PriorOverrideHash+".artwork")
	if err = os.Remove(snapshotPath); err != nil {
		t.Fatal(err)
	}
	current, _ := os.ReadFile(path)
	if report := testUndo(t, a, latest.ID, 0); report.Failed != 1 {
		t.Fatal("missing snapshot accepted", report)
	}
	if preserved, _ := os.ReadFile(path); !bytes.Equal(preserved, current) {
		t.Fatal("missing snapshot replaced current cover")
	}
	if err = a.saveEditBlob(ctx, "artwork", first); err != nil {
		t.Fatal(err)
	}
	if report := testUndo(t, a, latest.ID, 0); report.Failed != 0 || report.Updated != 1 {
		t.Fatal(report)
	}
	restored, _ := os.ReadFile(path)
	if !bytes.Equal(first, restored) {
		t.Fatal("cover bytes not restored")
	}
	if raw, _ := a.meta(artworkOverrideKey("Album/cover.jpg")); raw != firstOverride {
		t.Fatal("previous cover policy not restored")
	}
	if _, err = os.Stat(oldPath); err != nil {
		t.Fatal("previous override blob not restored", err)
	}
	if report := testUndo(t, a, operations[1].ID, 0); report.Failed != 0 || report.Updated != 1 {
		t.Fatal(report)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("new cover was not removed", err)
	}
	if _, err = a.meta(artworkOverrideKey("Album/cover.jpg")); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("new override policy not removed", err)
	}
}

func TestOperationRetentionAndInterruptedIntent(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	ctx := context.Background()
	oldest := ""
	for i := 0; i < 51; i++ {
		text := "[00:01]" + strconv.Itoa(i)
		if err := a.saveEditBlob(ctx, "lyrics", []byte(text)); err != nil {
			t.Fatal(err)
		}
		item := operationTrack(source, digest("registered audio fixture"))
		item.AfterHash = digest(text)
		id, index, err := a.prepareOperation(ctx, s, "lyrics", "", item)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			oldest = id
		}
		a.finishOperation(id, index, "", nil)
	}
	operations, err := a.operations(ctx, s)
	if err != nil || len(operations) != 50 {
		t.Fatal("unbounded operation records", len(operations), err)
	}
	if _, err = a.readOperation(ctx, oldest); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("old record not pruned", err)
	}
	files, err := os.ReadDir(filepath.Join(a.cfg.ConfigDir, "edit-history"))
	if err != nil || len(files) != 50 {
		t.Fatal("evicted operation leaked snapshots", len(files), err)
	}
	id, _, err := a.prepareOperation(ctx, s, "lyrics", "", operationTrack(source, digest("registered audio fixture")))
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	reopened, err := New(a.cfg, a.logger, "test", a.assets)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	interrupted, err := reopened.readOperation(ctx, id)
	if err != nil || interrupted.Items[0].State != "interrupted" {
		t.Fatal(interrupted, err)
	}
	w := lyricsSession(t, reopened)("POST", "/api/maintenance/operations/"+id+"/undo", map[string]any{"items": []int{0}})
	if w.Code != 409 {
		t.Fatal("interrupted edit was eligible for automatic undo", w.Code)
	}
}

func TestTagUndoPreservesNewerPolicyEvenWithoutAudioChange(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	ctx := context.Background()
	item := operationTrack(source, digest("registered audio fixture"))
	item.Before = map[string]string{"title": "Original"}
	item.AppliedTags = map[string]string{"title": "Changed"}
	id, index, err := a.prepareOperation(ctx, s, "tags", "", item)
	if err != nil {
		t.Fatal(err)
	}
	a.finishOperation(id, index, "", nil)
	current := outputTagOverride{Version: 1, SourceRoot: s.Source, OutputRoot: s.Output, Rel: source.Rel, SourceHash: source.BuiltHash, Tags: map[string]string{"title": "Changed", "artist": "Explicit new policy"}}
	raw, _ := json.Marshal(current)
	if err = a.setMeta("output-tags:1", string(raw)); err != nil {
		t.Fatal(err)
	}
	if report := testUndo(t, a, id, 0); report.Failed != 1 || report.Updated != 0 {
		t.Fatal("newer policy was overwritten", report)
	}
	if actual, _ := a.meta("output-tags:1"); actual != string(raw) {
		t.Fatal("undo changed a newer correction policy")
	}
}

func TestStorageChecksAndMigrationPreviewPreservePaths(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	request := lyricsSession(t, a)
	before, _ := a.meta("instance")
	next := t.TempDir()
	newSource, newOutput := filepath.Join(next, "source"), filepath.Join(next, "output")
	for _, path := range []string{newSource, newOutput} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	w := request("POST", "/api/maintenance/migration/preview", map[string]string{"source": newSource, "output": newOutput})
	var plan struct {
		PathsApplied bool `json:"paths_applied"`
		Output       storageProbe
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.PathsApplied || !plan.Output.Empty || plan.Output.Free == 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	settings, _ := a.settings()
	if settings.Source != s.Source || settings.Output != s.Output {
		t.Fatal("preview applied paths")
	}
	if instance, _ := a.meta("instance"); instance != before {
		t.Fatal("preview changed ownership")
	}
	if files, err := os.ReadDir(newOutput); err != nil || len(files) != 0 {
		t.Fatal("preview created destination files", err)
	}
	if err := os.Symlink(newSource, filepath.Join(newSource, "nested")); err != nil {
		t.Fatal(err)
	}
	if w = request("POST", "/api/maintenance/migration/preview", map[string]string{"source": newSource, "output": filepath.Join(newSource, "nested")}); w.Code != 400 {
		t.Fatal("accepted resolved path overlap", w.Code, w.Body.String())
	}
	if err := os.WriteFile(filepath.Join(newOutput, "foreign"), []byte("preserve"), 0644); err != nil {
		t.Fatal(err)
	}
	if w = request("POST", "/api/maintenance/migration/preview", map[string]string{"source": newSource, "output": newOutput}); w.Code != 409 {
		t.Fatal("accepted unowned nonempty output", w.Code)
	}
	if err := os.Rename(s.Source, s.Source+".offline"); err != nil {
		t.Fatal(err)
	}
	if w = request("GET", "/api/maintenance/storage/health", nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("POST", "/api/maintenance/storage/check", map[string]any{}); w.Code != 200 || !strings.Contains(w.Body.String(), "error") {
		t.Fatal("offline source check was not reported", w.Code, w.Body.String())
	}
	for _, mismatch := range []bool{false, true} {
		if mismatch {
			if err := os.WriteFile(filepath.Join(s.Output, source.Output), []byte("external audio"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		w = request("POST", "/api/maintenance/storage/verify", map[string]any{"ids": []int64{source.ID}})
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		a.toolWG.Wait()
		report := a.toolReports["storage"]
		if report.Processed != 1 || mismatch != (report.Failed == 1) {
			t.Fatal(mismatch, report)
		}
	}
	data, _ := os.ReadFile(filepath.Join(s.Output, source.Output))
	if string(data) != "external audio" {
		t.Fatal("verification changed output")
	}
}
