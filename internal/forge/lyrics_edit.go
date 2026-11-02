package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (a *App) verifyOutputStorage(ctx context.Context, s Settings) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.Output)
	if err != nil {
		return errors.New("Output storage offline or ownership marker missing")
	}
	defer root.Close()
	instance, err := a.metaContext(ctx, "instance")
	if err != nil {
		return err
	}
	marker, err := root.ReadFile(".musicforge")
	if err != nil || string(marker) != instance {
		return errors.New("Output storage offline or ownership marker missing")
	}
	return nil
}

var lrcOffset = regexp.MustCompile(`(?im)^\[offset:\s*([+-]?[0-9]+)\s*\]\s*`)

// Bake timing into each timestamp so clients need not implement LRC offsets.
// Positive delay moves lyrics later; an existing positive LRC offset advances them.
func shiftLRC(text string, delay int64) (string, error) {
	if delay < -3600000 || delay > 3600000 {
		return "", errors.New("Lyrics delay must be between -3600000 and 3600000 milliseconds")
	}
	text, err := normalizeLRC(text)
	if err != nil {
		return "", err
	}
	offset := int64(0)
	match := lrcOffset.FindStringSubmatch(text)
	if delay == 0 && len(match) == 0 { return text, nil }
	if len(match) > 0 {
		offset, err = strconv.ParseInt(match[1], 10, 64)
		if err != nil || offset < -86400000 || offset > 86400000 {
			return "", errors.New("Invalid LRC offset")
		}
	}
	text = lrcOffset.ReplaceAllString(text, "")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i, line := range lines {
		var stamps strings.Builder
		for {
			stamp := lrcTimestamp.FindString(line)
			if stamp == "" {
				break
			}
			minutes, seconds, _ := strings.Cut(stamp[1:len(stamp)-1], ":")
			minute, _ := strconv.ParseInt(minutes, 10, 64)
			parts := strings.FieldsFunc(seconds, func(r rune) bool { return r == '.' || r == ':' })
			second, _ := strconv.ParseInt(parts[0], 10, 64)
			fraction := int64(0)
			if len(parts) > 1 {
				fraction, _ = strconv.ParseInt((parts[1]+"000")[:3], 10, 64)
			}
			millis := max(0, minute*60000+second*1000+fraction-offset+delay)
			if millis/60000 > 9999 {
				return "", errors.New("Adjusted lyrics timestamp exceeds the LRC range")
			}
			fmt.Fprintf(&stamps, "[%02d:%02d.%03d]", millis/60000, millis/1000%60, millis%1000)
			line = line[len(stamp):]
		}
		lines[i] = stamps.String() + line
	}
	return normalizeLRC(strings.Join(lines, "\n"))
}

func (a *App) getLyricsEdit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 400, errors.New("invalid source ID"))
		return
	}
	target, err := a.captureLyricsTarget(r.Context(), id, true)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	text := ""
	if target.FilePresent {
		_, text, _, err = a.readLyricsFile(r.Context(), target.Settings, lyricsRel(target.Source.Output))
		if err != nil || digest(text) != target.FileSignature {
			apiError(w, 409, errors.New("LRC changed during publication; file preserved"))
			return
		}
	}
	choice := a.newLyricsChoices(target, "source", "", []lyricsCandidate{{Provider: "source", text: &lyricsText{Original: text}}}, false, 0)
	respond(w, 200, map[string]any{"token": choice.Token, "text": text})
}

func (a *App) saveLyricsEdit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
		Text string `json:"text"`
		Delay int64 `json:"delay_ms"`
	}
	if !decode(w, r, &body, 8*maxLyricsBytes+4096) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	a.lyricsMu.Lock()
	a.trimLyricsChoices()
	set := a.lyricsChoices[body.Token]
	var target lyricsTarget
	valid := err == nil && id > 0 && set != nil && set.Provider == "source" && set.Target.OutputOnly && set.Target.Source.ID == id
	if valid {
		target = set.Target
	}
	a.lyricsMu.Unlock()
	if !valid {
		apiError(w, 409, errors.New("Lyrics candidates expired; search again"))
		return
	}
	text, err := shiftLRC(body.Text, body.Delay)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	if err = a.publishLyricsTarget(r.Context(), target, text, "source"); err != nil {
		apiError(w, 409, err)
		return
	}
	status := LyricsStatus{State: "ready", Output: target.Source.Output, Signature: target.Signature,
		Path: lyricsRel(target.Source.Output), Provider: "source", Checked: time.Now().Unix(), Instrumental: text == instrumentalLRC}
	raw, _ := json.Marshal(status)
	if err = a.setMeta("lyrics-status:"+strconv.FormatInt(id, 10), string(raw)); err != nil {
		apiError(w, 500, err)
		return
	}
	a.discardLyricsSearches(id)
	a.lyricsMu.Lock()
	delete(a.lyricsChoices, body.Token)
	a.lyricsMu.Unlock()
	respond(w, 200, map[string]any{"status": status, "text": text})
}
