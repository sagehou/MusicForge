package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestSimplifyLRCKeepsTimingMetadataAndOtherLanguages(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"phrases and other characters", "[ti:愛這風雲]\n[offset:-250]\n[00:01.23][02:03:456]乾坤一擲，愛這風雲 ＬＯＶＥ café 🙂!\n", "[ti:愛這風雲]\n[offset:-250]\n[00:01.23][02:03:456]乾坤一掷，爱这风云 ＬＯＶＥ café 🙂!\n"},
		{"mixed Chinese scripts", "[00:01.00]爱這風雲\n", "[00:01.00]爱这风云\n"},
		{"less common traditional characters", "[00:01]憂鬱\n", "[00:01]忧郁\n"},
		{"simplified", "[00:01]爱这风云\n", "[00:01]爱这风云\n"},
		{"English", "[ti:愛]\n[00:01]Love ＬＯＶＥ!\n", "[ti:愛]\n[00:01]Love ＬＯＶＥ!\n"},
		{"other alphabets", "[00:01]사랑 café Привет مرحبا\n", "[00:01]사랑 café Привет مرحبا\n"},
		{"Japanese across lines", "[00:01]愛這風雲\n[00:02]愛してる\n", "[00:01]愛這風雲\n[00:02]愛してる\n"},
		{"Korean with ideographs", "[00:01]愛 사랑\n", "[00:01]愛 사랑\n"},
		{"shared Han", "[00:01]山水天地\n", "[00:01]山水天地\n"},
		{"instrumental", instrumentalLRC, instrumentalLRC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := simplifyLRC(tc.text); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLyricsSimplificationIsOutputOnlyAndPreservesMatchingAndConflicts(t *testing.T) {
	a, settings := testApp(t)
	source := seedLyricsArtifact(t, a, settings)
	ctx := context.Background()
	original, converted := "[00:01.23][02:03.456]愛這風雲 Love!\n", "[00:01.23][02:03.456]爱这风云 Love!\n"
	if err := a.writeLyricsFile(ctx, settings, source, original, "unison"); err != nil {
		t.Fatal(err)
	}
	status := LyricsStatus{State: "ready", Output: source.Output, Signature: digest("registered audio fixture"),
		Path: lyricsRel(source.Output), Provider: "unison", Checked: 123, Attempts: []lyricsAttempt{{Provider: "unison", State: "ready"}}}
	raw, _ := json.Marshal(status)
	if err := a.setMeta("lyrics-status:1", string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("DELETE FROM meta WHERE key='lyrics-script:1'"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settings.Source, "Album/01.lrc"), []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(settings.Source, settings.Source+"-offline"); err != nil {
		t.Fatal(err)
	}
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		t.Error("conversion contacted a provider", r.URL)
		w.WriteHeader(500)
	})
	request := lyricsSession(t, a)
	if w := request("PUT", "/api/lyrics/options", map[string]any{"providers": []string{}, "min_score": 100}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, flag := range []string{"refresh", "replace_traditional", "inspect_only"} {
		if w := request("POST", "/api/lyrics/search", map[string]any{"all": true, "simplify_traditional": true, flag: true}); w.Code != 400 {
			t.Fatal("conversion accepted another mode", flag, w.Code, w.Body.String())
		}
	}
	if w := request("POST", "/api/lyrics/search", map[string]any{"all": true, "simplify_traditional": true}); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if report := waitLyrics(t, a); report.Mode != "simplify" || report.Total != 1 || report.Replaced != 1 || report.Unchanged != 0 || report.Failed != 0 {
		t.Fatal(report)
	}
	entry, text, present, err := a.readLyricsFile(ctx, settings, lyricsRel(source.Output))
	if err != nil || !present || text != converted || entry.Provider != "unison" {
		t.Fatal(entry, text, err)
	}
	rows := []Source{source}
	if err = a.annotateLyrics(ctx, rows); err != nil || rows[0].LyricsScript != "simplified" {
		t.Fatal(rows, err)
	}
	w := request("GET", "/api/library/1/history?kind=lyrics", nil)
	var history editHistory
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &history) != nil || len(history.Entries) != 2 || history.Entries[1].Hash != digest(original) {
		t.Fatal("conversion did not retain the original", w.Code, w.Body.String())
	}
	path := filepath.Join(settings.Output, lyricsRel(source.Output))
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if w = request("POST", "/api/lyrics/search", map[string]any{"ids": []int64{source.ID}, "simplify_traditional": true}); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if report := waitLyrics(t, a); report.Replaced != 0 || report.Unchanged != 1 || report.Failed != 0 {
		t.Fatal("repeated conversion was not a no-op", report)
	}
	if after, err := os.Stat(path); err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("no-op rewrote the LRC", err)
	}
	w = request("GET", "/api/maintenance/progress", nil)
	var progress struct{ Reports []maintenanceProgress }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &progress) != nil || len(progress.Reports) == 0 || progress.Reports[0].Updated != 0 || progress.Reports[0].Skipped != 1 {
		t.Fatal("task progress counted a skipped track as changed", w.Code, w.Body.String())
	}
	modified := "[00:01.23]手動改過的愛\n"
	if err = os.WriteFile(path, []byte(modified), 0644); err != nil {
		t.Fatal(err)
	}
	for _, unregistered := range []bool{false, true} {
		if unregistered {
			if _, err = a.db.Exec("DELETE FROM meta WHERE key=?", lyricsFileKey(lyricsRel(source.Output))); err != nil {
				t.Fatal(err)
			}
		}
		if w = request("POST", "/api/lyrics/search", map[string]any{"all": true, "simplify_traditional": true}); w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		if report := waitLyrics(t, a); report.Failed != 1 || report.Replaced != 0 || report.Unchanged != 0 {
			t.Fatal("conflict was not reported", report)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != modified {
			t.Fatal("conversion overwrote conflicting lyrics", err)
		}
	}
	if after, err := a.meta("lyrics-status:1"); err != nil || after != string(raw) {
		t.Fatal("conversion changed matching provenance or check time", after, err)
	}
	if data, err := os.ReadFile(filepath.Join(settings.Source+"-offline", "Album/01.lrc")); err != nil || string(data) != original {
		t.Fatal("conversion changed source lyrics", err)
	}
	if data, err := os.ReadFile(filepath.Join(settings.Output, source.Output)); err != nil || string(data) != "registered audio fixture" {
		t.Fatal("conversion changed audio", err)
	}
	if err = os.Rename(settings.Output, settings.Output+"-offline"); err != nil {
		t.Fatal(err)
	}
	if w = request("POST", "/api/lyrics/search", map[string]any{"all": true}); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if report := waitLyrics(t, a); report.Total != 0 || report.Skipped != 1 {
		t.Fatal("ordinary filling did not skip the preserved ready result", report)
	}
}

