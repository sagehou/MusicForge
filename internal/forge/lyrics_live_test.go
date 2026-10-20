package forge

import (
	"context"
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
		{"English", Source{Title: "Imagine", Artist: "John Lennon", Duration: 183}},
		{"Chinese", Source{Title: "晴天", Artist: "周杰伦", Duration: 269}},
		{"Japanese", Source{Title: "夜に駆ける", Artist: "YOASOBI", Duration: 261.12}},
	}
	for _, provider := range lyricsProviders {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			app := &App{version: "lyrics-provider-check"}
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
