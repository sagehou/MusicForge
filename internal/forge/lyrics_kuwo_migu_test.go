package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func additionalLyricsResponse(t *testing.T, w http.ResponseWriter, r *http.Request, winner string, source Source) bool {
	t.Helper()
	switch r.URL.Host + r.URL.Path {
	case "www.kuwo.cn/search/searchMusicBykeyWord":
		if r.URL.Query().Get("rn") != "8" || r.URL.Query().Get("encoding") != "utf8" {
			t.Error("unbounded or non-Unicode Kuwo search")
		}
		if winner != "kuwo" {
			lyricsJSON(w, `{"abslist":[]}`)
			return true
		}
		respond(w, 200, map[string]any{"abslist": []any{map[string]any{"MUSICRID": "MUSIC_123", "SONGNAME": source.Title, "ARTIST": source.Artist, "ALBUM": source.Album, "DURATION": source.Duration}}})
	case "www.kuwo.cn/openapi/v1/www/lyric/getlyric":
		if r.URL.Query().Get("musicId") != "123" {
			t.Error("Kuwo lost recording ID")
		}
		lyricsJSON(w, `{"code":200,"data":{"lrclist":[{"time":"0.000","lineLyric":"//"},{"time":"1.000","lineLyric":"夜の歌"},{"time":"NaN","lineLyric":"invalid"}]}}`)
	case "app.u.nf.migu.cn/pc/resource/song/item/search/v1.0":
		if r.URL.Query().Get("pageSize") != "8" || r.URL.Query().Get("pageNo") != "1" {
			t.Error("unbounded Migu search")
		}
		if winner != "migu" {
			lyricsJSON(w, `[]`)
			return true
		}
		respond(w, 200, []any{map[string]any{"copyrightId": "600123", "songName": source.Title, "singerList": []any{map[string]string{"name": source.Artist}}, "album": source.Album, "duration": source.Duration, "ext": map[string]string{"lrcUrl": "https://d.musicapp.migu.cn/data/oss/resource/fixture/original.lrc"}}})
	case "d.musicapp.migu.cn/data/oss/resource/fixture/original.lrc":
		_, _ = w.Write([]byte("[00:01.00]夜の歌"))
	default:
		return false
	}
	return true
}

func TestAdditionalLyricsSourcesUseRecordingMetadataAndCache(t *testing.T) {
	for _, provider := range []string{"kuwo", "migu"} {
		t.Run(provider, func(t *testing.T) {
			a, s := testApp(t)
			source := seedLyricsArtifact(t, a, s)
			calls := 0
			mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if !additionalLyricsResponse(t, w, r, provider, source) {
					t.Error("untrusted lyric URL requested", r.URL)
					w.WriteHeader(500)
				}
			})
			target, err := a.captureLyricsTarget(context.Background(), source.ID)
			if err != nil {
				t.Fatal(err)
			}
			candidates, _, _, _, err := a.cachedLyricsCandidates(context.Background(), target, provider, "", false, false)
			if err != nil || len(candidates) != 1 || !candidates[0].HighConfidence || candidates[0].MatchScore != 100 {
				t.Fatal(candidates, err)
			}
			text, _, state, err := a.chooseLyrics(context.Background(), source, candidates, lyricsBatchOptions{MinScore: 96})
			if err != nil || state != "ready" || !strings.Contains(text, "夜の歌") {
				t.Fatal(text, state, err)
			}
			// Downloaded previews use the existing bounded search cache format.
			candidates[0].text = &lyricsText{Original: text}
			if err = a.storeLyricsSearch(context.Background(), target, provider, "", candidates, false, time.Now().Unix()); err != nil {
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
				t.Error("valid cache queried a provider", r.URL)
				w.WriteHeader(500)
			})
			target, err = reopened.captureLyricsTarget(context.Background(), source.ID)
			if err != nil {
				t.Fatal(err)
			}
			cached, _, hit, _, err := reopened.cachedLyricsCandidates(context.Background(), target, provider, "", false, false)
			if err != nil || !hit || len(cached) != 1 || cached[0].remoteID != candidates[0].remoteID || cached[0].lyricsURL != candidates[0].lyricsURL || calls != 2 {
				t.Fatal(cached, hit, calls, err)
			}
			if text, _, state, err := reopened.chooseLyrics(context.Background(), source, cached, lyricsBatchOptions{MinScore: 96}); err != nil || state != "ready" || !strings.Contains(text, "夜の歌") {
				t.Fatal(text, state, err)
			}
		})
	}
}

func TestAdditionalLyricsSourcesRejectMissingDurationAndUnsafeIDs(t *testing.T) {
	for _, provider := range []string{"kuwo", "migu"} {
		t.Run(provider, func(t *testing.T) {
			a, _ := testApp(t)
			calls := 0
			mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if provider == "kuwo" {
					lyricsJSON(w, `{"abslist":[{"MUSICRID":"MUSIC_123","NAME":"Track","ARTIST":"Artist","ALBUM":"Album"},{"MUSICRID":"https://127.0.0.1/secret","NAME":"Track","ARTIST":"Artist"}]}`)
				} else {
					lyricsJSON(w, `[{"copyrightId":"600123","songName":"Track","singer":"Artist","album":"Album","ext":{"lrcUrl":"https://d.musicapp.migu.cn/data/oss/resource/fixture/original.lrc"}},{"copyrightId":"../secret","songName":"Track","singer":"Artist","lrcUrl":"http://127.0.0.1/private"}]`)
				}
			})
			source := Source{Title: "Track", Artist: "Artist", Album: "Album", Duration: 183}
			candidates, _, err := a.providerCandidates(context.Background(), provider, source, "", false)
			if err != nil || len(candidates) != 1 || candidates[0].Duration != 0 || candidates[0].HighConfidence {
				t.Fatal(candidates, err)
			}
			text, _, state, err := a.chooseLyrics(context.Background(), source, candidates, lyricsBatchOptions{MinScore: 0})
			if err != nil || text != "" || state != "needs_review" || calls != 1 {
				t.Fatal("missing duration bypassed recording checks", text, state, calls, err)
			}
			if _, err = a.candidateLyrics(context.Background(), lyricsCandidate{Provider: provider, remoteID: "https://127.0.0.1/secret"}); err == nil || calls != 1 {
				t.Fatal("unsafe recording ID used", calls, err)
			}
		})
	}
	for raw, want := range map[string]float64{`183`: 183, `"03:03.5"`: 183.5, `"00:03:03"`: 183, `null`: 0, `"NaN"`: 0, `"03:75"`: 0, `183000`: 0} {
		if got := lyricsProviderSeconds(json.RawMessage(raw)); got != want {
			t.Fatal(raw, got, want)
		}
	}
	for _, raw := range []string{"http://d.musicapp.migu.cn/data/oss/resource/a", "https://127.0.0.1/data/oss/resource/a", "https://d.musicapp.migu.cn.evil.test/data/oss/resource/a", "https://d.musicapp.migu.cn/data/oss/resource/../secret", "https://d.musicapp.migu.cn/data/oss/resource/a?token=secret", "https://d.musicapp.migu.cn:443/data/oss/resource/a", "https://d.musicapp.migu.cn/data/oss/resource/" + strings.Repeat("a", 4096)} {
		if validMiguLyricsURL(raw) {
			t.Fatal("accepted untrusted lyric URL", raw)
		}
	}
}
