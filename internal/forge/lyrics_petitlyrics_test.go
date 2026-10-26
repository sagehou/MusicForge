package forge

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
)

func petitLyricsXML(t *testing.T, w http.ResponseWriter, songs ...petitLyricsSong) {
	t.Helper()
	w.Header().Set("Content-Type", "application/xml")
	response := struct {
		XMLName xml.Name          `xml:"response"`
		Status  string            `xml:"status"`
		Songs   []petitLyricsSong `xml:"songs>song"`
	}{Status: "00000000", Songs: songs}
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		t.Fatal(err)
	}
}

// LRCLIB-focused maintenance tests still visit higher-priority sources. Return
// healthy empty searches rather than hiding requests or using the real network.
func emptyLyricsProviderResponse(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
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
			lyricsJSON(w, `{"code":0,"request":{"code":0,"data":{"session":{"uid":"0","sid":"guest","userip":"127.0.0.1"}}}}`)
		} else {
			lyricsJSON(w, `{"code":0,"request":{"code":0,"data":{"body":{"item_song":[]}}}}`)
		}
	case "mobiles.kugou.com/api/v3/search/song":
		lyricsJSON(w, `{"status":1,"data":{"info":[]}}`)
	case "interface.music.163.com/eapi/register/anonimous":
		readNetEaseParams(t, r)
		guestNetEaseResponse(w)
	case "interface.music.163.com/eapi/search/song/list/page":
		readNetEaseParams(t, r)
		encryptedNetEaseResponse(w, `{"code":200,"data":{"resources":[]}}`)
	case "on.petitlyrics.com/api/GetPetitLyricsData.php":
		petitLyricsXML(t, w)
	default:
		return false
	}
	return true
}

func TestPetitLyricsMetadataAndExactRecordingPreview(t *testing.T) {
	a, _ := testApp(t)
	source := Source{Title: "夜に駆ける", Artist: "YOASOBI", Album: "THE BOOK", Duration: 258.899}
	metadata, previews := 0, 0
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Host != "on.petitlyrics.com" || r.URL.Path != "/api/GetPetitLyricsData.php" {
			t.Fatal("wrong PetitLyrics endpoint", r.URL)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("clientAppId") != "p1110417" || r.Form.Get("terminalType") != "10" || r.Form.Get("logFlag") != "0" || r.Header.Get("Cookie") != "" || r.Form.Get("userId") != "" {
			t.Fatal("unexpected client identity or private request data", r.Form)
		}
		song := petitLyricsSong{ID: "2936416", Title: source.Title, Artist: source.Artist, Album: source.Album, Duration: 258899}
		if r.Form.Get("lyricsType") == "0" {
			metadata++
			if len(r.Form) != 8 || r.Form.Get("maxCount") != "32" || r.Form.Get("key_title") != source.Title || r.Form.Get("key_artist") != source.Artist {
				t.Fatal("metadata query lost fields", r.Form)
			}
			if metadata == 1 {
				if r.Form.Get("key_album") != source.Album {
					t.Fatal("album-specific search must run first")
				}
				petitLyricsXML(t, w, song)
			} else {
				if r.Form.Get("key_album") != "" {
					t.Fatal("broad search retained album filter")
				}
				songs := []petitLyricsSong{song, {ID: "not-an-id", Title: source.Title, Artist: source.Artist}}
				for i := 0; i < 32; i++ {
					other := song
					other.ID, other.Duration = strconv.Itoa(i+1), 300000
					songs = append(songs, other)
				}
				petitLyricsXML(t, w, songs...)
			}
			return
		}
		previews++
		// Exact-ID preview must not repeat the metadata search or select a cover.
		if len(r.Form) != 6 || r.Form.Get("lyricsType") != "3" || r.Form.Get("key_lyricsId") != song.ID || r.Form.Get("maxCount") != "1" || r.Form.Get("key_title") != "" {
			t.Fatal("preview did not bind the selected recording", r.Form)
		}
		song.Type, song.Data = 3, base64.StdEncoding.EncodeToString([]byte(`<wsy><line><linestring>テスト &amp; sample</linestring><word><starttime>870</starttime></word></line><line><linestring></linestring></line><line><linestring>次の行</linestring><word><starttime>8123</starttime></word></line></wsy>`))
		petitLyricsXML(t, w, song)
	})
	candidates, instrumental, err := a.providerCandidates(context.Background(), "petitlyrics", source, "", true)
	if err != nil || instrumental || metadata != 2 || previews != 0 || len(candidates) != 8 || candidates[0].remoteID != "2936416" || candidates[0].MatchScore != 100 || candidates[0].Duration != source.Duration {
		t.Fatal("metadata search lost the matching album, duration units or candidate bound", candidates, metadata, previews, err)
	}
	text, err := a.candidateLyrics(context.Background(), candidates[0])
	if err != nil || text.Original != "[00:00.870]テスト & sample\n[00:08.123]次の行\n" || text.diagnostic != nil || previews != 1 {
		t.Fatal(text, err, previews)
	}
}

