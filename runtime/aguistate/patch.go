package aguistate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/viant/agently-core/protocol/agui"
)

// ApplyPatchJSON applies RFC 6902 atomically to any JSON value. A transparent
// wrapper avoids json-patch's object/array-only root limitation. Root removal
// yields JSON null, matching the reference fast-json-patch consumer.
func ApplyPatchJSON(state, patch []byte) ([]byte, error) {
	if !json.Valid(state) {
		return nil, fmt.Errorf("invalid JSON state")
	}
	event, err := json.Marshal(map[string]json.RawMessage{"type": json.RawMessage(`"STATE_DELTA"`), "delta": patch})
	if err != nil {
		return nil, err
	}
	if err = agui.ValidateEvent(event); err != nil {
		return nil, err
	}
	var operations []map[string]json.RawMessage
	if err = json.Unmarshal(patch, &operations); err != nil {
		return nil, err
	}
	wrapped, err := json.Marshal(map[string]json.RawMessage{"__agui_state": state})
	if err != nil {
		return nil, err
	}
	for _, operation := range operations {
		var kind, path, from string
		json.Unmarshal(operation["op"], &kind)
		json.Unmarshal(operation["path"], &path)
		json.Unmarshal(operation["from"], &from)
		var current map[string]json.RawMessage
		if err = json.Unmarshal(wrapped, &current); err != nil {
			return nil, err
		}
		raw := current["__agui_state"]
		if raw == nil {
			raw = json.RawMessage(`null`)
		}
		value, err := decodePatchValue(raw)
		if err != nil {
			return nil, err
		}
		if kind == "move" && properPointerPrefix(from, path) {
			return nil, fmt.Errorf("move cannot target a descendant of its source")
		}
		if err = validateArrayPointer(value, path, kind == "add" || kind == "copy" || kind == "move"); err != nil {
			return nil, err
		}
		if kind == "move" || kind == "copy" {
			if err = validateArrayPointer(value, from, false); err != nil {
				return nil, err
			}
		}
		if kind == "test" {
			actual, exists := patchPointer(value, path)
			expected, err := decodePatchValue(operation["value"])
			if err != nil {
				return nil, err
			}
			if !exists || !patchEqual(actual, expected) {
				return nil, fmt.Errorf("JSON Patch test failed at %q", path)
			}
			continue
		}
		adjusted := make(map[string]json.RawMessage, len(operation))
		for key, value := range operation {
			adjusted[key] = value
		}
		adjusted["path"], _ = json.Marshal("/__agui_state" + path)
		if kind == "move" || kind == "copy" {
			adjusted["from"], _ = json.Marshal("/__agui_state" + from)
		}
		encoded, _ := json.Marshal([]map[string]json.RawMessage{adjusted})
		p, err := jsonpatch.DecodePatch(encoded)
		if err != nil {
			return nil, err
		}
		wrapped, err = p.ApplyWithOptions(wrapped, &jsonpatch.ApplyOptions{SupportNegativeIndices: false})
		if err != nil {
			return nil, err
		}
		if kind == "remove" && path == "" {
			wrapped = []byte(`{"__agui_state":null}`)
		}
	}
	var result map[string]json.RawMessage
	if err = json.Unmarshal(wrapped, &result); err != nil {
		return nil, err
	}
	value := result["__agui_state"]
	if value == nil {
		value = json.RawMessage(`null`)
	}
	return append([]byte(nil), value...), nil
}
func decodePatchValue(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	err := decoder.Decode(&value)
	return value, err
}
func pointerTokens(path string) []string {
	if path == "" {
		return nil
	}
	parts := strings.Split(path[1:], "/")
	for i := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(parts[i], "~1", "/"), "~0", "~")
	}
	return parts
}
func properPointerPrefix(from, path string) bool {
	a, b := pointerTokens(from), pointerTokens(path)
	if len(a) >= len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func arrayIndex(token string) (int, bool) {
	if token == "" || len(token) > 1 && token[0] == '0' {
		return 0, false
	}
	for _, c := range token {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	index, err := strconv.Atoi(token)
	return index, err == nil
}
func patchPointer(value any, path string) (any, bool) {
	for _, token := range pointerTokens(path) {
		switch current := value.(type) {
		case map[string]any:
			var exists bool
			value, exists = current[token]
			if !exists {
				return nil, false
			}
		case []any:
			index, valid := arrayIndex(token)
			if !valid || index >= len(current) {
				return nil, false
			}
			value = current[index]
		default:
			return nil, false
		}
	}
	return value, true
}
func validateArrayPointer(value any, path string, allowAppend bool) error {
	tokens := pointerTokens(path)
	for i, token := range tokens {
		switch current := value.(type) {
		case map[string]any:
			child, exists := current[token]
			if !exists {
				return nil
			}
			value = child
		case []any:
			if token == "-" && i == len(tokens)-1 && allowAppend {
				return nil
			}
			index, valid := arrayIndex(token)
			if !valid {
				return fmt.Errorf("invalid RFC 6901 array index %q", token)
			}
			if index >= len(current) {
				return nil
			}
			value = current[index]
		default:
			return nil
		}
	}
	return nil
}
func patchEqual(a, b any) bool {
	if an, ok := a.(json.Number); ok {
		bn, ok := b.(json.Number)
		return ok && numericForm(an.String()) == numericForm(bn.String())
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			other, exists := bv[k]
			if !exists || !patchEqual(v, other) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !patchEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

// Normalize decimal notation without expanding huge exponent values.
func numericForm(value string) string {
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign = "-"
		value = value[1:]
	}
	exponent := new(big.Int)
	if split := strings.IndexAny(value, "eE"); split >= 0 {
		exponent.SetString(value[split+1:], 10)
		value = value[:split]
	}
	if point := strings.IndexByte(value, '.'); point >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(value)-point-1)))
		value = value[:point] + value[point+1:]
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "0"
	}
	stripped := strings.TrimRight(value, "0")
	exponent.Add(exponent, big.NewInt(int64(len(value)-len(stripped))))
	return sign + stripped + "e" + exponent.String()
}
