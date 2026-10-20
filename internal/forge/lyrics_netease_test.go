package forge

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func netEaseRequestCookie(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func readNetEaseParams(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	if r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("Origin") != "orpheus://orpheus" {
		t.Error("NetEase client headers or method missing")
	}
	if err := r.ParseForm(); err != nil {
		t.Error("invalid form", err)
	}
	raw, err := hex.DecodeString(r.PostForm.Get("params"))
	if err != nil {
		t.Error("invalid EAPI hex", err)
	}
	raw, err = neteaseDecrypt(raw)
	if err != nil {
		t.Error("invalid EAPI ciphertext", err)
	}
	parts := bytes.Split(raw, []byte("-36cd479b6b5-"))
	if len(parts) != 3 {
		t.Error("invalid EAPI envelope")
		return nil
	}
	canonical := strings.Replace(r.URL.Path, "/eapi/", "/api/", 1)
	sum := md5.Sum([]byte("nobody" + canonical + "use" + string(parts[1]) + "md5forencrypt"))
	if string(parts[0]) != canonical || string(parts[2]) != fmt.Sprintf("%x", sum) {
		t.Error("EAPI path or signature mismatch")
	}
	var params map[string]any
	if json.Unmarshal(parts[1], &params) != nil || params["e_r"] != true {
		t.Error("invalid EAPI parameters")
	}
	metadata, _ := params["header"].(string)
	var header map[string]any
	if json.Unmarshal([]byte(metadata), &header) != nil || header["deviceId"] != netEaseRequestCookie(r, "deviceId") || header["os"] == "" {
		t.Error("EAPI header lost its session identity")
	}
	return params
}

func encryptedNetEaseResponse(w http.ResponseWriter, data string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(neteaseEncrypt([]byte(data)))
}

func guestNetEaseResponse(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "MUSIC_A", Value: "guest-session", MaxAge: 864000, Secure: true})
	http.SetCookie(w, &http.Cookie{Name: "__csrf", Value: "guest-csrf", MaxAge: 864000, Secure: true})
	encryptedNetEaseResponse(w, `{"code":200,"userId":12345}`)
}

func TestNetEaseGuestSessionReuseAndRenewal(t *testing.T) {
	a := &App{version: "test"}
	registrations, searches := 0, 0
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		params := readNetEaseParams(t, r)
		if r.URL.Path == "/eapi/register/anonimous" {
			registrations++
			if params["username"] == "" || netEaseRequestCookie(r, "MUSIC_U") != "" {
				t.Error("guest registration used login credentials or lacked a username")
			}
			guestNetEaseResponse(w)
			return
		}
		searches++
		if netEaseRequestCookie(r, "MUSIC_A") == "expired-guest" {
			lyricsJSON(w, `{"code":301}`)
			return
		}
		if netEaseRequestCookie(r, "MUSIC_A") != "guest-session" || netEaseRequestCookie(r, "__csrf") != "guest-csrf" {
			t.Error("anonymous session was not used")
		}
		encryptedNetEaseResponse(w, `{"code":200,"data":{"resources":[]}}`)
	})
	for i := 0; i < 3; i++ {
		if _, err := a.neteaseCandidates(context.Background(), "歌曲 Artist"); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if registrations != 1 {
				t.Fatal("guest session not reused", registrations)
			}
			a.netease.session.expires = time.Now().Add(-time.Second)
		}
	}
	if registrations != 2 || searches != 3 {
		t.Fatal("expired guest session was not renewed", registrations, searches)
	}
	a.netease.session.cookies["MUSIC_A"] = "expired-guest"
	if _, err := a.neteaseCandidates(context.Background(), "Song Artist"); err == nil || !strings.Contains(err.Error(), "guest session") || a.netease.session != nil {
		t.Fatal("rejected guest credentials were not invalidated", err)
	}
	if _, err := a.neteaseCandidates(context.Background(), "Song Artist"); err == nil || searches != 4 || time.Until(a.netease.retryAt) < 59*time.Second {
		t.Fatal("guest rejection did not pause requests", err, searches)
	}
	a.netease.retryAt = time.Now().Add(-time.Second)
	if _, err := a.neteaseCandidates(context.Background(), "Song Artist"); err != nil || registrations != 3 || searches != 5 {
		t.Fatal("rejected guest session was not renewed after cooldown", err, registrations, searches)
	}
}

