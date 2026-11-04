package forge

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOutputTagsPreserveAudioLyricsAndRebuildCorrections(t *testing.T) {
	for _, codec := range []string{"mp3", "opus"} {
		t.Run(codec, func(t *testing.T) {
			a, s := testApp(t)
			s.Encoding.Codec = codec
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			input := makeFLAC(t, a, s, "Artist/Album/01.flac", "Original", false)
			if err := a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			source, err := a.sourceRel("Artist/Album/01.flac")
			if err != nil {
				t.Fatal(err)
			}
			var signature string
			if err = a.reads.QueryRow("SELECT signature FROM managed WHERE path=?", source.Output).Scan(&signature); err != nil {
				t.Fatal(err)
			}
			if err = a.writeLyricsFile(context.Background(), s, source, "[00:00.00]Manual", "source"); err != nil {
				t.Fatal(err)
			}
			status := LyricsStatus{State: "ready", Output: source.Output, Signature: signature, Provider: "source", Checked: 77}
			raw, _ := json.Marshal(status)
			if err = a.setMeta("lyrics-status:"+strconv.FormatInt(source.ID, 10), string(raw)); err != nil {
				t.Fatal(err)
			}
			decode := func() []byte {
				t.Helper()
				raw, err := exec.Command(a.cfg.FFmpeg, "-nostdin", "-v", "error", "-i", filepath.Join(s.Output, source.Output), "-map", "0:a:0", "-map_metadata", "-1", "-c:a", "pcm_s16le", "-f", "wav", "pipe:1").Output()
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			before := decode()
			request := lyricsSession(t, a)
			w := request("POST", "/api/library/tags/preview", map[string]any{"ids": []int64{source.ID}, "tags": map[string]string{"title": "Corrected", "artist": "Corrected Artist", "album": "", "album_artist": "Album Artist", "track": "2/12", "disc": "1/2"}})
			var preview tagPreview
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || len(preview.Tracks) != 1 || preview.Tracks[0].Before["title"] != "Original" {
				t.Fatal(w.Code, w.Body.String())
			}
			if !bytes.Equal(before, decode()) {
				t.Fatal("preview changed output")
			}
			w = request("POST", "/api/maintenance/tags", map[string]any{"token": preview.Token})
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			a.toolWG.Wait()
			a.toolMu.Lock()
			report := *a.toolReports["tags"]
			a.toolMu.Unlock()
			if report.Failed != 0 || report.Updated != 1 {
				t.Fatal(report)
			}
			if !bytes.Equal(before, decode()) {
				t.Fatal("tag correction changed decoded audio")
			}
			if hash, err := fileHash(context.Background(), input); err != nil || hash != source.Hash {
				t.Fatal("source changed", err)
			}
			list, err := a.sourcesWithStatusContext(context.Background())
			if err == nil {
				err = a.annotateLyrics(context.Background(), list)
			}
			if err != nil || list[0].Title != "Original" || list[0].OutputTags["title"] != "Corrected" || list[0].Lyrics == nil || list[0].Lyrics.Checked != 77 || list[0].OutputGain != nil {
				t.Fatal(list, err)
			}
			if w = request("POST", "/api/maintenance/tags", map[string]any{"token": preview.Token}); w.Code != 409 {
				t.Fatal("reused preview token", w.Code)
			}
			// A real encoding-profile rebuild must retain the optional correction.
			s.Encoding.Mode, s.Encoding.Bitrate = "cbr", 128
			if err = a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			if _, err = a.enqueue("convert", "tags:rebuild", BuildRequest{ID: source.ID, Hash: source.Hash, Profile: s.Encoding}, true); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			probe, err := a.probe(context.Background(), filepath.Join(s.Output, source.Output))
			if err != nil || probe.tags()["title"] != "Corrected" || probe.tags()["artist"] != "Corrected Artist" || probe.tags()["album"] != "" || probe.tags()["album_artist"] != "Album Artist" || probe.tags()["track"] != "2/12" || probe.tags()["disc"] != "1/2" {
				t.Fatal(probe.tags(), err)
			}
		})
	}
}

func TestOutputTagValidationAndPublicationRecovery(t *testing.T) {
	for _, tags := range []map[string]string{{"path": "bad"}, {"title": "bad\nline"}, {"track": "0"}, {"disc": "3/2"}, {"track": "1.5"}} {
		if validateOutputTags(tags) == nil {
			t.Fatal("accepted", tags)
		}
	}
	for _, promoted := range []bool{false, true} {
		t.Run(strconv.FormatBool(promoted), func(t *testing.T) {
			a, s := testApp(t)
			source := seedLyricsArtifact(t, a, s)
			next := digest("new tagged output")
			override := outputTagOverride{Version: 1, SourceRoot: s.Source, OutputRoot: s.Output, Rel: source.Rel, SourceHash: source.BuiltHash, Tags: map[string]string{"title": "Restored"}}
			raw, _ := json.Marshal(outputTagPending{ID: source.ID, Path: source.Output, Signature: next, Override: override})
			if err := a.setMeta("output-tags-pending:1", string(raw)); err != nil {
				t.Fatal(err)
			}
			if promoted {
				if _, err := a.db.Exec("UPDATE managed SET signature=? WHERE kind='audio'", next); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.recoverOutputGains(context.Background()); err != nil {
				t.Fatal(err)
			}
			changes, err := a.outputTagChanges(context.Background(), s, source, source.BuiltHash)
			if err != nil || promoted != (changes["title"] == "Restored") {
				t.Fatal(changes, err)
			}
			changes, err = a.outputTagChanges(context.Background(), s, source, digest("changed source"))
			if err != nil || len(changes) != 0 {
				t.Fatal("applied corrections to different source content", changes, err)
			}
		})
	}
}

func TestEditHistoryRestoresLyricsAndBoundsSnapshots(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	request := lyricsSession(t, a)
	for i := 0; i < 12; i++ {
		w := request("GET", "/api/library/1/lyrics/edit", nil)
		var edit struct{ Token string }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &edit) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		w = request("PUT", "/api/library/1/lyrics/edit", map[string]any{"token": edit.Token, "text": "[00:01]Revision " + strconv.Itoa(i)})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := request("GET", "/api/library/1/history?kind=lyrics", nil)
	var history editHistory
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &history) != nil || len(history.Entries) != 10 {
		t.Fatal(w.Code, w.Body.String())
	}
	files, err := os.ReadDir(filepath.Join(a.cfg.ConfigDir, "edit-history"))
	if err != nil || len(files) != 12 {
		t.Fatal("operation snapshots must outlive the ten-entry editor history", len(files), err)
	}
	w = request("GET", "/api/library/1/history?kind=lyrics&revision="+history.Entries[9].Hash, nil)
	var revision struct{ Text string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &revision) != nil || !strings.Contains(revision.Text, "Revision 2") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "/api/library/1/lyrics/edit", nil)
	var edit struct{ Token string }
	_ = json.Unmarshal(w.Body.Bytes(), &edit)
	w = request("PUT", "/api/library/1/lyrics/edit", map[string]any{"token": edit.Token, "text": revision.Text})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("GET", "/api/library/1/history?kind=artwork&revision="+history.Entries[9].Hash, nil); w.Code != 404 {
		t.Fatal("cross-kind history leak", w.Code)
	}
	if w = request("GET", "/api/library/1/history?kind=lyrics&revision=../../config", nil); w.Code != 404 {
		t.Fatal("accepted invalid revision", w.Code)
	}
}

