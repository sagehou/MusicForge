package forge

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRepairPCMAllowsOnlyBoundedCompleteSamples(t *testing.T) {
	pcm := &flacPCM{Rate: 44100, Channels: 2, Bits: 16, Samples: 4}
	content := make([]byte, pcm.byteCount())
	pcm.MD5 = md5.Sum(content)
	for _, tc := range []struct {
		name string
		data []byte
		verified bool
		failed bool
	}{
		{"verified", content, true, false},
		{"short", content[:8], false, false},
		{"empty", nil, false, true},
		{"partial", content[:7], false, true},
		{"extra", append(append([]byte{}, content...), 0), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			var report repairPCM
			err := pcm.copyRepairPCM(&output, bytes.NewReader(tc.data), &report)
			if (err != nil) != tc.failed || err == nil && report.Verified != tc.verified || int64(output.Len()) > pcm.byteCount() {
				t.Fatal("invalid bounded PCM result", report, err)
			}
		})
	}
}

func TestRepairReservationUsesRunningJobsNotWorkerSlot(t *testing.T) {
	for _, kind := range []string{"convert", "prepare"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := testApp(t)
			var running int64
			var err error
			if kind == "convert" {
				running, err = a.enqueue("convert", "convert:occupied", BuildRequest{ID: 101}, false)
			} else {
				running, err = a.enqueue("scan", "scan:prepare:occupied", ScanRequest{Dirs: []string{"Occupied"}}, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.db.Exec("UPDATE jobs SET state='running' WHERE id=?", running); err != nil {
				t.Fatal(err)
			}
			pending, err := a.enqueue("convert", "convert:waiting", BuildRequest{ID: 102}, false)
			if err != nil {
				t.Fatal(err)
			}
			a.repairRunning = true
			a.jobsMu.Lock()
			_, err = a.claimWithRepairCapacity(true, 2)
			a.jobsMu.Unlock()
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("repair plus running job exceeded capacity", err)
			}
			if _, err = a.db.Exec("UPDATE jobs SET state='success' WHERE id=?", running); err != nil {
				t.Fatal(err)
			}
			a.jobsMu.Lock()
			job, err := a.claimWithRepairCapacity(true, 2)
			a.jobsMu.Unlock()
			if err != nil || job.ID != pending {
				t.Fatal("released capacity did not become available", job, err)
			}
		})
	}
}

