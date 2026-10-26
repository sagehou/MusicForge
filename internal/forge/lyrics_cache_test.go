package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLyricsSearchRestoresPreviewsAfterRestart(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	previewTestLyrics(t, a, lyricsSession(t, a))
	cfg, logger, assets := a.cfg, a.logger, a.assets
	a.Close()
	reopened, err := New(cfg, logger, "test", assets)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	mockLyricsServices(t, reopened, func(w http.ResponseWriter, r *http.Request) {
		t.Error("restoring or previewing a saved result contacted a provider", r.URL)
		w.WriteHeader(500)
	})
	request := lyricsSession(t, reopened)
	w := request("GET", "/api/library/1/lyrics/candidates", nil)
	var result struct {
		Query  string                 `json:"query"`
		Groups []lyricsCandidateGroup `json:"groups"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Query != "別名 Artist" || len(result.Groups) != 1 || !result.Groups[0].Cached {
		t.Fatal(w.Code, w.Body.String())
	}
	group := result.Groups[0]
	body := map[string]any{"token": group.Token, "index": 0}
	if w = request("PUT", "/api/library/1/lyrics", body); w.Code != 409 {
		t.Fatal("restored data bypassed preview", w.Code, w.Body.String())
	}
	if w = request("POST", "/api/library/1/lyrics/preview", body); w.Code != 200 || !strings.Contains(w.Body.String(), "Original") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("POST", "/api/library/1/lyrics/candidates", map[string]any{"provider": "netease", "query": "別名 Artist"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cached":true`) {
		t.Fatal("ordinary search did not reuse saved results", w.Code, w.Body.String())
	}
	if w = request("PUT", "/api/library/1/lyrics", body); w.Code != 200 {
		t.Fatal("restored candidate could not be saved", w.Code, w.Body.String())
	}
	var count int
	if err = reopened.reads.QueryRow("SELECT count(*) FROM meta WHERE key LIKE 'lyrics-search:1:%'").Scan(&count); err != nil || count != 0 {
		t.Fatal("saved lyrics retained obsolete searches", count, err)
	}
	w = request("GET", "/api/library/1/lyrics/candidates", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"groups":[]`) {
		t.Fatal("changed lyrics did not invalidate saved results", w.Code, w.Body.String())
	}
}

func TestLyricsCachedNamesRecomputeAcrossRestartWithoutNewRequests(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	if _, err := a.db.Exec("UPDATE sources SET title='让爱随风',artist='新声歌手 / 客席歌手',album='摇滚乐' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	target, err := a.captureLyricsTarget(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	// The existing schema-2/version-1 record stores complete artist credits.
	// Simulate a saved low score from before Chinese normalization was enabled.
	candidate := lyricsCandidate{Title: "讓愛隨風", Artist: "Novel Voice / Guest Voice", Album: "搖滾樂", Duration: 183.25,
		MatchScore: 49, artistAliases: []string{"新声歌手 / 客席歌手"}, text: &lyricsText{Original: "[00:01.00]让爱随风\n"}}
	if err = a.storeLyricsSearch(context.Background(), target, "netease", "让爱随风", []lyricsCandidate{candidate}, false, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	cfg, logger, assets := a.cfg, a.logger, a.assets
	a.Close()
	reopened, err := New(cfg, logger, "test", assets)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	mockLyricsServices(t, reopened, func(w http.ResponseWriter, r *http.Request) {
		t.Error("rescoring saved metadata contacted a provider", r.URL)
		w.WriteHeader(500)
	})
	request := lyricsSession(t, reopened)
	w := request("GET", "/api/library/1/lyrics/candidates", nil)
	var result struct {
		Groups []lyricsCandidateGroup `json:"groups"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Groups) != 1 || len(result.Groups[0].Candidates) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	group := result.Groups[0]
	if c := group.Candidates[0]; !group.Cached || c.MatchScore != 99 || !c.IdentityMatch || !c.HighConfidence || c.Title != candidate.Title || c.Artist != candidate.Artist {
		t.Fatal("cached names were not rescored without rewriting display metadata", c)
	}
	body := map[string]any{"token": group.Token, "index": 0}
	if w = request("PUT", "/api/library/1/lyrics", body); w.Code != 409 {
		t.Fatal("improved matching bypassed explicit preview", w.Code, w.Body.String())
	}
	if w = request("POST", "/api/library/1/lyrics/preview", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("PUT", "/api/library/1/lyrics", body); w.Code != 200 {
		t.Fatal("restored alias selection could not be saved", w.Code, w.Body.String())
	}
}

func TestLyricsSearchReuseRefreshAndInvalidation(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	request := lyricsSession(t, a)
	calls := 0
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/search" {
			t.Error("unexpected endpoint", r.URL)
		}
		lyricsJSON(w, `[{"trackName":"Track","artistName":"Artist","albumName":"Album","duration":183,"syncedLyrics":"[00:01.00]爱这风云"}]`)
	})
	search := func(refresh bool) {
		t.Helper()
		w := request("POST", "/api/library/1/lyrics/candidates", map[string]any{"provider": "lrclib", "refresh": refresh})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	search(false)
	search(false)
	if calls != 1 {
		t.Fatal("repeated search downloaded again", calls)
	}
	search(true)
	if calls != 2 {
		t.Fatal("explicit refresh did not search again", calls)
	}
	target, err := a.captureLyricsTarget(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	key := lyricsSearchKey(target, "lrclib", "")
	raw, err := a.meta(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"source", "audio", "output", "roots", "tags", "lyrics"} {
		changed := target
		switch change {
		case "source":
			changed.Source.Hash = "new"
		case "audio":
			changed.Signature = "new"
		case "output":
			changed.Source.Output = "moved.opus"
		case "roots":
			changed.Settings.Source = "/another-source"
		case "tags":
			changed.Source.Duration++
		case "lyrics":
			changed.FilePresent, changed.FileSignature = true, "new"
		}
		if _, ok := decodeLyricsSearch(raw, changed); ok {
			t.Fatal("reused stale results after", change)
		}
	}
	var record lyricsSearchRecord
	if err = json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatal(err)
	}
	record.Searched = time.Now().Add(-lyricsSearchTTL - time.Second).Unix()
	expired, _ := json.Marshal(record)
	if err = a.setMeta(key, string(expired)); err != nil {
		t.Fatal(err)
	}
	search(false)
	if calls != 3 {
		t.Fatal("expired search was reused", calls)
	}
}

