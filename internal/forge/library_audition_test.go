package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func auditionSession(t *testing.T, a *App) func(string, string, string) *httptest.ResponseRecorder {
	t.Helper()
	issued := httptest.NewRecorder()
	if err := a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	return func(method, path, byteRange string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		r.AddCookie(issued.Result().Cookies()[0])
		if byteRange != "" {
			r.Header.Set("Range", byteRange)
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
}

func TestAuditionAuthenticatedRangesAndOutputOnlyRead(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	lrc := "[00:00.00]First\n[00:01.00]Second\n"
	if err := a.writeLyricsFile(context.Background(), s, source, lrc, "source"); err != nil {
		t.Fatal(err)
	}
	request := auditionSession(t, a)
	// Even disabled, changed or deleted sources retain their existing output.
	s.Enabled = false
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("UPDATE sources SET present=0,hash='changed'"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(s.Source, s.Source+".offline"); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	rows, err := a.reads.Query("SELECT key,value FROM meta")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			t.Fatal(err)
		}
		before[key] = value
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	w := request("GET", "/api/library/1/audition", "")
	var preview audition
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.Lyrics != lrc || preview.LyricsState != "ready" || preview.Format != ".opus" || preview.Size != 24 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/library/1/audition", preview.AudioURL} {
		for _, method := range []string{"GET", "HEAD"} {
			unauthorized := httptest.NewRecorder()
			a.Handler().ServeHTTP(unauthorized, httptest.NewRequest(method, path, nil))
			if unauthorized.Code != 401 {
				t.Fatal("unauthenticated audio or lyrics", path, method, unauthorized.Code)
			}
		}
	}
	for _, test := range []struct {
		method, byteRange, body, contentRange string
		status                              int
	}{
		{"GET", "", "registered audio fixture", "", 200},
		{"HEAD", "", "", "", 200},
		{"GET", "bytes=4-10", "stered ", "bytes 4-10/24", 206},
		{"GET", "bytes=-7", "fixture", "bytes 17-23/24", 206},
		{"GET", "bytes=24-", "", "bytes */24", 416},
	} {
		w = request(test.method, preview.AudioURL, test.byteRange)
		if w.Code != test.status || test.status != 416 && w.Body.String() != test.body || w.Header().Get("Content-Range") != test.contentRange {
			t.Fatal(test, w.Code, w.Body.String(), w.Header())
		}
		if test.status != 416 && (w.Header().Get("Content-Type") != "audio/ogg; codecs=opus" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Accept-Ranges") != "bytes") {
			t.Fatal("missing private seekable audio headers", w.Header())
		}
	}
	for _, path := range []string{"/api/library/no/audition", "/api/library/0/audition", "/api/library/1/audition/audio"} {
		if w = request("GET", path, ""); w.Code != 400 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/library/2/audition", "/api/library/1/audition/audio?v=stale"} {
		if w = request("GET", path, "bytes=0-1"); w.Code != 409 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for key, value := range before {
		if after, err := a.meta(key); err != nil || after != value {
			t.Fatal("audition changed metadata", key, after, err)
		}
	}
	var count int
	if err = a.reads.QueryRow("SELECT count(*) FROM meta").Scan(&count); err != nil || count != len(before) {
		t.Fatal("audition created metadata", count, err)
	}
	if bytes, err := os.ReadFile(filepath.Join(s.Output, source.Output)); err != nil || string(bytes) != "registered audio fixture" {
		t.Fatal("audition modified audio", err)
	}
}

func TestAuditionRejectsUnownedFilesEscapesAndChangedVersions(t *testing.T) {
	for _, kind := range []string{"unowned", "wrong-owner", "missing", "absent", "marker", "symlink", "ancestor-escape", "traversal", "version"} {
		t.Run(kind, func(t *testing.T) {
			a, s := testApp(t)
			seedLyricsArtifact(t, a, s)
			request := auditionSession(t, a)
			path := filepath.Join(s.Output, "Album/01.opus")
			outside := filepath.Join(t.TempDir(), "secret.opus")
			if err := os.WriteFile(outside, []byte("outside secret"), 0644); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "unowned":
				_, err = a.db.Exec("DELETE FROM managed")
			case "wrong-owner":
				_, err = a.db.Exec("UPDATE managed SET source_id=2")
			case "missing":
				err = os.Remove(path)
			case "absent":
				_, err = a.db.Exec("UPDATE sources SET output_present=0")
			case "marker":
				err = os.WriteFile(filepath.Join(s.Output, ".musicforge"), []byte("another instance"), 0644)
			case "symlink":
				if err = os.Remove(path); err == nil {
					err = os.Symlink(outside, path)
				}
			case "ancestor-escape":
				if err = os.RemoveAll(filepath.Dir(path)); err == nil {
					err = os.Symlink(filepath.Dir(outside), filepath.Dir(path))
				}
			case "traversal":
				if _, err = a.db.Exec("UPDATE sources SET output='../secret.opus'"); err == nil {
					_, err = a.db.Exec("UPDATE managed SET path='../secret.opus'")
				}
			case "version":
				_, err = a.db.Exec("UPDATE managed SET signature='new-version'")
			}
			if err != nil {
				t.Fatal(err)
			}
			url := "/api/library/1/audition"
			if kind == "version" {
				url += "/audio?v=" + digest("registered audio fixture")
			}
			w := request("GET", url, "")
			if w.Code != 409 || strings.Contains(w.Body.String(), "outside secret") {
				t.Fatal(kind, w.Code, w.Body.String())
			}
		})
	}
}

func TestAuditionLyricsOwnershipAndInstrumental(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	request := auditionSession(t, a)
	check := func(state, text string) {
		t.Helper()
		w := request("GET", "/api/library/1/audition", "")
		var preview audition
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.LyricsState != state || preview.Lyrics != text {
			t.Fatal(state, w.Code, w.Body.String())
		}
	}
	check("missing", "")
	if err := a.writeLyricsFile(context.Background(), s, source, instrumentalLRC, "manual"); err != nil {
		t.Fatal(err)
	}
	check("ready", instrumentalLRC)
	path := filepath.Join(s.Output, lyricsRel(source.Output))
	if err := os.WriteFile(path, []byte("[00:00]external modification\n"), 0644); err != nil {
		t.Fatal(err)
	}
	check("unavailable", "")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(s.Source, "private.lrc"), path); err != nil {
		t.Fatal(err)
	}
	check("unavailable", "")
}