func TestPetitLyricsLineTimingAndMalformedData(t *testing.T) {
	for _, permuted := range []bool{false, true} {
		raw := make([]byte, 0xcc+6)
		copy(raw, "MHDROBJT")
		binary.LittleEndian.PutUint32(raw[0x38:], 3)
		key := uint16(0xb0dc)
		binary.LittleEndian.PutUint16(raw[0x1a:], key)
		if permuted {
			raw[0x19] = 1
			key = key&0xc003 | (key&0x0ccc)<<2 | (key&0x3330)>>2
		}
		for i, timing := range []uint16{87, 65530, 14} {
			binary.LittleEndian.PutUint16(raw[0xcc+2*i:], timing^key)
		}
		text, err := petitLyricsLines(raw, []byte("テスト\r\n\r\n終わり\r\n"))
		if err != nil || text != "[00:00.870]テスト\n[10:55.300]\n[10:55.500]終わり\n" {
			t.Fatal(text, err)
		}
		for _, bad := range [][]byte{nil, raw[:0xcc], append([]byte("bad"), raw[3:]...)} {
			if _, err := petitLyricsLines(bad, []byte("テスト\n\n終わり\n")); err == nil {
				t.Fatal("accepted invalid timing data")
			}
		}
		if _, err := petitLyricsLines(raw, []byte("only one line")); err == nil {
			t.Fatal("joined timings from a different recording")
		}
	}
	for _, raw := range []string{"not XML", `<wsy><line><linestring>missing timing</linestring></line></wsy>`, `<wsy><line><linestring>bad</linestring><word><starttime>-1</starttime></word></line></wsy>`, `<wsy><line><linestring>bad</linestring><word><starttime>999999999999999999999</starttime></word></line></wsy>`} {
		if _, err := petitLyricsWords([]byte(raw)); err == nil {
			t.Fatal("accepted invalid synchronized data", raw)
		}
	}
}

func TestPetitLyricsUnparsedContentAndWrongRecordingCannotBeSaved(t *testing.T) {
	for _, tc := range []struct {
		name, id, data string
		kind           int
		requestError   bool
	}{
		{"plain", "123", base64.StdEncoding.EncodeToString([]byte("plain test lyrics")), 1, false},
		{"empty", "123", "", 3, false},
		{"bad base64", "123", "!!!", 3, false},
		{"bad XML", "123", base64.StdEncoding.EncodeToString([]byte("invalid word data")), 3, false},
		{"different recording", "456", "", 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := testApp(t)
			mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
				petitLyricsXML(t, w, petitLyricsSong{ID: tc.id, Type: tc.kind, Data: tc.data})
			})
			text, err := a.candidateLyrics(context.Background(), lyricsCandidate{Provider: "petitlyrics", remoteID: "123"})
			if (err != nil) != tc.requestError || text.Original != "" {
				t.Fatal("unparsed or wrong-recording lyrics became savable", text, err)
			}
			if !tc.requestError && (text.diagnostic == nil || text.diagnostic.Error == "" || len(text.diagnostic.Fields) == 0) {
				t.Fatal("failed lyrics lost their inspection content", text)
			}
		})
	}
}

