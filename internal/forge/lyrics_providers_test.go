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
		accept := "application/json"
		if r.URL.Host == "on.petitlyrics.com" {
			accept = "application/xml"
		}
		if r.URL.Scheme != "https" || !strings.Contains(r.Header.Get("User-Agent"), "MusicForge/") || r.Header.Get("Accept") != accept {
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
	track := Source{Title: "夜に駆ける", Artist: "YOASOBI", Album: "THE BOOK", Duration: 261}
	for _, provider := range lyricsProviders {
		t.Run(provider, func(t *testing.T) {
			a, _ := testApp(t)
			var calls []string
			mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Host+r.URL.Path)
				switch r.URL.Host + r.URL.Path {
				case "lrclib.net/api/get":
					if provider == "lrclib" {
						lyricsJSON(w, `{"trackName":"夜に駆ける","artistName":"YOASOBI","albumName":"THE BOOK","duration":261,"syncedLyrics":"[00:01.00]夜の歌"}`)
					} else {
						w.WriteHeader(404)
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
					if provider != "qq" {
						w.Header().Set("Retry-After", "60")
						w.WriteHeader(429)
						return
					}
					if body.Request.Method == "GetSession" {
						lyricsJSON(w, `{"code":0,"request":{"code":0,"data":{"session":{"uid":"0","sid":"guest-session","userip":"127.0.0.1"}}}}`)
						return
					}
					if body.Request.Method != "DoSearchForQQMusicMobile" || body.Comm.SID != "guest-session" || !strings.Contains(body.Request.Param.Query, track.Title) {
						t.Error("QQ search lost Unicode or its anonymous session")
					}
					if provider == "qq" {
						lyricsJSON(w, `{"code":0,"request":{"code":0,"data":{"body":{"item_song":[{"id":123,"mid":"song-mid","title":"夜に駆ける (向夜晚奔去)","name":"夜に駆ける","interval":261,"album":{"name":"THE BOOK"},"singer":[{"name":"YOASOBI"}]}]}}}}`)
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
				case "interface.music.163.com/eapi/register/anonimous":
					readNetEaseParams(t, r)
					guestNetEaseResponse(w)
				case "interface.music.163.com/eapi/search/song/list/page":
					params := readNetEaseParams(t, r)
					if params["keyword"] != track.Title+" "+track.Artist || netEaseRequestCookie(r, "MUSIC_A") != "guest-session" {
						t.Error("NetEase search lost metadata or guest credentials")
					}
					if provider == "netease" {
						encryptedNetEaseResponse(w, `{"code":200,"data":{"resources":[{"baseInfo":{"simpleSongData":{"id":456,"name":"夜に駆ける","dt":261000,"ar":[{"name":"YOASOBI"}],"al":{"name":"THE BOOK"}}}}]}}`)
					} else {
						encryptedNetEaseResponse(w, `{"code":200,"data":{"resources":[]}}`)
					}
				case "interface.music.163.com/eapi/song/lyric/v1":
					params := readNetEaseParams(t, r)
					if params["id"] != float64(456) || params["yv"] != "-1" {
						t.Error("NetEase downloaded wrong recording or format")
					}
					encryptedNetEaseResponse(w, `{"code":200,"lrc":{"lyric":"[00:01.00]夜の歌"},"tlyric":{"lyric":"[00:01.00]夜晚的歌"},"romalrc":{"lyric":"[00:01.00]yoru no uta"}}`)
				case "lrclib.net/api/search":
					lyricsJSON(w, `[]`)
				case "on.petitlyrics.com/api/GetPetitLyricsData.php":
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					if provider == "petitlyrics" {
						song := petitLyricsSong{ID: "123", Title: track.Title, Artist: track.Artist, Album: track.Album, Duration: 261000}
						if r.Form.Get("lyricsType") == "3" {
							song.Type, song.Data = 3, base64.StdEncoding.EncodeToString([]byte(`<wsy><line><linestring>夜の歌</linestring><word><starttime>1000</starttime></word></line></wsy>`))
						}
						petitLyricsXML(t, w, song)
					} else {
						petitLyricsXML(t, w)
					}
				default:
					t.Error("unexpected lyrics request", r.URL.Host+r.URL.Path)
					w.WriteHeader(500)
				}
			})
			text, actual, state, attempts, err := a.searchOnlineLyrics(context.Background(), lyricsTarget{Source: track}, lyricsBatchOptions{MinScore: defaultLyricsMinScore})
			if err != nil || actual != provider || state != "ready" || !strings.HasSuffix(text, "]夜の歌\n") {
				t.Fatal(actual, state, text, err, calls)
			}
			if calls[0] != "u.y.qq.com/cgi-bin/musicu.fcg" || attempts[len(attempts)-1].Provider != provider {
				t.Fatal("priority lost", calls, attempts)
			}
			if len(attempts) != lyricsProviderPriority(provider)+1 {
				t.Fatal("queried lower-priority providers after an eligible 100-point result", calls, attempts)
			}
			if provider != "qq" && (attempts[0].State != "failed" || time.Until(a.lyricsRetries["qq"]) < 55*time.Second) {
				t.Fatal("provider cooldown or failure was lost", attempts)
			}
			if provider == "netease" && (attempts[1].Provider != "kugou" || attempts[1].State != "failed") {
				t.Fatal("Kugou rejection was mistaken for missing lyrics", attempts)
			}
		})
	}
}

