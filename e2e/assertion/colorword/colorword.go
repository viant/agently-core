// Package colorword validates literal color readouts in native e2e fixtures.
package colorword

import "strings"

// Matches accepts exactly the requested ASCII color word, ignoring letter case,
// surrounding whitespace, one terminal punctuation mark and paired quote wrappers.
// It does not infer a color from narration, explanations or refusals.
func Matches(text, expected string) bool {
	if expected == "" {
		return false
	}
	for _, char := range expected {
		if char < 'A' || char > 'Z' {
			if char < 'a' || char > 'z' {
				return false
			}
		}
	}
	text = strings.TrimSpace(text)
	punctuationRemoved := false
	if len(text) > 0 && strings.ContainsRune(".!?", rune(text[len(text)-1])) {
		text = text[:len(text)-1]
		punctuationRemoved = true
	}
	if len(text) >= 2 && (text[0] == '\'' || text[0] == '"') {
		if text[len(text)-1] != text[0] {
			return false
		}
		text = text[1 : len(text)-1]
	}
	if !punctuationRemoved && len(text) > 0 && strings.ContainsRune(".!?", rune(text[len(text)-1])) {
		text = text[:len(text)-1]
	}
	return strings.EqualFold(text, expected)
}