func failedRepairSource(t *testing.T, a *App, s Settings) (Source, string, []byte) {
	t.Helper()
	path := makeFLAC(t, a, s, "Artist/Album/01.flac", "Repair preview", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	source, _ := a.sourceRel("Artist/Album/01.flac")
	output, err := os.ReadFile(filepath.Join(s.Output, source.Output))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content[26] ^= 1
	if err = os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	quiet := time.Now().Add(-time.Minute)
	if err = os.Chtimes(path, quiet, quiet); err != nil {
		t.Fatal(err)
	}
	hash, err := fileHash(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec("UPDATE sources SET hash=?,size=?,mtime=?,error=? WHERE id=?", hash, info.Size(), info.ModTime().UnixNano(), "Audio decoding failed\nFLAC PCM MD5 mismatch", source.ID); err != nil {
		t.Fatal(err)
	}
	source, _ = a.source(source.ID)
	return source, path, output
}

func awaitRepair(t *testing.T, a *App, source Source) *RepairPreview {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state := a.repairSnapshot(source)
		if state != nil && state.State != "running" {
			return state
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("repair did not finish")
	return nil
}

func TestRepairPreviewDoesNotPublishOrAlterSchema2State(t *testing.T) {
	for _, codec := range []string{"opus", "mp3"} {
		t.Run(codec, func(t *testing.T) {
			a, s := testApp(t)
			s.Encoding.Codec = codec
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			source, path, output := failedRepairSource(t, a, s)
			before, _ := json.Marshal(source)
			original, _ := os.ReadFile(path)
			var jobs, managed, beforeDirty int
			_ = a.db.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs)
			_ = a.db.QueryRow("SELECT count(*) FROM managed").Scan(&managed)
			_ = a.db.QueryRow("SELECT count(*) FROM dirty_dirs").Scan(&beforeDirty)
			state, err := a.startRepair(source, s)
			if err != nil || state.State != "running" {
				t.Fatal(state, err)
			}
			state = awaitRepair(t, a, source)
			if state.State != "ready" || state.DownloadURL == "" || !strings.Contains(state.Filename, "possibly-damaged") || state.DecodedSamples <= 0 || state.PCMVerified {
				t.Fatal("invalid recovery result", state)
			}
			a.repairMu.Lock()
			previewPath := a.repairs[source.ID].path
			a.repairMu.Unlock()
			probe, err := a.probe(context.Background(), previewPath)
			if err != nil || probe.tags()["musicforge_recovery"] != "possibly_damaged" || probe.tags()["title"] != "Repair preview" {
				t.Fatal("preview lost its warning or tags", probe.tags(), err)
			}
			current, _ := a.source(source.ID)
			after, _ := json.Marshal(current)
			if !bytes.Equal(before, after) {
				t.Fatal("repair changed the source or its artifact record", string(before), string(after))
			}
			sourceBytes, _ := os.ReadFile(path)
			outputBytes, _ := os.ReadFile(filepath.Join(s.Output, source.Output))
			if !bytes.Equal(original, sourceBytes) || !bytes.Equal(output, outputBytes) {
				t.Fatal("repair replaced source or playable output")
			}
			var afterJobs, afterManaged, dirty int
			_ = a.db.QueryRow("SELECT count(*) FROM jobs").Scan(&afterJobs)
			_ = a.db.QueryRow("SELECT count(*) FROM managed").Scan(&afterManaged)
			_ = a.db.QueryRow("SELECT count(*) FROM dirty_dirs").Scan(&dirty)
			if jobs != afterJobs || managed != afterManaged || dirty != beforeDirty {
				t.Fatal("repair changed the published job or artifact formats")
			}
			list, err := a.sourcesWithStatus()
			if err != nil || len(list) != 1 || list[0].Status != "failed" || list[0].Repair == nil || !list[0].CanRepair {
				t.Fatal("library lost the damage warning", list, err)
			}
			if err = a.removeRepair(source.ID); err != nil || a.repairSnapshot(source) != nil {
				t.Fatal("preview removal failed", err)
			}
			entries, _ := os.ReadDir(a.repairDir)
			staged, _ := os.ReadDir(a.stagingDir)
			if len(entries) != 0 || len(staged) != 0 {
				t.Fatal("repair scratch files leaked")
			}
		})
	}
}

func TestRepairPreviewWithDamagedFLACFrame(t *testing.T) {
	a, s := testApp(t)
	source, path, output := failedRepairSource(t, a, s)
	raw, err := runTool(context.Background(), a.cfg.FFprobe, "-v", "error", "-select_streams", "a:0", "-show_packets", "-show_entries", "packet=pos,size", "-of", "json", path)
	if err != nil {
		t.Fatal(err)
	}
	var packets struct {
		Packets []struct { Pos string `json:"pos"`; Size string `json:"size"` } `json:"packets"`
	}
	if err = json.Unmarshal(raw, &packets); err != nil || len(packets.Packets) < 3 {
		t.Fatal("fixture needs multiple frames", err)
	}
	packet := packets.Packets[len(packets.Packets)/2]
	pos, _ := strconv.Atoi(packet.Pos)
	size, _ := strconv.Atoi(packet.Size)
	content, err := os.ReadFile(path)
	if err != nil || pos < 0 || size < 2 || pos+size > len(content) {
		t.Fatal("invalid frame fixture", err)
	}
	content[26] ^= 1 // Restore the original PCM checksum before damaging a frame.
	content[pos+size-1] ^= 1
	if err = os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := fileHash(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if _, err = a.db.Exec("UPDATE sources SET hash=?,mtime=? WHERE id=?", hash, info.ModTime().UnixNano(), source.ID); err != nil {
		t.Fatal(err)
	}
	source, _ = a.source(source.ID)
	if _, err = a.startRepair(source, s); err != nil {
		t.Fatal(err)
	}
	state := awaitRepair(t, a, source)
	if state.State != "ready" || state.DecodedSamples == 0 || state.DownloadURL == "" {
		t.Fatal("damaged-frame recovery failed", state)
	}
	current, _ := os.ReadFile(filepath.Join(s.Output, source.Output))
	if !bytes.Equal(output, current) {
		t.Fatal("damaged-frame recovery replaced the validated output")
	}
}

func TestRepairPreviewIsDisposableOnRestart(t *testing.T) {
	a, s := testApp(t)
	source, _, _ := failedRepairSource(t, a, s)
	if _, err := a.startRepair(source, s); err != nil {
		t.Fatal(err)
	}
	if state := awaitRepair(t, a, source); state.State != "ready" {
		t.Fatal(state)
	}
	a.Close()
	reopened, err := New(a.cfg, a.logger, "test", a.assets)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.repairSnapshot(source) != nil {
		t.Fatal("disposable repair survived restart")
	}
	entries, _ := os.ReadDir(reopened.repairDir)
	if len(entries) != 0 {
		t.Fatal("restart retained repair scratch files")
	}
	current, _ := reopened.source(source.ID)
	if current.Hash != source.Hash || current.Error != source.Error || current.Output != source.Output {
		t.Fatal("restart changed persistent source state", current)
	}
}

func TestRepairAPIRequiresConsentHashAndAuthenticatedDownload(t *testing.T) {
	a, s := testApp(t)
	source, _, _ := failedRepairSource(t, a, s)
	issued := httptest.NewRecorder()
	if err := a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	cookie := issued.Result().Cookies()[0]
	send := func(method, suffix, hash string, authenticated, csrf bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]string{"hash": hash})
		r := httptest.NewRequest(method, "/api/library/"+strconv.FormatInt(source.ID, 10)+"/repair"+suffix, bytes.NewReader(raw))
		if authenticated {
			r.AddCookie(cookie)
		}
		if csrf {
			r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	if w := send("POST", "", source.Hash, false, false); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("POST", "", source.Hash, true, false); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("POST", "", "stale", true, true); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("POST", "", source.Hash, true, true); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	state := awaitRepair(t, a, source)
	if state.State != "ready" {
		t.Fatal(state)
	}
	if w := send("GET", "/download", "", false, false); w.Code != 401 {
		t.Fatal("anonymous preview download", w.Code)
	}
	if w := send("GET", "/download", "", true, false); w.Code != http.StatusOK || w.Body.Len() == 0 || !strings.Contains(w.Header().Get("Content-Disposition"), "possibly-damaged") {
		t.Fatal("preview download failed", w.Code, w.Header())
	}
	if _, err := a.db.Exec("UPDATE sources SET hash='changed' WHERE id=?", source.ID); err != nil {
		t.Fatal(err)
	}
	if w := send("GET", "/download", "", true, false); w.Code != 404 {
		t.Fatal("stale preview remained downloadable", w.Code)
	}
}

func TestRepairCancellationAndInvalidPCMKeepOriginalOutput(t *testing.T) {
	for _, behavior := range []string{"empty", "partial", "hang"} {
		t.Run(behavior, func(t *testing.T) {
			a, s := testApp(t)
			source, _, output := failedRepairSource(t, a, s)
			tools := t.TempDir()
			script := "#!/bin/sh\n"
			switch behavior {
			case "empty":
				script += "exit 1\n"
			case "partial":
				script += "printf x\nexit 1\n"
			case "hang":
				script += "exec sleep 30\n"
			}
			if err := os.WriteFile(filepath.Join(tools, "flac"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
			if _, err := a.startRepair(source, s); err != nil {
				t.Fatal(err)
			}
			if behavior == "hang" {
				time.Sleep(100 * time.Millisecond)
				if err := a.removeRepair(source.ID); err != nil {
					t.Fatal(err)
				}
			} else if state := awaitRepair(t, a, source); state.State != "failed" || state.DownloadURL != "" {
				t.Fatal("invalid PCM was made downloadable", state)
			}
			a.repairWG.Wait()
			current, _ := os.ReadFile(filepath.Join(s.Output, source.Output))
			staged, _ := os.ReadDir(a.stagingDir)
			previews, _ := os.ReadDir(a.repairDir)
			if !bytes.Equal(output, current) || len(staged) != 0 || len(previews) != 0 {
				t.Fatal("failed or cancelled repair changed output or leaked files")
			}
		})
	}
}

func TestRepairShortPCMAndChangedSource(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(strconv.FormatBool(changed), func(t *testing.T) {
			a, s := testApp(t)
			source, _, output := failedRepairSource(t, a, s)
			tools := t.TempDir()
			started, release := filepath.Join(tools, "started"), filepath.Join(tools, "release")
			script := "#!/bin/sh\nprintf started > " + strconv.Quote(started) +
				"\nwhile [ ! -e " + strconv.Quote(release) + " ]; do sleep 0.02; done\n" +
				"dd if=/dev/zero bs=2 count=22050 2>/dev/null\nexit 1\n"
			if err := os.WriteFile(filepath.Join(tools, "flac"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
			if _, err := a.startRepair(source, s); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(started); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("repair decoder did not start")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if changed {
				if _, err := a.db.Exec("UPDATE sources SET hash='changed' WHERE id=?", source.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(release, nil, 0600); err != nil {
				t.Fatal(err)
			}
			state := awaitRepair(t, a, source)
			if changed {
				if state.State != "failed" || state.DownloadURL != "" || !strings.Contains(state.Diagnostics, "Source changed") {
					t.Fatal("source change was not rejected", state)
				}
			} else if state.State != "ready" || state.DecodedSamples != 22050 || state.PCMVerified || state.DecodedSamples >= state.DeclaredSamples {
				t.Fatal("short PCM was not marked as unverified", state)
			}
			if err := a.removeRepair(source.ID); err != nil {
				t.Fatal(err)
			}
			current, _ := os.ReadFile(filepath.Join(s.Output, source.Output))
			previews, _ := os.ReadDir(a.repairDir)
			if !bytes.Equal(output, current) || len(previews) != 0 {
				t.Fatal("repair changed the existing output or leaked a preview")
			}
		})
	}
}
