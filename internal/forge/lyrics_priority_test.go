package forge

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestAutomaticLyricsUsesScoreThenProviderPriority(t *testing.T) {
	for _, tc := range []struct {
		name, winner string
		durations    map[string]float64
	}{
		{"equal scores", "qq", map[string]float64{}},
		{"Kugou beats a lower QQ score", "kugou", map[string]float64{"qq": 183.5}},
		{"NetEase beats lower QQ and Kugou scores", "netease", map[string]float64{"qq": 183.5, "kugou": 183.5}},
		{"PetitLyrics beats lower domestic scores", "petitlyrics", map[string]float64{"qq": 183.5, "kugou": 183.5, "netease": 183.5}},
		{"LRCLIB higher score beats every source", "lrclib", map[string]float64{"qq": 183.5, "kugou": 183.5, "netease": 183.5, "petitlyrics": 183.5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, s := testApp(t)
			seedLyricsArtifact(t, a, s)
			target, err := a.captureLyricsTarget(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			for _, provider := range lyricsProviders {
				duration := 183.0
				if override, ok := tc.durations[provider]; ok {
					duration = override
				}
				// Distinct bodies make the selected source observable. Traditional
				// QQ lyrics must still win a score tie against simplified fallbacks.
				body := "[00:01.00]爱这风云\n"
				if provider == "qq" {
					body = "[00:01.00]愛這風雲\n"
				}
				candidate := lyricsCandidate{Provider: provider, remoteID: "123", Title: "Track", Artist: "Artist", Album: "Album", Duration: duration, text: &lyricsText{Original: body}}
				if err := a.storeLyricsSearch(context.Background(), target, provider, "", []lyricsCandidate{candidate}, false, time.Now().Unix()); err != nil {
					t.Fatal(err)
				}
			}
			mockLyricsServices(t, a, func(w http.ResponseWriter, r *http.Request) {
				t.Error("valid saved search/preview made a provider request", r.URL)
				w.WriteHeader(500)
			})
			text, provider, state, attempts, err := a.searchOnlineLyrics(context.Background(), target, lyricsBatchOptions{MinScore: 96})
			if err != nil || text == "" || state != "ready" || provider != tc.winner || len(attempts) != lyricsProviderPriority(tc.winner)+1 {
				t.Fatal(text, provider, state, attempts, err)
			}
		})
	}
}

func TestLyricsReplacementStillFindsEligibleSimplifiedOriginal(t *testing.T) {
	a, _ := testApp(t)
	source := Source{Title: "Song", Artist: "Artist", Album: "Album", Duration: 183}
	traditional := lyricsCandidate{Provider: "qq", Title: source.Title, Artist: source.Artist, Album: source.Album, Duration: 183, text: &lyricsText{Original: "[00:01.00]愛這風雲\n"}}
	simplified := traditional
	simplified.Duration, simplified.text = 183.5, &lyricsText{Original: "[00:01.00]爱这风云\n"}
	text, score, state, err := a.chooseLyrics(context.Background(), source, []lyricsCandidate{traditional, simplified}, lyricsBatchOptions{MinScore: 96, ReplaceTraditional: true})
	if err != nil || text != simplified.text.Original || score != 99 || state != "ready" {
		t.Fatal("higher traditional score hid an explicit simplified replacement", text, score, state, err)
	}
	simplified.text = &lyricsText{Original: "[00:02.00]爱这风云\n"}
	if text, _, state, err := a.chooseLyrics(context.Background(), source, []lyricsCandidate{traditional, simplified}, lyricsBatchOptions{MinScore: 96, ReplaceTraditional: true}); err != nil || text != "" || state != "needs_review" {
		t.Fatal("replacement ignored conflicting timing", text, state, err)
	}
}
