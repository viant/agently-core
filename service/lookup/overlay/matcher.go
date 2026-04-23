// Package overlay implements the server-side overlay layer:
// loading overlays, matching them against incoming JSON Schemas, evaluating
// per-overlay mode, composing the survivors with priority-based tie-break,
// and emitting forge Item.Lookup metadata onto matched properties.
//
// Overlays never leave the server — the client only ever sees the already-
// refined schema or the composed named-token registry.
package overlay

import (
	"regexp"
	"strings"

	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
)

// matchBinding returns the schema property names this binding matches.
// schemaProps is the property map from the incoming JSON Schema
// (map[string]interface{}).
func matchBinding(schemaProps map[string]interface{}, m loproto.Match) []string {
	var candidates []string

	switch {
	case m.Path != "":
		name := strings.TrimPrefix(m.Path, "$.properties.")
		if strings.Contains(name, ".") {
			// Nested path — not supported at v1; skip.
			return nil
		}
		if _, ok := schemaProps[name]; ok {
			candidates = []string{name}
		}

	case m.PathGlob != "":
		pattern := strings.TrimPrefix(m.PathGlob, "$.properties.")
		re, err := globToRegex(pattern)
		if err != nil {
			return nil
		}
		for k := range schemaProps {
			if re.MatchString(k) {
				candidates = append(candidates, k)
			}
		}

	case m.FieldName != "":
		if _, ok := schemaProps[m.FieldName]; ok {
			candidates = []string{m.FieldName}
		}

	case m.FieldNameRegex != "":
		re, err := regexp.Compile(m.FieldNameRegex)
		if err != nil {
			return nil
		}
		for k := range schemaProps {
			if re.MatchString(k) {
				candidates = append(candidates, k)
			}
		}

	default:
		return nil
	}

	// Apply type/format constraints.
	if m.Type == "" && m.Format == "" {
		return candidates
	}
	out := candidates[:0]
	for _, c := range candidates {
		prop, _ := schemaProps[c].(map[string]interface{})
		if prop == nil {
			continue
		}
		if m.Type != "" {
			if t, _ := prop["type"].(string); t != m.Type {
				continue
			}
		}
		if m.Format != "" {
			if f, _ := prop["format"].(string); f != m.Format {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// globToRegex converts a simple glob ("*") to an anchored regex.
func globToRegex(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range glob {
		if r == '*' {
			b.WriteString(".*")
			continue
		}
		// Escape regex metacharacters.
		if strings.ContainsRune(`.+?()|[]{}^$\\`, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// targetMatches decides whether the overlay's Target admits this context.
// contextID is the opaque "kind:id" (e.g. "template:site_list_planner").
// schemaProps is used for Target.SchemaContains checks.
//
// Wildcard semantics:
//
//   - contextKind == "*" (or "") bypasses the Target.Kind check, so
//     overlays with any Kind can still fire. This is how the refiner hook
//     reaches template/tool/elicitation overlays uniformly without a
//     per-request context plumbed into refiner.Refine.
//   - contextID == "*" (or "") bypasses both Target.ID and Target.IDGlob.
//
// SchemaContains still applies, giving workspace authors a narrow filter
// when they need one.
func targetMatches(t loproto.Target, contextKind, contextID string, schemaProps map[string]interface{}) bool {
	kindWildcard := contextKind == "" || contextKind == "*"
	idWildcard := contextID == "" || contextID == "*"

	if !kindWildcard && t.Kind != "" && t.Kind != contextKind {
		return false
	}
	if !idWildcard && t.ID != "" && t.ID != contextID {
		return false
	}
	if !idWildcard && t.IDGlob != "" {
		re, err := globToRegex(t.IDGlob)
		if err != nil || !re.MatchString(contextID) {
			return false
		}
	}
	for _, required := range t.SchemaContains {
		if _, ok := schemaProps[required]; !ok {
			return false
		}
	}
	return true
}
