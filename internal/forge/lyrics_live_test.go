package forge

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// This file lives only on the temporary provider-check branch. Public metadata
// is sent to real services; no library files or lyric text are published.
func TestLyricsProvidersOnline(t *testing.T) {
	tracks := []struct {
		language string
		Source
	}{
		{"Japanese", Source{Title: "夜に駆ける", Artist: "YOASOBI", Duration: 261.12}},
		{"English", Source{Title: "Imagine", Artist: "John Lennon", Duration: 183}},
		{"Chinese", Source{Title: "晴天", Artist: "周杰伦", Duration: 269}},
	}
	for _, provider := range lyricsProviders {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			app := &App{version: "lyrics-provider-check"}
			app.lyricsHTTP = &http.Client{Timeout: 10 * time.Second, Transport: lyricsTransport(func(request *http.Request) (*http.Response, error) {
				response, err := http.DefaultTransport.RoundTrip(request)
				if err != nil {
					return response, err
				}
				raw, err := io.ReadAll(io.LimitReader(response.Body, 8*maxLyricsBytes+1))
				response.Body.Close()
				if err != nil {
					return nil, err
				}
				response.Body = io.NopCloser(bytes.NewReader(raw))
				var value any
				decoded := raw
				if request.URL.Host == "interface.music.163.com" && !json.Valid(raw) {
					if plaintext, decryptErr := neteaseDecrypt(raw); decryptErr == nil {
						decoded = plaintext
					}
				}
				decodeErr := json.Unmarshal(decoded, &value)
				if request.URL.Host == "u.y.qq.com" {
					var metadata struct {
						Request struct {
							Data struct {
								Body struct {
									Songs []struct {
										Title    string `json:"title"`
										Name     string `json:"name"`
										Subtitle string `json:"subtitle"`
										Main     string `json:"title_main"`
										Extra    string `json:"title_extra"`
									} `json:"item_song"`
								} `json:"body"`
							} `json:"data"`
						} `json:"request"`
					}
					if json.Unmarshal(raw, &metadata) == nil && len(metadata.Request.Data.Body.Songs) > 0 {
						t.Logf("QQ first recording metadata: %+v", metadata.Request.Data.Body.Songs[0])
					}
				}
				shape, _ := json.Marshal(liveLyricsShape(value, "", 8))
				if len(shape) > 5000 {
					shape = shape[:5000]
				}
				t.Logf("%s%s HTTP %d type=%q JSON=%v schema=%s", request.URL.Host, request.URL.Path, response.StatusCode, response.Header.Get("Content-Type"), decodeErr, shape)
				return response, nil
			})}
			for _, track := range tracks {
				t.Run(track.language, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
					defer cancel()
					candidates, _, err := app.providerCandidates(ctx, provider, track.Source, track.Title+" "+track.Artist, true)
					if err != nil {
						t.Fatal("search:", err)
					}
					for _, candidate := range candidates {
						if !candidate.HighConfidence {
							continue
						}
						text, err := app.candidateLyrics(ctx, candidate)
						if err != nil {
							t.Fatal("preview:", err)
						}
						if text.Original == "" {
							t.Fatal("matched recording has no synchronized lyrics")
						}
						t.Logf("matched %q by %q, duration %.2fs, LRC bytes %d, translation=%t, romanization=%t", candidate.Title, candidate.Artist, candidate.Duration, len(text.Original), text.Translation != "", text.Romanization != "")
						return
					}
					for _, candidate := range candidates {
						t.Logf("candidate %q by %q, duration %.2fs", candidate.Title, candidate.Artist, candidate.Duration)
					}
					t.Fatalf("no matching recording among %d candidates", len(candidates))
				})
			}
		})
	}
}

func liveLyricsShape(value any, key string, depth int) any {
	if key == "userId" || key == "cookie" || key == "MUSIC_A" || key == "MUSIC_U" || key == "__csrf" {
		return "redacted"
	}
	if depth == 0 {
		return fmt.Sprintf("%T", value)
	}
	switch value := value.(type) {
	case map[string]any:
		result := map[string]any{}
		for key, child := range value {
			result[key] = liveLyricsShape(child, key, depth-1)
		}
		return result
	case []any:
		result := map[string]any{"count": len(value)}
		if len(value) > 0 {
			result["first"] = liveLyricsShape(value[0], "", depth-1)
		}
		return result
	case string:
		if key == "code" {
			if len(value) > 160 {
				return value[:160]
			}
			return value
		}
		return fmt.Sprintf("string(%d)", len(value))
	default:
		return value
	}
}

// Diagnose wire compatibility with public protocol examples, without logging
// registration usernames, cookies, encrypted bodies or other credentials.
func TestNetEaseGuestProtocolOnline(t *testing.T) {
	for _, knownDevice := range []bool{false, true} {
		t.Run(fmt.Sprintf("reference-profile-known-device-%t", knownDevice), func(t *testing.T) {
			a := &App{version: "lyrics-provider-check"}
			a.lyricsHTTP = &http.Client{Timeout: 10 * time.Second, Transport: lyricsTransport(func(r *http.Request) (*http.Response, error) {
				r.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Safari/537.36 Chrome/91.0.4472.164 NeteaseMusicDesktop/3.1.3.203419 MusicForge/lyrics-provider-check")
				r.Header.Set("Accept", "*/*")
				return http.DefaultTransport.RoundTrip(r)
			})}
			session := newNetEaseSession(nil)
			session.cookies["mode"] = "ASUS ROG STRIX Z790"
			if knownDevice {
				// Public sample identifier from the upstream protocol reference.
				session.cookies["deviceId"] = "AA9955F5FE37BA7EAF48F8EF0C9966B28293CC8D6415CCD93549"
			}
			device := session.cookies["deviceId"]
			xor := []byte(device)
			key := "3go8&$8*3*3h0k(2)2"
			for i := range xor {
				xor[i] ^= key[i%len(key)]
			}
			sum := md5.Sum(xor)
			username := base64.StdEncoding.EncodeToString([]byte(device + " " + base64.StdEncoding.EncodeToString(sum[:])))
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			code, cookies, err := a.neteaseEAPI(ctx, "/eapi/register/anonimous", map[string]any{"username": username}, session, new(any))
			if err != nil {
				t.Fatalf("registration code=%d: %v", code, err)
			}
			present := false
			for _, cookie := range cookies {
				if cookie.Name == "MUSIC_A" && cookie.Value != "" {
					present = true
				}
			}
			if !present {
				t.Fatal("registration did not issue MUSIC_A")
			}
			t.Log("guest registration succeeded")
		})
	}
}
