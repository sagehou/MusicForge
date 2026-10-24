package forge

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var lyricsProviders = []string{"lrclib", "qq", "kugou", "netease"}

type lyricsAttempt struct {
	Provider string `json:"provider"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
}

type lyricsText struct {
	Original     string `json:"original"`
	Translation  string `json:"translation,omitempty"`
	Romanization string `json:"romanization,omitempty"`
}

type lyricsCandidate struct {
	Index                       int      `json:"index"`
	Provider                    string   `json:"provider"`
	Title                       string   `json:"title"`
	Artist                      string   `json:"artist"`
	Album                       string   `json:"album"`
	Duration                    float64  `json:"duration"`
	HighConfidence              bool     `json:"high_confidence"`
	IdentityMatch               bool     `json:"identity_match"`
	MatchScore                  int      `json:"match_score"`
	DurationDelta               *float64 `json:"duration_delta,omitempty"`
	Script                      string   `json:"script"`
	TranslationScript           string   `json:"translation_script,omitempty"`
	remoteID, mid, hash         string
	titleAliases, artistAliases []string
	text                        *lyricsText
}

func foldLyricsWidth(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= '！' && r <= '～' {
			return r - 0xfee0
		}
		return r
	}, text)
}

// Preserve letters and accents; normalize Chinese script, width, case and
// punctuation for comparison. Live and Remix remain part of the title.
func lyricsTag(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, normalizeLyricsMetadata(text))
}

var lyricsArtistSeparator = regexp.MustCompile(`(?i)\s+(?:feat\.?|ft\.?|featuring|with|and)\s+|[&;/、，,]`)
var lyricsVersionWords = regexp.MustCompile(`(?i)\b(?:live|remix|instrumental|karaoke|acoustic|remaster(?:ed)?|edit|version)\b|现场|伴奏|ライブ|실황`)

func sameLyricsArtist(left, right string) bool {
	if key := lyricsArtistKey(left); key != "" && key == lyricsArtistKey(right) {
		return true
	}
	split := func(text string) []string {
		var names []string
		for _, name := range lyricsArtistSeparator.Split(normalizeLyricsMetadata(text), -1) {
			if name = lyricsArtistKey(name); name != "" {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		return names
	}
	a, b := split(left), split(right)
	return len(a) > 0 && strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

func candidateMatches(candidate lyricsCandidate, source Source) bool {
	if validLyricsSearch(source) != nil {
		return false
	}
	title, artist := candidateIdentity(candidate, source)
	return title && artist && candidate.Duration > 0 && !math.IsNaN(candidate.Duration) && !math.IsInf(candidate.Duration, 0) &&
		math.Abs(candidate.Duration-source.Duration) <= 2 &&
		(source.Album == "" || sameLyricsTag(candidate.Album, source.Album))
}

func candidateIdentity(candidate lyricsCandidate, source Source) (bool, bool) {
	title := sameLyricsTag(candidate.Title, source.Title)
	if strings.EqualFold(strings.Join(lyricsVersionWords.FindAllString(normalizeLyricsMetadata(candidate.Title), -1), "/"), strings.Join(lyricsVersionWords.FindAllString(normalizeLyricsMetadata(source.Title), -1), "/")) {
		for _, alias := range candidate.titleAliases {
			title = title || sameLyricsTag(alias, source.Title)
		}
	}
	artist := sameLyricsArtist(candidate.Artist, source.Artist)
	for _, alias := range candidate.artistAliases {
		artist = artist || sameLyricsArtist(alias, source.Artist)
	}
	return title, artist
}

// A conservative script hint, not language identification or text conversion.
// Shared Han characters remain unknown; kana/Hangul are never labelled Chinese.
var lyricsScriptForms = func() map[rune]rune {
	forms := map[rune]rune{}
	for _, pair := range strings.Fields("這这 來来 時时 愛爱 說说 聽听 風风 雲云 夢梦 歸归 遠远 還还 讓让 從从 開开 關关 會会 過过 無无 為为 裡里 後后 個个 們们 點点 葉叶 淚泪 歲岁 亂乱 對对 難难 見见 觸触 覺觉 樂乐 歡欢 聲声 燈灯 轉转 動动 飛飞 願愿 隨随 給给 總总 記记 憶忆 間间 門门 問问 題题 顏颜 擁拥 體体 溫温 寫写 畫画 線线 終终 結结 絲丝 紅红 綠绿 藍蓝 黃黄 頭头 隻只 變变 幾几 麼么 嗎吗 於于 與与 錯错 尋寻 親亲 輕轻 輪轮 迴回 儘尽 盡尽 絕绝 將将 兩两 萬万 連连 國国 鄉乡 處处 讀读 書书 學学 長长 語语 銀银 邊边 靜静 陽阳 陰阴 熱热 龍龙 馬马 車车 鄧邓 麗丽 華华 寶宝 貝贝 廣广 雙双 傷伤 隱隐 請请 應应 許许 諾诺 識识 實实 現现 證证 擔担 懷怀 賴赖 敗败 戰战 勝胜 計计 劃划 傳传 統统 緣缘 練练 獨独 擇择 壞坏 遲迟 遺遗 謝谢 輸输 贏赢 絢绚 爛烂 燦灿 確确 載载 機机 卻却 戀恋") {
		p := []rune(pair)
		if p[0] != p[1] {
			forms[p[0]], forms[p[1]] = p[1], p[1]
		}
	}
	return forms
}()

func foldLyricsScript(text string) string {
	return strings.Map(func(r rune) rune {
		if simplified, ok := lyricsScriptForms[r]; ok {
			return simplified
		}
		return r
	}, text)
}

func lyricsScript(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if lrcTimestamp.MatchString(line) {
			for lrcTimestamp.MatchString(line) {
				line = lrcTimestamp.ReplaceAllString(line, "")
			}
			lines = append(lines, line)
		}
	}
	text = strings.Join(lines, "\n")
	for _, r := range text {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			return "unknown"
		}
	}
	simplified, traditional := 0, 0
	for _, r := range text {
		if form, ok := lyricsScriptForms[r]; ok {
			if form == r {
				simplified++
			} else {
				traditional++
			}
		}
	}
	if simplified > 0 && traditional == 0 {
		return "simplified"
	}
	if traditional > 0 && simplified == 0 {
		return "traditional"
	}
	return "unknown"
}

func rankLyricsCandidates(candidates []lyricsCandidate, source Source) {
	for i := range candidates {
		c := &candidates[i]
		c.HighConfidence, c.MatchScore, c.DurationDelta = candidateMatches(*c, source), 0, nil
		title, artist := candidateIdentity(*c, source)
		c.IdentityMatch = title && artist
		if title {
			c.MatchScore += 40
		}
		if artist {
			c.MatchScore += 30
		}
		if source.Album == "" || sameLyricsTag(c.Album, source.Album) {
			c.MatchScore += 10
		}
		if source.Duration > 0 && c.Duration > 0 && !math.IsNaN(c.Duration) && !math.IsInf(c.Duration, 0) {
			delta := math.Abs(c.Duration - source.Duration)
			c.DurationDelta = &delta
			if delta <= 2 {
				c.MatchScore += 20 - int(math.Ceil(delta*2))
			} else if delta <= 5 {
				c.MatchScore += 12
			} else if delta <= 10 {
				c.MatchScore += 5
			}
		}
		c.Script, c.TranslationScript = "unknown", ""
		if c.text != nil {
			c.Script, c.TranslationScript = lyricsScript(c.text.Original), lyricsScript(c.text.Translation)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.HighConfidence != b.HighConfidence {
			return a.HighConfidence
		}
		if (a.Script == "simplified") != (b.Script == "simplified") {
			return a.Script == "simplified"
		}
		if (a.TranslationScript == "simplified") != (b.TranslationScript == "simplified") {
			return a.TranslationScript == "simplified"
		}
		return a.MatchScore > b.MatchScore
	})
}

func validLyricsSearch(source Source) error {
	if lyricsTag(source.Title) == "" || lyricsTag(source.Artist) == "" || source.Duration < 1 || source.Duration > 3600 || math.IsNaN(source.Duration) || math.IsInf(source.Duration, 0) {
		return errors.New("Online lyrics search requires title, artist and a duration between 1 and 3600 seconds")
	}
	return nil
}

func (a *App) searchOnlineLyrics(ctx context.Context, target lyricsTarget, batch lyricsBatchOptions) (text, provider, state string, attempts []lyricsAttempt, err error) {
	source := target.Source
	if err = validLyricsSearch(source); err != nil {
		return
	}
	review, instrumental, failed := false, false, false
	var traditionalText, traditionalProvider string
	for _, name := range lyricsProviders {
		candidates, noVocals, _, searched, searchErr := a.cachedLyricsCandidates(ctx, target, name, "", false, batch.Refresh)
		instrumental = instrumental || noVocals
		result, resultState := "", "not_found"
		if searchErr == nil {
			result, resultState, searchErr = a.chooseLyrics(ctx, source, candidates, batch)
			if ctx.Err() == nil {
				if cacheErr := a.storeLyricsSearch(ctx, target, name, "", candidates, noVocals, searched); cacheErr != nil {
					return "", "", "failed", attempts, cacheErr
				}
			}
		}
		if ctx.Err() != nil {
			err = ctx.Err()
			return
		}
		attempt := lyricsAttempt{Provider: name, State: resultState}
		if searchErr != nil {
			attempt.State, attempt.Error, failed = "failed", searchErr.Error(), true
		}
		if noVocals && resultState == "not_found" && searchErr == nil {
			attempt.State = "instrumental"
		}
		attempts = append(attempts, attempt)
		if result != "" && searchErr == nil {
			script := lyricsScript(result)
			if batch.ReplaceTraditional && script != "simplified" {
				continue
			}
			if script == "simplified" || traditionalText == "" && script != "traditional" {
				return result, name, "ready", attempts, nil
			}
			if traditionalText == "" {
				traditionalText, traditionalProvider = result, name
			}
		}
		review = review || resultState == "needs_review"
	}
	if traditionalText != "" {
		return traditionalText, traditionalProvider, "ready", attempts, nil
	}
	switch {
	case review:
		state = "needs_review"
	case failed:
		state, err = "failed", errors.New("Lyrics providers unavailable; retry later")
	case instrumental:
		state = "instrumental"
	default:
		state = "not_found"
	}
	return
}

func (a *App) chooseLyrics(ctx context.Context, source Source, candidates []lyricsCandidate, batch lyricsBatchOptions) (string, string, error) {
	rankLyricsCandidates(candidates, source)
	var safe []*lyricsCandidate
	for i := range candidates {
		candidate := candidates[i]
		if validLyricsSearch(source) == nil && candidate.IdentityMatch && candidate.Duration > 0 && candidate.DurationDelta != nil && *candidate.DurationDelta <= 10 && candidate.MatchScore >= batch.MinScore {
			safe = append(safe, &candidates[i])
		}
	}
	// ponytail: bound downloads per track; crowded or conflicting recordings are
	// left for the existing candidate picker instead of guessing a winner.
	if len(safe) > 3 {
		return "", "needs_review", nil
	}
	var selected string
	selectedScript := ""
	var failures []error
	for _, candidate := range safe {
		text, err := a.candidateLyrics(ctx, *candidate)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if text.Original == "" {
			continue
		}
		candidate.text = &text
		if selected != "" && selected != text.Original {
			// Script preference cannot resolve different words or timing.
			if foldLyricsScript(selected) != foldLyricsScript(text.Original) {
				return "", "needs_review", nil
			}
		}
		script := lyricsScript(text.Original)
		if selected == "" || script == "simplified" && selectedScript != "simplified" {
			selected, selectedScript = text.Original, script
		}
	}
	if len(failures) > 0 {
		return "", "failed", errors.Join(failures...)
	}
	if selected != "" {
		if batch.ReplaceTraditional && selectedScript != "simplified" {
			return "", "not_found", nil
		}
		return selected, "ready", nil
	}
	if len(candidates) > len(safe) {
		return "", "needs_review", nil
	}
	return "", "not_found", nil
}

func (a *App) providerCandidates(ctx context.Context, provider string, source Source, query string, manual bool) ([]lyricsCandidate, bool, error) {
	if query == "" {
		query = strings.TrimSpace(source.Title + " " + source.Artist)
	}
	var candidates []lyricsCandidate
	var instrumental bool
	var err error
	switch provider {
	case "lrclib":
		candidates, instrumental, err = a.lrclibCandidates(ctx, source, query, manual)
	case "qq":
		candidates, err = a.qqCandidates(ctx, query)
	case "kugou":
		candidates, err = a.kugouCandidates(ctx, query)
	case "netease":
		candidates, err = a.neteaseCandidates(ctx, query)
	default:
		return nil, false, errors.New("Unknown lyrics provider")
	}
	rankLyricsCandidates(candidates, source)
	var result []lyricsCandidate
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Artist) == "" || len(candidate.Title) > 2000 || len(candidate.Artist) > 4000 || len(candidate.Album) > 2000 || math.IsNaN(candidate.Duration) || math.IsInf(candidate.Duration, 0) {
			continue
		}
		candidate.Provider, candidate.Index = provider, len(result)
		key := candidate.remoteID
		if key == "" && candidate.text != nil {
			key = digest(candidate.Title + "\x00" + candidate.Artist + "\x00" + candidate.Album + "\x00" + strconv.FormatFloat(candidate.Duration, 'f', -1, 64) + "\x00" + candidate.text.Original)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, candidate)
		if len(result) == 8 {
			break
		}
	}
	return result, instrumental, err
}

func (a *App) lrclibCandidates(ctx context.Context, source Source, query string, manual bool) ([]lyricsCandidate, bool, error) {
	convert := func(record lrclibRecord) (lyricsCandidate, bool) {
		text, err := providerLRC(record.Synced)
		return lyricsCandidate{Title: record.Title, Artist: record.Artist, Album: record.Album, Duration: record.Duration,
			Provider: "lrclib", text: &lyricsText{Original: text}}, err == nil && !record.Instrumental
	}
	params := url.Values{"track_name": {source.Title}, "artist_name": {source.Artist}}
	if !manual {
		params.Set("duration", strconv.FormatFloat(source.Duration, 'f', 3, 64))
		if source.Album != "" {
			params.Set("album_name", source.Album)
		}
		var exact lrclibRecord
		code, err := a.lyricsGET(ctx, "/api/get?"+params.Encode(), &exact)
		if err != nil {
			return nil, false, err
		}
		if code == 200 && matchingLyrics(exact, source) {
			if exact.Instrumental {
				return nil, true, nil
			}
			if candidate, ok := convert(exact); ok && candidateMatches(candidate, source) {
				return []lyricsCandidate{candidate}, false, nil
			}
		}
		params.Del("duration")
		params.Del("album_name")
	} else {
		params = url.Values{"q": {query}}
	}
	var records []lrclibRecord
	if _, err := a.lyricsGET(ctx, "/api/search?"+params.Encode(), &records); err != nil {
		return nil, false, err
	}
	var result []lyricsCandidate
	for _, record := range records {
		if candidate, ok := convert(record); ok {
			result = append(result, candidate)
		}
	}
	return result, false, nil
}

func (a *App) qqCall(ctx context.Context, module, method string, params any, result any) error {
	comm := map[string]any{"ct": 11, "cv": "1003006", "v": "1003006", "tmeAppID": "qqmusiclight", "nettype": "NETWORK_WIFI", "udid": "0"}
	var guest struct {
		Session map[string]any `json:"session"`
	}
	// ponytail: use a fresh anonymous session per call; no credentials or durable
	// session state are needed for the small, rate-limited lyrics requests.
	if err := a.qqRequest(ctx, comm, "music.getSession.session", "GetSession", map[string]any{"caller": 0, "uid": "0", "vkey": 0}, &guest); err != nil {
		return err
	}
	for _, key := range []string{"uid", "sid", "userip"} {
		value, ok := guest.Session[key]
		if !ok || value == nil {
			return errors.New("QQ Music did not provide an anonymous session")
		}
		comm[key] = value
	}
	return a.qqRequest(ctx, comm, module, method, params, result)
}

func (a *App) qqRequest(ctx context.Context, comm map[string]any, module, method string, params any, result any) error {
	body, err := json.Marshal(map[string]any{"comm": comm,
		"request": map[string]any{"module": module, "method": method, "param": params}})
	if err != nil {
		return err
	}
	var response struct {
		Code    int `json:"code"`
		Request struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		} `json:"request"`
	}
	if _, err = a.lyricsRequest(ctx, "qq", "POST", "https://u.y.qq.com/cgi-bin/musicu.fcg", body, &response); err != nil {
		return err
	}
	if response.Code != 0 || response.Request.Code != 0 || len(response.Request.Data) == 0 {
		return fmt.Errorf("QQ Music rejected the lyrics request (codes %d/%d)", response.Code, response.Request.Code)
	}
	return json.Unmarshal(response.Request.Data, result)
}

func (a *App) qqCandidates(ctx context.Context, query string) ([]lyricsCandidate, error) {
	type song struct {
		ID       int64   `json:"id"`
		Mid      string  `json:"mid"`
		Title    string  `json:"title"`
		Name     string  `json:"name"`
		Subtitle string  `json:"subtitle"`
		Interval float64 `json:"interval"`
		Album    struct {
			Name string `json:"name"`
		} `json:"album"`
		Singer []struct {
			Name        string `json:"name"`
			Title       string `json:"title"`
			ForeignName string `json:"foreign_name"`
		} `json:"singer"`
	}
	var data struct {
		Body struct {
			Song struct {
				List []song `json:"list"`
			} `json:"song"`
			Items []song `json:"item_song"`
		} `json:"body"`
	}
	searchID := (rand.Int64N(20)+1)<<54 + rand.Int64N(4194305)<<32 + time.Now().UnixMilli()%86400000
	err := a.qqCall(ctx, "music.search.SearchCgiService", "DoSearchForQQMusicMobile", map[string]any{"query": query, "search_type": 0, "num_per_page": 8, "page_num": 1, "grp": 1,
		"search_id": strconv.FormatInt(searchID, 10), "remoteplace": "search.android.keyboard", "highlight": 0, "nqc_flag": 0, "page_id": 1}, &data)
	if err != nil {
		return nil, err
	}
	songs := data.Body.Song.List
	if len(songs) == 0 {
		songs = data.Body.Items
	}
	var result []lyricsCandidate
	for _, song := range songs {
		var artists []lyricsArtistCredit
		for _, artist := range song.Singer {
			artists = append(artists, lyricsArtistCredit{Name: artist.Name, Aliases: []string{artist.Title, artist.ForeignName}})
		}
		credit, aliases := lyricsArtistCredits(artists)
		title := song.Title
		if song.Subtitle != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(song.Subtitle)) {
			title += " (" + song.Subtitle + ")"
		}
		result = append(result, lyricsCandidate{Title: title, Artist: credit, Album: song.Album.Name, Duration: song.Interval, remoteID: strconv.FormatInt(song.ID, 10), mid: song.Mid, titleAliases: []string{song.Title, song.Name}, artistAliases: aliases})
	}
	return result, nil
}

func (a *App) kugouCandidates(ctx context.Context, query string) ([]lyricsCandidate, error) {
	params := url.Values{"keyword": {query}, "page": {"1"}, "pagesize": {"8"}, "showtype": {"14"}, "highlight": {""}, "version": {"9108"}}
	var response struct {
		Status int `json:"status"`
		Data   struct {
			Info []struct {
				ID       json.Number `json:"album_audio_id"`
				Hash     string      `json:"hash"`
				Title    string      `json:"songname"`
				Artist   string      `json:"singername"`
				Album    string      `json:"album_name"`
				Duration float64     `json:"duration"`
			} `json:"info"`
		} `json:"data"`
	}
	if _, err := a.lyricsRequest(ctx, "kugou", "GET", "https://mobiles.kugou.com/api/v3/search/song?"+params.Encode(), nil, &response); err != nil {
		return nil, err
	}
	if response.Status != 1 {
		return nil, errors.New("Kugou rejected the lyrics request")
	}
	var result []lyricsCandidate
	for _, song := range response.Data.Info {
		result = append(result, lyricsCandidate{Title: song.Title, Artist: song.Artist, Album: song.Album, Duration: song.Duration, remoteID: string(song.ID), hash: song.Hash})
	}
	return result, nil
}

func (a *App) kugouLyricsGET(ctx context.Context, path string, params url.Values, result any) (int, error) {
	params.Set("appid", "3116")
	params.Set("clientver", "11070")
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// The published client protocol uses MD5, not a password or stored user key.
	const key = "LnT6xpN3khm36zse0QzvmgTZ3waWdRSA"
	var signature strings.Builder
	signature.WriteString(key)
	for _, name := range keys {
		signature.WriteString(name + "=" + params.Get(name))
	}
	signature.WriteString(key)
	params.Set("signature", fmt.Sprintf("%x", md5.Sum([]byte(signature.String()))))
	return a.lyricsRequest(ctx, "kugou", "GET", "https://lyrics.kugou.com"+path+"?"+params.Encode(), nil, result)
}

func (a *App) candidateLyrics(ctx context.Context, candidate lyricsCandidate) (lyricsText, error) {
	if candidate.text != nil {
		return *candidate.text, nil
	}
	var text lyricsText
	switch candidate.Provider {
	case "qq":
		params := url.Values{"songmid": {candidate.mid}, "format": {"json"}, "nobase64": {"1"}, "g_tk": {"5381"}}
		var response struct {
			Code        int    `json:"retcode"`
			Lyric       string `json:"lyric"`
			Translation string `json:"trans"`
		}
		_, err := a.lyricsRequest(ctx, "qq", "GET", "https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg?"+params.Encode(), nil, &response)
		if err == nil && response.Code == 0 {
			text = lyricsText{Original: response.Lyric, Translation: response.Translation}
		}
		if _, valid := providerLRC(text.Original); valid != nil {
			var cloud struct {
				Lyric        string `json:"lyric"`
				Translation  string `json:"trans"`
				Romanization string `json:"roma"`
			}
			params := map[string]any{"songID": json.Number(candidate.remoteID), "songName": base64.StdEncoding.EncodeToString([]byte(candidate.Title)),
				"singerName": base64.StdEncoding.EncodeToString([]byte(candidate.Artist)), "albumName": base64.StdEncoding.EncodeToString([]byte(candidate.Album)),
				"interval": int(candidate.Duration), "crypt": 1, "qrc": 1, "qrc_t": 0, "lrc_t": 0, "trans": 1, "trans_t": 0, "roma": 1, "roma_t": 0, "type": 0, "ct": 19, "cv": 2111}
			if cloudErr := a.qqCall(ctx, "music.musichallSong.PlayLyricInfo", "GetPlayLyricInfo", params, &cloud); cloudErr != nil {
				return text, errors.Join(err, cloudErr)
			}
			var decodeErr error
			text.Original, decodeErr = decodeCloudQRC(cloud.Lyric)
			if decodeErr != nil {
				return text, decodeErr
			}
			text.Translation, _ = decodeCloudQRC(cloud.Translation)
			text.Romanization, _ = decodeCloudQRC(cloud.Romanization)
		}
	case "kugou":
		params := url.Values{"man": {"no"}, "keyword": {candidate.Artist + " - " + candidate.Title}, "duration": {strconv.FormatInt(int64(candidate.Duration*1000), 10)}, "hash": {candidate.hash}, "album_audio_id": {candidate.remoteID}, "lrctxt": {"1"}}
		var response struct {
			Status     int `json:"status"`
			Candidates []struct {
				ID        json.Number `json:"id"`
				AccessKey string      `json:"accesskey"`
				Duration  float64     `json:"duration"`
			} `json:"candidates"`
		}
		if _, err := a.kugouLyricsGET(ctx, "/v1/search", params, &response); err != nil {
			return text, err
		}
		if response.Status != 200 {
			return text, errors.New("Kugou rejected the lyrics request")
		}
		for _, lyric := range response.Candidates {
			if lyric.ID == "" || lyric.AccessKey == "" || math.Abs(lyric.Duration/1000-candidate.Duration) > 2 {
				continue
			}
			params = url.Values{"ver": {"1"}, "client": {"mobi"}, "id": {string(lyric.ID)}, "accesskey": {lyric.AccessKey}, "fmt": {"lrc"}, "charset": {"utf8"}}
			var data struct {
				Content string `json:"content"`
				Status  int    `json:"status"`
			}
			if _, err := a.kugouLyricsGET(ctx, "/download", params, &data); err != nil {
				return text, err
			}
			if data.Status != 200 {
				return text, errors.New("Kugou rejected the lyrics request")
			}
			decoded, err := base64.StdEncoding.DecodeString(data.Content)
			if err != nil {
				return text, errors.New("Kugou returned invalid lyrics data")
			}
			text.Original = string(decoded)
			break
		}
	case "netease":
		params := map[string]any{"id": json.Number(candidate.remoteID), "lv": "-1", "tv": "-1", "rv": "-1", "yv": "-1"}
		var response struct {
			Code int `json:"code"`
			LRC  struct {
				Lyric string `json:"lyric"`
			} `json:"lrc"`
			Translation struct {
				Lyric string `json:"lyric"`
			} `json:"tlyric"`
			Romanization struct {
				Lyric string `json:"lyric"`
			} `json:"romalrc"`
		}
		if err := a.neteaseCall(ctx, "/eapi/song/lyric/v1", params, &response); err != nil {
			return text, err
		}
		text = lyricsText{Original: response.LRC.Lyric, Translation: response.Translation.Lyric, Romanization: response.Romanization.Lyric}
	default:
		return text, errors.New("Unknown lyrics provider")
	}
	var err error
	if text.Original, err = providerLRC(text.Original); err != nil {
		return lyricsText{}, nil
	} // Plain lyrics never become a candidate file.
	text.Translation, _ = providerLRC(text.Translation)
	text.Romanization, _ = providerLRC(text.Romanization)
	return text, nil
}

func (a *App) lyricsRequest(ctx context.Context, provider, method, endpoint string, body []byte, target any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if provider == "qq" {
		request.Header.Set("Referer", "https://y.qq.com/")
		request.Header.Set("Cookie", "tmeLoginType=-1;")
	}
	response, raw, err := a.lyricsHTTPResponse(provider, request)
	if response == nil {
		return 0, err
	}
	if err == nil && response.StatusCode == 200 && json.Unmarshal(raw, target) != nil {
		err = fmt.Errorf("%s returned invalid or oversized lyrics data", provider)
	}
	return response.StatusCode, err
}

// Both JSON and NetEase's encrypted responses share request bounds, cancellation,
// redirect protection and per-provider rate limits. Response bodies are closed.
func (a *App) lyricsHTTPResponse(provider string, request *http.Request) (*http.Response, []byte, error) {
	ctx := request.Context()
	a.lyricsMu.Lock()
	if a.lyricsGates == nil {
		a.lyricsGates = map[string]chan struct{}{}
		a.lyricsRetries = map[string]time.Time{}
	}
	gate := a.lyricsGates[provider]
	if gate == nil {
		gate = make(chan struct{}, 1)
		a.lyricsGates[provider] = gate
	}
	a.lyricsMu.Unlock()
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	defer func() { <-gate }()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	a.lyricsMu.Lock()
	next := a.lyricsRetries[provider]
	if provider == "lrclib" {
		next = a.lyricsRetryAt
	}
	a.lyricsMu.Unlock()
	wait := time.Until(next)
	if wait > 2*time.Second {
		return nil, nil, fmt.Errorf("%s lyrics service is cooling down; retry later", provider)
	}
	if wait > 0 && !pause(ctx, wait) {
		return nil, nil, ctx.Err()
	}
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", "MusicForge/"+a.version+" (https://github.com/sagehou/MusicForge)")
	}
	request.Header.Set("Accept", "application/json")
	client := http.Client{Timeout: 10 * time.Second}
	if a.lyricsHTTP != nil {
		client = *a.lyricsHTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	next = time.Now().Add(800 * time.Millisecond)
	if err == nil && (response.StatusCode == 429 || response.StatusCode == 503) {
		delay := time.Minute
		if seconds, parseErr := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 32); parseErr == nil && seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		} else if until, parseErr := http.ParseTime(response.Header.Get("Retry-After")); parseErr == nil && time.Until(until) > 0 {
			delay = time.Until(until)
		}
		next = time.Now().Add(delay)
		if delay <= 24*time.Hour {
			next = next.Add(time.Duration(rand.Int64N(int64(delay/3) + 1)))
		}
	}
	a.lyricsMu.Lock()
	if a.lyricsRetries[provider].After(next) {
		next = a.lyricsRetries[provider]
	}
	a.lyricsRetries[provider] = next
	if provider == "lrclib" {
		a.lyricsRetryAt = next
	}
	a.lyricsMu.Unlock()
	if ctx.Err() != nil {
		if response != nil {
			response.Body.Close()
		}
		return nil, nil, ctx.Err()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%s lyrics request failed; check connectivity or retry later", provider)
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return response, nil, nil
	}
	if response.StatusCode != 200 {
		return response, nil, fmt.Errorf("%s lyrics service returned HTTP %d; retry later", provider, response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8*maxLyricsBytes+1))
	if ctx.Err() != nil {
		return response, nil, ctx.Err()
	}
	if err != nil || len(raw) > 8*maxLyricsBytes {
		return response, nil, fmt.Errorf("%s returned invalid or oversized lyrics data", provider)
	}
	return response, raw, nil
}

// A few web lyric endpoints still return base64 or HTML entities inside JSON.
func decodeWebLyrics(text string) string {
	if !strings.Contains(text, "[") {
		if decoded, err := base64.StdEncoding.DecodeString(text); err == nil {
			text = string(decoded)
		}
	}
	return html.UnescapeString(text)
}
