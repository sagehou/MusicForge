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
	return host == "musicbrainz.org" || host == "coverartarchive.org" || host == "archive.org" || strings.HasSuffix(host, ".archive.org")
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
	request.Header.Set("User-Agent", "MusicForge/0.2 (https://github.com/sagehou/MusicForge)")
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

func (a *App) searchArtwork(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query string `json:"query"`
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
	a.artworkMu.Lock()
	if time.Since(a.artworkSearched) < time.Second {
		a.artworkMu.Unlock()
		apiError(w, 429, errors.New("Wait a second before searching for covers again"))
		return
	}
	a.artworkSearched = time.Now()
	a.artworkMu.Unlock()
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
	if err != nil || id < 1 || !releaseID.MatchString(release) {
		apiError(w, 400, errors.New("Invalid cover candidate"))
		return
	}
	if _, err = a.source(id); err != nil {
		apiError(w, 404, err)
		return
	}
	data, err := fetchCoverData(r.Context(), "https://coverartarchive.org/release/"+release+"/front-1200", 8*1024*1024)
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
	respond(w, 200, map[string]any{"image": base64.StdEncoding.EncodeToString(canonical), "width": config.Width, "height": config.Height, "source": "MusicBrainz / Cover Art Archive", "url": "https://musicbrainz.org/release/" + release})
}
