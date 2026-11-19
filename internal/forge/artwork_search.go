package forge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var releaseID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func coverURLAllowed(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "itunes.apple.com" || strings.HasSuffix(host, ".mzstatic.com") || host == "music.163.com" ||
		host == "p1.music.126.net" || host == "p2.music.126.net" || host == "p3.music.126.net" || host == "p4.music.126.net" ||
		host == "musicbrainz.org" || host == "coverartarchive.org" || host == "archive.org" || strings.HasSuffix(host, ".archive.org")
}

func fetchCoverData(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	u, err := url.Parse(endpoint)
	if err != nil || !coverURLAllowed(u) {
		return nil, errors.New("Unsupported cover provider URL")
	}
	request, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "MusicForge (https://github.com/sagehou/MusicForge)")
	if u.Hostname() == "music.163.com" {
		request.Header.Set("Referer", "https://music.163.com/")
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 4 || !coverURLAllowed(r.URL) {
			return errors.New("Unsupported cover provider redirect")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("Cover provider returned HTTP %d", response.StatusCode)
	}
	return readLimited(response.Body, limit)
}

type artworkSearchChoice struct {
	Created time.Time
	Image   string
	Source  string
	URL     string
}

type artworkCandidate struct {
	ID      string `json:"id"`
	Image   string `json:"-"`
	Title   string `json:"title"`
	Artist  string `json:"artist"`
	Date    string `json:"date"`
	Country string `json:"country"`
	Source  string `json:"source"`
	URL     string `json:"url"`
}

type appleArtworkResult struct {
	Results []struct {
		ID      int64  `json:"collectionId"`
		Title   string `json:"collectionName"`
		Artist  string `json:"artistName"`
		Date    string `json:"releaseDate"`
		Country string `json:"country"`
		Image   string `json:"artworkUrl100"`
	} `json:"results"`
}

type neteaseArtworkAlbum struct {
	ID      int64  `json:"id"`
	Title   string `json:"name"`
	Image   string `json:"picUrl"`
	Date    int64  `json:"publishTime"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
}

func artworkImageURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	// Album APIs still return HTTP CDN links; always download over HTTPS.
	if u.Scheme == "http" {
		u.Scheme = "https"
	}
	host := strings.ToLower(u.Hostname())
	if !coverURLAllowed(u) || !(strings.HasSuffix(host, ".mzstatic.com") || host == "p1.music.126.net" || host == "p2.music.126.net" || host == "p3.music.126.net" || host == "p4.music.126.net") {
		return ""
	}
	return strings.Replace(u.String(), "/100x100bb.jpg", "/1200x1200bb.jpg", 1)
}

func searchCatalogArtwork(ctx context.Context, provider, query string) ([]artworkCandidate, error) {
	candidates := []artworkCandidate{}
	if provider == "itunes" {
		data, err := fetchCoverData(ctx, "https://itunes.apple.com/search?"+url.Values{"term": {query}, "entity": {"album"}, "limit": {"12"}}.Encode(), 2*1024*1024)
		if err != nil {
			return nil, err
		}
		var result appleArtworkResult
		if err = json.Unmarshal(data, &result); err != nil {
			return nil, errors.New("Cover provider returned invalid data")
		}
		for _, album := range result.Results {
			if album.ID < 1 || artworkImageURL(album.Image) == "" {
				continue
			}
			id := strconv.FormatInt(album.ID, 10)
			candidates = append(candidates, artworkCandidate{ID: "itunes:" + id, Image: artworkImageURL(album.Image), Title: album.Title, Artist: album.Artist, Date: strings.Split(album.Date, "T")[0], Country: album.Country, Source: "Apple Music / iTunes", URL: "https://music.apple.com/album/" + id})
		}
		return candidates, nil
	}
	data, err := fetchCoverData(ctx, "https://music.163.com/api/search/get?"+url.Values{"s": {query}, "type": {"10"}, "limit": {"12"}, "offset": {"0"}}.Encode(), 2*1024*1024)
	if err != nil {
		return nil, err
	}
	var result struct {
		Code   int `json:"code"`
		Result struct {
			Albums []neteaseArtworkAlbum `json:"albums"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &result) != nil || result.Code != 200 {
		return nil, errors.New("Cover provider returned invalid data")
	}
	for _, album := range result.Result.Albums {
		if album.ID < 1 || artworkImageURL(album.Image) == "" {
			continue
		}
		artists := []string{}
		for _, artist := range album.Artists {
			artists = append(artists, artist.Name)
		}
		date := ""
		if album.Date > 0 {
			date = time.UnixMilli(album.Date).UTC().Format("2006-01-02")
		}
		id := strconv.FormatInt(album.ID, 10)
		candidates = append(candidates, artworkCandidate{ID: "netease:" + id, Image: artworkImageURL(album.Image), Title: album.Title, Artist: strings.Join(artists, ", "), Date: date, Source: "NetEase Music", URL: "https://music.163.com/album?id=" + id})
	}
	return candidates, nil
}

