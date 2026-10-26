package forge

import (
	"context"
	"strings"
	"testing"
)

func TestLyricsMetadataChineseNormalizationAndArtistNames(t *testing.T) {
	for _, tc := range []struct {
		name, title, artist, album string
		match                      bool
	}{
		{"traditional", "讓愛隨風", "周杰倫", "搖滾樂", true},
		{"English stage name", "让爱随风", "Jay Chou", "摇滚乐", true},
		{"full width English", "讓愛隨風", "ＪＡＹ　ＣＨＯＵ", "搖滾樂", true},
		{"paired stage names", "讓愛隨風", "周杰倫（Jay Chou）", "搖滾樂", true},
		{"unrelated paired names", "让爱随风", "Jay Chou (Jolin Tsai)", "摇滚乐", false},
		{"another artist", "让爱随风", "Jay Park", "摇滚乐", false},
		{"substring", "让爱随风", "Jay", "摇滚乐", false},
		{"extra collaborator", "让爱随风", "Jay Chou / Jolin Tsai", "摇滚乐", false},
		{"Live version", "讓愛隨風 (Live)", "Jay Chou", "搖滾樂", false},
		{"traditional live version", "讓愛隨風（現場）", "周杰倫", "搖滾樂", false},
		{"Remix version", "让爱随风 (Remix)", "Jay Chou", "摇滚乐", false},
		{"different album", "让爱随风", "Jay Chou", "别的专辑", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := Source{Title: "让爱随风", Artist: "周杰伦", Album: "摇滚乐", Duration: 183}
			candidate := lyricsCandidate{Title: tc.title, Artist: tc.artist, Album: tc.album, Duration: 183}
			if got := candidateMatches(candidate, source); got != tc.match {
				t.Fatalf("match = %v, want %v: %+v", got, tc.match, candidate)
			}
			candidates := []lyricsCandidate{candidate}
			rankLyricsCandidates(candidates, source)
			if tc.match && (candidates[0].MatchScore != 100 || !candidates[0].IdentityMatch) {
				t.Fatal("equivalent names lost matching points", candidates[0])
			}
			if tc.match && !candidateMatches(lyricsCandidate{Title: source.Title, Artist: source.Artist, Album: source.Album, Duration: source.Duration}, Source{Title: tc.title, Artist: tc.artist, Album: tc.album, Duration: 183}) {
				t.Fatal("name equivalence was not symmetric")
			}
		})
	}
	if !sameLyricsTag("乾坤一擲", "乾坤一掷") || sameLyricsTag("乾坤", "干坤") {
		t.Fatal("phrase exceptions were lost in single-character conversion")
	}
	if sameLyricsTag("Beyoncé", "Beyonce") || sameLyricsTag("爱你", "爱我") {
		t.Fatal("normalization erased meaningful name differences")
	}
	if !sameLyricsArtist("蔡依林／周杰倫", "Jay Chou feat. Jolin Tsai") || sameLyricsArtist("Jay Chou", "蔡依林 / 周杰伦") {
		t.Fatal("artist aliases changed collaboration membership")
	}
	if !sameLyricsArtist("陳奕迅", "Eason Chan") || sameLyricsArtist("张学友", "Jacky Chan") {
		t.Fatal("English stage names confused unrelated artists")
	}
}

func TestLyricsNormalizationKeepsVersionAndTextConflictChecks(t *testing.T) {
	a, _ := testApp(t)
	source := Source{Title: "让爱随风", Artist: "周杰伦", Album: "摇滚乐", Duration: 183}
	candidate := lyricsCandidate{Title: "讓愛隨風", Artist: "Jay Chou", Album: "搖滾樂", Duration: 183.25,
		text: &lyricsText{Original: "[00:01.00]讓愛隨風\n"}}
	text, _, state, err := a.chooseLyrics(context.Background(), source, []lyricsCandidate{candidate}, lyricsBatchOptions{MinScore: defaultLyricsMinScore})
	if err != nil || state != "ready" || text != candidate.text.Original {
		t.Fatal("equivalent metadata did not retain original lyric text", text, state, err)
	}
	candidate.Title += " (Live)"
	candidate.titleAliases = []string{source.Title}
	if candidateMatches(candidate, source) {
		t.Fatal("a translated title alias erased the Live recording version")
	}
	candidate.Title = "讓愛隨風（現場）"
	if candidateMatches(candidate, source) {
		t.Fatal("a traditional title alias erased the live recording version")
	}
	candidate.Title = "讓愛隨風"
	other := candidate
	other.text = &lyricsText{Original: "[00:02.00]讓愛隨風\n"}
	if text, _, state, err = a.chooseLyrics(context.Background(), source, []lyricsCandidate{candidate, other}, lyricsBatchOptions{MinScore: defaultLyricsMinScore}); err != nil || text != "" || state != "needs_review" {
		t.Fatal("matching metadata bypassed conflicting lyric timing", text, state, err)
	}
}

func TestLyricsArtistCreditAliasesKeepEveryCollaborator(t *testing.T) {
	credit, aliases := lyricsArtistCredits([]lyricsArtistCredit{
		{Name: "Novel Voice", Aliases: []string{"新声歌手", "Neo Voice"}},
		{Name: "Guest Voice", Aliases: []string{"客席歌手"}},
	})
	if credit != "Novel Voice / Guest Voice" {
		t.Fatal(credit)
	}
	candidate := lyricsCandidate{Title: "Song", Artist: credit, Album: "Album", Duration: 183, artistAliases: aliases}
	source := Source{Title: "Song", Artist: "客席歌手 / 新聲歌手", Album: "Album", Duration: 183}
	if !candidateMatches(candidate, source) {
		t.Fatal("provider aliases did not match a complete collaboration", aliases)
	}
	source.Artist = "新声歌手"
	if candidateMatches(candidate, source) {
		t.Fatal("one singer's alias stood in for all collaborators")
	}
	var many []string
	for i := range 100 {
		many = append(many, strings.Repeat("a", i+1))
	}
	_, aliases = lyricsArtistCredits([]lyricsArtistCredit{{Name: "One", Aliases: many}, {Name: "Two", Aliases: many}, {Name: "Three", Aliases: many}})
	if len(aliases) > 31 {
		t.Fatal("provider artist aliases grew without a bound", len(aliases))
	}
	for _, alias := range aliases {
		if strings.Count(alias, " / ") != 2 {
			t.Fatal("bounded aliases dropped a collaborator", alias)
		}
	}
}
