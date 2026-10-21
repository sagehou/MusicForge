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

func lyricsSession(t *testing.T, a *App) func(string, string, any) *httptest.ResponseRecorder {
	t.Helper()
	issued := httptest.NewRecorder()
	if err := a.newSession(issued, httptest.NewRequest("GET", "/", nil), "local"); err != nil {
		t.Fatal(err)
	}
	cookie := issued.Result().Cookies()[0]
	return func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
}

func previewTestLyrics(t *testing.T, a *App, request func(string, string, any) *httptest.ResponseRecorder) (string, int) {
	t.Helper()
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "interface.music.163.com" {
			t.Error("unexpected provider", r.URL.Host)
		}
		switch r.URL.Path {
		case "/eapi/register/anonimous":
			readNetEaseParams(t, r)
			guestNetEaseResponse(w)
		case "/eapi/search/song/list/page":
			readNetEaseParams(t, r)
			encryptedNetEaseResponse(w, `{"code":200,"data":{"resources":[{"baseInfo":{"simpleSongData":{"id":321,"name":"別名","dt":183000,"ar":[{"name":"Artist"}],"al":{"name":"Album"}}}}]}}`)
		case "/eapi/song/lyric/v1":
			if params := readNetEaseParams(t, r); params["id"] != float64(321) {
				t.Error("download did not use server candidate identity")
			}
			encryptedNetEaseResponse(w, `{"code":200,"lrc":{"lyric":"[00:01.00]Original"},"tlyric":{"lyric":"[00:01.00]译文"},"romalrc":{"lyric":"[00:01.00]romanization"}}`)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	w := request("POST", "/api/library/1/lyrics/candidates", map[string]any{"provider": "netease", "query": "別名 Artist"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Token      string            `json:"token"`
		Candidates []lyricsCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Candidates) != 1 || result.Candidates[0].HighConfidence {
		t.Fatal("expected a manual candidate", result, err)
	}
	if strings.Contains(w.Body.String(), "Original") || strings.Contains(w.Body.String(), "remoteID") {
		t.Fatal("search exposed cached content or internal remote parameters")
	}
	w = request("POST", "/api/library/1/lyrics/preview", map[string]any{"token": result.Token, "index": 0})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Original") || !strings.Contains(w.Body.String(), "romanization") {
		t.Fatal(w.Code, w.Body.String())
	}
	return result.Token, 0
}

func TestLyricsCandidatePreviewAndExplicitLanguageSelection(t *testing.T) {
	a, s := testApp(t)
	source := seedLyricsArtifact(t, a, s)
	request := lyricsSession(t, a)
	token, index := previewTestLyrics(t, a, request)
	path := filepath.Join(s.Output, "Album/01.lrc")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("search or preview wrote a file", err)
	}
	w := request("PUT", "/api/library/1/lyrics", map[string]any{"token": token, "index": index, "translation": true, "romanization": true})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "[00:01.000]Original\n[00:01.000]译文\n[00:01.000]romanization\n" {
		t.Fatal(string(data), err)
	}
	var status LyricsStatus
	if err = json.Unmarshal(w.Body.Bytes(), &status); err != nil || status.Provider != "netease" || status.Signature != digest("registered audio fixture") || status.Checked < 1 {
		t.Fatal(status, err)
	}
	if data, err = os.ReadFile(filepath.Join(s.Output, source.Output)); err != nil || string(data) != "registered audio fixture" {
		t.Fatal("selection modified audio", err)
	}
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		t.Error("filling replaced a manually selected managed LRC")
		w.WriteHeader(500)
	})
	if status = a.maintainLyrics(context.Background(), s, source.ID, lyricsBatchOptions{MinScore: defaultLyricsMinScore}); status.State != "ready" || status.Provider != "netease" {
		t.Fatal(status)
	}
	w = request("PUT", "/api/library/1/lyrics", map[string]any{"token": token, "index": index})
	if w.Code != 409 {
		t.Fatal("consumed token was reused", w.Code)
	}
}

func TestLyricsCandidatesRejectStaleTargetsAndClientContent(t *testing.T) {
	for _, change := range []string{"audio", "source", "user file", "managed file", "expired token", "client content", "wrong track"} {
		t.Run(change, func(t *testing.T) {
			a, s := testApp(t)
			source := seedLyricsArtifact(t, a, s)
			request := lyricsSession(t, a)
			token, index := previewTestLyrics(t, a, request)
			path := filepath.Join(s.Output, "Album/01.lrc")
			body := map[string]any{"token": token, "index": index}
			endpoint := "/api/library/1/lyrics"
			want := 409
			switch change {
			case "audio":
				if _, err := a.db.Exec("UPDATE managed SET signature='new-audio' WHERE kind='audio'"); err != nil {
					t.Fatal(err)
				}
			case "source":
				if _, err := a.db.Exec("UPDATE sources SET hash='new-source' WHERE id=1"); err != nil {
					t.Fatal(err)
				}
			case "user file":
				if err := os.WriteFile(path, []byte("user lyrics"), 0644); err != nil {
					t.Fatal(err)
				}
			case "managed file":
				a.files.Lock()
				err := a.writeLyricsFile(context.Background(), s, source, "[00:02.00]Another selection", "lrclib")
				a.files.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			case "expired token":
				a.lyricsMu.Lock()
				a.lyricsChoices[token].Created = time.Now().Add(-11 * time.Minute)
				a.lyricsMu.Unlock()
			case "client content":
				body["original"] = "[00:01.00]Injected"
				want = 400
			case "wrong track":
				endpoint = "/api/library/2/lyrics"
			}
			w := request("PUT", endpoint, body)
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			data, err := os.ReadFile(path)
			switch change {
			case "user file":
				if err != nil || string(data) != "user lyrics" {
					t.Fatal("overwrote user file", string(data), err)
				}
			case "managed file":
				if err != nil || string(data) != "[00:02.00]Another selection\n" {
					t.Fatal("overwrote concurrent selection", string(data), err)
				}
			default:
				if !os.IsNotExist(err) {
					t.Fatal("stale or injected selection published", string(data), err)
				}
			}
		})
	}
}
