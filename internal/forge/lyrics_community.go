package forge

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strconv"
)

func communityLyricsID(id json.Number) bool {
	value, err := strconv.ParseUint(string(id), 10, 53)
	return err == nil && value > 0
}

func (a *App) amllCandidates(ctx context.Context, query string) ([]lyricsCandidate, error) {
	var records []struct {
		ID     json.Number `json:"id"`
		Title  string      `json:"trackName"`
		Artist string      `json:"artistName"`
		Album  string      `json:"albumName"`
		Synced string      `json:"syncedLyrics"`
	}
	params := url.Values{"q": {query}, "pageSize": {"8"}}
	code, err := a.lyricsRequest(ctx, "amll", "GET", "https://api.amll.dev/v1/lrclib/search?"+params.Encode(), nil, &records)
	if err != nil || code == 404 {
		return nil, err
	}
	if records == nil {
		return nil, errors.New("amll returned invalid or oversized lyrics data")
	}
	var candidates []lyricsCandidate
	for _, record := range records {
		text, err := providerLRC(record.Synced)
		if !communityLyricsID(record.ID) || err != nil {
			continue
		}
		// AMLL's duration is the final lyric timestamp, not the audio length.
		// Leave it unknown: these candidates require explicit preview and choice.
		candidates = append(candidates, lyricsCandidate{Provider: "amll", remoteID: string(record.ID), Title: record.Title,
			Artist: record.Artist, Album: record.Album, text: &lyricsText{Original: text}})
	}
	return candidates, nil
}

type unisonLyricsRecord struct {
	ID       json.Number     `json:"id"`
	Title    string          `json:"song"`
	Artist   string          `json:"artist"`
	Album    string          `json:"album"`
	Duration json.RawMessage `json:"duration"`
	Format   string          `json:"format"`
	SyncType string          `json:"syncType"`
	Lyrics   string          `json:"lyrics"`
}

func (record unisonLyricsRecord) synchronized() bool {
	return (record.Format == "lrc" || record.Format == "ttml") && (record.SyncType == "linesync" || record.SyncType == "richsync")
}

func (a *App) unisonCandidates(ctx context.Context, query string) ([]lyricsCandidate, error) {
	var response struct {
		Success bool                 `json:"success"`
		Data    []unisonLyricsRecord `json:"data"`
	}
	params := url.Values{"q": {query}, "limit": {"8"}}
	code, err := a.lyricsRequest(ctx, "unison", "GET", "https://unison.boidu.dev/lyrics/search?"+params.Encode(), nil, &response)
	if err != nil || code == 404 {
		return nil, err
	}
	if !response.Success || response.Data == nil {
		return nil, errors.New("unison returned invalid or oversized lyrics data")
	}
	var candidates []lyricsCandidate
	for _, record := range response.Data {
		if communityLyricsID(record.ID) && record.synchronized() {
			candidates = append(candidates, lyricsCandidate{Provider: "unison", remoteID: string(record.ID), Title: record.Title,
				Artist: record.Artist, Album: record.Album, Duration: lyricsProviderSeconds(record.Duration)})
		}
	}
	return candidates, nil
}

func (a *App) unisonLyrics(ctx context.Context, candidate lyricsCandidate) (lyricsText, error) {
	if !communityLyricsID(json.Number(candidate.remoteID)) {
		return lyricsText{}, errors.New("unison returned invalid or oversized lyrics data")
	}
	var response struct {
		Success bool               `json:"success"`
		Data    unisonLyricsRecord `json:"data"`
	}
	code, err := a.lyricsRequest(ctx, "unison", "GET", "https://unison.boidu.dev/lyrics/"+candidate.remoteID, nil, &response)
	if err != nil || code == 404 {
		return lyricsText{}, err
	}
	record := response.Data
	duration := lyricsProviderSeconds(record.Duration)
	if !response.Success || string(record.ID) != candidate.remoteID || !sameLyricsTag(record.Title, candidate.Title) ||
		!sameLyricsArtist(record.Artist, candidate.Artist) || record.Album != "" && candidate.Album != "" && !sameLyricsTag(record.Album, candidate.Album) ||
		duration > 0 && candidate.Duration > 0 && math.Abs(duration-candidate.Duration) > 2 {
		return lyricsText{}, errors.New("unison returned invalid or oversized lyrics data")
	}
	diagnostic := &lyricsDiagnostic{}
	diagnostic.add("lyric", record.Lyrics)
	if !record.synchronized() {
		return diagnostic.failed(errors.New("No synchronized LRC lines found")), nil
	}
	var text string
	if record.Format == "ttml" {
		text, err = ttmlLRC(record.Lyrics)
	} else {
		text, err = providerLRC(record.Lyrics)
	}
	if err != nil {
		return diagnostic.failed(err), nil
	}
	return lyricsText{Original: text}, nil
}