func TestNetEaseConcurrentInitializationCanBeCancelled(t *testing.T) {
	a := &App{version: "test"}
	started, release := make(chan struct{}), make(chan struct{})
	var registrations atomic.Int32
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/eapi/register/anonimous" {
			if registrations.Add(1) == 1 {
				close(started)
				<-release
			}
			guestNetEaseResponse(w)
			return
		}
		encryptedNetEaseResponse(w, `{"code":200,"data":{"resources":[]}}`)
	})
	lookup := func(ctx context.Context, done chan error) {
		_, err := a.neteaseCandidates(ctx, "Song Artist")
		done <- err
	}
	first := make(chan error, 1)
	go lookup(context.Background(), first)
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.neteaseCandidates(ctx, "Cancelled"); !errors.Is(err, context.Canceled) {
		t.Error("session wait ignored cancellation", err)
	}
	second := make(chan error, 1)
	go lookup(context.Background(), second)
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if registrations.Load() != 1 {
		t.Fatal("concurrent requests registered duplicate guests")
	}
}

func TestNetEaseCookieRotationAndProviderIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netease.cookie")
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("MUSIC_U=private-old; __csrf=private-csrf\n")
	a := &App{version: "test", cfg: Runtime{NetEaseCookieFile: path}}
	calls := 0
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host == "lrclib.net" {
			if r.Header.Get("Cookie") != "" {
				t.Error("NetEase credentials reached another provider")
			}
			lyricsJSON(w, `[]`)
			return
		}
		calls++
		if r.URL.Host != "interface.music.163.com" || r.URL.Path == "/eapi/register/anonimous" {
			t.Error("login Cookie used outside the fixed API or as guest")
		}
		readNetEaseParams(t, r)
		if netEaseRequestCookie(r, "MUSIC_U") == "private-old" {
			lyricsJSON(w, `{"code":301,"message":"private-old private-csrf"}`)
			return
		}
		if netEaseRequestCookie(r, "MUSIC_U") != "private-new" {
			t.Error("rotated Cookie not loaded")
		}
		encryptedNetEaseResponse(w, `{"code":200,"data":{"resources":[]}}`)
	})
	for i := 0; i < 2; i++ {
		_, err := a.neteaseCandidates(context.Background(), "歌曲 歌手")
		if err == nil || !strings.Contains(err.Error(), "replace the Cookie file") || strings.Contains(err.Error(), "private-") {
			t.Fatal("credential failure missing, unsafe or misclassified", err)
		}
	}
	if calls != 1 {
		t.Fatal("rejected credentials were retried during cooldown", calls)
	}
	write("MUSIC_U=private-new; __csrf=private-csrf\n")
	if _, err := a.neteaseCandidates(context.Background(), "歌曲 歌手"); err != nil || calls != 2 {
		t.Fatal("rotated credentials did not clear auth cooldown", calls, err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "MUSIC_U=private-new; __csrf=private-csrf\n" {
		t.Fatal("application modified the mounted secret")
	}
	if _, _, err := a.providerCandidates(context.Background(), "lrclib", Source{}, "Song Artist", true); err != nil {
		t.Fatal(err)
	}
	write("MUSIC_U=private-new\r\nInjected: private-csrf")
	if _, err := a.neteaseCandidates(context.Background(), "Song Artist"); err == nil || err.Error() != neteaseCookieError || calls != 2 {
		t.Fatal("invalid secret was used or leaked", err, calls)
	}
}

func TestNetEaseCookieFileValidationAndRuntime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "netease.cookie")
	for _, raw := range []string{"", "__csrf=without-login", "MUSIC_U=", "MUSIC_U=a; MUSIC_U=b", "MUSIC_U=a\nheader: b", "MUSIC_U=a\x00b", "MUSIC_U=a; bad name=b", strings.Repeat("x", 16*1024+1)} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadNetEaseCookies(path); err == nil || err.Error() != neteaseCookieError {
			t.Fatal("accepted invalid or exposed secret content", err)
		}
	}
	if err := os.WriteFile(path, []byte("MUSIC_U=login; __csrf=csrf;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "secret-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if cookies, _, err := loadNetEaseCookies(link); err != nil || cookies["MUSIC_U"] != "login" {
		t.Fatal("regular-file secret symlink rejected", err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{dir, fifo, filepath.Join(dir, "missing")} {
		if _, _, err := loadNetEaseCookies(invalid); err == nil || err.Error() != neteaseCookieError {
			t.Fatal("accepted nonregular/missing secret", err)
		}
	}
	t.Setenv("MUSICFORGE_NETEASE_COOKIE_FILE", path)
	cfg, err := LoadRuntime(dir)
	if err != nil || cfg.NetEaseCookieFile != path {
		t.Fatal("runtime secret-file configuration missing", err)
	}
	raw, _ := json.Marshal(cfg)
	if strings.Contains(string(raw), "netease") || strings.Contains(string(raw), path) {
		t.Fatal("secret-file configuration serialized")
	}
	t.Setenv("MUSICFORGE_NETEASE_COOKIE_FILE", "relative.cookie")
	if _, err := LoadRuntime(dir); err == nil {
		t.Fatal("relative secret path accepted")
	}
}

func TestNetEaseResponseBoundsAndPadding(t *testing.T) {
	block, _ := aes.NewCipher([]byte("e82ckenh8dichen8"))
	badPadding := bytes.Repeat([]byte{'x'}, aes.BlockSize)
	badPadding[len(badPadding)-1] = 2 // Last two bytes must both equal 2.
	block.Encrypt(badPadding, badPadding)
	for _, raw := range [][]byte{nil, []byte("short"), badPadding, make([]byte, 8*maxLyricsBytes+1)} {
		if _, err := neteaseDecrypt(raw); err == nil {
			t.Fatal("invalid EAPI response accepted")
		}
	}
	for _, raw := range [][]byte{badPadding, []byte("private-server-response"), []byte(`{"code":200,"data":"private-invalid-shape"}`), bytes.Repeat([]byte{'x'}, 8*maxLyricsBytes+1)} {
		a := &App{version: "test"}
		mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) })
		var response struct {
			Data map[string]any `json:"data"`
		}
		_, _, err := a.neteaseEAPI(context.Background(), "/eapi/search/song/list/page", map[string]any{}, newNetEaseSession(nil), &response)
		if err == nil || strings.Contains(err.Error(), "private-") {
			t.Fatal("bad response accepted or exposed", err)
		}
	}
}

func TestNetEaseHTTPRetryAfterSurvivesCredentialRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netease.cookie")
	if err := os.WriteFile(path, []byte("MUSIC_U=old"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &App{version: "test", cfg: Runtime{NetEaseCookieFile: path}}
	calls := 0
	mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err := a.neteaseCandidates(context.Background(), "Song Artist"); err == nil {
		t.Fatal("HTTP rejection ignored")
	}
	if err := os.WriteFile(path, []byte("MUSIC_U=new"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.neteaseCandidates(context.Background(), "Song Artist"); err == nil || !strings.Contains(err.Error(), "cooling down") || calls != 1 {
		t.Fatal("rotation bypassed HTTP cooldown", err, calls)
	}
	if time.Until(a.lyricsRetries["netease"]) < 119*time.Second {
		t.Fatal("provider cooldown was shortened")
	}
}
