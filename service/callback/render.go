package callback

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"
)

// renderPayload applies the callback's payload template against root and
// returns the parsed tool-arg map. The template output MUST produce valid
// JSON; scalar outputs are wrapped as {"input": <scalar>} before parsing
// so simple tools can accept a single argument.
func renderPayload(body string, root map[string]interface{}) (map[string]interface{}, error) {
	rendered, err := renderTemplate(body, root)
	if err != nil {
		return nil, fmt.Errorf("render payload: %w", err)
	}
	trimmed := strings.TrimSpace(rendered)
	if trimmed == "" {
		return map[string]interface{}{}, nil
	}
	if !strings.HasPrefix(trimmed, "{") {
		// Wrap bare scalar or array as {"input": ...}. Encode the raw
		// rendered value as a JSON string to keep the wrapper valid.
		wrapped := fmt.Sprintf(`{"input": %s}`, maybeQuoted(trimmed))
		trimmed = wrapped
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, fmt.Errorf("rendered payload is not valid JSON: %w\n---\n%s\n---", err, rendered)
	}
	return out, nil
}

// maybeQuoted wraps v in quotes when it does not already parse as a JSON
// value (number, bool, null, array, object). Heuristic but safe for the
// small set of scalars typical callbacks emit.
func maybeQuoted(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return `""`
	}
	// Fast paths for things that are already JSON-valued.
	if trimmed == "true" || trimmed == "false" || trimmed == "null" {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, `"`) {
		return trimmed
	}
	if _, err := jsonNumber(trimmed); err == nil {
		return trimmed
	}
	// Fall back to a JSON-quoted string.
	b, _ := json.Marshal(trimmed)
	return string(b)
}

func jsonNumber(s string) (float64, error) {
	var n float64
	return n, json.Unmarshal([]byte(s), &n)
}

// renderTemplate applies Go text/template to src with the supplied root
// and the callback func map.
func renderTemplate(src string, root map[string]interface{}) (string, error) {
	t, err := template.New("callback").
		Option("missingkey=zero").
		Funcs(funcMap()).
		Parse(src)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, root); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"json":    jsonEncode,
		"lower":   strings.ToLower,
		"upper":   strings.ToUpper,
		"trim":    strings.TrimSpace,
		"default": defaultFunc,
		"iso":     isoFormat,
	}
}

func jsonEncode(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// defaultFunc returns fallback when v is nil or the empty string.
// Usage: {{default "unknown" .agencyId}}
func defaultFunc(fallback, v interface{}) interface{} {
	if v == nil {
		return fallback
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
		return fallback
	}
	return v
}

// isoFormat turns a time.Time into RFC3339. Accepts time.Time or string;
// passes strings through unchanged (useful when upstream already formatted).
func isoFormat(v interface{}) string {
	switch t := v.(type) {
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	case string:
		return t
	default:
		return ""
	}
}