func TestNetEaseVerificationIsReported(t *testing.T) {
	a, _ := testApp(t)
	calls := 0
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		calls++
		lyricsJSON(w, `{"code":-462,"message":"请绑定手机后再试哦~"}`)
	})
	for i := 0; i < 2; i++ {
		candidates, err := a.neteaseCandidates(context.Background(), "晴天 周杰伦")
		if len(candidates) != 0 || err == nil || !strings.Contains(err.Error(), "verification") || !strings.Contains(err.Error(), "-462") {
			t.Fatal("account verification was reported as missing lyrics", candidates, err)
		}
	}
	if calls != 1 || time.Until(a.netease.retryAt) < 29*time.Minute {
		t.Fatal("account verification did not pause the provider", calls)
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

func TestLyricsProviderArtistAliasesIncludeTranslatedCollaborators(t *testing.T) {
	for _, provider := range []string{"qq", "netease"} {
		t.Run(provider, func(t *testing.T) {
			a, _ := testApp(t)
			mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Host + r.URL.Path {
				case "u.y.qq.com/cgi-bin/musicu.fcg":
					var body struct {
						Request struct {
							Method string `json:"method"`
						} `json:"request"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body.Request.Method == "GetSession" {
						lyricsJSON(w, `{"code":0,"request":{"code":0,"data":{"session":{"uid":"0","sid":"guest-session","userip":"127.0.0.1"}}}}`)
						return
					}
					lyricsJSON(w, `{"code":0,"request":{"code":0,"data":{"body":{"item_song":[{"id":123,"mid":"recording","title":"讓愛隨風","interval":183,"album":{"name":"搖滾樂"},"singer":[{"name":"Novel Voice","foreign_name":"新声歌手"},{"name":"Guest Voice","title":"客席歌手"}]}]}}}}`)
				case "interface.music.163.com/eapi/register/anonimous":
					readNetEaseParams(t, r)
					guestNetEaseResponse(w)
				case "interface.music.163.com/eapi/search/song/list/page":
					readNetEaseParams(t, r)
					encryptedNetEaseResponse(w, `{"code":200,"data":{"resources":[{"baseInfo":{"simpleSongData":{"id":321,"name":"讓愛隨風","dt":183000,"ar":[{"name":"Novel Voice","alias":["Neo Voice"],"tns":["新声歌手"]},{"name":"Guest Voice","alias":["客席歌手"]}],"al":{"name":"搖滾樂"}}}}]}}`)
				default:
					t.Error("unexpected alias lookup", r.URL)
					w.WriteHeader(500)
				}
			})
			source := Source{Title: "让爱随风", Artist: "客席歌手 / 新聲歌手", Album: "摇滚乐", Duration: 183}
			candidates, _, err := a.providerCandidates(context.Background(), provider, source, "Song", true)
			if err != nil || len(candidates) != 1 || !candidates[0].IdentityMatch || candidates[0].MatchScore != 100 {
				t.Fatal("provider aliases did not reach scoring", candidates, err)
			}
			source.Artist = "新声歌手"
			if candidateMatches(candidates[0], source) {
				t.Fatal("provider aliases silently removed a collaborating artist")
			}
		})
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
	if data, _, state, err := app.chooseLyrics(context.Background(), source, []lyricsCandidate{first, second}, lyricsBatchOptions{MinScore: defaultLyricsMinScore}); err != nil || data != "" || state != "needs_review" {
		t.Fatal("silently chose conflicting lyrics", data, state, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := app.lyricsGET(ctx, "/api/search", new(any)); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
}