func (a *App) searchArtwork(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query    string `json:"query"`
		Provider string `json:"provider"`
	}
	if !decode(w, r, &body) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 400, errors.New("invalid source ID"))
		return
	}
	if _, err = a.source(id); err != nil {
		apiError(w, 404, err)
		return
	}
	body.Query = strings.TrimSpace(body.Query)
	if body.Query == "" || len(body.Query) > 500 {
		apiError(w, 400, errors.New("Enter an artist and album, at most 500 bytes"))
		return
	}
	if body.Provider == "" {
		body.Provider = "itunes"
	}
	if body.Provider != "itunes" && body.Provider != "netease" && body.Provider != "musicbrainz" {
		apiError(w, 400, errors.New("Unsupported cover provider"))
		return
	}
	a.artworkMu.Lock()
	if a.artworkSearched == nil {
		a.artworkSearched = make(map[string]time.Time)
	}
	if time.Since(a.artworkSearched[body.Provider]) < time.Second {
		a.artworkMu.Unlock()
		apiError(w, 429, errors.New("Wait a second before searching for covers again"))
		return
	}
	a.artworkSearched[body.Provider] = time.Now()
	a.artworkMu.Unlock()
	if body.Provider != "musicbrainz" {
		candidates, err := searchCatalogArtwork(r.Context(), body.Provider, body.Query)
		if err != nil {
			apiError(w, 502, err)
			return
		}
		a.artworkMu.Lock()
		if a.artworkCandidates == nil {
			a.artworkCandidates = make(map[string]artworkSearchChoice)
		}
		for key, choice := range a.artworkCandidates {
			if time.Since(choice.Created) > 10*time.Minute {
				delete(a.artworkCandidates, key)
			}
		}
		for _, candidate := range candidates {
			a.artworkCandidates[strconv.FormatInt(id, 10)+":"+candidate.ID] = artworkSearchChoice{Created: time.Now(), Image: candidate.Image, Source: candidate.Source, URL: candidate.URL}
		}
		a.artworkMu.Unlock()
		respond(w, 200, map[string]any{"candidates": candidates})
		return
	}
	data, err := fetchCoverData(r.Context(), "https://musicbrainz.org/ws/2/release/?"+url.Values{"query": {body.Query}, "fmt": {"json"}, "limit": {"8"}}.Encode(), 2*1024*1024)
	if err != nil {
		apiError(w, 502, err)
		return
	}
	var result struct {
		Releases []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Date    string `json:"date"`
			Country string `json:"country"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artist-credit"`
		} `json:"releases"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		apiError(w, 502, errors.New("Cover provider returned invalid data"))
		return
	}
	candidates := []map[string]string{}
	for _, release := range result.Releases {
		if !releaseID.MatchString(release.ID) {
			continue
		}
		artists := []string{}
		for _, artist := range release.Artists {
			artists = append(artists, artist.Name)
		}
		candidates = append(candidates, map[string]string{"id": release.ID, "title": release.Title, "artist": strings.Join(artists, ", "), "date": release.Date, "country": release.Country, "source": "MusicBrainz / Cover Art Archive", "url": "https://musicbrainz.org/release/" + release.ID})
	}
	respond(w, 200, map[string]any{"candidates": candidates})
}

func (a *App) previewArtwork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	release := r.URL.Query().Get("release")
	provider, catalogID, catalog := strings.Cut(release, ":")
	numericID, numericErr := strconv.ParseInt(catalogID, 10, 64)
	if err != nil || id < 1 || !releaseID.MatchString(release) && (!catalog || provider != "itunes" && provider != "netease" || numericErr != nil || numericID < 1 || strconv.FormatInt(numericID, 10) != catalogID) {
		apiError(w, 400, errors.New("Invalid cover candidate"))
		return
	}
	if _, err = a.source(id); err != nil {
		apiError(w, 404, err)
		return
	}
	endpoint, source, sourceURL := "https://coverartarchive.org/release/"+release+"/front-1200", "MusicBrainz / Cover Art Archive", "https://musicbrainz.org/release/"+release
	if catalog {
		a.artworkMu.Lock()
		choice, ok := a.artworkCandidates[strconv.FormatInt(id, 10)+":"+release]
		a.artworkMu.Unlock()
		if !ok || time.Since(choice.Created) > 10*time.Minute {
			apiError(w, 409, errors.New("Cover candidate is no longer available; search again"))
			return
		}
		endpoint, source, sourceURL = choice.Image, choice.Source, choice.URL
	}
	data, err := fetchCoverData(r.Context(), endpoint, 8*1024*1024)
	if err != nil {
		apiError(w, 502, err)
		return
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		apiError(w, 502, errors.New("Cover candidate is not a supported image"))
		return
	}
	canonical, err := canonicalArtwork(data)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	respond(w, 200, map[string]any{"image": base64.StdEncoding.EncodeToString(canonical), "width": config.Width, "height": config.Height, "source": source, "url": sourceURL})
}