func TestPetitLyricsLinePreviewFetchesTextForTheSameID(t *testing.T) {
	a, _ := testApp(t)
	raw := make([]byte, 0xce)
	copy(raw, "MHDROBJT")
	binary.LittleEndian.PutUint32(raw[0x38:], 1)
	binary.LittleEndian.PutUint16(raw[0xcc:], 123)
	calls := 0
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("key_lyricsId") != "123" {
			t.Fatal("text fallback changed recording")
		}
		song := petitLyricsSong{ID: "123", Type: 2, Data: base64.StdEncoding.EncodeToString(raw)}
		if calls == 2 {
			if r.Form.Get("lyricsType") != "1" {
				t.Fatal("missing plain-text request")
			}
			song.Type, song.Data = 1, base64.StdEncoding.EncodeToString([]byte("テスト\n"))
		}
		petitLyricsXML(t, w, song)
	})
	text, err := a.candidateLyrics(context.Background(), lyricsCandidate{Provider: "petitlyrics", remoteID: "123"})
	if err != nil || text.Original != "[00:01.230]テスト\n" || calls != 2 {
		t.Fatal(text, err, calls)
	}
}

func TestPetitLyricsRejectedAndCancelledRequests(t *testing.T) {
	a, _ := testApp(t)
	calls := 0
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`<response><status>8150f001</status></response>`))
	})
	if _, err := a.petitLyricsCandidates(context.Background(), Source{Title: "Song", Artist: "Artist"}, "Song Artist"); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.petitLyricsCandidates(ctx, Source{Title: "Song", Artist: "Artist"}, "Song Artist"); err != context.Canceled || calls != 1 {
		t.Fatal("cancellation issued a provider request", err, calls)
	}
}

func TestPetitLyricsLiveSynchronizedFormats(t *testing.T) {
	if os.Getenv("MUSICFORGE_TEST_PETITLYRICS") != "1" {
		t.Skip("opt-in live provider validation in Actions")
	}
	a, _ := testApp(t)
	ctx := context.Background()
	source := Source{Title: "夜に駆ける", Artist: "YOASOBI", Album: "THE BOOK", Duration: 258.899}
	candidates, _, err := a.providerCandidates(ctx, "petitlyrics", source, "", true)
	if err != nil || len(candidates) == 0 || candidates[0].MatchScore != 100 {
		t.Fatal("live metadata matching failed", len(candidates), err)
	}
	text, err := a.candidateLyrics(ctx, candidates[0])
	if err != nil || text.Original == "" || text.diagnostic != nil {
		t.Fatal("live synchronized preview failed", err)
	}
	// Exercise the service's actual line-timing representation as well as its
	// preferred word timing. Do not log or retain the downloaded lyric text.
	params := url.Values{"lyricsType": {"2"}, "maxCount": {"1"}, "key_lyricsId": {candidates[0].remoteID}}
	lineSongs, err := a.petitLyricsRequest(ctx, params)
	if err != nil || len(lineSongs) != 1 || lineSongs[0].Type != 2 || lineSongs[0].ID != candidates[0].remoteID {
		t.Fatal("live line-timing request failed", err)
	}
	params.Set("lyricsType", "1")
	plainSongs, err := a.petitLyricsRequest(ctx, params)
	if err != nil || len(plainSongs) != 1 || plainSongs[0].Type != 1 || plainSongs[0].ID != candidates[0].remoteID {
		t.Fatal("live same-ID text request failed", err)
	}
	timing, err := base64.StdEncoding.DecodeString(lineSongs[0].Data)
	if err != nil {
		t.Fatal("invalid live line data")
	}
	plain, err := base64.StdEncoding.DecodeString(plainSongs[0].Data)
	if err != nil {
		t.Fatal("invalid live text data")
	}
	lineText, err := petitLyricsLines(timing, plain)
	if err != nil {
		t.Fatal("live line conversion failed", err)
	}
	if _, err := normalizeLRC(lineText); err != nil {
		t.Fatal("live line conversion was not synchronized", err)
	}
	t.Logf("Live PetitLyrics: %d candidates, score %d, word and line formats parsed", len(candidates), candidates[0].MatchScore)
}
