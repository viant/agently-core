package agent

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/viant/agently-core/genai/llm"
)

type cloneSchemaMarshaler struct {
	calls *int
}

func (m cloneSchemaMarshaler) MarshalJSON() ([]byte, error) {
	(*m.calls)++
	return []byte(`{"custom":[1,"value"]}`), nil
}

// Keep a reference to the original behavior, including its error fallback.
func originalCloneInterfaceMap(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return nil
	}
	raw, err := json.Marshal(src)
	if err != nil {
		out := make(map[string]interface{}, len(src))
		for key, value := range src {
			out[key] = value
		}
		return out
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

func TestCloneInterfaceMapJSONEquivalence(t *testing.T) {
	var nilMap map[string]interface{}
	var nilSlice []interface{}
	var nilPointer *int
	calls := 0
	cycle := map[string]interface{}{}
	cycle["cycle"] = cycle
	deep := map[string]interface{}{"leaf": "value"}
	for i := 0; i < 1100; i++ {
		deep = map[string]interface{}{"nested": deep}
	}
	for _, tc := range []struct {
		name string
		src  map[string]interface{}
	}{
		{"nil", nil},
		{"empty", map[string]interface{}{}},
		{"native", map[string]interface{}{"string": "<&>\u2028", "bool": true, "number": 1.25, "negative_zero": math.Copysign(0, -1), "nil": nil, "nested": []interface{}{map[string]interface{}{"value": "x"}, []interface{}{false, 2.0}}}},
		{"nested_empty_and_typed_nil", map[string]interface{}{"nil_map": nilMap, "nil_slice": nilSlice, "empty_map": map[string]interface{}{}, "empty_slice": []interface{}{}}},
		{"mixed_nested_fallback", map[string]interface{}{"nested": []interface{}{map[string]interface{}{"int": 42, "strings": []string{"a", "b"}, "number": json.Number("1.25"), "typed_nil": nilPointer}}}},
		{"unmarshal_number_overflow", map[string]interface{}{"number": json.Number("1e1000")}},
		{"custom_marshaler", map[string]interface{}{"native": []interface{}{map[string]interface{}{"custom": cloneSchemaMarshaler{calls: &calls}}}}},
		{"invalid_utf8_value", map[string]interface{}{"value": string([]byte{0xff, 'a'})}},
		{"invalid_utf8_key", map[string]interface{}{string([]byte{0xff}): "value"}},
		{"unsupported_nested", map[string]interface{}{"native": map[string]interface{}{"integer": 42}, "bad": []interface{}{make(chan int)}}},
		{"nonfinite", map[string]interface{}{"native": map[string]interface{}{"integer": 42}, "bad": math.Inf(1)}},
		{"cycle", cycle},
		{"deep", deep},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cloneInterfaceMap(tc.src)
			want := originalCloneInterfaceMap(tc.src)
			if !reflect.DeepEqual(got, want) {
				t.Fatal("clone differs from the original JSON round trip")
			}
			if tc.name == "native" && math.Signbit(got["negative_zero"].(float64)) != math.Signbit(want["negative_zero"].(float64)) {
				t.Fatal("clone changed the sign of negative zero")
			}
		})
	}
	if calls != 2 {
		t.Fatalf("custom marshaler called %d times, want one call per clone", calls)
	}
}

func TestCloneToolDefinitionIsolation(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "native"
		if fallback {
			name = "mixed_fallback"
		}
		t.Run(name, func(t *testing.T) {
			schema := map[string]interface{}{
				"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
				"choices":    []interface{}{map[string]interface{}{"enum": []interface{}{"a", "b"}}},
			}
			if fallback {
				schema["minimum"] = 1 // Forces JSON normalization of the entire schema.
			}
			input := llm.ToolDefinition{Parameters: schema, OutputSchema: schema, Required: []string{"name"}}
			first := cloneToolDefinition(input)
			second := cloneToolDefinition(input)
			first.Parameters["properties"].(map[string]interface{})["name"].(map[string]interface{})["type"] = "number"
			first.Parameters["choices"].([]interface{})[0].(map[string]interface{})["enum"].([]interface{})[0] = "changed"
			first.OutputSchema["properties"].(map[string]interface{})["added"] = true
			first.Required[0] = "changed"
			if !reflect.DeepEqual(second.Parameters, originalCloneInterfaceMap(schema)) || !reflect.DeepEqual(second.OutputSchema, originalCloneInterfaceMap(schema)) {
				t.Fatal("mutating one clone affected another clone or the source")
			}
			if schema["properties"].(map[string]interface{})["name"].(map[string]interface{})["type"] != "string" || input.Required[0] != "name" || second.Required[0] != "name" {
				t.Fatal("mutating one clone affected the source or required fields")
			}
			if schema["choices"].([]interface{})[0].(map[string]interface{})["enum"].([]interface{})[0] != "a" || second.Parameters["choices"].([]interface{})[0].(map[string]interface{})["enum"].([]interface{})[0] != "a" {
				t.Fatal("nested slices share mutable data")
			}
			if _, exists := first.Parameters["properties"].(map[string]interface{})["added"]; exists {
				t.Fatal("parameter and output schema maps share mutable data")
			}
		})
	}
}
