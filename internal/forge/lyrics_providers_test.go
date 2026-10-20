package forge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mockLyricsServices(t *testing.T, a *App, handler http.HandlerFunc) {
	t.Helper()
	a.lyricsHTTP = &http.Client{Timeout: time.Second, Transport: lyricsTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || !strings.Contains(r.Header.Get("User-Agent"), "MusicForge/") || r.Header.Get("Accept") != "application/json" {
			t.Error("lyrics request lost HTTPS or client identity")
		}
		w := httptest.NewRecorder()
		handler(w, r)
		return w.Result(), nil
	})}
}

func lyricsJSON(w http.ResponseWriter, data string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, data)
}

func TestLyricsProviderPriorityAndFallback(t *testing.T) {
	track := Source{Title: "夜に駆ける", Artist: "YOASOBI", Album: "THE BOOK", Duration: 261.12}
	for _, provider := range lyricsProviders {
		t.Run(provider, func(t *testing.T) {
			a, _ := testApp(t)
			var calls []string
			mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Host+r.URL.Path)
				switch r.URL.Host + r.URL.Path {
				case "lrclib.net/api/get":
					if provider == "lrclib" {
						lyricsJSON(w, `{"trackName":"夜に駆ける","artistName":"YOASOBI","albumName":"THE BOOK","duration":261.12,"syncedLyrics":"[00:01.00]夜の歌"}`)
					} else {
						w.Header().Set("Retry-After", "60")
						w.WriteHeader(429)
					}
				case "u.y.qq.com/cgi-bin/musicu.fcg":
					if r.Method != "POST" || r.Header.Get("Referer") != "https://y.qq.com/" {
						t.Error("QQ request headers/method missing")
					}
					var body struct {
						Comm struct {
							App string `json:"tmeAppID"`
							SID string `json:"sid"`
						} `json:"comm"`
						Request struct {
							Method string `json:"method"`
							Param  struct {
								Query string `json:"query"`
							} `json:"param"`
						} `json:"request"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal("invalid QQ request", err)
					}
					if body.Comm.App != "qqmusiclight" || r.Header.Get("Cookie") != "tmeLoginType=-1;" {
						t.Error("QQ anonymous client identity missing")
					}
					if body.Request.Method == "GetSession" {
						lyricsJSON(w, `{"code":0,"request":{"code":0,"data":{"session":{"uid":"0","sid":"guest-session","userip":"127.0.0.1"}}}}`)
						return
					}
					if body.Request.Method != "DoSearchForQQMusicLite" || body.Comm.SID != "guest-session" || !strings.Contains(body.Request.Param.Query, track.Title) {
						t.Error("QQ search lost Unicode or its anonymous session")
					}
					if provider == "qq" {
						lyricsJSON(w, `{"code":0,"request":{"code":0,"data":{"body":{"item_song":[{"id":123,"mid":"song-mid","title":"夜に駆ける","interval":261,"album":{"name":"THE BOOK"},"singer":[{"name":"YOASOBI"}]}]}}}}`)
					} else {
						lyricsJSON(w, `{"code":0,"request":{"code":0,"data":{"body":{"song":{"list":[]}}}}}`)
					}
				case "c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg":
					if provider != "qq" || r.URL.Query().Get("songmid") != "song-mid" {
						t.Error("downloaded wrong QQ recording")
					}
					respond(w, 200, map[string]any{"retcode": 0, "lyric": base64.StdEncoding.EncodeToString([]byte("[00:01.00]夜の歌"))})
				case "mobiles.kugou.com/api/v3/search/song":
					if provider == "kugou" || provider == "netease" {
						lyricsJSON(w, `{"status":1,"data":{"info":[{"album_audio_id":234,"hash":"audio-hash","songname":"夜に駆ける","singername":"YOASOBI","album_name":"THE BOOK","duration":261}]}}`)
					} else {
						lyricsJSON(w, `{"status":1,"data":{"info":[]}}`)
					}
				case "lyrics.kugou.com/v1/search":
					if r.URL.Query().Get("duration") != "261000" || r.URL.Query().Get("hash") != "audio-hash" || r.URL.Query().Get("appid") != "3116" || len(r.URL.Query().Get("signature")) != 32 {
						t.Error("Kugou lost milliseconds, recording hash or signed client parameters")
					}
					if provider == "netease" {
						lyricsJSON(w, `{"status":403}`)
						return
					}
					lyricsJSON(w, `{"status":200,"candidates":[{"id":"999","accesskey":"wrong","duration":999000},{"id":"345","accesskey":"lyric-key","duration":261000}]}`)
				case "lyrics.kugou.com/download":
					if r.URL.Query().Get("id") != "345" || r.URL.Query().Get("fmt") != "lrc" || r.URL.Query().Get("client") != "mobi" || len(r.URL.Query().Get("signature")) != 32 {
						t.Error("Kugou accepted a different duration or format")
					}
					respond(w, 200, map[string]any{"status": 200, "content": base64.StdEncoding.EncodeToString([]byte("[00:01.00]夜の歌"))})
				case "music.163.com/api/search/pc":
					if r.URL.Query().Get("s") != track.Title+" "+track.Artist {
						t.Error("NetEase search corrupted metadata")
					}
					lyricsJSON(w, `{"code":200,"result":{"songs":[{"id":456,"name":"夜に駆ける","duration":261120,"artists":[{"name":"YOASOBI"}],"album":{"name":"THE BOOK"}}]}}`)
				case "music.163.com/api/song/lyric":
					lyricsJSON(w, `{"code":200,"lrc":{"lyric":"[00:01.00]夜の歌"},"tlyric":{"lyric":"[00:01.00]夜晚的歌"},"romalrc":{"lyric":"[00:01.00]yoru no uta"}}`)
				default:
					t.Error("unexpected lyrics request", r.URL.Host+r.URL.Path)
					w.WriteHeader(500)
				}
			})
			text, actual, state, attempts, err := a.searchOnlineLyrics(context.Background(), track)
			if err != nil || actual != provider || state != "ready" || text != "[00:01.00]夜の歌\n" {
				t.Fatal(actual, state, text, err, calls)
			}
			if calls[0] != "lrclib.net/api/get" || attempts[len(attempts)-1].Provider != provider {
				t.Fatal("priority lost", calls, attempts)
			}
			if provider == "lrclib" && len(calls) != 1 {
				t.Fatal("queried fallbacks after LRCLIB succeeded", calls)
			}
			if provider != "lrclib" && (attempts[0].State != "failed" || time.Until(a.lyricsRetryAt) < 55*time.Second) {
				t.Fatal("provider cooldown or failure was lost", attempts)
			}
			if provider == "netease" && (attempts[2].Provider != "kugou" || attempts[2].State != "failed") {
				t.Fatal("Kugou rejection was mistaken for missing lyrics", attempts)
			}
		})
	}
}

func TestNetEaseVerificationIsReported(t *testing.T) {
	a, _ := testApp(t)
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		lyricsJSON(w, `{"code":-462,"message":"请绑定手机后再试哦~"}`)
	})
	candidates, err := a.neteaseCandidates(context.Background(), "晴天 周杰伦")
	if len(candidates) != 0 || err == nil || !strings.Contains(err.Error(), "verification") || !strings.Contains(err.Error(), "-462") {
		t.Fatal("account verification was reported as missing lyrics", candidates, err)
	}
}

func TestLyricsMultilingualMatchingAndVersionSafety(t *testing.T) {
	for _, item := range []struct {
		title, artist, candidateTitle, candidateArtist string
		match                                          bool
	}{
		{"夜に駆ける", "YOASOBI", "夜に駆ける", "ＹＯＡＳＯＢＩ", true},
		{"봄날", "방탄소년단", "봄날", "방탄소년단", true},
		{"Привет", "Певец", "ПРИВЕТ", "певец", true},
		{"أغنية", "فنان", "أغنية", "فنان", true},
		{"L’été", "Beyoncé feat. Friend", "L' été", "Friend / Beyoncé", true},
		{"Song", "Artist", "Song (Live)", "Artist", false},
		{"Song", "Artist", "Song (Remix)", "Artist", false},
		{"Song", "Beyoncé", "Song", "Beyonce", false},
	} {
		candidate := lyricsCandidate{Title: item.candidateTitle, Artist: item.candidateArtist, Album: "Album", Duration: 183}
		source := Source{Title: item.title, Artist: item.artist, Album: "Album", Duration: 183}
		if candidateMatches(candidate, source) != item.match {
			t.Fatal("incorrect multilingual match", item)
		}
	}
	alias := lyricsCandidate{Title: "夜に駆ける", Artist: "YOASOBI", Album: "Album", Duration: 183, titleAliases: []string{"Racing into the Night"}}
	track := Source{Title: "Racing into the Night", Artist: "YOASOBI", Album: "Album", Duration: 183}
	if !candidateMatches(alias, track) {
		t.Fatal("provider's explicit title alias was ignored")
	}
	alias.Title += " (Live)"
	if candidateMatches(alias, track) {
		t.Fatal("an alias erased a recording version")
	}
	alias.Title = "夜に駆ける (Ｌｉｖｅ)"
	if candidateMatches(alias, track) {
		t.Fatal("an alias erased a full-width recording version")
	}
	track.Duration, alias.Duration = 0, 1
	alias.Title = track.Title
	if candidateMatches(alias, track) {
		t.Fatal("missing source duration was a high-confidence match")
	}
}

func TestLyricsProviderFormatsAndLanguageMerge(t *testing.T) {
	qrc := `<QrcInfos><Lyric_1 LyricType="1" LyricContent="[ti:Title]
[1000,2000]你(1000,500)好(1500,500)&amp;(2000,1000)
[4000,1000]世界(4000,1000)"/></QrcInfos>`
	converted, err := providerLRC(qrc)
	if err != nil || converted != "[ti:Title]\n[00:01.000]你好&\n[00:04.000]世界\n" {
		t.Fatal(converted, err)
	}
	if _, err = providerLRC("[999999999999999999999,1]bad"); err == nil {
		t.Fatal("accepted overflowing timestamp")
	}
	text := lyricsText{Original: "[ti:Title]\n[00:01.00][00:03.00]こんにちは\n", Translation: "[offset:100]\n[00:01.100]你好\n", Romanization: "[00:01.00]konnichiwa\n"}
	combined, err := combineLyrics(text, true, true)
	if err != nil || combined != "[ti:Title]\n[00:01.000]こんにちは\n[00:01.000]你好\n[00:01.000]konnichiwa\n[00:03.000]こんにちは\n" {
		t.Fatal(combined, err)
	}
	if _, err = combineLyrics(lyricsText{Original: text.Original}, true, false); err == nil {
		t.Fatal("saved a missing language track")
	}
	if text, err := decodeCloudQRC(""); err != nil || text != "" {
		t.Fatal("an unavailable language track was reported as corrupt data", err)
	}
	for _, invalid := range []string{"bad hex", "00000000", strings.Repeat("a", 4*maxLyricsBytes+1)} {
		if _, err = decodeCloudQRC(invalid); err == nil {
			t.Fatal("accepted invalid cloud lyrics")
		}
	}
	app, _ := testApp(t)
	source := Source{Title: "Song", Artist: "Artist", Album: "Album", Duration: 183}
	first := lyricsCandidate{Title: "Song", Artist: "Artist", Album: "Album", Duration: 183, text: &lyricsText{Original: "[00:01.00]First\n"}}
	second := first
	second.text = &lyricsText{Original: "[00:01.00]Different\n"}
	if data, state, err := app.chooseLyrics(context.Background(), source, []lyricsCandidate{first, second}); err != nil || data != "" || state != "needs_review" {
		t.Fatal("silently chose conflicting lyrics", data, state, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := app.lyricsGET(ctx, "/api/search", new(any)); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
}
