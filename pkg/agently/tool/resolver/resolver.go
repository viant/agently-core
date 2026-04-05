package resolver

import (
	"fmt"
	"strconv"
	"strings"
)

// Select resolves a value from either the input or output JSON-like roots
// (maps/slices produced by json.Unmarshal) based on a dot/bracket selector.
//
// Supported prefixes:
//   - "output" (default when no prefix is provided)
//   - "input"
//
// Supported path syntax:
//   - dot-separated keys: a.b.c
//   - array indices: a.0.b or a[0].b
//
// When a path segment cannot be resolved the function returns nil.
func Select(selector string, input, output interface{}) interface{} {
	sel := strings.TrimSpace(selector)
	if sel == "" || sel == "output" {
		return output
	}
	if sel == "input" {
		return input
	}

	// Determine root
	var root interface{}
	switch {
	case strings.HasPrefix(sel, "output."):
		root = output
		sel = strings.TrimPrefix(sel, "output.")
	case strings.HasPrefix(sel, "input."):
		root = input
		sel = strings.TrimPrefix(sel, "input.")
	default:
		// default to output root
		root = output
	}

	// Tokenize sel into path segments supporting brackets
	tokens := tokenize(sel)
	if len(tokens) == 0 {
		return root
	}
	cur := root
	for _, tok := range tokens {
		if cur == nil {
			return nil
		}
		// Attempt array index first
		if idx, ok := parseIndex(tok); ok {
			switch arr := cur.(type) {
			case []any:
				if idx < 0 || idx >= len(arr) {
					return nil
				}
				cur = arr[idx]
				continue
			}
			return nil
		}
		switch m := cur.(type) {
		case map[string]any:
			cur = m[tok]
		default:
			return nil
		}
	}
	return cur
}

// Assign writes a value into the provided JSON-like map using the same
// selector syntax supported by Select, but without the input/output prefix.
// Missing object segments are created automatically. Array writes are only
// supported when the indexed segment already exists.
func Assign(target map[string]interface{}, selector string, value interface{}) error {
	if target == nil {
		return fmt.Errorf("target is nil")
	}
	path := strings.TrimSpace(selector)
	path = strings.TrimPrefix(path, "input.")
	path = strings.TrimPrefix(path, "output.")
	tokens := tokenize(path)
	if len(tokens) == 0 {
		return fmt.Errorf("selector is required")
	}
	var cur interface{} = target
	for i, tok := range tokens {
		last := i == len(tokens)-1
		if idx, ok := parseIndex(tok); ok {
			arr, ok := cur.([]interface{})
			if !ok {
				return fmt.Errorf("selector %q expects array at %q", selector, tok)
			}
			if idx < 0 || idx >= len(arr) {
				return fmt.Errorf("selector %q index %d out of bounds", selector, idx)
			}
			if last {
				arr[idx] = value
				return nil
			}
			cur = arr[idx]
			continue
		}
		obj, ok := cur.(map[string]interface{})
		if !ok {
			return fmt.Errorf("selector %q expects object at %q", selector, tok)
		}
		if last {
			obj[tok] = value
			return nil
		}
		next, exists := obj[tok]
		if !exists || next == nil {
			if _, nextIsIndex := parseIndex(tokens[i+1]); nextIsIndex {
				return fmt.Errorf("selector %q cannot create array segment %q automatically", selector, tok)
			}
			child := map[string]interface{}{}
			obj[tok] = child
			cur = child
			continue
		}
		cur = next
	}
	return nil
}

// tokenize splits a selector into tokens, handling a[0].b and a.0.b styles.
func tokenize(path string) []string {
	if path == "" {
		return nil
	}
	// Normalize bracket indices to dot form: a[0].b -> a.0.b
	normalized := bracketToDot(path)
	// Remove duplicate dots
	normalized = strings.Trim(normalized, ".")
	if normalized == "" {
		return nil
	}
	parts := strings.Split(normalized, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func bracketToDot(s string) string {
	// Replace [number] with .number
	b := strings.Builder{}
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '[' {
			// find closing ]
			j := strings.IndexByte(s[i+1:], ']')
			if j == -1 {
				// no closing bracket; write rest and break
				b.WriteString(s[i:])
				break
			}
			j = i + 1 + j
			idx := s[i+1 : j]
			if _, err := strconv.Atoi(idx); err == nil {
				b.WriteByte('.')
				b.WriteString(idx)
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func parseIndex(tok string) (int, bool) {
	if tok == "" {
		return 0, false
	}
	// Fast path: all digits
	for i := 0; i < len(tok); i++ {
		if tok[i] < '0' || tok[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.Atoi(tok)
	if err != nil {
		return 0, false
	}
	return v, true
}
