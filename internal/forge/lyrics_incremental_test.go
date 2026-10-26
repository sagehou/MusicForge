package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadyLyricsAreSkippedWithoutStorageReadsAfterRestart(t *testing.T) {
	for _, kind := range []string{"source", "instrumental", "petitlyrics"} {
		t.Run(kind, func(t *testing.T) {
			a, settings := testApp(t)
			seedLyricsArtifact(t, a, settings)
			request := lyricsSession(t, a)
			if kind == "instrumental" {
				token, index := previewInstrumentalLyrics(t, a, request)
				if w := request("PUT", "/api/library/1/lyrics", map[string]any{"token": token, "index": index}); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
			} else if kind == "petitlyrics" {
				target, err := a.captureLyricsTarget(context.Background(), 1)
				if err != nil {
					t.Fatal(err)
				}
				candidate := lyricsCandidate{Provider: "petitlyrics", remoteID: "123", Title: "Track", Artist: "Artist", Album: "Album", Duration: 183, text: &lyricsText{Original: "[00:00.870]テストの歌\n"}}
				group := a.newLyricsChoices(target, "petitlyrics", "Track Artist", []lyricsCandidate{candidate}, true, time.Now().Unix())
				choice := map[string]any{"token": group.Token, "index": 0}
				if w := request("POST", "/api/library/1/lyrics/preview", choice); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				if w := request("PUT", "/api/library/1/lyrics", choice); w.Code != 200 || !strings.Contains(w.Body.String(), `"additional_provider":"petitlyrics"`) {
					t.Fatal(w.Code, w.Body.String())
				}
			} else {
				if err := os.WriteFile(filepath.Join(settings.Source, "Album/01.lrc"), []byte("[00:01.00]爱这风云"), 0644); err != nil {
					t.Fatal(err)
				}
				if w := request("POST", "/api/lyrics/search", map[string]any{"all": true}); w.Code != 202 {
					t.Fatal(w.Code, w.Body.String())
				}
				if report := waitLyrics(t, a); report.Total != 1 || report.Found != 1 {
					t.Fatal(report)
				}
			}
			before, err := a.meta("lyrics-status:1")
			if err != nil {
				t.Fatal(err)
			}
			cfg, logger, assets := a.cfg, a.logger, a.assets
			a.Close()
			// Neither volume is accessible: ordinary fill must decide entirely from
			// persisted state, even after restart and a matching-threshold change.
			for _, root := range []string{settings.Source, settings.Output} {
				if err = os.Rename(root, root+"-offline"); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := New(cfg, logger, "test", assets)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			mockLyricsServices(t, reopened, func(w http.ResponseWriter, r *http.Request) {
				t.Error("ready lyrics contacted a provider", r.URL)
				w.WriteHeader(500)
			})
			request = lyricsSession(t, reopened)
			if w := request("PUT", "/api/lyrics/options", map[string]any{"auto_search": true, "min_score": 100}); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, body := range []map[string]any{{"all": true}, {"ids": []int64{1}}, {"all": true}} {
				w := request("POST", "/api/lyrics/search", body)
				var result struct{ Selected, Skipped int }
				if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Selected != 0 || result.Skipped != 1 {
					t.Fatal("ready track entered a batch", w.Code, w.Body.String())
				}
				if report := waitLyrics(t, reopened); report.State != "complete" || report.Total != 0 || report.Processed != 0 || report.Found != 0 || report.Skipped != 1 {
					t.Fatal("skips counted as processed tracks", report)
				}
			}
			reopened.scheduleLyrics(settings)
			if after, err := reopened.meta("lyrics-status:1"); err != nil || after != before {
				t.Fatal("skipping rewrote the successful check", after, err)
			}
		})
	}
}

func TestLyricsFillProcessesOnlyPendingArtifactsAndAllowsExplicitRefresh(t *testing.T) {
	a, settings := testApp(t)
	source := seedLyricsArtifact(t, a, settings)
	request := lyricsSession(t, a)
	local := filepath.Join(settings.Source, "Album/01.lrc")
	if err := os.WriteFile(local, []byte("[00:01.00]原来歌词"), 0644); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/lyrics/search", map[string]any{"all": true}); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if report := waitLyrics(t, a); report.Found != 1 {
		t.Fatal(report)
	}
	before, _ := a.meta("lyrics-status:1")
	if _, err := a.db.Exec(`INSERT INTO sources(id,rel,hash,built_hash,artist,album,title,duration,output,output_present) VALUES(2,'Album/02.flac','second','second','Artist','Album','Second',183,'Album/02.opus',1)`); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settings.Output, "Album/02.opus"), []byte("second audio"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("INSERT INTO managed(path,source_id,kind,signature) VALUES('Album/02.opus',2,'audio',?)", digest("second audio")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settings.Source, "Album/02.lrc"), []byte("[00:01.00]新增歌词"), 0644); err != nil {
		t.Fatal(err)
	}
	// A changed source sidecar must not replace the existing choice on fill.
	if err := os.WriteFile(local, []byte("[00:01.00]新的源歌词"), 0644); err != nil {
		t.Fatal(err)
	}
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		t.Error("source lyrics queried a provider", r.URL)
		w.WriteHeader(500)
	})
	w := request("POST", "/api/lyrics/search", map[string]any{"all": true})
	var result struct{ Selected, Skipped int }
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Selected != 1 || result.Skipped != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	if report := waitLyrics(t, a); report.Total != 1 || report.Found != 1 || report.Skipped != 1 {
		t.Fatal(report)
	}
	if after, _ := a.meta("lyrics-status:1"); after != before {
		t.Fatal("incremental batch rewrote an existing result")
	}
	path := filepath.Join(settings.Output, lyricsRel(source.Output))
	if text, err := os.ReadFile(path); err != nil || string(text) != "[00:01.00]原来歌词\n" {
		t.Fatal("ordinary fill replaced an existing choice", string(text), err)
	}
	if w = request("POST", "/api/lyrics/search", map[string]any{"ids": []int64{1}, "refresh": true}); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if report := waitLyrics(t, a); report.Total != 1 || report.Found != 1 || report.Skipped != 0 {
		t.Fatal("explicit refresh was skipped", report)
	}
	if text, err := os.ReadFile(path); err != nil || string(text) != "[00:01.00]新的源歌词\n" {
		t.Fatal("explicit refresh ignored source changes", string(text), err)
	}
	// A rebuilt audio signature invalidates the old ready result and re-enters
	// planning; the already valid LRC remains usable without another lookup.
	if _, err := a.db.Exec("UPDATE managed SET signature='rebuilt' WHERE source_id=1 AND kind='audio'"); err != nil {
		t.Fatal(err)
	}
	w = request("POST", "/api/lyrics/search", map[string]any{"all": true})
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Selected != 1 || result.Skipped != 1 {
		t.Fatal("changed audio did not re-enter planning", w.Code, w.Body.String())
	}
	if report := waitLyrics(t, a); report.Total != 1 || report.Found != 1 {
		t.Fatal(report)
	}
	if raw, _ := a.meta("lyrics-status:1"); !json.Valid([]byte(raw)) || raw == before {
		t.Fatal("rebuilt audio retained stale status", raw)
	}
}

