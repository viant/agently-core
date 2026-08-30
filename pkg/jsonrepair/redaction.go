package jsonrepair

import (
	"bytes"
	"encoding/json"
	"strings"
)

// NormalizeBareRedactionMarkers repairs upstream safety filters that replace a
// JSON scalar with an unquoted [REDACTED:KIND] marker. Markers inside strings
// remain unchanged. The result is returned only when the complete JSON value
// is valid after repair.
func NormalizeBareRedactionMarkers(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || json.Valid([]byte(trimmed)) || !strings.Contains(trimmed, "[REDACTED:") {
		return "", false
	}
	var out bytes.Buffer
	inString := false
	escaped := false
	replaced := false
	for i := 0; i < len(input); {
		ch := input[i]
		if inString {
			out.WriteByte(ch)
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			i++
			continue
		}
		if ch == '"' {
			inString = true
			out.WriteByte(ch)
			i++
			continue
		}
		if strings.HasPrefix(input[i:], "[REDACTED:") {
			if end := strings.IndexByte(input[i:], ']'); end >= 0 {
				written := out.Bytes()
				cut := len(written)
				for cut > 0 && isJSONNumberByte(written[cut-1]) {
					cut--
				}
				if cut < len(written) {
					out.Truncate(cut)
				}
				out.WriteString("null")
				i += end + 1
				replaced = true
				continue
			}
		}
		out.WriteByte(ch)
		i++
	}
	if !replaced || !json.Valid(out.Bytes()) {
		return "", false
	}
	return out.String(), true
}

func isJSONNumberByte(value byte) bool {
	switch value {
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '+', '-', '.', 'e', 'E':
		return true
	default:
		return false
	}
}