func TestPlaylistMappingPreservesOrderAndRefreshesWithoutFileIO(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	request := lyricsSession(t, a)
	if err := os.Rename(s.Source, s.Source+".offline"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(s.Output, s.Output+".offline"); err != nil {
		t.Fatal(err)
	}
	input := playlistInput{Name: "My playlist", Base: "Album", Text: "\ufeff#EXTM3U\n01.flac\n" + s.Source + "/Album/01.flac\nmissing.flac\n../../outside.flac\nhttps://example.test/audio\n01.flac\n"}
	w := request("POST", "/api/library/playlists/preview", input)
	var preview playlistResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.Ready != 3 || len(preview.Missing) != 3 || strings.Count(preview.Content, "./Album/01.opus") != 3 {
		t.Fatal(w.Code, w.Body.String())
	}
	if list, _ := a.savedPlaylists(context.Background(), s); len(list) != 0 {
		t.Fatal("preview persisted a playlist")
	}
	w = request("POST", "/api/playlists", input)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || !validDigest(preview.ID) {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := a.db.Exec("UPDATE sources SET output='Album/01.mp3' WHERE id=?", source.ID); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "/api/playlists/"+preview.ID, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "./Album/01.mp3") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("DELETE", "/api/playlists/"+preview.ID, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestSpectralFingerprintsIgnoreGainAndRejectDifferentAudio(t *testing.T) {
	samples := func(frequency, gain float64) []byte {
		pcm := make([]byte, 8000*4*3)
		for i := 0; i < len(pcm)/4; i++ {
			value := float32(gain * math.Sin(2*math.Pi*frequency*float64(i)/8000))
			binary.LittleEndian.PutUint32(pcm[i*4:], math.Float32bits(value))
		}
		return pcm
	}
	a, err := spectralFingerprint(samples(440, .8))
	if err != nil {
		t.Fatal(err)
	}
	b, err := spectralFingerprint(samples(440, .1))
	if err != nil {
		t.Fatal(err)
	}
	c, err := spectralFingerprint(samples(880, .8))
	if err != nil {
		t.Fatal(err)
	}
	if !similarFingerprints(audioFingerprint{Duration: 3, Codes: a}, audioFingerprint{Duration: 3, Codes: b}) {
		t.Fatal("gain change lost acoustic identity")
	}
	if similarFingerprints(audioFingerprint{Duration: 3, Codes: a}, audioFingerprint{Duration: 3, Codes: c}) {
		t.Fatal("different tones matched")
	}
	if similarFingerprints(audioFingerprint{Duration: 3, Codes: a}, audioFingerprint{Duration: 10, Codes: b}) {
		t.Fatal("different durations matched")
	}
	if _, err = spectralFingerprint(make([]byte, 8000*4*3)); err == nil {
		t.Fatal("silence became a fingerprint")
	}
}

func TestFingerprintsCompareCodecsAndReuseOfflineCache(t *testing.T) {
	a, s := testApp(t)
	makeFLAC(t, a, s, "Artist/Album/01.flac", "One", false)
	makeFLAC(t, a, s, "Artist/Album/02.flac", "Two", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	one, _ := a.sourceRel("Artist/Album/01.flac")
	two, _ := a.sourceRel("Artist/Album/02.flac")
	s.Encoding.Codec = "mp3"
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	if _, err := a.enqueue("convert", "fingerprint:mp3", BuildRequest{ID: two.ID, Hash: two.Hash, Profile: s.Encoding}, true); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	two, _ = a.source(two.ID)
	first, fresh, err := a.fingerprintOutput(context.Background(), one)
	if err != nil || !fresh {
		t.Fatal(first, fresh, err)
	}
	second, _, err := a.fingerprintOutput(context.Background(), two)
	if err != nil || !similarFingerprints(first, second) {
		t.Fatal("identical audio across Opus and MP3 did not match", err, first, second)
	}
	if err = os.Rename(s.Output, s.Output+".offline"); err != nil {
		t.Fatal(err)
	}
	if cached, fresh, err := a.fingerprintOutput(context.Background(), one); err != nil || fresh || !similarFingerprints(first, cached) {
		t.Fatal("cache required file I/O", fresh, err)
	}
}
