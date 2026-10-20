package forge

import (
	"bytes"
	"context"
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
				if provider == "netease" && track.language == "Chinese" {
					// This catalog currently returns only covers for 晴天. Keep the
					// recording guard intact and exercise a licensed original here.
					track.Source = Source{Title: "十年", Artist: "陈奕迅", Duration: 205}
				}
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