func TestOwnedLyricsWithoutStatusWinBeforeSourceLookup(t *testing.T) {
	a, settings := testApp(t)
	source := seedLyricsArtifact(t, a, settings)
	ctx := context.Background()
	if err := a.writeLyricsFile(ctx, settings, source, "[00:01.00]手选歌词", "netease"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settings.Source, "Album/01.lrc"), []byte("invalid source sidecar"), 0644); err != nil {
		t.Fatal(err)
	}
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		t.Error("existing LRC contacted a provider", r.URL)
		w.WriteHeader(500)
	})
	if _, started := a.startLyrics(settings, []Source{source}, lyricsBatchOptions{}, false); !started {
		t.Fatal("batch did not start")
	}
	if report := waitLyrics(t, a); report.Found != 1 || report.Failed != 0 {
		t.Fatal("existing lyrics did not win before source validation", report)
	}
	_, text, present, err := a.readLyricsFile(ctx, settings, lyricsRel(source.Output))
	if err != nil || !present || text != "[00:01.00]手选歌词\n" {
		t.Fatal("existing lyrics were replaced", text, err)
	}
}

func TestLyricsFillRetryPolicy(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		state     string
		age       time.Duration
		automatic bool
		want      bool
	}{
		{"ready", 30 * 24 * time.Hour, false, false},
		{"ready", 30 * 24 * time.Hour, true, false},
		{"failed", time.Hour - time.Second, true, false},
		{"failed", time.Hour, true, true},
		{"not_found", 7*24*time.Hour - time.Second, true, false},
		{"not_found", 7 * 24 * time.Hour, true, true},
		{"instrumental", time.Hour, true, false},
		{"needs_review", time.Hour, true, false},
		{"needs_review", time.Second, false, true},
		{"failed", time.Second, false, true},
	} {
		source := Source{Output: "Album/01.opus", OutputPresent: true, Hash: "same", BuiltHash: "same", Lyrics: &LyricsStatus{State: tc.state, Checked: now.Add(-tc.age).Unix()}}
		if got := lyricsNeedsFilling(source, tc.automatic, now); got != tc.want {
			t.Fatalf("state=%s age=%s automatic=%v: got %v want %v", tc.state, tc.age, tc.automatic, got, tc.want)
		}
		source.Hash = "waiting for rebuild"
		if lyricsNeedsFilling(source, false, now) {
			t.Fatal("unfinished rebuild entered a fill batch")
		}
	}
}
