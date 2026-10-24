package forge

import (
	_ "embed"
	"sort"
	"strings"
)

// OpenCC ver.1.4.2 dictionaries; provenance and Apache-2.0 license are in
// THIRD_PARTY_NOTICES.md. Keep this metadata comparison separate from the
// conservative script hints and conflict checks on actual lyric text.
//
//go:embed lyricsdata/opencc/TSCharacters.txt
var lyricsTraditionalCharacters string

//go:embed lyricsdata/opencc/TSPhrases.txt
var lyricsTraditionalPhrases string

var lyricsMetadataSimplifier = func() *strings.Replacer {
	entries := map[string]string{}
	for _, dictionary := range []string{lyricsTraditionalCharacters, lyricsTraditionalPhrases} {
		for _, line := range strings.Split(dictionary, "\n") {
			if strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				entries[fields[0]] = fields[1]
			}
		}
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	// Phrase exceptions must win over individual characters at the same offset.
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	pairs := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		pairs = append(pairs, key, entries[key])
	}
	return strings.NewReplacer(pairs...)
}()

func normalizeLyricsMetadata(text string) string {
	return lyricsMetadataSimplifier.Replace(foldLyricsWidth(text))
}

// These explicit stage names supplement aliases returned by providers. Never
// infer an English name through transliteration, substrings or edit distance.
//
//go:embed lyricsdata/artist-aliases.txt
var lyricsArtistAliases string

var lyricsArtistNames = func() map[string]string {
	names := map[string]string{}
	for _, line := range strings.Split(lyricsArtistAliases, "\n") {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		group := strings.Split(line, "\t")
		canonical := lyricsTag(group[0])
		for _, name := range group {
			names[lyricsTag(name)] = canonical
		}
	}
	return names
}()

func lyricsArtistKey(name string) string {
	key := lyricsTag(name)
	if canonical, ok := lyricsArtistNames[key]; ok {
		return canonical
	}
	text := strings.TrimSpace(foldLyricsWidth(name))
	if strings.HasSuffix(text, ")") {
		if index := strings.LastIndex(text, "("); index > 0 {
			primary, knownPrimary := lyricsArtistNames[lyricsTag(text[:index])]
			alias, knownAlias := lyricsArtistNames[lyricsTag(text[index+1:len(text)-1])]
			if knownPrimary && knownAlias && primary == alias {
				return primary
			}
		}
	}
	return key
}

type lyricsArtistCredit struct {
	Name    string
	Aliases []string
}

// Keep the published artist_aliases format: every entry names the complete
// credit, never one collaborator standing in for all participating artists.
func lyricsArtistCredits(artists []lyricsArtistCredit) (string, []string) {
	var names []string
	variants := []string{""}
	for _, artist := range artists {
		if strings.TrimSpace(artist.Name) == "" {
			return "", nil
		}
		names = append(names, artist.Name)
		choices := []string{artist.Name}
		seen := map[string]bool{lyricsTag(artist.Name): true}
		for _, alias := range artist.Aliases {
			key := lyricsTag(alias)
			if key != "" && !seen[key] && len(alias) <= 4000 && len(choices) < 8 {
				choices = append(choices, alias)
				seen[key] = true
			}
		}
		var next []string
		for _, prefix := range variants {
			for _, name := range choices {
				credit := name
				if prefix != "" {
					credit = prefix + " / " + name
				}
				if len(credit) <= 4000 && len(next) < 32 {
					next = append(next, credit)
				}
			}
		}
		variants = next
	}
	primary := strings.Join(names, " / ")
	var aliases []string
	for _, variant := range variants {
		if variant != primary {
			aliases = append(aliases, variant)
		}
	}
	return primary, aliases
}
