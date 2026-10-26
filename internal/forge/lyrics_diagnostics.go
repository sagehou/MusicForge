package forge

import (
	"strings"
	"unicode/utf8"
)

const maxLyricsDiagnosticBytes = 64 * 1024

// Only explicitly selected public lyric fields may enter a diagnostic. Never
// include an HTTP response, headers, cookies or provider session metadata.
type lyricsRawField struct {
	Name      string `json:"name"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

type lyricsDiagnostic struct {
	Error        string           `json:"error"`
	Fields       []lyricsRawField `json:"fields"`
	Instrumental bool             `json:"instrumental,omitempty"`
	Uncollected  bool             `json:"uncollected,omitempty"`
}

func (d *lyricsDiagnostic) add(name, text string) {
	remaining := maxLyricsDiagnosticBytes
	for _, field := range d.Fields {
		remaining -= len(field.Text)
	}
	text = strings.ToValidUTF8(text, "\ufffd")
	truncated := len(text) > remaining
	if truncated {
		text = text[:remaining]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	d.Fields = append(d.Fields, lyricsRawField{Name: name, Text: text, Truncated: truncated})
}

func (d *lyricsDiagnostic) failed(err error) lyricsText {
	d.Error = err.Error()
	if d.Fields == nil {
		d.Fields = []lyricsRawField{}
	}
	// This pointer is request-only and is never serialized into cached lyricsText.
	return lyricsText{diagnostic: d}
}