func TestBackgroundLyricsKeepsHighConfidencePendingCandidates(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Host + r.URL.Path {
		case "lrclib.net/api/get":
			w.WriteHeader(404)
		case "lrclib.net/api/search":
			lyricsJSON(w, `[{"trackName":"Track","artistName":"Artist","albumName":"Album","duration":183,"syncedLyrics":"[00:01.00]爱你"},{"trackName":"Track","artistName":"Artist","albumName":"Album","duration":183,"syncedLyrics":"[00:01.00]First"},{"trackName":"Track","artistName":"Artist","albumName":"Album","duration":183,"syncedLyrics":"[00:01.00]Second"},{"trackName":"Track","artistName":"Artist","albumName":"Album","duration":183,"syncedLyrics":"[00:01.00]Third"}]`)
		default:
			w.WriteHeader(503)
		}
	})
	status := a.maintainLyrics(context.Background(), s, 1, lyricsBatchOptions{MinScore: defaultLyricsMinScore})
	if status.State != "needs_review" {
		t.Fatal(status)
	}
	w := lyricsSession(t, a)("GET", "/api/library/1/lyrics/candidates", nil)
	var restored struct {
		Groups []lyricsCandidateGroup `json:"groups"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &restored) != nil || len(restored.Groups) != 1 || len(restored.Groups[0].Candidates) != 4 {
		t.Fatal(w.Code, w.Body.String())
	}
	if c := restored.Groups[0].Candidates[0]; !c.HighConfidence || c.MatchScore != 100 || c.Script != "simplified" {
		t.Fatal("lost recommendation in pending search", c)
	}
}

func TestLyricsRecommendationUsesMetadataAndScriptWithoutGuessing(t *testing.T) {
	a, _ := testApp(t)
	source := Source{Title: "Song", Artist: "Artist", Album: "Album", Duration: 183}
	base := lyricsCandidate{Title: "Song", Artist: "Artist", Album: "Album", Duration: 183}
	simplified, traditional, wrongVersion := base, base, base
	simplified.Duration = 184.2
	simplified.text = &lyricsText{Original: "[00:01.00]爱这风云\n"}
	traditional.text = &lyricsText{Original: "[00:01.00]愛這風雲\n"}
	wrongVersion.Title = "Song (Live)"
	candidates := []lyricsCandidate{wrongVersion, traditional, simplified}
	rankLyricsCandidates(candidates, source)
	if candidates[0].Script != "traditional" || !candidates[0].HighConfidence || candidates[0].MatchScore != 100 || candidates[2].HighConfidence {
		t.Fatal(candidates)
	}
	if data, _, state, err := a.chooseLyrics(context.Background(), source, candidates[:2], lyricsBatchOptions{MinScore: defaultLyricsMinScore}); err != nil || state != "ready" || data != traditional.text.Original {
		t.Fatal("did not prefer higher-scoring equivalent lyrics", data, state, err)
	}
	traditional.text = &lyricsText{Original: "[00:02.00]愛這風雲\n"}
	if _, _, state, _ := a.chooseLyrics(context.Background(), source, []lyricsCandidate{simplified, traditional}, lyricsBatchOptions{MinScore: defaultLyricsMinScore}); state != "needs_review" {
		t.Fatal("script preference erased conflicting timing", state)
	}
	for _, text := range []string{"[ti:爱这风云]\n[00:01.00]hello", "[00:01.00]爱してる", "[00:01.00]하늘과爱", "[00:01.00]你好"} {
		if script := lyricsScript(text); script != "unknown" {
			t.Fatal("guessed Chinese script", text, script)
		}
	}
}

func TestAutomaticLyricsUsesQQWithTraditionalFallback(t *testing.T) {
	for _, available := range []bool{true, false} {
		a, _ := testApp(t)
		mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Host + r.URL.Path {
			case "lrclib.net/api/get":
				lyricsJSON(w, `{"trackName":"Song","artistName":"Artist","albumName":"Album","duration":183,"syncedLyrics":"[00:01.00]愛這風雲"}`)
			case "u.y.qq.com/cgi-bin/musicu.fcg":
				if !available {
					w.WriteHeader(503)
					return
				}
				var body struct {
					Request struct {
						Method string `json:"method"`
					} `json:"request"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Request.Method == "GetSession" {
					lyricsJSON(w, `{"code":0,"request":{"code":0,"data":{"session":{"uid":"0","sid":"guest","userip":"127.0.0.1"}}}}`)
				} else {
					lyricsJSON(w, `{"code":0,"request":{"code":0,"data":{"body":{"song":{"list":[{"id":123,"mid":"mid","title":"Song","interval":183,"album":{"name":"Album"},"singer":[{"name":"Artist"}]}]}}}}}`)
				}
			case "c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg":
				lyricsJSON(w, `{"retcode":0,"lyric":"[00:01.00]爱这风云"}`)
			default:
				w.WriteHeader(503)
			}
		})
		target := lyricsTarget{Source: Source{Title: "Song", Artist: "Artist", Album: "Album", Duration: 183}}
		text, provider, state, _, err := a.searchOnlineLyrics(context.Background(), target, lyricsBatchOptions{MinScore: defaultLyricsMinScore})
		want := "traditional"
		if available {
			want = "simplified"
		}
		if err != nil || state != "ready" || lyricsScript(text) != want || available && provider != "qq" || !available && provider != "lrclib" {
			t.Fatal(available, text, provider, state, err)
		}
	}
}

