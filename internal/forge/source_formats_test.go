package forge

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func makeAudioSource(t *testing.T, a *App, s Settings, rel, title, codec string, embedded bool) string {
	t.Helper()
	path := filepath.Join(s.Source, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	args := []string{"-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=2"}
	if embedded {
		cover := filepath.Join(t.TempDir(), "cover.jpg")
		writeCover(t, cover)
		args = append(args, "-i", cover, "-map", "0:a:0", "-map", "1:v:0", "-c:v", "copy", "-disposition:v", "attached_pic")
	}
	args = append(args, "-ac", "2", "-c:a", codec, "-strict", "-2", "-metadata", "artist=Mixed Artist", "-metadata", "album=Mixed Album", "-metadata", "title="+title, "-metadata", "track=2/9", "-metadata", "disc=3/4", "-metadata", "REPLAYGAIN_TRACK_GAIN=-6.00 dB")
	if codec == "vorbis" {
		args = append(args, "-f", "ogg")
	}
	if strings.EqualFold(filepath.Ext(path), ".mka") {
		args = append(args, "-f", "matroska")
	}
	args = append(args, path)
	if _, err := runTool(context.Background(), a.cfg.FFmpeg, args...); err != nil {
		t.Fatal(err)
	}
	stable := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, stable, stable); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMixedAudioSourcesBuildAndRemainIncremental(t *testing.T) {
	fixtures := []struct {
		rel, codec string
		embedded   bool
	}{
		{"01.flac", "flac", false},
		{"02.MP3", "libmp3lame", true},
		{"03-aac.m4a", "aac", true},
		{"04-alac.m4a", "alac", false},
		{"05.mp4", "aac", false},
		{"06.aac", "aac", false},
		{"07.wav", "pcm_s24le", false},
		{"08.aiff", "pcm_s24be", false},
		{"09.oga", "vorbis", false},
		{"10.opus", "libopus", false},
		{"11.ogg", "libopus", false},
		{"12.wma", "wmav2", false},
		{"13.wv", "wavpack", false},
		{"14.mka", "flac", false},
	}
	for _, codec := range []string{"opus", "mp3"} {
		t.Run(codec, func(t *testing.T) {
			a, s := testApp(t)
			s.Encoding.Codec = codec
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			expected := map[string]map[string]string{}
			for _, fixture := range fixtures {
				rel := "Artist/Album/" + fixture.rel
				path := makeAudioSource(t, a, s, rel, fixture.rel, fixture.codec, fixture.embedded)
				p, err := a.probe(context.Background(), path)
				if err != nil {
					t.Fatal(err)
				}
				expected[rel] = p.tags()
			}
			if err := os.WriteFile(filepath.Join(s.Source, "Artist/Album/notes.txt"), []byte("not a track"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			tracks, err := a.sourcesWithStatus()
			if err != nil || len(tracks) != len(fixtures) {
				t.Fatal("mixed sources were skipped", len(tracks), err)
			}
			for _, source := range tracks {
				if source.Status != "ready" || source.Output != outputRel(source.Rel, codec) || !reflect.DeepEqual(source.Metadata, expected[source.Rel]) {
					t.Fatalf("unexpected source/artifact: %+v", source)
				}
				if source.Track != 0 && source.Track != 2 || source.Disc != 0 && source.Disc != 3 {
					t.Fatalf("track/disc numbers were lost: %+v", source)
				}
				for _, key := range []string{"track", "tracknumber", "track_number"} {
					if source.Metadata[key] != "" && source.Track != 2 {
						t.Fatalf("track tag %s was not indexed: %+v", key, source)
					}
				}
				for _, key := range []string{"disc", "discnumber", "disc_number"} {
					if source.Metadata[key] != "" && source.Disc != 3 {
						t.Fatalf("disc tag %s was not indexed: %+v", key, source)
					}
				}
				hash, err := fileHash(context.Background(), filepath.Join(s.Source, source.Rel))
				if err != nil || hash != source.Hash {
					t.Fatal("source hash changed meaning", source.Rel, err)
				}
				p, err := a.probe(context.Background(), filepath.Join(s.Output, source.Output))
				if err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"artist", "album", "title", "replaygain_track_gain"} {
					if want := source.Metadata[key]; want != "" && p.tags()[key] != want {
						t.Fatalf("%s lost %s: %q", source.Rel, key, p.tags()[key])
					}
				}
				for _, stream := range p.Streams {
					if stream.Type == "video" {
						t.Fatal("artwork was duplicated in a track", source.Output)
					}
				}
			}
			if _, err = os.Stat(filepath.Join(s.Output, "Artist/Album/cover.jpg")); err != nil {
				t.Fatal("non-FLAC embedded album artwork was lost", err)
			}
			a.cfg.FFprobe = "/bin/false"
			if err = a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal("unchanged mixed library required another probe", err)
			}
			var jobs int
			if err = a.db.QueryRow("SELECT count(*) FROM jobs WHERE (kind='convert' OR dedup LIKE 'scan:prepare:%')").Scan(&jobs); err != nil || jobs != len(fixtures) {
				t.Fatal("unchanged mixed sources were re-encoded", jobs, err)
			}
		})
	}
}

func TestMixedSourceOutputConflictPreservesExistingArtifact(t *testing.T) {
	a, s := testApp(t)
	makeFLAC(t, a, s, "Artist/Album/01.flac", "Original", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	original, _ := a.sourceRel("Artist/Album/01.flac")
	artifact := filepath.Join(s.Output, original.Output)
	before, err := fileHash(context.Background(), artifact)
	if err != nil {
		t.Fatal(err)
	}
	path := makeAudioSource(t, a, s, "Artist/Album/01.mp3", "Different source", "libmp3lame", false)
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	j, err := a.claim(true)
	if err != nil {
		t.Fatal(err)
	}
	err = a.execute(context.Background(), j)
	if err == nil || !strings.Contains(err.Error(), "output path conflict") || !strings.Contains(err.Error(), original.Rel) {
		t.Fatal("mixed-source conflict was not explained", err)
	}
	if err = a.completeJob(j, "failed", 3, 0, err.Error(), 0); err != nil {
		t.Fatal(err)
	}
	conflicting, _ := a.sourceRel("Artist/Album/01.mp3")
	if conflicting.Error == "" || conflicting.Output != "" {
		t.Fatal("conflict missing from Library", conflicting)
	}
	after, err := fileHash(context.Background(), artifact)
	if err != nil || after != before {
		t.Fatal("existing artifact was overwritten", err)
	}
	if err = a.owned(original.Output, original.ID); err != nil {
		t.Fatal("existing owner was lost", err)
	}
	if err = os.Rename(path, filepath.Join(s.Source, "Artist/Album/02.mp3")); err != nil {
		t.Fatal(err)
	}
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	fixed, _ := a.source(conflicting.ID)
	if fixed.Rel != "Artist/Album/02.mp3" || fixed.Output != "Artist/Album/02.opus" || fixed.Error != "" {
		t.Fatal("renaming the conflicting source did not recover", fixed)
	}
}

func TestSourceFormatReplacementPreservesOutputAndRetries(t *testing.T) {
	for _, fixture := range []struct {
		oldExt, oldCodec, newExt, newCodec, entry string
	}{
		{"mp3", "libmp3lame", "flac", "flac", "scan"},
		{"mp3", "libmp3lame", "flac", "flac", "retry"},
		{"m4a", "aac", "flac", "flac", "rebuild"},
		{"wav", "pcm_s24le", "m4a", "alac", "missing-output"},
		{"flac", "flac", "MP3", "libmp3lame", "scan"},
		{"m4a", "alac", "wv", "wavpack", "retry"},
		{"ogg", "vorbis", "opus", "libopus", "rebuild"},
		{"aiff", "pcm_s24be", "m4a", "aac", "scan"},
	} {
		t.Run(fixture.oldExt+"-to-"+fixture.newExt+"/"+fixture.entry, func(t *testing.T) {
			entry := fixture.entry
			a, s := testApp(t)
			ctx := context.Background()
			oldRel, newRel := "Artist/Album/01."+fixture.oldExt, "Artist/Album/01."+fixture.newExt
			oldPath := makeAudioSource(t, a, s, oldRel, "Original", fixture.oldCodec, false)
			if err := a.scan(ctx, ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			original, err := a.sourceRel(oldRel)
			if err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(s.Output, original.Output)
			before, err := fileHash(ctx, artifact)
			if err != nil {
				t.Fatal(err)
			}
			// An inherited best-effort import must retain its warning on failure.
			if err = a.setMeta(repairArtifactKey(original.ID, before), `{"state":"imported","diagnostics":"Original warning"}`); err != nil {
				t.Fatal(err)
			}
			makeAudioSource(t, a, s, newRel, "Replaced", fixture.newCodec, false)
			if entry == "scan" || entry == "missing-output" {
				if err = os.Remove(oldPath); err != nil {
					t.Fatal(err)
				}
			}
			wantPresent := entry != "missing-output"
			if !wantPresent {
				if err = os.Remove(artifact); err != nil {
					t.Fatal(err)
				}
			}
			if err = a.scan(ctx, ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			j, err := a.claim(true)
			if err != nil {
				t.Fatal(err)
			}
			if entry == "retry" || entry == "rebuild" {
				// Reproduce an already failed conflict, then remove the old source.
				failure := a.execute(ctx, j)
				if failure == nil || !strings.Contains(failure.Error(), "output path conflict") {
					t.Fatal("coexisting sources did not conflict", failure)
				}
				if err = a.completeJob(j, "failed", 3, 0, failure.Error(), 0); err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(oldPath); err != nil {
					t.Fatal(err)
				}
				if entry == "retry" {
					if _, err = a.changeTasks("retry", selection{IDs: []int64{j.ID}}); err != nil {
						t.Fatal(err)
					}
				} else {
					source, readErr := a.sourceRel(newRel)
					if readErr != nil {
						t.Fatal(readErr)
					}
					if _, err = a.queueBuild(source, s.Encoding, false, true); err != nil {
						t.Fatal(err)
					}
				}
				if j, err = a.claim(true); err != nil {
					t.Fatal(err)
				}
			}
			ffmpeg := a.cfg.FFmpeg
			a.cfg.FFmpeg = "/bin/false"
			failure := a.execute(ctx, j)
			a.cfg.FFmpeg = ffmpeg
			if failure == nil || strings.Contains(failure.Error(), "output path conflict") {
				t.Fatal("replacement did not reach the encoder", failure)
			}
			if err = a.completeJob(j, "failed", 3, 0, failure.Error(), 0); err != nil {
				t.Fatal(err)
			}
			current, err := a.sourceRel(newRel)
			if err != nil || current.Output != original.Output || current.OutputPresent != wantPresent || current.BuiltHash != original.BuiltHash || current.Hash == original.Hash || current.Hash == "" || current.Error == "" {
				t.Fatal("failed format replacement lost playable output or content identity", current, err)
			}
			if wantPresent {
				if after, err := fileHash(ctx, artifact); err != nil || after != before {
					t.Fatal("failed format replacement changed playable audio", after, err)
				}
			} else if _, err = os.Stat(artifact); !os.IsNotExist(err) {
				t.Fatal("failed encoding published a missing output", err)
			}
			if err = a.owned(original.Output, current.ID); err != nil {
				t.Fatal("new source did not inherit output ownership", err)
			}
			inherited := []Source{current}
			if err = a.annotateRepairs(ctx, inherited); err != nil || inherited[0].PossiblyDamaged != wantPresent {
				t.Fatal("inherited output lost its damage flag", inherited, err)
			}
			warning, err := a.meta(repairArtifactKey(current.ID, before))
			if err != nil || !strings.Contains(warning, "Original warning") {
				t.Fatal("inherited output lost its damage warning", warning, err)
			}
			retired, err := a.source(original.ID)
			if err != nil || retired.Present || retired.Output != "" || retired.OutputPresent || retired.BuiltHash != "" {
				t.Fatal("old source still claims the output", retired, err)
			}
			if _, err = a.changeTasks("retry", selection{IDs: []int64{j.ID}}); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			rebuilt, err := a.sourceRel(newRel)
			if err != nil || rebuilt.ID != current.ID || rebuilt.BuiltHash != current.Hash || rebuilt.BuiltProfile != s.Encoding.Fingerprint() || rebuilt.Error != "" {
				t.Fatal("retry did not finish the format replacement", rebuilt, err)
			}
			p, err := a.probe(ctx, artifact)
			if err != nil || p.tags()["title"] != "Replaced" {
				t.Fatal("old output was not replaced by the validated new source", p.tags(), err)
			}
			list := []Source{rebuilt}
			if err = a.annotateRepairs(ctx, list); err != nil || list[0].PossiblyDamaged {
				t.Fatal("strict rebuild retained the inherited damage warning", list, err)
			}
			// Old delete/upgrade work must not remove the successor's artifact.
			if err = a.deleteExpired([]int64{original.ID}); err != nil {
				t.Fatal(err)
			}
			if err = a.finishUpgrade(ctx, UpgradeRequest{New: []string{newRel}, Old: []string{oldRel}}); err != nil {
				t.Fatal(err)
			}
			if err = a.owned(rebuilt.Output, rebuilt.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInvalidMixedAudioAndVideoDoNotExpireKnownSources(t *testing.T) {
	a, s := testApp(t)
	old := makeAudioSource(t, a, s, "Retained/01.mp3", "Retained", "libmp3lame", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	known, _ := a.sourceRel("Retained/01.mp3")
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(s.Source, "broken.m4a")
	if err := os.WriteFile(broken, []byte("invalid audio"), 0600); err != nil {
		t.Fatal(err)
	}
	cover := filepath.Join(t.TempDir(), "cover.jpg")
	writeCover(t, cover)
	video := filepath.Join(s.Source, "video.mp4")
	if _, err := runTool(context.Background(), a.cfg.FFmpeg, "-nostdin", "-v", "error", "-i", cover, "-f", "lavfi", "-i", "sine=duration=2", "-map", "1:a:0", "-map", "0:v:0", "-c:a", "aac", "-c:v", "copy", "-disposition:v", "0", video); err != nil {
		t.Fatal(err)
	}
	stable := time.Now().Add(-time.Minute)
	for _, path := range []string{broken, video} {
		if err := os.Chtimes(path, stable, stable); err != nil {
			t.Fatal(err)
		}
	}
	makeAudioSource(t, a, s, "Healthy/01.m4a", "Healthy", "alac", false)
	if err := a.scan(context.Background(), ScanRequest{}); err == nil || !strings.Contains(err.Error(), `Source: "broken.m4a"`) || !strings.Contains(err.Error(), `Source: "video.mp4"`) || !strings.Contains(err.Error(), "ffprobe failed:") || !strings.Contains(err.Error(), "not a valid audio-only source") {
		t.Fatal("scan diagnostics omitted invalid source paths or causes", err)
	}
	for _, rel := range []string{"broken.m4a", "video.mp4"} {
		source, err := a.sourceRel(rel)
		if err != nil || source.Error == "" || source.Hash != "" {
			t.Fatal("invalid source not exposed", rel, source, err)
		}
	}
	retained, _ := a.source(known.ID)
	if !retained.Present || !retained.OutputPresent {
		t.Fatal("incomplete scan expired a known source", retained)
	}
	drain(t, a, true)
	healthy, _ := a.sourceRel("Healthy/01.m4a")
	if !healthy.OutputPresent {
		t.Fatal("invalid source blocked healthy conversion", healthy)
	}
}
