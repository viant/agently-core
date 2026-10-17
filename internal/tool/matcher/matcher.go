package matcher

import (
	mcpname2 "github.com/viant/agently-core/protocol/mcpname"
	"strings"
)

// Canon normalizes a tool name/pattern by trimming spaces and replacing
// both '/' and ':' with '_' for stable comparisons across different
// separators used in definitions and patterns.
func Canon(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ReplaceAll(mcpname2.Canonical(s), "-", "_")
}

// Match returns true when pattern matches name using simple rules:
// - Exact match
// - '*' suffix means prefix match
// - Service-only pattern (no ':' and no '*') matches any method under that service
func Match(pattern, name string) bool {
	return Prepare(pattern).MatchCanonical(Canon(name))
}

// PreparedPattern reuses pattern normalization while matching one catalog.
// Values are immutable; preparing a pattern grants no tool visibility or access.
type PreparedPattern struct {
	raw, canonical string
}

func Prepare(pattern string) PreparedPattern {
	return PreparedPattern{raw: pattern, canonical: Canon(pattern)}
}

// Key identifies equivalent patterns for local match-result reuse. With the
// legacy plain-prefix fallback, raw service-only spelling cannot change the
// result of two patterns with the same canonical form.
func (p PreparedPattern) Key() string { return p.canonical }

// MatchCanonical expects a name normalized with Canon, not mcpname.Canonical.
func (p PreparedPattern) MatchCanonical(ncanon string) bool {
	pcanon := p.canonical
	if pcanon == "*" {
		return true
	}
	if pcanon == ncanon {
		return true
	}
	if strings.Contains(pcanon, "*") {
		prefix := strings.TrimSuffix(pcanon, "*")
		return strings.HasPrefix(ncanon, prefix)
	}
	// Service-only pattern: when user provided just a service (no method)
	// Detect on the raw pattern to allow forms like "system/exec" or "system:exec".
	raw := strings.TrimSpace(p.raw)
	if raw != "" && !strings.Contains(raw, ":") && !strings.Contains(raw, "*") {
		// Compare service portion only
		return strings.HasPrefix(ncanon, pcanon)
	}
	// Fallback: plain prefix match (legacy behavior)
	if strings.HasPrefix(ncanon, pcanon) {
		return true
	}
	return false
}
