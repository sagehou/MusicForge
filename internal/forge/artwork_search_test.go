package forge

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnlineCoversUseFixedProvidersWithoutPublishingPreviews(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	request := lyricsSession(t, a)
	cover := filepath.Join(t.TempDir(), "cover.jpg")
	writeCover(t, cover)
	jpeg, err := os.ReadFile(cover)
	if err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	const id = "12345678-abcd-abcd-abcd-123456789abc"
	http.DefaultTransport = lyricsTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("User-Agent") == "" {
			t.Error("provider credentials or missing identification")
		}
		data := jpeg
		switch r.URL.Host {
		case "musicbrainz.org":
			data = []byte(`{"releases":[{"id":"` + id + `","title":"Album","date":"2026","artist-credit":[{"name":"Artist"}]}]}`)
		case "coverartarchive.org":
			if r.URL.Path != "/release/"+id+"/front-1200" {
				t.Error("invalid candidate path", r.URL.Path)
			}
		default:
			t.Error("unexpected cover host", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}, Request: r}, nil
	})
	w := request("POST", "/api/library/1/artwork/search", map[string]string{"query": "Artist Album", "provider": "musicbrainz"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), id) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "/api/library/1/artwork/preview?release="+id, nil)
	var preview struct {
		Image         string
		Width, Height int
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.Width != 32 || preview.Height != 32 || preview.Image == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = os.Stat(filepath.Join(s.Output, "Album/cover.jpg")); !os.IsNotExist(err) {
		t.Fatal("preview published a cover", err)
	}
	for _, endpoint := range []string{"http://archive.org/a", "https://archive.org.evil.test/a", "https://127.0.0.1/a", "https://musicbrainz.org:8443/a", "https://user:password@musicbrainz.org/a"} {
		u, _ := url.Parse(endpoint)
		if coverURLAllowed(u) {
			t.Fatal("unsafe provider", endpoint)
		}
	}
	if w = request("GET", "/api/library/1/artwork/preview?release=https://evil.test/a", nil); w.Code != 400 {
		t.Fatal("accepted arbitrary candidate URL", w.Code)
	}
	http.DefaultTransport = lyricsTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.test/cover.jpg"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	w = request("GET", "/api/library/1/artwork/preview?release="+id, nil)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "Unsupported cover provider redirect") {
		t.Fatal("followed unsafe redirect", w.Code, w.Body.String())
	}
}

func TestCatalogArtworkSourcesPreviewOnlyRecentTrustedSearchImages(t *testing.T) {
	for _, provider := range []string{"itunes", "netease"} {
		t.Run(provider, func(t *testing.T) {
			a, s := testApp(t)
			seedLyricsArtifact(t, a, s)
			request := lyricsSession(t, a)
			cover := filepath.Join(t.TempDir(), "cover.jpg")
			writeCover(t, cover)
			jpeg, err := os.ReadFile(cover)
			if err != nil {
				t.Fatal(err)
			}
			previous := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = previous })
			images := 0
			http.DefaultTransport = lyricsTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.Scheme != "https" {
					t.Fatal("credentials or insecure request", r.URL)
				}
				var data []byte
				switch r.URL.Host {
				case "itunes.apple.com":
					if r.URL.Path != "/search" || r.URL.Query().Get("term") != "Artist Album" || r.URL.Query().Get("entity") != "album" {
						t.Error("incorrect album search", r.URL)
					}
					data = []byte(`{"results":[{"collectionId":123,"collectionName":"Album","artistName":"Artist","artworkUrl100":"http://is1-ssl.mzstatic.com/image/thumb/art/100x100bb.jpg"},{"collectionId":456,"artworkUrl100":"https://evil.test/cover.jpg"}]}`)
				case "music.163.com":
					album := `{"id":123,"name":"Album","artists":[{"name":"Artist"}],"picUrl":"http://p1.music.126.net/art.jpg"}`
					if r.URL.Path == "/api/search/get" {
						if r.URL.Query().Get("s") != "Artist Album" || r.URL.Query().Get("type") != "10" {
							t.Error("incorrect album search", r.URL)
						}
						data = []byte(`{"code":200,"result":{"albums":[` + album + `,{"id":456,"picUrl":"https://evil.test/cover.jpg"}]}}`)
					} else {
						t.Fatal("unexpected album lookup", r.URL)
					}
				case "is1-ssl.mzstatic.com", "p1.music.126.net":
					if strings.Contains(r.URL.Path, "100x100") {
						t.Fatal("downloaded a thumbnail instead of the large cover")
					}
					images++
					data = jpeg
				default:
					t.Fatal("unexpected provider", r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}, Request: r}, nil
			})
			w := request("POST", "/api/library/1/artwork/search", map[string]string{"query": "Artist Album", "provider": provider})
			var result struct {
				Candidates []artworkCandidate `json:"candidates"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Candidates) != 1 || result.Candidates[0].ID != provider+":123" || images != 0 {
				t.Fatal("search did not filter unavailable images", w.Code, w.Body.String())
			}
			w = request("POST", "/api/library/1/artwork/search", map[string]string{"query": "Artist Album", "provider": provider})
			if w.Code != 429 {
				t.Fatal("source rate limit missing", w.Code)
			}
			other := "itunes"
			if provider == other {
				other = "netease"
			}
			w = request("POST", "/api/library/1/artwork/search", map[string]string{"query": "Artist Album", "provider": other})
			if w.Code != 200 {
				t.Fatal("switching sources was rate limited", w.Code, w.Body.String())
			}
			w = request("GET", "/api/library/1/artwork/preview?release="+url.QueryEscape(result.Candidates[0].ID), nil)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"width":32`) || images != 1 {
				t.Fatal("preview failed", w.Code, w.Body.String())
			}
			w = request("GET", "/api/library/1/artwork/preview?release=itunes:999", nil)
			if w.Code != 409 || images != 1 {
				t.Fatal("preview accepted a catalog ID outside this search", w.Code, w.Body.String())
			}
			for _, id := range []string{"itunes:https://evil.test/a", "netease:../123", "netease:00123", "other:123"} {
				if w = request("GET", "/api/library/1/artwork/preview?release="+url.QueryEscape(id), nil); w.Code != 400 {
					t.Fatal("accepted invalid catalog ID", id, w.Code)
				}
			}
			if _, err = os.Stat(filepath.Join(s.Output, "Album/cover.jpg")); !os.IsNotExist(err) {
				t.Fatal("catalog preview published an image", err)
			}
		})
	}
}