func TestLyricsSearchCacheIsBoundedAndPrunesExpiredRecords(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	ctx := context.Background()
	target, err := a.captureLyricsTarget(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 514; i++ {
		searched := time.Now().Unix()
		if i == 513 {
			searched = time.Now().Add(-8 * 24 * time.Hour).Unix()
		}
		value := map[string]any{"searched": searched}
		if i == 0 {
			value["searched"] = time.Now().Add(-time.Hour).Unix()
			value["candidates"] = []any{map[string]any{"candidate": map[string]any{"high_confidence": true}}}
		}
		raw, _ := json.Marshal(value)
		if _, err = tx.Exec("INSERT INTO meta(key,value) VALUES(?,?)", lyricsSearchPrefix+"99:"+strconv.Itoa(i), string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = a.storeLyricsSearch(ctx, target, "lrclib", "", nil, false, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = a.reads.QueryRow("SELECT count(*) FROM meta WHERE key LIKE 'lyrics-search:%'").Scan(&count); err != nil || count != 512 {
		t.Fatal("search count not bounded", count, err)
	}
	if _, err = a.meta(lyricsSearchKey(target, "lrclib", "")); err != nil {
		t.Fatal("new search was evicted", err)
	}
	if _, err = a.meta("lyrics-search:99:0"); err != nil {
		t.Fatal("new empty results evicted high-confidence pending candidates", err)
	}
	if err = a.reads.QueryRow("SELECT count(*) FROM meta WHERE key='lyrics-search:99:513'").Scan(&count); err != nil || count != 0 {
		t.Fatal("expired entry retained", count, err)
	}
}
