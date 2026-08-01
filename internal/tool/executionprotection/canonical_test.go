package executionprotection

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestCanonicalJSONVectors(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		want  string
	}{
		{name: "sorted object", value: map[string]interface{}{"z": 1, "a": "x"}, want: `{"a":"x","z":1}`},
		{name: "array order", value: []interface{}{2, 1}, want: `[2,1]`},
		{name: "null", value: map[string]interface{}{"a": nil}, want: `{"a":null}`},
		{name: "integer exponent", value: map[string]interface{}{"n": 1000}, want: `{"n":1e3}`},
		{name: "number equivalence", value: map[string]interface{}{"n": json.Number("1.00")}, want: `{"n":1}`},
		{name: "negative zero", value: map[string]interface{}{"n": math.Copysign(0, -1)}, want: `{"n":0}`},
		{name: "unicode exact", value: map[string]interface{}{"s": "e\u0301"}, want: `{"s":"é"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := CanonicalJSON(test.value)
			if err != nil {
				t.Fatalf("CanonicalJSON() error = %v", err)
			}
			if string(got) != test.want {
				t.Fatalf("CanonicalJSON() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCanonicalJSONRejectsInvalidValuesAndNumbers(t *testing.T) {
	invalidUTF8 := string([]byte{0xff, 'x'})
	values := []interface{}{
		map[string]interface{}{"f": func() {}},
		map[string]interface{}{"n": math.NaN()},
		map[string]interface{}{"n": math.Inf(1)},
		map[string]interface{}{"n": json.Number("01")},
		map[string]interface{}{"n": json.Number("1e1000001")},
		map[string]interface{}{"n": json.Number(strings.Repeat("9", 1025))},
		invalidUTF8,
		map[string]interface{}{"value": invalidUTF8},
		map[string]interface{}{invalidUTF8: "value"},
	}
	for _, value := range values {
		if _, err := CanonicalJSON(value); err == nil {
			t.Fatalf("CanonicalJSON(%T) error = nil", value)
		}
	}
}

func TestClaimKeyRejectsInvalidUTF8Components(t *testing.T) {
	invalid := string([]byte{0xff})
	validHash := strings.Repeat("a", 64)
	for _, parts := range [][4]string{
		{invalid, "service/tool", "turn", validHash},
		{"rule", invalid, "turn", validHash},
		{"rule", "service/tool", invalid, validHash},
	} {
		if _, err := ClaimKey(parts[0], parts[1], parts[2], parts[3]); err == nil {
			t.Fatalf("ClaimKey(%q, %q, %q, ...) error = nil", parts[0], parts[1], parts[2])
		}
	}
}

func TestCanonicalJSONNullDiffersFromAbsent(t *testing.T) {
	nullValue, err := CanonicalJSON(map[string]interface{}{"a": nil})
	if err != nil {
		t.Fatal(err)
	}
	absent, err := CanonicalJSON(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if string(nullValue) == string(absent) {
		t.Fatalf("null and absent canonical JSON are equal: %s", nullValue)
	}
}

func TestClaimKeyVector(t *testing.T) {
	got, err := ClaimKey("rule-1", "service/tool", "turn-1", "015abd7f5cc57a2dd94b7590f04ad8084273905ee33ec5cebeae62276a97f862")
	if err != nil {
		t.Fatalf("ClaimKey() error = %v", err)
	}
	const want = "fd6599009fe10f57386538e9281694cf8072b2c51dada79b8f5d8c57905595ea"
	if got != want {
		t.Fatalf("ClaimKey() = %q, want %q", got, want)
	}
}
