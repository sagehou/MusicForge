package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func testApp(t *testing.T) (*App, Settings) {
	t.Helper()
	root := t.TempDir()
	a, err := New(Runtime{ConfigDir: filepath.Join(root, "config"), FFmpeg: "ffmpeg", FFprobe: "ffprobe"}, slog.New(slog.NewTextHandler(io.Discard, nil)), "test", fstest.MapFS{"index.html": {Data: []byte("MusicForge")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	s := DefaultSettings()
	s.Source = filepath.Join(root, "source")
	s.Output = filepath.Join(root, "output")
	s.Enabled = true
	for _, dir := range []string{s.Source, s.Output} {
		if err = os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.initializeStorage(s); err != nil {
		t.Fatal(err)
	}
	if err = a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	return a, s
}
func makeFLAC(t *testing.T, a *App, s Settings, rel, title string, embedded bool) string {
	t.Helper()
	path := filepath.Join(s.Source, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	args := []string{"-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=2"}
	if embedded {
		cover := filepath.Join(t.TempDir(), "cover.jpg")
		writeCover(t, cover)
		args = append(args, "-i", cover, "-map", "0:a", "-map", "1:v", "-c:v", "copy", "-disposition:v", "attached_pic")
	}
	args = append(args, "-c:a", "flac", "-metadata", "artist=Test Artist", "-metadata", "album=Test Album", "-metadata", "title="+title, "-metadata", "track=1", "-metadata", "disc=1", "-metadata", "REPLAYGAIN_TRACK_GAIN=-6.00 dB", path)
	if _, err := runTool(context.Background(), a.cfg.FFmpeg, args...); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}
func writeCover(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for x := 0; x < 32; x++ {
		for y := 0; y < 32; y++ {
			img.Set(x, y, color.RGBA{70, 210, 160, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
	f.Close()
}
func drain(t *testing.T, a *App, convert bool) {
	t.Helper()
	for i := 0; i < 50; i++ {
		j, err := a.claim(convert)
		if err != nil {
			return
		}
		if err = a.execute(context.Background(), j); err != nil {
			t.Fatalf("job %d %s: %v", j.ID, j.Kind, err)
		}
		if _, err = a.db.Exec("UPDATE jobs SET state='success' WHERE id=?", j.ID); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("queue did not drain")
}

func TestEncodingLifecycle(t *testing.T) {
	for _, e := range []Encoding{{"opus", "vbr", 192, 2}, {"opus", "cbr", 192, 2}, {"mp3", "vbr", 192, 2}, {"mp3", "cbr", 192, 2}} {
		t.Run(e.Codec+e.Mode, func(t *testing.T) {
			a, s := testApp(t)
			s.Encoding = e
			if err := a.saveSettings(s); err != nil {
				t.Fatal(err)
			}
			makeFLAC(t, a, s, "Artist/Album/01.flac", "Track", true)
			if err := a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			drain(t, a, true)
			source, err := a.sourceRel("Artist/Album/01.flac")
			if err != nil {
				t.Fatal(err)
			}
			p, err := a.probe(context.Background(), filepath.Join(s.Output, source.Output))
			if err != nil {
				t.Fatal(err)
			}
			tags := p.tags()
			if tags["title"] != "Track" || tags["replaygain_track_gain"] != "-6.00 dB" {
				t.Fatalf("lost tags: %#v", tags)
			}
			for _, stream := range p.Streams {
				if stream.Type == "video" {
					t.Fatal("embedded cover duplicated")
				}
			}
			if _, err = os.Stat(filepath.Join(s.Output, "Artist/Album/cover.jpg")); err != nil {
				t.Fatal("album artwork missing:", err)
			}
			var before int
			_ = a.db.QueryRow("SELECT count(*) FROM jobs WHERE kind='convert'").Scan(&before)
			if err = a.scan(context.Background(), ScanRequest{}); err != nil {
				t.Fatal(err)
			}
			var after int
			_ = a.db.QueryRow("SELECT count(*) FROM jobs WHERE kind='convert'").Scan(&after)
			if before != after {
				t.Fatal("unchanged scan re-encoded track")
			}
		})
	}
}

func TestMoveRebuildAndExpiration(t *testing.T) {
	a, s := testApp(t)
	old := makeFLAC(t, a, s, "Artist/Album/01.flac", "Original", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	source, _ := a.sourceRel("Artist/Album/01.flac")
	output := filepath.Join(s.Output, source.Output)
	hash, _ := fileHash(context.Background(), output)
	newPath := filepath.Join(s.Source, "Artist/Album/renamed.flac")
	if err := os.Rename(old, newPath); err != nil {
		t.Fatal(err)
	}
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, false)
	moved, _ := a.source(source.ID)
	movedHash, _ := fileHash(context.Background(), filepath.Join(s.Output, moved.Output))
	if hash != movedHash || !strings.Contains(moved.Output, "renamed.opus") {
		t.Fatal("rename did not reuse artifact")
	}
	s.Encoding = Encoding{"mp3", "vbr", 192, 2}
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	var pending int
	_ = a.db.QueryRow("SELECT count(*) FROM jobs WHERE kind='convert' AND state='pending'").Scan(&pending)
	if pending != 0 {
		t.Fatal("profile change automatically queued rebuild")
	}
	if _, err := a.queueBuild(moved, s.Encoding, false, true); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	rebuilt, _ := a.source(source.ID)
	if filepath.Ext(rebuilt.Output) != ".mp3" {
		t.Fatal("codec rebuild failed")
	}
	if _, err := os.Stat(filepath.Join(s.Output, moved.Output)); !os.IsNotExist(err) {
		t.Fatal("obsolete codec artifact retained")
	}
	if err := os.Remove(newPath); err != nil {
		t.Fatal(err)
	}
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	expired, _ := a.source(source.ID)
	if expired.Present || !expired.OutputPresent {
		t.Fatal("ordinary deletion did not preserve output")
	}
	if err := a.deleteExpired([]int64{source.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Output, rebuilt.Output)); !os.IsNotExist(err) {
		t.Fatal("manual deletion did not remove output")
	}
}

func TestOwnershipOfflineAndFailedDedup(t *testing.T) {
	a, s := testApp(t)
	makeFLAC(t, a, s, "track.flac", "Track", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	conflict := filepath.Join(s.Output, "track.opus")
	if err := os.WriteFile(conflict, []byte("unregistered"), 0600); err != nil {
		t.Fatal(err)
	}
	j, err := a.claim(true)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.execute(context.Background(), j); err == nil {
		t.Fatal("overwrote unregistered output")
	}
	b, _ := os.ReadFile(conflict)
	if string(b) != "unregistered" {
		t.Fatal("foreign file changed")
	}
	_, _ = a.db.Exec("UPDATE jobs SET state='failed',attempts=3 WHERE id=?", j.ID)
	if err = a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	var pending int
	_ = a.db.QueryRow("SELECT count(*) FROM jobs WHERE kind='convert' AND state='pending'").Scan(&pending)
	if pending != 0 {
		t.Fatal("failed target was automatically reset")
	}
	if err = os.Rename(s.Source, s.Source+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(s.Source, 0755); err != nil {
		t.Fatal(err)
	}
	if err = a.scan(context.Background(), ScanRequest{}); err == nil {
		t.Fatal("disappeared mount treated as empty library")
	}
	source, _ := a.sourceRel("track.flac")
	if !source.Present {
		t.Fatal("offline storage expired files")
	}
}

func TestAuthenticationAndWebhook(t *testing.T) {
	a, s := testApp(t)
	handler := a.Handler()
	send := func(method, path string, body any, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		r.Header.Set("X-CSRF-Token", csrf)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := send("GET", "/api/library", nil, nil, "", ""); w.Code != 401 {
		t.Fatal("library not protected")
	}
	r := httptest.NewRequest("GET", "/api/library", nil)
	r.Header.Set("X-Forwarded-User", "admin")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("trusted spoofed auth headers")
	}
	w = send("POST", "/api/auth/setup", map[string]string{"code": a.bootstrap, "username": "admin", "password": "long-test-password"}, nil, "", "")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	csrf := digest(cookie.Value + ":csrf")
	if w = send("POST", "/api/library/scan", map[string]any{}, cookie, "", ""); w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	if w = send("POST", "/api/library/scan", map[string]any{}, cookie, csrf, "https://evil.example"); w.Code != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
	if w = send("POST", "/api/library/scan", map[string]any{}, cookie, csrf, ""); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	if w = send("POST", "/api/auth/setup", map[string]string{"code": "expired", "username": "other", "password": "long-test-password"}, nil, "", ""); w.Code != 403 {
		t.Fatal("setup remained open")
	}
	s.WebhookHash = digest("separate-integration-secret")
	s.NavPassword = "never-return-this"
	s.OIDCSecret = "nor-this"
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	w = send("GET", "/api/settings", nil, cookie, "", "")
	if strings.Contains(w.Body.String(), s.NavPassword) || strings.Contains(w.Body.String(), s.OIDCSecret) || strings.Contains(w.Body.String(), s.WebhookHash) {
		t.Fatal("secrets leaked")
	}
	payload := `{"eventType":"Test","instanceName":"Lidarr","unknownNativeField":true}`
	r = httptest.NewRequest("POST", "/api/webhook/lidarr", strings.NewReader(payload))
	r.SetBasicAuth("musicforge", "separate-integration-secret")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestSafePaths(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"../outside", "/absolute", "escape/new.opus"} {
		if _, err := safePath(root, rel); err == nil {
			t.Fatalf("accepted unsafe path %q", rel)
		}
	}
	if _, err := safePath(root, "Artist/Album/new.opus"); err != nil {
		t.Fatal(err)
	}
	s := DefaultSettings()
	s.Source = root
	s.LidarrPrefix = "/lidarr/flac"
	if _, err := mapLidarr(s, "/lidarr/flac/../bad.flac"); err == nil {
		t.Fatal("accepted webhook traversal")
	}
}

func TestUpgradePreservesOldUntilAllNewValidate(t *testing.T) {
	a, s := testApp(t)
	old := makeFLAC(t, a, s, "Album/old.flac", "Old", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, true)
	source, _ := a.sourceRel("Album/old.flac")
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	makeFLAC(t, a, s, "Album/new.flac", "New", false)
	if err := a.scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	r := UpgradeRequest{New: []string{"Album/new.flac"}, Old: []string{"Album/old.flac"}}
	if err := a.finishUpgrade(context.Background(), r); err == nil {
		t.Fatal("upgrade cleaned before replacement validated")
	}
	if _, err := os.Stat(filepath.Join(s.Output, source.Output)); err != nil {
		t.Fatal("old playable output removed early")
	}
	drain(t, a, true)
	if err := a.finishUpgrade(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Output, source.Output)); !os.IsNotExist(err) {
		t.Fatal("upgrade retained explicitly replaced artifact")
	}
}

func TestNavidromeTargetAndFallback(t *testing.T) {
	for _, version := range []string{"0.59.0", "0.58.0"} {
		t.Run(version, func(t *testing.T) {
			a, s := testApp(t)
			var targets []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("p") != "" || r.URL.Query().Get("t") == "" {
					t.Error("unsafe Subsonic credentials")
				}
				if strings.Contains(r.URL.Path, "startScan") {
					targets = r.URL.Query()["target"]
				}
				respond(w, 200, map[string]any{"subsonic-response": map[string]any{"status": "ok", "type": "navidrome", "serverVersion": version}})
			}))
			defer server.Close()
			s.NavURL = server.URL
			s.NavUser = "admin"
			s.NavPassword = "password"
			_ = a.saveSettings(s)
			if err := os.MkdirAll(filepath.Join(s.Output, "Artist"), 0755); err != nil {
				t.Fatal(err)
			}
			_ = a.dirty("Artist/DeletedAlbum/track.opus")
			if err := a.refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			if version == "0.59.0" && (len(targets) != 1 || targets[0] != "1:Artist") {
				t.Fatalf("bad surviving parent target %#v", targets)
			}
			if version == "0.58.0" && len(targets) != 0 {
				t.Fatal("sent unsupported targets to older Navidrome")
			}
		})
	}
}