func TestLyricsSimplificationResumesAfterRestartWithoutSourceStorage(t *testing.T) {
	a, settings := testApp(t)
	source := seedLyricsArtifact(t, a, settings)
	if err := a.writeLyricsFile(context.Background(), settings, source, "[00:01]愛這風雲\n", "source"); err != nil {
		t.Fatal(err)
	}
	a.lyricsBatch = &lyricsBatch{Version: 1, Source: settings.Source, Output: settings.Output,
		Options: lyricsBatchOptions{SimplifyTraditional: true}, Remaining: []lyricsBatchTrack{{ID: source.ID, Path: source.Rel}},
		Report: lyricsReport{State: "running", Mode: "simplify", Total: 2, Processed: 1, Replaced: 1}}
	if err := a.saveLyricsBatch(); err != nil {
		t.Fatal(err)
	}
	cfg, logger, assets := a.cfg, a.logger, a.assets
	a.Close()
	if err := os.Rename(settings.Source, settings.Source+"-offline"); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(cfg, logger, "test", assets)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	mockLyricsServices(t, reopened, func(w http.ResponseWriter, r *http.Request) {
		t.Error("resumed conversion contacted a provider", r.URL)
		w.WriteHeader(500)
	})
	if report := reopened.lyricsReport; report.State != "stopped" || report.Mode != "simplify" || !report.Resumable || report.Remaining != 1 {
		t.Fatal("conversion mode was not restored", report)
	}
	request := lyricsSession(t, reopened)
	if w := request("POST", "/api/lyrics/resume", nil); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if report := waitLyrics(t, reopened); report.Mode != "simplify" || report.Processed != 2 || report.Replaced != 2 || report.Failed != 0 || report.Remaining != 0 {
		t.Fatal("continuation did not convert the remaining lyrics", report)
	}
	if _, text, present, err := reopened.readLyricsFile(context.Background(), settings, lyricsRel(source.Output)); err != nil || !present || text != "[00:01]爱这风云\n" {
		t.Fatal(text, err)
	}
}
