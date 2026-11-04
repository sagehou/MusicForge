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

const communityTTMLFixture = `<tt xmlns="http://www.w3.org/ns/ttml" xmlns:ttm="http://www.w3.org/ns/ttml#metadata"><body><div><p begin="0:01.250" end="0:03.000"><span begin="0:01.250">夜</span><span begin="0:02.000">の歌</span><span ttm:role="x-translation">翻译</span><span ttm:role="x-roman">yoru</span><span ttm:role="x-bg">和声</span></p><p begin="65.5s">Hello <span>world</span></p></div></body></tt>`

func TestCommunityTTMLPreservesRealLineTimes(t *testing.T) {
	text, err := ttmlLRC(communityTTMLFixture)
	if err != nil || text != "[00:01.250]夜の歌\n[01:05.500]Hello world\n" {
		t.Fatal(text, err)
	}
	for _, raw := range []string{
		"plain text", `<tt><body><p>no time</p></body></tt>`,
		`<tt><body><p begin="1s"><p begin="2s">nested</p></p></body></tt>`,
		`<tt><body begin="10s"><p begin="1s">relative</p></body></tt>`,
		`<tt><body timeContainer="seq"><p begin="1s">relative</p></body></tt>`,
		`<tt timeBase="clock"><body><p begin="1s">unsupported clock</p></body></tt>`,
		`<tt><body><p begin="3s" end="2s">reversed</p></body></tt>`,
		`<tt><body><p begin="00:01:02:12">frames</p></body></tt>`,
		`<tt xmlns="urn:other"><body><p begin="1s">wrong namespace</p></body></tt>`,
		`<!DOCTYPE tt [<!ENTITY words "text">]><tt><body><p begin="1s">&words;</p></body></tt>`,
		communityTTMLFixture + communityTTMLFixture,
		strings.Repeat("x", maxLyricsBytes+1),
		`<tt><body><p begin="1s">` + strings.Repeat("<span>", 65) + "deep" + strings.Repeat("</span>", 65) + `</p></body></tt>`,
	} {
		if _, err := ttmlLRC(raw); err == nil {
			t.Fatal("accepted untimed, unsupported or malformed TTML", raw[:min(len(raw), 100)])
		}
	}
	for raw, expected := range map[string]int64{"0:01.250": 1250, "00:01:05.5": 65500, "1250ms": 1250, "1.25s": 1250} {
		if actual, err := ttmlTimestamp(raw); err != nil || actual != expected {
			t.Fatal(raw, actual, err)
		}
	}
	for _, raw := range []string{"", "1", "NaN", "Inf", "-1s", "+1s", "1e3s", "0x1p2", "00:65.1", "1:02s", "90000s", "00::01", "1t"} {
		if _, err := ttmlTimestamp(raw); err == nil {
			t.Fatal("accepted invalid timestamp", raw)
		}
	}
}

