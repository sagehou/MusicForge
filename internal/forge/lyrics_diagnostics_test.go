package forge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestLyricsFailedPreviewShowsRawWithoutCachingOrSaving(t *testing.T) {
	for _, tc := range []struct {
		name, provider, payload, wantField, wantText, wantError string
		instrumental, uncollected                               bool
	}{
		{"NetEase plain and YRC", "netease", `{"code":200,"lrc":{"lyric":"Plain <script>alert(1)</script> lyrics"},"yrc":{"lyric":"[1000,2000](1000,1000,0)逐字歌词"},"tlyric":{"lyric":"没有时间轴的译文"},"cookie":"private-cookie"}`, "yrc.lyric", "[1000,2000](1000,1000,0)逐字歌词", "No synchronized LRC lines found", false, false},
		{"NetEase empty instrumental", "netease", `{"code":200,"nolyric":true,"lrc":{"lyric":""},"cookie":"private-cookie"}`, "lrc.lyric", "", "No synchronized LRC lines found", true, false},
		{"NetEase uncollected", "netease", `{"code":200,"uncollected":true}`, "lrc.lyric", "", "No synchronized LRC lines found", false, true},
		{"NetEase invalid timestamp", "netease", `{"code":200,"lrc":{"lyric":"[999999999,1000]Bad timestamp"}}`, "lrc.lyric", "[999999999,1000]Bad timestamp", "Invalid lyrics timestamp", false, false},
		{"QQ invalid QRC", "qq", `{"code":0,"request":{"code":0,"data":{"lyric":"invalid-QRC","trans":"Plain translation"}}}`, "qrc.lyric", "invalid-QRC", "QQ Music returned invalid lyrics data", false, false},
		{"Kugou invalid encoding", "kugou", `{"status":200,"content":"not-base64!"}`, "content.base64", "not-base64!", "Kugou returned invalid lyrics data", false, false},
		{"Kugou plain text", "kugou", `{"status":200,"content":"UGxhaW4gbHlyaWNz"}`, "content", "Plain lyrics", "No synchronized LRC lines found", false, false},
		{"Kugou no lyric candidates", "kugou", `{"status":200,"candidates":[]}`, "", "", "No synchronized LRC lines found", false, false},
		{"PetitLyrics plain text", "petitlyrics", `<response><status>00000000</status><songs><song><lyricsId>321</lyricsId><lyricsType>1</lyricsType><lyricsData>UGxhaW4gbHlyaWNz</lyricsData></song></songs></response>`, "lyricsData.text", "Plain lyrics", "No synchronized LRC lines found", false, false},
		{"PetitLyrics invalid word XML", "petitlyrics", `<response><status>00000000</status><songs><song><lyricsId>321</lyricsId><lyricsType>3</lyricsType><lyricsData>UGxhaW4gbHlyaWNz</lyricsData></song></songs></response>`, "lyricsData.wsy", "Plain lyrics", "petitlyrics returned invalid or oversized lyrics data", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, s := testApp(t)
			source := seedLyricsArtifact(t, a, s)
			const previous = "[00:01.00]Existing lyrics\n"
			if err := a.writeLyricsFile(context.Background(), s, source, previous, "lrclib"); err != nil {
				t.Fatal(err)
			}
			target, err := a.captureLyricsTarget(context.Background(), source.ID)
			if err != nil {
				t.Fatal(err)
			}
			candidate := lyricsCandidate{Provider: tc.provider, Title: source.Title, Artist: source.Artist, Album: source.Album, Duration: source.Duration, remoteID: "321", mid: "recording", hash: "audio-hash"}
			valid := candidate
			valid.text = &lyricsText{Original: "[00:01.00]Valid preview\n"}
			group := a.newLyricsChoices(target, tc.provider, "Track Artist", []lyricsCandidate{valid, candidate}, false, time.Now().Unix())
			mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Host + r.URL.Path {
				case "interface.music.163.com/eapi/register/anonimous":
					readNetEaseParams(t, r)
					guestNetEaseResponse(w)
				case "interface.music.163.com/eapi/song/lyric/v1":
					readNetEaseParams(t, r)
					encryptedNetEaseResponse(w, tc.payload)
				case "c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg":
					lyricsJSON(w, `{"retcode":0,"lyric":"Plain primary lyrics"}`)
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
					} else {
						lyricsJSON(w, tc.payload)
					}
				case "lyrics.kugou.com/v1/search":
					if tc.wantField == "" {
						lyricsJSON(w, tc.payload)
						return
					}
					lyricsJSON(w, `{"status":200,"candidates":[{"id":123,"accesskey":"private-access-key","duration":183000}]}`)
				case "lyrics.kugou.com/download":
					lyricsJSON(w, tc.payload)
				case "on.petitlyrics.com/api/GetPetitLyricsData.php":
					w.Header().Set("Content-Type", "application/xml")
					_, _ = w.Write([]byte(tc.payload))
				default:
					t.Error("unexpected diagnostic request", r.URL)
					w.WriteHeader(500)
				}
			})
			request := lyricsSession(t, a)
			if w := request("POST", "/api/library/1/lyrics/preview", map[string]any{"token": group.Token, "index": 0}); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			key := lyricsSearchKey(target, tc.provider, "Track Artist")
			before, err := a.metaContext(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			w := request("POST", "/api/library/1/lyrics/preview", map[string]any{"token": group.Token, "index": 1})
			var result struct {
				Original   string            `json:"original"`
				Diagnostic *lyricsDiagnostic `json:"diagnostic"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Original != "" || result.Diagnostic == nil {
				t.Fatal("missing inspection result", w.Code, w.Body.String())
			}
			d := result.Diagnostic
			if d.Error != tc.wantError || d.Instrumental != tc.instrumental || d.Uncollected != tc.uncollected {
				t.Fatal("incorrect diagnostic reason or source labels", d)
			}
			found := tc.wantField == "" && len(d.Fields) == 0
			for _, field := range d.Fields {
				found = found || field.Name == tc.wantField && field.Text == tc.wantText
			}
			if !found || strings.Contains(w.Body.String(), "private-cookie") || strings.Contains(w.Body.String(), "private-access-key") {
				t.Fatal("raw lyric field missing or credentials exposed", w.Body.String())
			}
			for _, index := range []int{0, 1} {
				if w = request("PUT", "/api/library/1/lyrics", map[string]any{"token": group.Token, "index": index}); w.Code != 409 {
					t.Fatal("failed diagnostic authorized a new or stale save", w.Code, w.Body.String())
				}
			}
			after, err := a.metaContext(context.Background(), key)
			if err != nil || after != before {
				t.Fatal("diagnostic changed published search-cache contents", err)
			}
			if text, _, _, err := a.chooseLyrics(context.Background(), source, []lyricsCandidate{candidate}, lyricsBatchOptions{MinScore: defaultLyricsMinScore}); text != "" {
				t.Fatal("automatic matching accepted raw diagnostic", text, err)
			}
			if data, err := os.ReadFile(filepath.Join(s.Output, "Album/01.lrc")); err != nil || string(data) != previous {
				t.Fatal("diagnostic replaced existing lyrics", string(data), err)
			}
		})
	}
}

func TestLyricsDiagnosticBoundsUTF8AndRemainsRequestOnly(t *testing.T) {
	d := &lyricsDiagnostic{}
	d.add("lrc.lyric", strings.Repeat("爱", maxLyricsDiagnosticBytes))
	d.add("yrc.lyric", "More lyrics")
	total := 0
	for _, field := range d.Fields {
		total += len(field.Text)
		if !utf8.ValidString(field.Text) || !field.Truncated {
			t.Fatal("diagnostic exceeded UTF-8 truncation boundary", field.Name)
		}
	}
	if total > maxLyricsDiagnosticBytes {
		t.Fatal("unbounded diagnostic", total)
	}
	encoded, err := json.Marshal(d.failed(errors.New("No synchronized LRC lines found")))
	if err != nil || string(encoded) != `{"original":""}` {
		t.Fatal("request-only diagnostic entered the published lyric text format", string(encoded), err)
	}
}
