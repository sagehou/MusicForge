package forge

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"errors"
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
		name     string
		data     []byte
		verified bool
		failed   bool
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
	for _, kind := range []string{"convert", "prepare", "cancelled-but-active"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := testApp(t)
			var running int64
			var err error
			if kind != "prepare" {
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
			if kind == "cancelled-but-active" {
				if _, err = a.db.Exec("UPDATE jobs SET state='pending' WHERE id=?", running); err != nil {
					t.Fatal(err)
				}
				a.activeJobs[running] = func() {}
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
			delete(a.activeJobs, running)
			a.jobsMu.Lock()
			job, err := a.claimWithRepairCapacity(true, 2)
			a.jobsMu.Unlock()
			if err != nil || job.ID != pending {
				t.Fatal("released capacity did not become available", job, err)
			}
		})
	}
}

func failedRepairSource(t *testing.T, a *App, s Settings, keepOutput bool) (Source, string, []byte) {
	t.Helper()
	path := makeFLAC(t, a, s, "Artist/Album/01.flac", "Repair track", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	source, _ := a.sourceRel("Artist/Album/01.flac")
	output, err := os.ReadFile(filepath.Join(s.Output, source.Output))
	if err != nil {
		t.Fatal(err)
	}
	if !keepOutput {
		if err = a.removeOwned(s, source.Output, source.ID); err != nil {
			t.Fatal(err)
		}
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

func awaitRepair(t *testing.T, a *App, source Source) *RepairStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if state := a.repairSnapshot(source); state != nil && state.State != "running" {
			return state
		}
		list, err := a.sourcesWithStatus()
		if err != nil {
			t.Fatal(err)
		}
		for _, current := range list {
			if current.ID == source.ID && current.Repair != nil && current.Repair.State == "imported" {
				a.repairWG.Wait()
				return current.Repair
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("repair did not finish")
	return nil
}

func assertNoRepairTemps(t *testing.T, a *App) {
	t.Helper()
	var temps int
	if err := a.db.QueryRow("SELECT count(*) FROM managed WHERE kind='temp'").Scan(&temps); err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadDir(a.stagingDir)
	if err != nil || temps != 0 || len(staged) != 0 {
		t.Fatal("repair scratch files leaked", temps, staged, err)
	}
}

func TestRepairImportsNormalOutputWithPersistentDamageAnnotation(t *testing.T) {
	for _, codec := range []string{"opus", "mp3"} {
		t.Run(codec, func(t *testing.T) {
			a, s := testApp(t)
			s.Encoding.Codec = codec
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			source, path, _ := failedRepairSource(t, a, s, false)
			original, _ := os.ReadFile(path)
			var jobs int
			_ = a.db.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs)
			_, _ = a.db.Exec("DELETE FROM dirty_dirs")
			if state, err := a.startRepair(source, s); err != nil || state.State != "running" {
				t.Fatal(state, err)
			}
			state := awaitRepair(t, a, source)
			if state.State != "imported" || state.DecodedSamples <= 0 || state.PCMVerified || !strings.Contains(state.Diagnostics, source.Error) {
				t.Fatal("invalid imported recovery report", state)
			}
			history, total, err := a.repairTaskList(context.Background(), "success", 25, 0)
			if err != nil || total != 1 || len(history) != 1 || history[0].SourceID != source.ID || history[0].Progress != 1 || history[0].Log != state.Diagnostics {
				t.Fatal("successful repair missing from task history", history, total, err)
			}
			r := httptest.NewRequest("DELETE", "/", nil)
			r.SetPathValue("id", history[0].ID)
			w := httptest.NewRecorder()
			a.controlRepairTask(w, r)
			if w.Code != 200 { t.Fatal(w.Code, w.Body.String()) }
			current, err := a.source(source.ID)
			target := outputRel(source.Rel, codec)
			if err != nil || current.Output != target || !current.OutputPresent || current.BuiltHash != source.Hash || current.BuiltProfile != s.Encoding.Fingerprint() || current.Error != "" {
				t.Fatal("repair was not published as normal managed audio", current, err)
			}
			probe, err := a.probe(context.Background(), filepath.Join(s.Output, target))
			if err != nil || probe.tags()["title"] != "Repair track" || probe.tags()["musicforge_recovery"] != "" {
				t.Fatal("repair changed normal audio tags", probe.tags(), err)
			}
			sourceBytes, _ := os.ReadFile(path)
			if !bytes.Equal(original, sourceBytes) {
				t.Fatal("repair modified source bytes")
			}
			var afterJobs, version, dirty int
			_ = a.db.QueryRow("SELECT count(*) FROM jobs").Scan(&afterJobs)
			_ = a.db.QueryRow("PRAGMA user_version").Scan(&version)
			_ = a.db.QueryRow("SELECT count(*) FROM dirty_dirs WHERE path='Artist/Album'").Scan(&dirty)
			if jobs != afterJobs || version != 2 || dirty != 1 {
				t.Fatal("repair changed schema/job semantics or missed Navidrome scan notification", jobs, afterJobs, version, dirty)
			}
			list, err := a.sourcesWithStatus()
			if err != nil || len(list) != 1 || list[0].Status != "possibly_damaged" || !list[0].PossiblyDamaged || list[0].CanRepair {
				t.Fatal("library lost the damage warning", list, err)
			}
			if err = a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			list, _ = a.sourcesWithStatus()
			if !list[0].PossiblyDamaged {
				t.Fatal("scan erased imported damage annotation")
			}
			assertNoRepairTemps(t, a)
		})
	}
}

func TestRepairPreservesExistingPlayableOutput(t *testing.T) {
	a, s := testApp(t)
	source, path, output := failedRepairSource(t, a, s, true)
	original, _ := os.ReadFile(path)
	if canRepair(source) {
		t.Fatal("existing playable audio should not offer destructive repair")
	}
	if _, err := a.startRepair(source, s); err != nil {
		t.Fatal(err)
	}
	state := awaitRepair(t, a, source)
	if state.State != "failed" || !strings.Contains(state.Diagnostics, "Existing output is preserved") {
		t.Fatal("existing output was not protected", state)
	}
	current, _ := os.ReadFile(filepath.Join(s.Output, source.Output))
	sourceBytes, _ := os.ReadFile(path)
	if !bytes.Equal(output, current) || !bytes.Equal(original, sourceBytes) {
		t.Fatal("repair replaced source or existing audio")
	}
	assertNoRepairTemps(t, a)
}

func TestRepairDamagedFrameKeepsStrictConversionStrict(t *testing.T) {
	a, s := testApp(t)
	source, path, _ := failedRepairSource(t, a, s, false)
	raw, err := runTool(context.Background(), a.cfg.FFprobe, "-v", "error", "-select_streams", "a:0", "-show_packets", "-show_entries", "packet=pos,size", "-of", "json", path)
	if err != nil {
		t.Fatal(err)
	}
	var packets struct {
		Packets []struct {
			Pos  string `json:"pos"`
			Size string `json:"size"`
		} `json:"packets"`
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
	content[26] ^= 1
	valid := append([]byte{}, content...)
	content[pos+size-1] ^= 1
	if err = os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	quiet := time.Now().Add(-time.Minute)
	_ = os.Chtimes(path, quiet, quiet)
	hash, _ := fileHash(context.Background(), path)
	info, _ := os.Stat(path)
	if _, err = a.db.Exec("UPDATE sources SET hash=?,mtime=? WHERE id=?", hash, info.ModTime().UnixNano(), source.ID); err != nil {
		t.Fatal(err)
	}
	source, _ = a.source(source.ID)
	build := BuildRequest{ID: source.ID, Hash: source.Hash, Profile: s.Encoding}
	if err = a.build(context.Background(), Job{Kind: "convert"}, build); err == nil {
		t.Fatal("strict conversion accepted the corrupt FLAC frame")
	}
	if _, err = a.startRepair(source, s); err != nil {
		t.Fatal(err)
	}
	if state := awaitRepair(t, a, source); state.State != "imported" || state.DecodedSamples <= 0 {
		t.Fatal("damaged-frame recovery failed", state)
	}
	imported, _ := a.source(source.ID)
	output, _ := os.ReadFile(filepath.Join(s.Output, imported.Output))
	if err = a.build(context.Background(), Job{Kind: "convert", Attempts: 1}, build); err != nil {
		t.Fatal("old automatic retry rejected an explicitly accepted import", err)
	}
	accepted, _ := a.source(source.ID)
	if accepted.Error != "" {
		t.Fatal("old automatic retry restored a cleared source failure")
	}
	if err = a.build(context.Background(), Job{Kind: "convert"}, build); err == nil {
		t.Fatal("strict rebuild skipped validation of possibly damaged audio")
	}
	current, _ := os.ReadFile(filepath.Join(s.Output, imported.Output))
	list, _ := a.sourcesWithStatus()
	if !bytes.Equal(output, current) || !list[0].PossiblyDamaged {
		t.Fatal("failed strict rebuild removed the imported artifact or warning")
	}
	if err = os.WriteFile(path, valid, 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(path, quiet, quiet)
	if err = a.scan(context.Background(), ScanRequest{Verify: true}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	list, _ = a.sourcesWithStatus()
	if list[0].Status != "ready" || list[0].PossiblyDamaged || list[0].Repair != nil {
		t.Fatal("strict conversion of the restored source did not clear the warning", list)
	}
	assertNoRepairTemps(t, a)
}

func TestRepairAnnotationSurvivesRestartMoveAndPromotionRecovery(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		t.Run(strconv.FormatBool(recovery), func(t *testing.T) {
			a, s := testApp(t)
			source, path, _ := failedRepairSource(t, a, s, false)
			if _, err := a.startRepair(source, s); err != nil {
				t.Fatal(err)
			}
			if state := awaitRepair(t, a, source); state.State != "imported" {
				t.Fatal(state)
			}
			imported, _ := a.source(source.ID)
			artifactPath := filepath.Join(s.Output, imported.Output)
			output, _ := os.ReadFile(artifactPath)
			if recovery {
				hash, _ := fileHash(context.Background(), artifactPath)
				p := promotion{source.ID, "Artist/Album/.musicforge-crash.part", imported.Output, "", source.Hash, s.Encoding.Fingerprint(), hash}
				raw, _ := json.Marshal(p)
				if err := a.setMeta("promotion:"+strconv.FormatInt(source.ID, 10), string(raw)); err != nil {
					t.Fatal(err)
				}
				_, _ = a.db.Exec("DELETE FROM managed WHERE path=?", imported.Output)
				_, _ = a.db.Exec("UPDATE sources SET output='',built_hash='',built_profile='',output_present=0 WHERE id=?", source.ID)
			}
			a.Close()
			reopened, err := New(a.cfg, a.logger, "test", a.assets)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if err = reopened.ensureRecovery(context.Background(), s); err != nil {
				t.Fatal(err)
			}
			list, err := reopened.sourcesWithStatus()
			current, _ := os.ReadFile(artifactPath)
			if err != nil || len(list) != 1 || list[0].Status != "possibly_damaged" || !list[0].PossiblyDamaged || !bytes.Equal(output, current) {
				t.Fatal("restart/recovery lost imported audio or damage annotation", list, err)
			}
			next := "Artist/Album/02.flac"
			if err = os.Rename(path, filepath.Join(s.Source, next)); err != nil {
				t.Fatal(err)
			}
			if _, err = reopened.db.Exec("UPDATE sources SET rel=? WHERE id=?", next, source.ID); err != nil {
				t.Fatal(err)
			}
			if err = reopened.build(context.Background(), Job{Kind: "move"}, BuildRequest{ID: source.ID, Hash: source.Hash, Profile: s.Encoding, Move: true}); err != nil {
				t.Fatal(err)
			}
			list, _ = reopened.sourcesWithStatus()
			if list[0].Output != outputRel(next, s.Encoding.Codec) || !list[0].PossiblyDamaged {
				t.Fatal("normal audio move lost damage annotation", list)
			}
			assertNoRepairTemps(t, reopened)
		})
	}
}

func TestRepairAPIRequiresAuthenticationCSRFAndConsentHash(t *testing.T) {
	a, s := testApp(t)
	source, _, _ := failedRepairSource(t, a, s, false)
	issued := httptest.NewRecorder()
	if err := a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	cookie := issued.Result().Cookies()[0]
	send := func(method, hash string, authenticated, csrf bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]string{"hash": hash})
		r := httptest.NewRequest(method, "/api/library/"+strconv.FormatInt(source.ID, 10)+"/repair", bytes.NewReader(raw))
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
	if w := send("POST", source.Hash, false, false); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("POST", source.Hash, true, false); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("POST", "stale", true, true); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("POST", source.Hash, true, true); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if state := awaitRepair(t, a, source); state.State != "imported" {
		t.Fatal(state)
	}
	if w := send("DELETE", "", true, false); w.Code != 403 {
		t.Fatal("cancel bypassed CSRF", w.Code)
	}
	if w := send("DELETE", "", true, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	list, _ := a.sourcesWithStatus()
	if !list[0].PossiblyDamaged || !list[0].OutputPresent {
		t.Fatal("cancel removed an already published output or warning")
	}
}

func TestRepairCancellationAndInvalidPCMDoNotPublish(t *testing.T) {
	for _, behavior := range []string{"empty", "partial", "hang"} {
		t.Run(behavior, func(t *testing.T) {
			a, s := testApp(t)
			source, _, _ := failedRepairSource(t, a, s, false)
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
			} else if state := awaitRepair(t, a, source); state.State != "failed" {
				t.Fatal("invalid PCM was imported", state)
			}
			a.repairWG.Wait()
			history, total, err := a.repairTaskList(context.Background(), "all", 25, 0)
			want := "failed"
			if behavior == "hang" { want = "stopped" }
			if err != nil || total != 1 || len(history) != 1 || history[0].State != want || history[0].Log == "" || history[0].Stopping {
				t.Fatal("repair terminal history missing", history, total, err)
			}
			if _, err := os.Stat(filepath.Join(s.Output, outputRel(source.Rel, s.Encoding.Codec))); !os.IsNotExist(err) {
				t.Fatal("failed or cancelled repair published audio", err)
			}
			current, _ := a.source(source.ID)
			if current.Error != source.Error || current.OutputPresent {
				t.Fatal("failed repair changed persistent source state", current)
			}
			assertNoRepairTemps(t, a)
		})
	}
}

func TestRepairShortPCMAndChangedInputs(t *testing.T) {
	for _, changed := range []string{"none", "source", "encoding"} {
		t.Run(changed, func(t *testing.T) {
			a, s := testApp(t)
			source, _, _ := failedRepairSource(t, a, s, false)
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
			switch changed {
			case "source":
				if _, err := a.db.Exec("UPDATE sources SET hash='changed' WHERE id=?", source.ID); err != nil {
					t.Fatal(err)
				}
			case "encoding":
				settings := s
				settings.Encoding.Codec = "mp3"
				if err := a.saveSettings(settings); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(release, nil, 0600); err != nil {
				t.Fatal(err)
			}
			state := awaitRepair(t, a, source)
			if changed != "none" {
				if state.State != "failed" || !strings.Contains(state.Diagnostics, "changed") {
					t.Fatal("changed input was not rejected", state)
				}
			} else if state.State != "imported" || state.DecodedSamples != 22050 || state.PCMVerified || state.DecodedSamples >= state.DeclaredSamples {
				t.Fatal("short PCM was not marked as unverified", state)
			}
			a.repairWG.Wait()
			assertNoRepairTemps(t, a)
		})
	}
}
