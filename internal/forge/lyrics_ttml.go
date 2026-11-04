package forge

import (
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
)

func ttmlTimestamp(raw string) (int64, error) {
	invalid := errors.New("Invalid lyrics timestamp")
	value, multiplier := raw, 1.0
	unit := strings.HasSuffix(value, "s")
	if strings.HasSuffix(value, "ms") {
		value, multiplier = strings.TrimSuffix(value, "ms"), 0.001
	} else {
		value = strings.TrimSuffix(value, "s")
	}
	parts := strings.Split(value, ":")
	if value == "" || len(parts) > 3 || len(parts) > 1 && unit || len(parts) == 1 && !unit {
		return 0, invalid
	}
	seconds := 0.0
	for i, part := range parts {
		// Frame/tick and relative timing are deliberately unsupported.
		if part == "" || strings.ContainsAny(part, "+-eE ") || strings.Count(part, ".") > 1 || i < len(parts)-1 && strings.Contains(part, ".") {
			return 0, invalid
		}
		for _, character := range part {
			if (character < '0' || character > '9') && character != '.' {
				return 0, invalid
			}
		}
		value, err := strconv.ParseFloat(part, 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || i > 0 && value >= 60 {
			return 0, invalid
		}
		seconds = seconds*60 + value
	}
	seconds *= multiplier
	if seconds > 24*60*60 {
		return 0, invalid
	}
	return int64(math.Round(seconds * 1000)), nil
}

func ttmlAttribute(element xml.StartElement, name string) string {
	for _, attribute := range element.Attr {
		if attribute.Name.Local == name {
			return attribute.Value
		}
	}
	return ""
}

// Preserve the upstream paragraph start; drop translations, romanization and
// background voices instead of mixing their words into the original line.
func ttmlLineText(decoder *xml.Decoder, start xml.StartElement, depth int) (string, error) {
	if depth > 64 || start.Name.Local == "p" && depth > 1 {
		return "", errors.New("Invalid synchronized TTML lyrics")
	}
	role := ttmlAttribute(start, "role")
	if role == "x-translation" || role == "x-roman" || role == "x-romanization" || role == "x-bg" {
		return "", decoder.Skip()
	}
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", err
		}
		switch token := token.(type) {
		case xml.StartElement:
			child, err := ttmlLineText(decoder, token, depth+1)
			if err != nil {
				return "", err
			}
			text.WriteString(child)
			if token.Name.Local == "br" {
				text.WriteByte(' ')
			}
		case xml.CharData:
			if strings.TrimSpace(string(token)) != "" || !strings.ContainsAny(string(token), "\r\n") {
				text.Write(token)
			}
		case xml.EndElement:
			return text.String(), nil
		case xml.Directive:
			return "", errors.New("Invalid synchronized TTML lyrics")
		}
	}
}

func ttmlLRC(raw string) (string, error) {
	invalid := errors.New("Invalid synchronized TTML lyrics")
	if len(raw) > maxLyricsBytes {
		return "", invalid
	}
	decoder := xml.NewDecoder(strings.NewReader(raw))
	depth, bodyDepth := 0, 0
	root := false
	var lines strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", invalid
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				if root || token.Name.Local != "tt" || token.Name.Space != "" && token.Name.Space != "http://www.w3.org/ns/ttml" {
					return "", invalid
				}
				root = true
			}
			if depth > 64 || ttmlAttribute(token, "timeContainer") == "seq" ||
				ttmlAttribute(token, "timeBase") != "" && ttmlAttribute(token, "timeBase") != "media" {
				return "", invalid
			}
			if token.Name.Local == "p" && bodyDepth > 0 {
				begin, err := ttmlTimestamp(ttmlAttribute(token, "begin"))
				if err != nil {
					return "", err
				}
				if end := ttmlAttribute(token, "end"); end != "" {
					until, err := ttmlTimestamp(end)
					if err != nil || until < begin {
						return "", invalid
					}
				}
				text, err := ttmlLineText(decoder, token, 1)
				if err != nil {
					return "", invalid
				}
				text = strings.Join(strings.Fields(text), " ")
				if text != "" {
					lines.WriteString(lyricsTimestamp(begin) + text + "\n")
				}
				continue
			}
			// Parent offsets require a full TTML timing engine. Never interpret
			// those paragraphs as absolute timestamps or guess missing timing.
			if ttmlAttribute(token, "begin") != "" {
				return "", invalid
			}
			depth++
			if token.Name.Local == "body" {
				bodyDepth = depth
			}
		case xml.EndElement:
			if depth == bodyDepth {
				bodyDepth = 0
			}
			depth--
		case xml.Directive:
			return "", invalid
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(token)) != "" {
				return "", invalid
			}
		}
	}
	if !root || depth != 0 {
		return "", invalid
	}
	return normalizeLRC(lines.String())
}
