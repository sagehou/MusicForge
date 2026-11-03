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
	a, s := testApp(t); seedLyricsArtifact(t, a, s); request := lyricsSession(t, a)
	cover := filepath.Join(t.TempDir(), "cover.jpg"); writeCover(t, cover)
	jpeg, err := os.ReadFile(cover); if err != nil { t.Fatal(err) }
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	const id = "12345678-abcd-abcd-abcd-123456789abc"
	http.DefaultTransport = lyricsTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("User-Agent") == "" { t.Error("provider credentials or missing identification") }
		data := jpeg
		switch r.URL.Host {
		case "musicbrainz.org": data = []byte(`{"releases":[{"id":"`+id+`","title":"Album","date":"2026","artist-credit":[{"name":"Artist"}]}]}`)
		case "coverartarchive.org": if r.URL.Path != "/release/"+id+"/front-1200" { t.Error("invalid candidate path", r.URL.Path) }
		default: t.Error("unexpected cover host", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}, Request: r}, nil
	})
	w := request("POST", "/api/library/1/artwork/search", map[string]string{"query":"Artist Album"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), id) { t.Fatal(w.Code, w.Body.String()) }
	w = request("GET", "/api/library/1/artwork/preview?release="+id, nil)
	var preview struct { Image string; Width, Height int }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.Width != 32 || preview.Height != 32 || preview.Image == "" { t.Fatal(w.Code, w.Body.String()) }
	if _, err = os.Stat(filepath.Join(s.Output,"Album/cover.jpg")); !os.IsNotExist(err) { t.Fatal("preview published a cover", err) }
	for _, endpoint := range []string{"http://archive.org/a", "https://archive.org.evil.test/a", "https://127.0.0.1/a", "https://musicbrainz.org:8443/a", "https://user:password@musicbrainz.org/a"} {
		u, _ := url.Parse(endpoint); if coverURLAllowed(u) { t.Fatal("unsafe provider", endpoint) }
	}
	if w = request("GET", "/api/library/1/artwork/preview?release=https://evil.test/a", nil); w.Code != 400 { t.Fatal("accepted arbitrary candidate URL", w.Code) }
	http.DefaultTransport = lyricsTransport(func(r *http.Request) (*http.Response, error) { return &http.Response{StatusCode:302,Header:http.Header{"Location":[]string{"https://evil.test/cover.jpg"}},Body:io.NopCloser(strings.NewReader("")),Request:r},nil })
	w = request("GET", "/api/library/1/artwork/preview?release="+id, nil)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "Unsupported cover provider redirect") { t.Fatal("followed unsafe redirect", w.Code, w.Body.String()) }
}