func TestCommunitySourcesCacheRestartPreviewAndSave(t *testing.T) {
	for _, provider := range []string{"amll", "unison"} {
		t.Run(provider, func(t *testing.T) {
			a, settings := testApp(t)
			source := seedLyricsArtifact(t, a, settings)
			request := lyricsSession(t, a)
			if w := request("PUT", "/api/lyrics/options", map[string]any{"providers": []string{provider}}); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			calls := 0
			mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch r.URL.Host + r.URL.Path {
				case "api.amll.dev/v1/lrclib/search":
					if r.URL.Query().Get("pageSize") != "8" || !strings.Contains(r.URL.Query().Get("q"), source.Title) {
						t.Error("AMLL lost bounded metadata search")
					}
					respond(w, 200, []any{map[string]any{"id": 123, "trackName": source.Title, "artistName": source.Artist, "albumName": source.Album, "duration": source.Duration, "syncedLyrics": "[00:01.250]夜の歌\n"}, map[string]any{"id": 124, "trackName": "Plain", "artistName": source.Artist, "syncedLyrics": "no timestamps"}})
				case "unison.boidu.dev/lyrics/search":
					if r.URL.Query().Get("limit") != "8" {
						t.Error("unbounded Unison search")
					}
					respond(w, 200, map[string]any{"success": true, "data": []any{map[string]any{"id": 123, "song": source.Title, "artist": source.Artist, "album": source.Album, "duration": source.Duration, "format": "ttml", "syncType": "richsync"}, map[string]any{"id": 124, "song": "Plain", "artist": source.Artist, "format": "plain", "syncType": "plain"}}})
				case "unison.boidu.dev/lyrics/123":
					respond(w, 200, map[string]any{"success": true, "data": map[string]any{"id": 123, "song": source.Title, "artist": source.Artist, "album": source.Album, "format": "ttml", "syncType": "richsync", "lyrics": communityTTMLFixture}})
				default:
					t.Error("unexpected community endpoint", r.URL)
					w.WriteHeader(500)
				}
			})
			target, err := a.captureLyricsTarget(context.Background(), source.ID)
			if err != nil {
				t.Fatal(err)
			}
			candidates, _, _, _, err := a.cachedLyricsCandidates(context.Background(), target, provider, "", false, false)
			if err != nil || len(candidates) != 1 {
				t.Fatal(candidates, err)
			}
			text, _, state, err := a.chooseLyrics(context.Background(), source, candidates, lyricsBatchOptions{MinScore: 0})
			if err != nil || provider == "unison" && (state != "ready" || !strings.Contains(text, "夜の歌")) ||
				provider == "amll" && (state != "needs_review" || text != "" || candidates[0].Duration != 0 || candidates[0].HighConfidence || candidates[0].DurationDelta != nil) {
				t.Fatal("community recording safeguards lost", text, state, candidates, err)
			}
			if err := a.storeLyricsSearch(context.Background(), target, provider, "", candidates, false, time.Now().Unix()); err != nil {
				t.Fatal(err)
			}
			if calls != map[string]int{"amll": 1, "unison": 2}[provider] {
				t.Fatal("unexpected request count", calls)
			}
			cfg, logger, assets := a.cfg, a.logger, a.assets
			a.Close()
			reopened, err := New(cfg, logger, "test", assets)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			mockLyricsServices(t, reopened, func(w http.ResponseWriter, r *http.Request) {
				t.Error("restart lost valid community preview", r.URL)
				w.WriteHeader(500)
			})
			request = lyricsSession(t, reopened)
			w := request("GET", "/api/library/1/lyrics/candidates", nil)
			var restored struct {
				Groups []lyricsCandidateGroup `json:"groups"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &restored) != nil || len(restored.Groups) != 1 || restored.Groups[0].Provider != provider {
				t.Fatal(w.Code, w.Body.String())
			}
			choice := map[string]any{"token": restored.Groups[0].Token, "index": 0}
			if w := request("POST", "/api/library/1/lyrics/preview", choice); w.Code != 200 || !strings.Contains(w.Body.String(), "夜の歌") {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := request("PUT", "/api/library/1/lyrics", choice); w.Code != 200 || !strings.Contains(w.Body.String(), `"community_provider":"`+provider+`"`) {
				t.Fatal(w.Code, w.Body.String())
			}
			if data, err := os.ReadFile(filepath.Join(settings.Output, lyricsRel(source.Output))); err != nil || !strings.Contains(string(data), "[00:01.250]夜の歌") {
				t.Fatal(string(data), err)
			}
		})
	}
}

func TestUnisonRejectsChangedRecordingAndUntimedContent(t *testing.T) {
	for _, change := range []string{"id", "title", "artist", "duration", "plain", "bad_lrc", "untimed_ttml", "missing_success"} {
		t.Run(change, func(t *testing.T) {
			a, _ := testApp(t)
			mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
				record := map[string]any{"id": 123, "song": "Track", "artist": "Artist", "duration": 183, "format": "lrc", "syncType": "linesync", "lyrics": "[00:01.00]歌"}
				switch change {
				case "id":
					record["id"] = 456
				case "title":
					record["song"] = "Other"
				case "artist":
					record["artist"] = "Other"
				case "duration":
					record["duration"] = 240
				case "plain":
					record["format"], record["syncType"] = "plain", "plain"
				case "bad_lrc":
					record["lyrics"] = "no timing"
				case "untimed_ttml":
					record["format"], record["lyrics"] = "ttml", "<tt><body><p>no timing</p></body></tt>"
				}
				respond(w, 200, map[string]any{"success": change != "missing_success", "data": record})
			})
			text, err := a.candidateLyrics(context.Background(), lyricsCandidate{Provider: "unison", remoteID: "123", Title: "Track", Artist: "Artist", Duration: 183})
			if text.Original != "" || err == nil && text.diagnostic == nil {
				t.Fatal("unsafe community lyric accepted", text, err)
			}
		})
	}
}
