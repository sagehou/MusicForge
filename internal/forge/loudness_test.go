package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestLoudnessRemuxPreservesAudioAndReadyLyrics(t *testing.T) {
	for _, codec := range []string{"mp3", "opus"} {
		t.Run(codec, func(t *testing.T) {
			a, s := testApp(t)
			s.Encoding.Codec = codec
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			path := makeFLAC(t, a, s, "Artist/Album/01.flac", "Gain track", false)
			sourceHash, err := fileHash(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			source, err := a.sourceRel("Artist/Album/01.flac")
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(s.Output, source.Output)
			decode := func() []byte {
				raw, err := exec.CommandContext(context.Background(), a.cfg.FFmpeg, "-nostdin", "-v", "error", "-i", output, "-map", "0:a:0", "-map_metadata", "-1", "-c:a", "pcm_s16le", "-f", "wav", "pipe:1").Output()
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			before := decode()
			if err = a.writeLyricsFile(context.Background(), s, source, "[00:00.00]手动歌词", "source"); err != nil {
				t.Fatal(err)
			}
			var signature string
			if err = a.reads.QueryRow("SELECT signature FROM managed WHERE path=?", source.Output).Scan(&signature); err != nil {
				t.Fatal(err)
			}
			status := LyricsStatus{State: "ready", Output: source.Output, Signature: signature, Path: lyricsRel(source.Output), Provider: "source", Checked: 1234567890}
			raw, _ := json.Marshal(status)
			if err = a.setMeta("lyrics-status:"+strconv.FormatInt(source.ID, 10), string(raw)); err != nil {
				t.Fatal(err)
			}
			warning, _ := json.Marshal(RepairStatus{State: "imported", PCMVerified: false, Diagnostics: "test damage annotation"})
			if err = a.setMeta(repairArtifactKey(source.ID, signature), string(warning)); err != nil {
				t.Fatal(err)
			}
			if _, err = a.db.Exec("UPDATE sources SET error='retained source read error' WHERE id=?", source.ID); err != nil {
				t.Fatal(err)
			}
			tracks, album, err := a.analyzeAlbum(context.Background(), []Source{source})
			if err != nil || len(tracks) != 1 {
				t.Fatal(err, tracks)
			}
			updated, err := a.writeOutputGain(context.Background(), tracks[0], gainTags(codec, tracks[0].Loudness, album))
			if err != nil || !updated {
				t.Fatal(updated, err)
			}
			if after := decode(); !bytes.Equal(before, after) {
				t.Fatal("tag maintenance changed decoded audio")
			}
			if hash, err := fileHash(context.Background(), path); err != nil || hash != sourceHash {
				t.Fatal("source changed", err)
			}
			probe, err := a.probe(context.Background(), output)
			if err != nil {
				t.Fatal(err)
			}
			tags := probe.tags()
			if codec == "mp3" && tags["replaygain_track_gain"] != "-6.00 dB" {
				t.Fatal("replaced manual ReplayGain", tags)
			}
			if codec == "opus" && (tags["r128_track_gain"] == "" || tags["r128_album_gain"] == "") {
				t.Fatal("missing Opus R128", tags)
			}
			list, err := a.sourcesWithStatusContext(context.Background())
			if err == nil {
				err = a.annotateLyrics(context.Background(), list)
			}
			if err != nil || len(list) != 1 || list[0].OutputGain == nil || list[0].Lyrics == nil || list[0].Lyrics.Checked != 1234567890 || list[0].Lyrics.Provider != "source" || !list[0].PossiblyDamaged || list[0].Error != "retained source read error" {
				t.Fatal("lost ready/manual/damage metadata", list, err)
			}
			// A cached repeat is a database-only no-op even with output offline.
			if err = os.Rename(s.Output, s.Output+".offline"); err != nil {
				t.Fatal(err)
			}
			w := lyricsSession(t, a)("POST", "/api/maintenance/loudness", map[string]any{"all": true})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			a.loudnessMu.Lock()
			report := *a.loudnessReport
			a.loudnessMu.Unlock()
			if report.State != "complete" || report.Total != 0 || report.Skipped != 1 {
				t.Fatal(report)
			}
		})
	}
}

func TestLoudnessJournalRetainsChoicesAcrossPublicationWindows(t *testing.T) {
	for _, promoted := range []bool{false, true} {
		t.Run(strconv.FormatBool(promoted), func(t *testing.T) {
			a, s := testApp(t)
			source := seedLyricsArtifact(t, a, s)
			previous := digest("registered audio fixture")
			next := digest("new audio signature")
			status := LyricsStatus{State: "ready", Output: source.Output, Signature: previous, Provider: "source", Checked: 77}
			raw, _ := json.Marshal(status)
			if err := a.setMeta("lyrics-status:1", string(raw)); err != nil {
				t.Fatal(err)
			}
			pending := outputGainJournal{ID: 1, Previous: previous, Gain: outputGain{Version: 1, Path: source.Output, Signature: next, Track: "1", Album: "2", Checked: time.Now().Unix()}}
			raw, _ = json.Marshal(pending)
			if err := a.setMeta("output-gain-pending:1", string(raw)); err != nil {
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
			rawText, err := a.meta("lyrics-status:1")
			if err != nil || json.Unmarshal([]byte(rawText), &status) != nil {
				t.Fatal(err)
			}
			want := previous
			if promoted {
				want = next
			}
			if status.Signature != want || status.Checked != 77 || status.Provider != "source" {
				t.Fatal("lost manual choice", status)
			}
		})
	}
}

func TestLoudnessScheduleWaitCanBeStopped(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	o := defaultMaintenanceOptions()
	o.ScheduleEnabled = true
	o.Start = time.Now().UTC().Add(time.Hour).Format("15:04")
	o.End = time.Now().UTC().Add(2 * time.Hour).Format("15:04")
	raw, _ := json.Marshal(o)
	if err := a.setMeta(maintenanceOptionsKey, string(raw)); err != nil {
		t.Fatal(err)
	}
	request := lyricsSession(t, a)
	w := request("POST", "/api/maintenance/loudness", map[string]any{"all": true})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("DELETE", "/api/maintenance/loudness", map[string]any{}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.loudnessWG.Wait()
	a.loudnessMu.Lock()
	defer a.loudnessMu.Unlock()
	if a.loudnessReport.State != "stopped" || a.loudnessBusy.Load() {
		t.Fatal(a.loudnessReport)
	}
}
