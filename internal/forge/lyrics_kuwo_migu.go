package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// The upstream search APIs mix JSON numbers and strings. Unknown duration stays
// unknown, so it cannot pass automatic matching by borrowing the source length.
func lyricsProviderSeconds(raw json.RawMessage) float64 {
	text := strings.Trim(string(raw), `"`)
	parts := strings.Split(text, ":")
	if len(parts) > 3 { return 0 }
	seconds := 0.0
	for i, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || len(parts) > 1 && i > 0 && value >= 60 { return 0 }
		seconds = seconds*60 + value
	}
	if seconds > 3600 { return 0 }
	return seconds
}

func (a *App) kuwoCandidates(ctx context.Context, query string) ([]lyricsCandidate, error) {
	params := url.Values{"all": {query}, "ft": {"music"}, "client": {"kt"}, "pn": {"0"}, "rn": {"8"}, "encoding": {"utf8"}, "rformat": {"json"}, "vipver": {"1"}, "cluster": {"0"}, "strategy": {"2012"}, "mobi": {"1"}, "issubtitle": {"1"}, "show_copyright_off": {"1"}}
	var response struct {
		Songs []struct {
			ID json.Number `json:"DC_TARGETID"`
			RID string `json:"MUSICRID"`
			Name string `json:"NAME"`
			SongName string `json:"SONGNAME"`
			Artist string `json:"ARTIST"`
			Album string `json:"ALBUM"`
			Duration json.RawMessage `json:"DURATION"`
		} `json:"abslist"`
	}
	code, err := a.lyricsRequest(ctx, "kuwo", "GET", "https://www.kuwo.cn/search/searchMusicBykeyWord?"+params.Encode(), nil, &response)
	if err != nil { return nil, err }
	if code == 404 { return nil, nil }
	if response.Songs == nil { return nil, errors.New("kuwo returned invalid or oversized lyrics data") }
	var candidates []lyricsCandidate
	for _, song := range response.Songs {
		id := string(song.ID)
		if id == "" { id = strings.TrimPrefix(song.RID, "MUSIC_") }
		if value, err := strconv.ParseUint(id, 10, 64); err != nil || value == 0 { continue }
		title := song.SongName
		if title == "" { title = song.Name }
		candidates = append(candidates, lyricsCandidate{Provider: "kuwo", remoteID: id, Title: html.UnescapeString(title), Artist: html.UnescapeString(song.Artist), Album: html.UnescapeString(song.Album), Duration: lyricsProviderSeconds(song.Duration)})
	}
	return candidates, nil
}

func (a *App) kuwoLyrics(ctx context.Context, candidate lyricsCandidate) (lyricsText, error) {
	if id, err := strconv.ParseUint(candidate.remoteID, 10, 64); err != nil || id == 0 { return lyricsText{}, errors.New("Invalid Kuwo lyrics recording ID") }
	var response struct {
		Status int `json:"code"`
		Data struct {
			Lines []struct {
				Time json.RawMessage `json:"time"`
				Text string `json:"lineLyric"`
			} `json:"lrclist"`
		} `json:"data"`
	}
	code, err := a.lyricsRequest(ctx, "kuwo", "GET", "https://www.kuwo.cn/openapi/v1/www/lyric/getlyric?musicId="+url.QueryEscape(candidate.remoteID), nil, &response)
	if err != nil { return lyricsText{}, err }
	if code == 404 { return lyricsText{}, nil }
	if response.Status != 200 { return lyricsText{}, errors.New("kuwo returned invalid or oversized lyrics data") }
	var text strings.Builder
	for _, line := range response.Data.Lines {
		if strings.TrimSpace(line.Text) == "" || line.Text == "//" { continue }
		seconds, err := strconv.ParseFloat(strings.Trim(string(line.Time), `"`), 64)
		if err != nil || seconds < 0 || seconds > 3600 || math.IsNaN(seconds) || math.IsInf(seconds, 0) { continue }
		ms := int64(math.Round(seconds*1000))
		fmt.Fprintf(&text, "[%02d:%02d.%03d]%s\n", ms/60000, ms/1000%60, ms%1000, strings.NewReplacer("\r", " ", "\n", " ").Replace(html.UnescapeString(line.Text)))
	}
	return lyricsText{Original: text.String()}, nil
}

var miguRecordingID = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
var miguLyricsPath = regexp.MustCompile(`^/data/oss/resource/[A-Za-z0-9/._-]+$`)

func validMiguLyricsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "d.musicapp.migu.cn" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.RawPath == "" && miguLyricsPath.MatchString(u.Path) && !strings.Contains(u.Path, "..")
}

func (a *App) miguCandidates(ctx context.Context, query string) ([]lyricsCandidate, error) {
	params := url.Values{"text": {query}, "pageNo": {"1"}, "pageSize": {"8"}}
	var songs []struct {
		ID string `json:"copyrightId"`
		Title string `json:"songName"`
		Artist string `json:"singer"`
		Artists []struct { Name string `json:"name"` } `json:"singerList"`
		Album string `json:"album"`
		Duration json.RawMessage `json:"duration"`
		LyricsURL string `json:"lrcUrl"`
		TranslationURL string `json:"trcUrl"`
		Ext struct { LyricsURL string `json:"lrcUrl"`; TranslationURL string `json:"trcUrl"` } `json:"ext"`
	}
	code, err := a.lyricsRequest(ctx, "migu", "GET", "https://app.u.nf.migu.cn/pc/resource/song/item/search/v1.0?"+params.Encode(), nil, &songs)
	if err != nil { return nil, err }
	if code == 404 { return nil, nil }
	if songs == nil { return nil, errors.New("migu returned invalid or oversized lyrics data") }
	var candidates []lyricsCandidate
	for _, song := range songs {
		if !miguRecordingID.MatchString(song.ID) { continue }
		if song.Ext.LyricsURL != "" { song.LyricsURL = song.Ext.LyricsURL }
		if song.Ext.TranslationURL != "" { song.TranslationURL = song.Ext.TranslationURL }
		if !validMiguLyricsURL(song.LyricsURL) { continue }
		if !validMiguLyricsURL(song.TranslationURL) { song.TranslationURL = "" }
		artist := song.Artist
		if len(song.Artists) > 0 {
			names := []string{}
			for _, singer := range song.Artists { if singer.Name != "" { names = append(names, singer.Name) } }
			artist = strings.Join(names, " / ")
		}
		candidates = append(candidates, lyricsCandidate{Provider: "migu", remoteID: song.ID, lyricsURL: song.LyricsURL, translationURL: song.TranslationURL, Title: html.UnescapeString(song.Title), Artist: html.UnescapeString(artist), Album: html.UnescapeString(song.Album), Duration: lyricsProviderSeconds(song.Duration)})
	}
	return candidates, nil
}

func (a *App) miguLyrics(ctx context.Context, candidate lyricsCandidate) (lyricsText, error) {
	if !miguRecordingID.MatchString(candidate.remoteID) || !validMiguLyricsURL(candidate.lyricsURL) { return lyricsText{}, errors.New("migu returned invalid or oversized lyrics data") }
	read := func(endpoint string) (string, error) {
		request, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if err != nil { return "", err }
		request.Header.Set("Accept", "text/plain")
		_, raw, err := a.lyricsHTTPResponse("migu", request)
		return string(raw), err
	}
	original, err := read(candidate.lyricsURL)
	text := lyricsText{Original: original}
	if err == nil && candidate.translationURL != "" && validMiguLyricsURL(candidate.translationURL) { text.Translation, _ = read(candidate.translationURL) }
	return text, err
}
