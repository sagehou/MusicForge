package forge

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestSelectedArtifactDeletionSurvivesRestartAndKeepsSources(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	path := filepath.Join(s.Source, source.Rel)
	if err := os.WriteFile(path, []byte("keep source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.writeLyricsFile(context.Background(), s, source, "[00:00.00]Owned lyrics\n", "source"); err != nil {
		t.Fatal(err)
	}
	if err := a.setMeta("lyrics-status:1", `{"state":"ready","output":"Album/01.opus","signature":"`+digest("registered audio fixture")+`","checked":77}`); err != nil {
		t.Fatal(err)
	}
	request := lyricsSession(t, a)
	for _, body := range []selection{{IDs: []int64{source.ID}}, {All: true}} {
		if w := request("POST", "/api/library/delete", body); w.Code != 200 || w.Body.String() != "{\"deleted\":0}\n" {
			t.Fatal("old delete requests must remain expired-only", w.Code, w.Body.String())
		}
	}
	for _, body := range []map[string]any{
		{"artifacts": true, "all": true}, {"artifacts": true, "ids": []int64{0}}, {"artifacts": true},
	} {
		if w := request("POST", "/api/library/delete", body); w.Code != 400 {
			t.Fatal("unsafe artifact selection accepted", w.Code, w.Body.String())
		}
	}
	unauthorized := httptest.NewRecorder()
	a.Handler().ServeHTTP(unauthorized, httptest.NewRequest("POST", "/api/library/delete", nil))
	if unauthorized.Code != 401 {
		t.Fatal("unauthenticated artifact deletion accepted", unauthorized.Code)
	}
	if _, err := a.db.Exec("DELETE FROM managed WHERE source_id=?", source.ID); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/library/delete", map[string]any{"ids": []int64{source.ID}, "artifacts": true}); w.Code != 409 {
		t.Fatal("unregistered artifact was accepted", w.Code, w.Body.String())
	}
	if _, err := a.db.Exec("INSERT INTO managed(path,source_id,kind,signature) VALUES(?,?,'audio',?)", source.Output, source.ID, digest("registered audio fixture")); err != nil {
		t.Fatal(err)
	}
	w := request("POST", "/api/library/delete", map[string]any{"ids": []int64{source.ID, source.ID}, "artifacts": true})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	job, err := a.claim(false)
	if err != nil || string(job.Args) != "{\"ids\":[1]}" {
		t.Fatal("manual deletion changed the published job arguments", job, err)
	}
	// Restoring the application must retain the explicit selection even when the
	// source mount cannot be read; only the selected output is being removed.
	if err = os.Rename(s.Source, s.Source+".offline"); err != nil {
		t.Fatal(err)
	}
	cfg := a.cfg
	a.Close()
	restored, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), "test", fstest.MapFS{"index.html": {Data: []byte("test")}})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	for range 2 {
		if err = restored.execute(context.Background(), job); err != nil {
			t.Fatal("explicit deletion did not recover safely", err)
		}
	}
	for _, rel := range []string{source.Output, lyricsRel(source.Output)} {
		if _, err = os.Stat(filepath.Join(s.Output, rel)); !os.IsNotExist(err) {
			t.Fatal("selected artifact was retained", rel, err)
		}
	}
	if content, err := os.ReadFile(filepath.Join(s.Source+".offline", source.Rel)); err != nil || string(content) != "keep source" {
		t.Fatal("source was modified", err)
	}
	current, err := restored.source(source.ID)
	if err != nil || !current.Present || current.Output != "" || current.OutputPresent || current.BuiltHash != "" {
		t.Fatal("manual deletion left stale artifact state", current, err)
	}
	if _, err = restored.meta("lyrics-status:1"); err != sql.ErrNoRows {
		t.Fatal("deleted lyrics retained a completed result that would skip refilling", err)
	}
	var dirty int
	if err = restored.db.QueryRow("SELECT count(*) FROM dirty_dirs WHERE path='Album'").Scan(&dirty); err != nil || dirty != 1 {
		t.Fatal("Navidrome refresh was not retained", dirty, err)
	}
	if err = restored.completeJob(job, "success", 0, 1, "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.changeTasks("delete", selection{IDs: []int64{job.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.meta("artifact-delete:" + job.Key); err != sql.ErrNoRows {
		t.Fatal("deleting task history retained its selection", err)
	}
}

func TestArtifactDeletionProtectsChangedFilesAndContinuesBatch(t *testing.T) {
	for _, change := range []string{"registered-rebuild", "reassigned", "external-edit", "symlink", "missing-file"} {
		t.Run(change, func(t *testing.T) {
			a, s := testApp(t)
			first := seedLyricsArtifact(t, a, s)
			second := "Album/02.opus"
			if _, err := a.db.Exec("INSERT INTO sources(id,rel,output,output_present) VALUES(2,'Album/02.flac',?,1)", second); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.Output, second), []byte("second artifact"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := a.db.Exec("INSERT INTO managed(path,source_id,kind,signature) VALUES(?,2,'audio',?)", second, digest("second artifact")); err != nil {
				t.Fatal(err)
			}
			request := lyricsSession(t, a)
			if w := request("POST", "/api/library/delete", map[string]any{"ids": []int64{1, 2}, "artifacts": true}); w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			path := filepath.Join(s.Output, first.Output)
			switch change {
			case "reassigned":
				if _, err := a.db.Exec("UPDATE managed SET source_id=2 WHERE path=?", first.Output); err != nil {
					t.Fatal(err)
				}
			case "registered-rebuild", "external-edit":
				if err := os.WriteFile(path, []byte("new audio"), 0600); err != nil {
					t.Fatal(err)
				}
				if change == "registered-rebuild" {
					if _, err := a.db.Exec("UPDATE managed SET signature=? WHERE source_id=1", digest("new audio")); err != nil {
						t.Fatal(err)
					}
				}
			case "symlink", "missing-file":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if change == "symlink" {
					if err := os.Symlink(filepath.Join(s.Output, second), path); err != nil {
						t.Fatal(err)
					}
				}
			}
			job, err := a.claim(false)
			if err != nil {
				t.Fatal(err)
			}
			err = a.execute(context.Background(), job)
			if (err == nil) != (change == "missing-file") {
				t.Fatal("changed artifact was not protected", change, err)
			}
			if _, err = os.Stat(filepath.Join(s.Output, second)); !os.IsNotExist(err) {
				t.Fatal("one failure blocked the other selected artifact", err)
			}
			current, err := a.source(first.ID)
			if err != nil || (current.Output == "") != (change == "missing-file") {
				t.Fatal("wrong artifact index was cleared", current, err)
			}
			if change != "missing-file" {
				if _, err = os.Lstat(path); err != nil {
					t.Fatal("protected file was removed", err)
				}
			}
			var plan artifactDeletePlan
			raw, err := a.meta("artifact-delete:" + job.Key)
			if err != nil || json.Unmarshal([]byte(raw), &plan) != nil || len(plan.Items) != 2 {
				t.Fatal("retry lost the original deletion selection", raw, err)
			}
		})
	}
}