func TestAuditionMP3UsesTheRegisteredArtifact(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	if err := os.Rename(filepath.Join(s.Output, "Album/01.opus"), filepath.Join(s.Output, "Album/01.mp3")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("UPDATE sources SET output='Album/01.mp3'; UPDATE managed SET path='Album/01.mp3'"); err != nil {
		t.Fatal(err)
	}
	request := auditionSession(t, a)
	w := request("GET", "/api/library/1/audition/audio?v="+digest("registered audio fixture"), "bytes=0-3")
	if w.Code != 206 || w.Header().Get("Content-Type") != "audio/mpeg" || w.Body.String() != "regi" {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
}

func TestAuditionOpenFileDoesNotBlockBuildsAndKeepsCapturedBytes(t *testing.T) {
	a, s := testApp(t)
	seedLyricsArtifact(t, a, s)
	file, _, _, err := a.openAudition(context.Background(), 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = a.lockFiles(ctx); err != nil {
		t.Fatal("streaming holds the file lock", err)
	}
	path := filepath.Join(s.Output, "Album/01.opus")
	if err = os.WriteFile(path+".next", []byte("replacement"), 0644); err == nil {
		err = os.Rename(path+".next", path)
	}
	a.files.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 24)
	if n, err := file.Read(data); err != nil || n != 24 || string(data) != "registered audio fixture" {
		t.Fatal("an in-flight audition switched to replaced bytes", n, string(data), err)
	}
}
