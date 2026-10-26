package forge

import (
	"encoding/json"
	"testing"
)

func TestAdditionalLyricsProvidersKeepPublishedJSONReadable(t *testing.T) {
	status := LyricsStatus{State: "ready", Output: "Album/01.opus", Signature: "audio", Path: "Album/01.lrc", Provider: "petitlyrics", Checked: 123,
		Attempts: []lyricsAttempt{{Provider: "qq", State: "not_found"}, {Provider: "petitlyrics", State: "ready"}, {Provider: "lrclib", State: "not_found"}}}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	// The alias represents the struct and closed provider labels shipped in
	// published frontends. New optional fields must stay outside these fields.
	var legacy lyricsStatusJSON
	if json.Unmarshal(raw, &legacy) != nil || legacy.State != "ready" || legacy.Provider != "" || len(legacy.Attempts) != 2 {
		t.Fatal("new provider reached legacy fields", string(raw))
	}
	for _, attempt := range legacy.Attempts {
		if !legacyLyricsProvider(attempt.Provider) {
			t.Fatal("old frontend cannot label attempt", attempt)
		}
	}
	var restored LyricsStatus
	if json.Unmarshal(raw, &restored) != nil || restored.Provider != status.Provider || restored.Checked != status.Checked || len(restored.Attempts) != 3 {
		t.Fatal("new provider identity did not round-trip", restored)
	}
	file := lyricsFile{Path: status.Path, ID: 1, SourceHash: "source", Signature: "lyrics", Provider: "petitlyrics"}
	raw, err = json.Marshal(file)
	var oldFile lyricsFileJSON
	if err != nil || json.Unmarshal(raw, &oldFile) != nil || oldFile.Provider != "" || oldFile.Path != file.Path || oldFile.Signature != file.Signature {
		t.Fatal("old ownership reader lost format", string(raw), err)
	}
	var newFile lyricsFile
	if json.Unmarshal(raw, &newFile) != nil || newFile != file {
		t.Fatal("lost provider ownership identity", newFile)
	}
}

func TestKnownLyricsProvidersKeepTheirPublishedJSON(t *testing.T) {
	for _, provider := range []string{"", "source", "lrclib", "qq", "kugou", "netease"} {
		status := LyricsStatus{State: "ready", Output: "a.opus", Signature: "audio", Provider: provider, Checked: 42, Attempts: []lyricsAttempt{{Provider: "qq", State: "not_found"}}}
		got, err := json.Marshal(status)
		want, _ := json.Marshal(lyricsStatusJSON(status))
		if err != nil || string(got) != string(want) {
			t.Fatal("changed existing status format", provider, string(got), string(want), err)
		}
		var restored LyricsStatus
		if json.Unmarshal(want, &restored) != nil || restored.Provider != provider || restored.Checked != status.Checked {
			t.Fatal("cannot read published status", restored)
		}
		file := lyricsFile{Path: "a.lrc", ID: 1, SourceHash: "source", Signature: "lyrics", Provider: provider}
		got, err = json.Marshal(file)
		want, _ = json.Marshal(lyricsFileJSON(file))
		if err != nil || string(got) != string(want) {
			t.Fatal("changed existing ownership format", provider, string(got), string(want), err)
		}
	}
}
