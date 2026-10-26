package forge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type petitLyricsSong struct {
	ID       string  `xml:"lyricsId"`
	Title    string  `xml:"title"`
	Artist   string  `xml:"artist"`
	Album    string  `xml:"album"`
	Duration float64 `xml:"duration"` // The service returns milliseconds.
	Type     int     `xml:"lyricsType"`
	Data     string  `xml:"lyricsData"`
}

func (a *App) petitLyricsRequest(ctx context.Context, params url.Values) ([]petitLyricsSong, error) {
	// Public SDK protocol identifiers, not account credentials. Keep our own
	// User-Agent; no user cookies, device identifiers or audio are sent.
	params.Set("clientAppId", "p1110417")
	params.Set("terminalType", "10")
	params.Set("logFlag", "0")
	request, err := http.NewRequestWithContext(ctx, "POST", "https://on.petitlyrics.com/api/GetPetitLyricsData.php", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	request.Header.Set("Accept", "application/xml")
	response, raw, err := a.lyricsHTTPResponse("petitlyrics", request)
	if err != nil || response.StatusCode == http.StatusNotFound {
		return nil, err
	}
	var result struct {
		XMLName xml.Name          `xml:"response"`
		Status  string            `xml:"status"`
		Songs   []petitLyricsSong `xml:"songs>song"`
	}
	if xml.Unmarshal(raw, &result) != nil || result.Status == "" {
		return nil, errors.New("petitlyrics returned invalid or oversized lyrics data")
	}
	if result.Status != "00000000" {
		return nil, errors.New("PetitLyrics rejected the lyrics request")
	}
	return result.Songs, nil
}

func (a *App) petitLyricsCandidates(ctx context.Context, source Source, query string) ([]lyricsCandidate, error) {
	title, artist := source.Title, source.Artist
	if query != lyricsSearchQuery(source, "") {
		title = strings.TrimSpace(query)
		// The common custom query is "new title existing artist". The API has
		// separate title/artist fields, so do not send that as a literal title.
		if strings.HasSuffix(title, " "+artist) {
			title = strings.TrimSpace(strings.TrimSuffix(title, artist))
		} else if strings.HasPrefix(title, artist+" ") {
			title = strings.TrimSpace(strings.TrimPrefix(title, artist))
		} else if title == artist {
			title = ""
		} else {
			artist = ""
		}
	}
	params := url.Values{"lyricsType": {"0"}, "maxCount": {"32"}, "key_title": {title}, "key_artist": {artist}}
	var candidates []lyricsCandidate
	// Album-specific results keep a crowded single/compilation search from
	// hiding the requested recording. Both requests retrieve metadata only.
	albums := []string{""}
	if source.Album != "" && query == lyricsSearchQuery(source, "") {
		albums = []string{source.Album, ""}
	}
	for _, album := range albums {
		params.Set("key_album", album)
		songs, err := a.petitLyricsRequest(ctx, params)
		if err != nil {
			if len(candidates) > 0 && ctx.Err() == nil {
				return candidates, nil
			}
			return nil, err
		}
		for _, song := range songs {
			if id, err := strconv.ParseUint(song.ID, 10, 64); err != nil || id == 0 {
				continue
			}
			candidates = append(candidates, lyricsCandidate{remoteID: song.ID, Title: song.Title, Artist: song.Artist, Album: song.Album, Duration: song.Duration / 1000})
		}
	}
	return candidates, nil
}

func (a *App) petitLyricsText(ctx context.Context, candidate lyricsCandidate) (lyricsText, error) {
	params := url.Values{"lyricsType": {"3"}, "maxCount": {"1"}, "key_lyricsId": {candidate.remoteID}}
	songs, err := a.petitLyricsRequest(ctx, params)
	if err != nil {
		return lyricsText{}, err
	}
	diagnostic := &lyricsDiagnostic{}
	if len(songs) == 0 {
		return diagnostic.failed(errors.New("No synchronized LRC lines found")), nil
	}
	if len(songs) != 1 || songs[0].ID != candidate.remoteID {
		err = errors.New("PetitLyrics returned a different recording")
		return lyricsText{}, err
	}
	song := songs[0]
	raw, err := base64.StdEncoding.DecodeString(song.Data)
	if err != nil || len(raw) > 2*maxLyricsBytes {
		diagnostic.add("lyricsData.base64", song.Data)
		err = errors.New("petitlyrics returned invalid or oversized lyrics data")
		return diagnostic.failed(err), nil
	}
	var text string
	switch song.Type {
	case 3:
		diagnostic.add("lyricsData.wsy", string(raw))
		text, err = petitLyricsWords(raw)
	case 2:
		diagnostic.add("lyricsData.lsy.base64", song.Data)
		params.Set("lyricsType", "1")
		var plain []petitLyricsSong
		plain, err = a.petitLyricsRequest(ctx, params)
		if err == nil {
			if len(plain) != 1 || plain[0].ID != song.ID || plain[0].Type != 1 {
				err = errors.New("PetitLyrics returned a different recording")
			} else {
				var content []byte
				content, err = base64.StdEncoding.DecodeString(plain[0].Data)
				if err == nil && len(content) <= maxLyricsBytes {
					diagnostic.add("lyricsData.text", string(content))
					text, err = petitLyricsLines(raw, content)
				} else {
					err = errors.New("petitlyrics returned invalid or oversized lyrics data")
				}
			}
		}
	default:
		diagnostic.add("lyricsData.text", string(raw))
		err = errors.New("No synchronized LRC lines found")
	}
	if err == nil {
		text, err = normalizeLRC(text)
	}
	if err != nil {
		return diagnostic.failed(err), nil
	}
	return lyricsText{Original: text}, nil
}

func petitLyricsWords(raw []byte) (string, error) {
	var lyrics struct {
		XMLName xml.Name `xml:"wsy"`
		Lines   []struct {
			Text  string `xml:"linestring"`
			Words []struct {
				Start string `xml:"starttime"`
			} `xml:"word"`
		} `xml:"line"`
	}
	if xml.Unmarshal(raw, &lyrics) != nil {
		return "", errors.New("petitlyrics returned invalid or oversized lyrics data")
	}
	var out strings.Builder
	for _, line := range lyrics.Lines {
		if strings.TrimSpace(line.Text) == "" {
			continue
		}
		if len(line.Words) == 0 || strings.ContainsAny(line.Text, "\r\n") {
			return "", errors.New("No synchronized LRC lines found")
		}
		time, err := strconv.ParseInt(line.Words[0].Start, 10, 64)
		if err != nil || time < 0 || time > 24*60*60*1000 {
			return "", errors.New("Invalid lyrics timestamp")
		}
		out.WriteString(lyricsTimestamp(time) + line.Text + "\n")
		if out.Len() > maxLyricsBytes {
			return "", errors.New("LRC must be UTF-8 and no larger than 1 MiB")
		}
	}
	return out.String(), nil
}

func petitLyricsLines(raw, plain []byte) (string, error) {
	invalid := errors.New("petitlyrics returned invalid or oversized lyrics data")
	if len(raw) < 0xcc || !bytes.HasPrefix(raw, []byte("MHDROBJT")) || !utf8.Valid(plain) || bytes.IndexByte(plain, 0) >= 0 {
		return "", invalid
	}
	count := int(binary.LittleEndian.Uint32(raw[0x38:0x3c]))
	if count == 0 || count > (len(raw)-0xcc)/2 {
		return "", invalid
	}
	key := binary.LittleEndian.Uint16(raw[0x1a:0x1c])
	if raw[0x19] != 0 {
		key = key&0xc003 | (key&0x0ccc)<<2 | (key&0x3330)>>2
	}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(string(plain), "\ufeff"), "\r\n", "\n"), "\r", "\n"), "\n")
	if len(lines) == count+1 && lines[count] == "" {
		lines = lines[:count]
	}
	if len(lines) != count {
		return "", invalid
	}
	var out strings.Builder
	var previous, wrap int64
	for i, line := range lines {
		time := int64(binary.LittleEndian.Uint16(raw[0xcc+i*2:]) ^ key)
		if i > 0 && time < previous {
			wrap += 65536
		}
		previous = time
		milliseconds := (time + wrap) * 10
		if milliseconds > 24*60*60*1000 {
			return "", errors.New("Invalid lyrics timestamp")
		}
		out.WriteString(lyricsTimestamp(milliseconds) + line + "\n")
		if out.Len() > maxLyricsBytes {
			return "", invalid
		}
	}
	return out.String(), nil
}
