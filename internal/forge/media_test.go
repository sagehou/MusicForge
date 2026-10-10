package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Simulate a native decoder regression while keeping the real reference
// decoder, PCM pipe, encoders and artifact validators in the conversion path.
func failNativeFLAC(t *testing.T, a *App) {
	t.Helper()
	tool := filepath.Join(t.TempDir(), "native-decoder-failure")
	script := `#!/bin/sh
previous=
for argument do
  if [ "$previous" = -i ]; then
    case "$argument" in
      *.source) printf '%s\n' '[dec:flac] Decoding error: Invalid data found when processing input' >&2; exit 183 ;;
    esac
  fi
  previous="$argument"
done
exec ffmpeg "$@"
`
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	a.cfg.FFmpeg = tool
}

func TestReferenceFLACFallbackPreservesAudioMetadataAndSingleSourceRead(t *testing.T) {
	for _, variant := range []struct{ codec, samples string }{{"opus", "s16"}, {"opus", "s32"}, {"mp3", "s16"}} {
		t.Run(variant.codec+variant.samples, func(t *testing.T) {
			a, s := testApp(t)
			s.Encoding.Codec = variant.codec
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			a.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			rel := "Artist/Album/01.flac"
			path := makeFLAC(t, a, s, rel, "Reference decoded", true)
			if variant.samples == "s32" {
				converted := filepath.Join(t.TempDir(), "24bit.flac")
				if _, err := runTool(context.Background(), "ffmpeg", "-v", "error", "-i", path, "-map", "0", "-c", "copy", "-c:a", "flac", "-sample_fmt", "s32", "-ar", "96000", converted); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(converted, path); err != nil {
					t.Fatal(err)
				}
				quiet := time.Now().Add(-time.Minute)
				if err := os.Chtimes(path, quiet, quiet); err != nil {
					t.Fatal(err)
				}
			}
			expected, err := fileHash(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(t.TempDir(), "source-reads")
			t.Setenv("MUSICFORGE_TEST_SOURCE_TRACE", trace)
			failNativeFLAC(t, a)
			if err = a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			source, err := a.sourceRel(rel)
			if err != nil || source.BuiltHash != expected || !source.OutputPresent || source.Error != "" {
				t.Fatal("fallback did not publish verified output", source, err)
			}
			p, err := a.probe(context.Background(), filepath.Join(s.Output, source.Output))
			if err != nil || p.tags()["title"] != "Reference decoded" || p.tags()["replaygain_track_gain"] != "-6.00 dB" {
				t.Fatal("fallback lost metadata or ReplayGain", p.tags(), err)
			}
			if _, err = os.Stat(filepath.Join(s.Output, "Artist/Album/cover.jpg")); err != nil {
				t.Fatal("fallback lost external artwork", err)
			}
			reads, _ := os.ReadFile(trace)
			if strings.Count(string(reads), "stage:"+rel+"\n") != 1 || !strings.Contains(logs.String(), "reference FLAC conversion completed") {
				t.Fatal("fallback reread the remote source or did not run", string(reads), logs.String())
			}
			entries, err := os.ReadDir(a.stagingDir)
			if err != nil || len(entries) != 0 || a.stagingBytes != 0 {
				t.Fatal("fallback leaked scratch files", entries, err)
			}
		})
	}
}

func TestReferenceFLACRejectsLateIntegrityFailureAndRereadsOnRetry(t *testing.T) {
	a, s := testApp(t)
	ctx := context.Background()
	rel := "Artist/Album/01.flac"
	path := makeFLAC(t, a, s, rel, "Keep old output", false)
	if err := a.scan(ctx, ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	original, _ := a.sourceRel(rel)
	output := filepath.Join(s.Output, original.Output)
	before, _ := os.ReadFile(output)
	content, err := os.ReadFile(path)
	if err != nil || len(content) < 42 || string(content[:4]) != "fLaC" || content[4]&0x7f != 0 {
		t.Fatal("fixture lacks STREAMINFO", err)
	}
	// Valid frames with a wrong STREAMINFO MD5: the reference decoder detects
	// this only after emitting the full PCM stream. ffmpeg can already exit 0.
	content[26] ^= 1
	if err = os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	quiet := time.Now().Add(-time.Minute)
	if err = os.Chtimes(path, quiet, quiet); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	a.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	trace := filepath.Join(t.TempDir(), "source-reads")
	t.Setenv("MUSICFORGE_TEST_SOURCE_TRACE", trace)
	failNativeFLAC(t, a)
	if err = a.scan(ctx, ScanRequest{Verify: true}); err != nil {
		t.Fatal(err)
	}
	var id int64
	for attempt := 1; attempt <= 3; attempt++ {
		j, claimErr := a.claim(true)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if attempt == 1 {
			id = j.ID
		} else if j.ID != id {
			t.Fatal("retry created a different job", id, j.ID)
		}
		cause := a.execute(ctx, j)
		var failure *conversionFailure
		if !errors.As(cause, &failure) || failure.stage != "decode" || !strings.Contains(cause.Error(), "MD5") {
			t.Fatal("reference integrity failure was not surfaced", cause)
		}
		state := "pending"
		if attempt == 3 {
			state = "failed"
		}
		if err = a.completeJob(j, state, attempt, 0, cause.Error(), 0); err != nil {
			t.Fatal(err)
		}
		a.logJobFinished(j, state, attempt, cause.Error(), cause)
		entries, readErr := os.ReadDir(a.stagingDir)
		if readErr != nil || len(entries) != 0 || a.stagingBytes != 0 {
			t.Fatal("failed reference conversion retained a cached source", entries, readErr)
		}
	}
	reads, _ := os.ReadFile(trace)
	if strings.Count(string(reads), "stage:"+rel+"\n") != 3 {
		t.Fatal("failed attempts did not obtain fresh source copies", string(reads))
	}
	after, _ := os.ReadFile(output)
	if !bytes.Equal(before, after) {
		t.Fatal("late decoder failure replaced the playable artifact")
	}
	items, _, err := a.taskItems(a.taskID(id), "failed", 50, 0)
	if err != nil || len(items) != 1 || items[0].Failure != "decode" || items[0].Attempts != 3 {
		t.Fatal("failed task summary is not actionable", items, err)
	}
	finished := 0
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err = json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["msg"] == "job finished" {
			finished++
			level := "WARN"
			if finished == 3 {
				level = "ERROR"
			}
			if entry["level"] != level || entry["path"] != rel || entry["error_kind"] != "decode" || entry["input_sha256"] == "" {
				t.Fatal("failure log is missing context", entry)
			}
		}
	}
	if finished != 3 {
		t.Fatal("missing retry diagnostics", finished)
	}
}

func TestReferenceFLACPipelineCancellationAndStall(t *testing.T) {
	for _, interrupt := range []bool{false, true} {
		t.Run(strconv.FormatBool(interrupt), func(t *testing.T) {
			a, s := testApp(t)
			makeFLAC(t, a, s, "Album/01.flac", "Playable", false)
			if err := a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			source, _ := a.sourceRel("Album/01.flac")
			before, _ := os.ReadFile(filepath.Join(s.Output, source.Output))
			s.Encoding.Codec = "mp3"
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			failNativeFLAC(t, a)
			toolsDir := t.TempDir()
			pidFile := filepath.Join(toolsDir, "pid")
			t.Setenv("MUSICFORGE_TEST_DECODER_PID", pidFile)
			if err := os.WriteFile(filepath.Join(toolsDir, "flac"), []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo flac-test; exit 0; fi\nprintf '%s' \"$$\" > \"$MUSICFORGE_TEST_DECODER_PID\"\nexec sleep 30\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", toolsDir+":"+os.Getenv("PATH"))
			a.cfg.SourceTimeoutSeconds = 1
			if _, err := a.queueBuild(source, s.Encoding, false, true); err != nil {
				t.Fatal(err)
			}
			j, err := a.claim(true)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- a.execute(ctx, j) }()
			deadline := time.Now().Add(3*time.Second)
			var rawPID []byte
			for time.Now().Before(deadline) {
				rawPID, _ = os.ReadFile(pidFile)
				if len(rawPID) > 0 {
					break
				}
				time.Sleep(10*time.Millisecond)
			}
			if len(rawPID) == 0 {
				t.Fatal("reference decoder did not start")
			}
			if interrupt {
				cancel()
			}
			select {
			case err = <-done:
				want := context.DeadlineExceeded
				if interrupt {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatal("pipeline did not preserve cancellation", err)
				}
			case <-time.After(3*time.Second):
				t.Fatal("reference decoder blocked cancellation or timeout")
			}
			deadline = time.Now().Add(3*time.Second)
			for len(a.sourceSlots) != 0 && time.Now().Before(deadline) {
				time.Sleep(10*time.Millisecond)
			}
			pid, _ := strconv.Atoi(string(rawPID))
			if len(a.sourceSlots) != 0 || !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				t.Fatal("pipeline leaked a process or worker slot")
			}
			after, _ := os.ReadFile(filepath.Join(s.Output, source.Output))
			if !bytes.Equal(before, after) {
				t.Fatal("interrupted fallback changed playable audio")
			}
		})
	}
}
