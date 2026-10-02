package forge

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// CI starts an official Navidrome container with a read-only output mount.
func TestRealNavidromeLibraryLifecycle(t *testing.T) {
	endpoint := os.Getenv("MUSICFORGE_TEST_NAV_URL")
	if endpoint == "" {
		t.Skip("real Navidrome service is provisioned by Actions")
	}
	a, s := testApp(t)
	s.Output = os.Getenv("MUSICFORGE_TEST_NAV_OUTPUT")
	s.NavURL = endpoint
	s.NavUser = "admin"
	s.NavPassword = "ci-only-musicforge-password"
	if err := a.initializeStorage(s); err != nil {
		t.Fatal(err)
	}
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	sourcePath := makeFLAC(t, a, s, "Acceptance/Album/01.flac", "MusicForge Acceptance", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	songs := func() int {
		t.Helper()
		salt := randomToken()
		hash := md5.Sum([]byte(s.NavPassword + salt))
		values := url.Values{"u": {s.NavUser}, "t": {hex.EncodeToString(hash[:])}, "s": {salt}, "v": {"1.16.1"}, "c": {"MusicForge-CI"}, "f": {"json"}, "query": {"MusicForge Acceptance"}}
		client := http.Client{Timeout: 10 * time.Second}
		response, err := client.Get(endpoint + "/rest/search3.view?" + values.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result struct {
			Response struct {
				Status string `json:"status"`
				Search struct {
					Songs []struct {
						Title string `json:"title"`
					} `json:"song"`
				} `json:"searchResult3"`
			} `json:"subsonic-response"`
		}
		if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if result.Response.Status != "ok" {
			t.Fatal("real Navidrome rejected search")
		}
		return len(result.Response.Search.Songs)
	}
	refresh := func() {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		for {
			err := a.refresh(context.Background())
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("real Navidrome scan did not complete", err)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	refresh()
	if count := songs(); count != 1 {
		t.Fatalf("Navidrome failed to discover converted track: %d", count)
	}
	source, err := a.sourceRel("Acceptance/Album/01.flac")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	refresh()
	if count := songs(); count != 1 {
		t.Fatal("ordinary source deletion removed playback", count)
	}
	if err = a.deleteExpired([]int64{source.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.Output, source.Output)); !os.IsNotExist(err) {
		t.Fatal("expired output retained", err)
	}
	refresh()
	if count := songs(); count != 0 {
		t.Fatal("Navidrome retained manually deleted artifact", count)
	}
}
