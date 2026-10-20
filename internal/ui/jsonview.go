package ui

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

var jsonToken = regexp.MustCompile(`("(?:\\.|[^"\\])*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false|null)\b`)

// PrettyJSON indents raw JSON; invalid JSON is returned unchanged.
func PrettyJSON(raw []byte) string {
	var out bytes.Buffer
	if json.Indent(&out, bytes.TrimSpace(raw), "", "  ") != nil {
		return string(raw)
	}
	return out.String()
}

// HighlightJSON pretty-prints and colours JSON for terminal display.
func HighlightJSON(raw []byte) string {
	pretty := PrettyJSON(raw)
	return jsonToken.ReplaceAllStringFunc(pretty, func(tok string) string {
		m := jsonToken.FindStringSubmatch(tok)
		switch {
		case m[1] != "" && m[2] != "":
			return Link.Render(m[1]) + Muted.Render(m[2])
		case m[1] != "":
			return Success.Render(m[1])
		case m[3] != "":
			return Warning.Render(m[3])
		case m[4] != "":
			return Accent.Render(m[4])
		}
		return tok
	})
}

// IndentLines prefixes every line.
func IndentLines(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
