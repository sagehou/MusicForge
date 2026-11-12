package forge

import (
	"context"
	"errors"
	"strings"
	"unicode"
)

func simplifyLRC(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !lrcTimestamp.MatchString(line) {
			continue
		}
		// Shared ideographs in Japanese/Korean lyrics must not be simplified.
		for _, r := range line {
			if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
				return text
			}
		}
		lines[i] = lyricsSimplifier.Replace(line)
	}
	return strings.Join(lines, "\n")
}

func (a *App) simplifyLyrics(ctx context.Context, settings Settings, id int64, operation string) (status LyricsStatus) {
	status.State = "failed"
	target, err := a.captureLyricsTarget(ctx, id, true)
	target.RecordHistory, target.OperationBatch = true, operation
	if err == nil && (target.Settings.Source != settings.Source || target.Settings.Output != settings.Output) {
		err = errors.New("Artifact or library settings changed during lyrics search; result discarded")
	}
	if err == nil && !target.FilePresent {
		status.State = "not_found"
		return
	}
	if err == nil && (target.File.ID != id || target.File.SourceHash != target.Source.BuiltHash) {
		err = errors.New("Invalid lyrics ownership annotation")
	}
	var text string
	if err == nil {
		_, text, _, err = a.readLyricsFile(ctx, target.Settings, lyricsRel(target.Source.Output))
		if err == nil && digest(text) != target.FileSignature {
			err = errors.New("LRC changed during publication; file preserved")
		}
	}
	if err == nil {
		converted := simplifyLRC(text)
		if converted != text {
			// Conversion keeps the original provider, matching result and check time.
			err = a.publishLyricsTarget(ctx, target, converted, target.File.Provider)
			if err == nil {
				status.replaced = true
				a.discardLyricsSearches(id)
			}
		}
	}
	if err != nil {
		status.Error = err.Error()
	} else {
		status.State = "ready"
	}
	return
}
