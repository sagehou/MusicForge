package forge

import (
	"bytes"
	"compress/zlib"
	"crypto/des"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var cloudLyricAttribute = regexp.MustCompile(`(?s)\bLyricContent="([^"]*)"`)
var millisecondLyricsLine = regexp.MustCompile(`^\[(\d+),(\d+)\](.*)$`)
var wordLyricsTiming = regexp.MustCompile(`\(\d+,\d+(?:,\d+)?\)|<\d+,\d+,\d+>`)
var enhancedLyricsTiming = regexp.MustCompile(`<\d{1,4}:[0-5]\d(?:\.\d{1,3})?>`)

func providerLRC(text string) (string, error) {
	if len(text) > 2*maxLyricsBytes {
		return "", errors.New("LRC must be UTF-8 and no larger than 1 MiB")
	}
	if attr := cloudLyricAttribute.FindStringSubmatch(text); len(attr) > 0 {
		text = html.UnescapeString(attr[1])
	} else {
		text = decodeWebLyrics(text)
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if match := millisecondLyricsLine.FindStringSubmatch(line); match != nil {
			milliseconds, err := strconv.ParseInt(match[1], 10, 64)
			if err != nil || milliseconds > 24*60*60*1000 {
				return "", errors.New("Invalid lyrics timestamp")
			}
			line = lyricsTimestamp(milliseconds) + wordLyricsTiming.ReplaceAllString(match[3], "")
		}
		lines[i] = enhancedLyricsTiming.ReplaceAllString(line, "")
	}
	return normalizeLRC(strings.Join(lines, "\n"))
}

// Cloud QRC uses 3DES ECB followed by zlib. Read with a fixed output bound so
// an upstream response cannot inflate into unbounded application memory.
func decodeCloudQRC(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	if _, err := providerLRC(text); err == nil {
		return text, nil
	}
	if len(text) > 4*maxLyricsBytes {
		return "", errors.New("QQ Music returned invalid lyrics data")
	}
	data, err := hex.DecodeString(text)
	if err != nil || len(data) == 0 || len(data)%des.BlockSize != 0 {
		return "", errors.New("QQ Music returned invalid lyrics data")
	}
	block, err := des.NewTripleDESCipher([]byte("!@#)(*$%123ZXC!@!@#)(NHL"))
	if err != nil {
		return "", err
	}
	for offset := 0; offset < len(data); offset += block.BlockSize() {
		block.Decrypt(data[offset:offset+block.BlockSize()], data[offset:offset+block.BlockSize()])
	}
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("QQ Music returned invalid lyrics data")
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, maxLyricsBytes+1))
	if err != nil || len(raw) > maxLyricsBytes {
		return "", errors.New("QQ Music returned invalid lyrics data")
	}
	return string(raw), nil
}

func lyricsTimestamp(milliseconds int64) string {
	return fmt.Sprintf("[%02d:%02d.%03d]", milliseconds/60000, milliseconds/1000%60, milliseconds%1000)
}

// Keep the original timestamps unless extra synchronized language tracks are
// selected. Merge language tracks in timestamp order, applying each offset once.
func combineLyrics(text lyricsText, translation, romanization bool) (string, error) {
	if !translation && !romanization {
		return normalizeLRC(text.Original)
	}
	inputs := []string{text.Original}
	if translation {
		if text.Translation == "" {
			return "", errors.New("Selected lyrics language is unavailable")
		}
		inputs = append(inputs, text.Translation)
	}
	if romanization {
		if text.Romanization == "" {
			return "", errors.New("Selected lyrics language is unavailable")
		}
		inputs = append(inputs, text.Romanization)
	}
	type line struct {
		time int64
		text string
	}
	var lines []line
	var tags []string
	for language, input := range inputs {
		if _, err := normalizeLRC(input); err != nil {
			return "", err
		}
		var offset int64
		for _, raw := range strings.Split(input, "\n") {
			if strings.HasPrefix(raw, "[offset:") && strings.HasSuffix(raw, "]") {
				parsed, err := strconv.ParseInt(strings.TrimSpace(raw[8:len(raw)-1]), 10, 32)
				if err != nil {
					return "", errors.New("Invalid lyrics timestamp")
				}
				offset = parsed
			}
		}
		for _, raw := range strings.Split(input, "\n") {
			var times []int64
			for {
				stamp := lrcTimestamp.FindString(raw)
				if stamp == "" {
					break
				}
				parts := strings.FieldsFunc(stamp[1:len(stamp)-1], func(r rune) bool { return r == ':' || r == '.' })
				minutes, _ := strconv.ParseInt(parts[0], 10, 32)
				seconds, _ := strconv.ParseInt(parts[1], 10, 32)
				var fraction int64
				if len(parts) == 3 {
					fraction, _ = strconv.ParseInt((parts[2] + "000")[:3], 10, 32)
				}
				// LRC positive offsets display lyrics earlier.
				times = append(times, max(0, minutes*60000+seconds*1000+fraction-offset))
				raw = raw[len(stamp):]
			}
			if len(times) == 0 && language == 0 && strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") && !strings.HasPrefix(raw, "[offset:") {
				tags = append(tags, raw)
			}
			for _, timestamp := range times {
				if language == 0 || strings.TrimSpace(raw) != "" {
					lines = append(lines, line{timestamp, raw})
				}
			}
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].time < lines[j].time })
	var out strings.Builder
	for _, tag := range tags {
		out.WriteString(tag + "\n")
	}
	seen := map[string]bool{}
	for _, line := range lines {
		value := lyricsTimestamp(line.time) + line.text + "\n"
		if !seen[value] {
			out.WriteString(value)
			seen[value] = true
		}
		if out.Len() > maxLyricsBytes {
			return "", errors.New("LRC must be UTF-8 and no larger than 1 MiB")
		}
	}
	return normalizeLRC(out.String())
}
