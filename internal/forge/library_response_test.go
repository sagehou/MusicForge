package forge

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLibraryConditionalAndCompressedResponse(t *testing.T) {
	list := make([]Source, 5000)
	for i := range list {
		list[i] = Source{ID: int64(i + 1), Rel: fmt.Sprintf("Artist/Album/%04d.flac", i), Status: "ready", Metadata: map[string]string{"albumartist": "Artist"}}
	}
	request := httptest.NewRequest("GET", "/api/library", nil)
	plain := httptest.NewRecorder()
	respondLibrary(plain, request, list)
	if plain.Code != 200 || plain.Header().Get("ETag") == "" || plain.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatal("invalid library response", plain.Code, plain.Header())
	}
	request.Header.Set("Accept-Encoding", "gzip")
	compressed := httptest.NewRecorder()
	respondLibrary(compressed, request, list)
	compressedBytes := compressed.Body.Len()
	reader, err := gzip.NewReader(compressed.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(decoded) != plain.Body.String() || compressed.Header().Get("Content-Encoding") != "gzip" || compressedBytes >= plain.Body.Len()/4 {
		t.Fatal("compression changed data or failed to reduce the library payload", err)
	}
	t.Logf("5000 tracks: JSON %d bytes, gzip %s bytes", plain.Body.Len(), compressed.Header().Get("Content-Length"))
	request.Header.Set("If-None-Match", plain.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	respondLibrary(cached, request, list)
	if cached.Code != 304 || cached.Body.Len() != 0 {
		t.Fatal("unchanged library transferred again", cached.Code)
	}
	list[0].Status = "failed"
	list[0].Error = "Synthetic diagnostic"
	changed := httptest.NewRecorder()
	respondLibrary(changed, request, list)
	if changed.Code != 200 || changed.Header().Get("ETag") == plain.Header().Get("ETag") {
		t.Fatal("status change was hidden by cache")
	}
	for _, encoding := range []string{"gzip;q=0", "br", "gzip;q=bad", "gzip;q=2"} {
		request.Header.Set("If-None-Match", "")
		request.Header.Set("Accept-Encoding", encoding)
		w := httptest.NewRecorder()
		respondLibrary(w, request, list)
		var received []Source
		if w.Header().Get("Content-Encoding") != "" || json.Unmarshal(w.Body.Bytes(), &received) != nil || len(received) != len(list) {
			t.Fatal("ignored encoding preference", encoding)
		}
	}
	if !acceptsLibraryGzip("br, gzip;q=0.5") || !strings.Contains(plain.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatal("encoding negotiation")
	}
}
