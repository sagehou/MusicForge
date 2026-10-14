package forge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestForgetMissingRecordsPreservesFilesOwnershipWorkAndHistory(t *testing.T) {
	a, s := testApp(t)
	for id := int64(1); id <= 16; id++ {
		if _, err := a.db.Exec("INSERT INTO sources(id,rel,present) VALUES(?,?,0)", id, "Artist/Album/"+strconv.FormatInt(id, 10)+".flac"); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		"UPDATE sources SET present=1 WHERE id=2",
		"UPDATE sources SET output='Artist/Album/3.opus',output_present=1,built_hash='built' WHERE id=3",
		"INSERT INTO managed(path,source_id,kind) VALUES('Artist/Album/4.opus',4,'audio')",
	} {
		if _, err := a.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	jobs := []struct {
		kind, key string
		args      any
		source    int64
	}{
		{"convert", "convert:5", BuildRequest{ID: 5}, 5},
		{"move", "move:6", BuildRequest{ID: 6}, 6},
		{"scan", "scan:prepare:7", ScanRequest{Dirs: []string{"Artist/Album/7.flac"}, Verify: true}, 7},
		{"delete", "delete:8", selection{IDs: []int64{8}}, 8},
		{"upgrade", "upgrade:9", UpgradeRequest{Old: []string{"Artist/Album/9.flac"}, New: []string{"Artist/Album/10.flac"}}, 9},
		{"convert", "convert:11", BuildRequest{ID: 11}, 11},
		{"convert", "convert:15", BuildRequest{ID: 15}, 15},
	}
	for _, job := range jobs {
		id, err := a.enqueue(job.kind, job.key, job.args, true)
		if err != nil {
			t.Fatal(err)
		}
		if job.source == 11 {
			if _, err = a.db.Exec("UPDATE jobs SET state='failed',log='Stopped by administrator' WHERE id=?", id); err != nil {
				t.Fatal(err)
			}
			a.activeJobs[id] = func() {}
		}
		if job.source == 15 {
			if _, err = a.changeTasks("pause", selection{IDs: []int64{id}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	a.repairs[12] = &repairEntry{RepairStatus: RepairStatus{State: "running"}}
	if err := a.setMeta("promotion:13", `{"id":13}`); err != nil {
		t.Fatal(err)
	}
	if err := a.setMeta("deletion:14:journal", `{"id":14}`); err != nil {
		t.Fatal(err)
	}
	if err := a.setMeta("repair-task:history", `{"source_id":1,"state":"failed"}`); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(s.Output, "unregistered.txt")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	// Cleanup must work even with offline mounts; only the SQLite index changes.
	if err := os.Rename(s.Source, s.Source+"-offline"); err != nil {
		t.Fatal(err)
	}
	issued := httptest.NewRecorder()
	if err := a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	cookie := issued.Result().Cookies()[0]
	send := func(body selection, auth, csrf bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/api/library/forget", bytes.NewReader(raw))
		if auth {
			r.AddCookie(cookie)
		}
		if csrf {
			r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	for _, credentials := range [][2]bool{{false, false}, {true, false}} {
		if w := send(selection{All: true}, credentials[0], credentials[1]); w.Code != 401 && w.Code != 403 {
			t.Fatal("cleanup accepted missing authentication/CSRF", w.Code)
		}
	}
	if w := send(selection{IDs: []int64{-1}}, true, true); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.files.Lock()
	w := send(selection{All: true}, true, true)
	a.files.Unlock()
	if w.Code != 409 {
		t.Fatal("busy file operations must not stall cleanup", w.Code)
	}
	w = send(selection{IDs: []int64{1, 1, 2, 3, 4}}, true, true)
	if w.Code != 200 || w.Body.String() != "{\"removed\":1,\"skipped\":3}\n" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send(selection{All: true}, true, true)
	if w.Code != 200 || w.Body.String() != "{\"removed\":1,\"skipped\":12}\n" {
		t.Fatal(w.Code, w.Body.String())
	}
	for id := int64(1); id <= 16; id++ {
		_, err := a.source(id)
		if (id == 1 || id == 16) && err != sql.ErrNoRows || id != 1 && id != 16 && err != nil {
			t.Fatalf("unexpected cleanup of record %d: %v", id, err)
		}
	}
	if raw, err := os.ReadFile(file); err != nil || string(raw) != "keep" {
		t.Fatal("cleanup touched an output file", err)
	}
	if raw, err := a.meta("repair-task:history"); err != nil || raw != `{"source_id":1,"state":"failed"}` {
		t.Fatal("cleanup changed repair history", err)
	}
	var version, history int
	if err := a.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatal("cleanup changed schema", version, err)
	}
	if err := a.db.QueryRow("SELECT count(*) FROM jobs").Scan(&history); err != nil || history != len(jobs) {
		t.Fatal("cleanup changed task history", history, err)
	}
	// A restored file is indexed normally after its former record was forgotten.
	if _, err := a.db.Exec("DELETE FROM meta WHERE key IN ('promotion:13','deletion:14:journal')"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(s.Source+"-offline", s.Source); err != nil {
		t.Fatal(err)
	}
	makeFLAC(t, a, s, "Artist/Album/1.flac", "Restored", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	if restored, err := a.sourceRel("Artist/Album/1.flac"); err != nil || !restored.Present {
		t.Fatal("restored file was not rediscovered", err)
	}
}
