package agui

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

func TestWireIntegerLexicalFormsAndOpaqueNumbers(t *testing.T) {
	raw := []byte(`{"type":"RUN_FINISHED","threadId":"t","runId":"r","timestamp":1e3,"usage":[{"provider":"p","model":"m","inputTokens":1.0,"outputTokens":9e1,"totalTokens":9.1e1}],"metadata":{"exact":9007199254740993,"decimal":1.00000000000000000001,"lexical":1e3}}`)
	if err := ValidateEvent(raw); err != nil {
		t.Fatal(err)
	}
	wire, err := DecodeEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeEvent(wire)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(encoded, &fields)
	if string(fields["timestamp"]) != "1000" {
		t.Fatal(string(encoded))
	}
	var metadata map[string]json.RawMessage
	json.Unmarshal(fields["metadata"], &metadata)
	for key, want := range map[string]string{"exact": "9007199254740993", "decimal": "1.00000000000000000001", "lexical": "1e3"} {
		if string(metadata[key]) != want {
			t.Fatalf("opaque %s lost: %s", key, metadata[key])
		}
	}
	var compatibility Event
	if err := json.Unmarshal(raw, &compatibility); err != nil {
		t.Fatal(err)
	}
	if compatibility.Timestamp != 1000 {
		t.Fatal(compatibility.Timestamp)
	}
	for _, bad := range []string{"1.25", "9007199254740992", "1e30", "-9007199254740992", "9223372036854775808", "1e-99999999999999999999"} {
		if _, err := wireSafeInteger(bad); err == nil {
			t.Errorf("accepted invalid bounded integer %s", bad)
		}
		if _, err := DecodeEvent([]byte(`{"type":"RAW","event":{},"timestamp":` + bad + `}`)); err == nil {
			t.Errorf("schema/codec accepted %s", bad)
		}
	}
	for value, want := range map[string]string{"1.0": "1", "1e3": "1000", "90071992547409910e-1": "9007199254740991", "-9007199254740991.0": "-9007199254740991", "0e999999999999999999999": "0", "-0.000e-999999999999999999999": "0"} {
		got, err := wireSafeInteger(value)
		if err != nil || got != want {
			t.Fatalf("%s -> %s %v", value, got, err)
		}
	}
}
func TestEveryDeclaredWireIntegerFieldAcceptsIntegralDecimals(t *testing.T) {
	data, err := os.ReadFile("testdata/wire-all-definitions.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]json.RawMessage
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	// Every generated object with declared safe integers, including the nested
	// usage/capabilities shapes and each event's inherited timestamp.
	var schema struct {
		Defs map[string]map[string]json.RawMessage `json:"$defs"`
	}
	data, err = os.ReadFile("wire-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	count := 0
	for name, definition := range schema.Defs {
		raw, known := fixtures[name]
		if !known {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		var properties map[string]struct {
			Type string `json:"type"`
		}
		json.Unmarshal(definition["properties"], &properties)
		if properties == nil {
			properties = map[string]struct {
				Type string `json:"type"`
			}{}
		}
		// Timestamp is composed through BaseEvent, so fixture presence is the
		// definitive generated property inventory for those event definitions.
		if _, present := fields["timestamp"]; present {
			properties["timestamp"] = struct {
				Type string `json:"type"`
			}{"integer"}
		}
		for key, property := range properties {
			if property.Type != "integer" {
				continue
			}
			count++
			t.Run(name+"/"+key, func(t *testing.T) {
				fields[key] = json.RawMessage(`0.0e3`)
				encoded, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				if err = validateDefinition(encoded, name); err != nil {
					t.Fatal(err)
				}
				normalized, err := normalizeWireIntegers(encoded, key)
				if err != nil {
					t.Fatal(err)
				}
				var output map[string]json.RawMessage
				json.Unmarshal(normalized, &output)
				if string(output[key]) != strconv.Itoa(0) {
					t.Fatal(string(normalized))
				}
			})
		}
	}
	if count < 40 {
		t.Fatalf("only %d declared integer fields covered", count)
	}
	// The complete union must also recurse into custom integer object decoders.
	caps, err := DecodeCapabilities([]byte(`{"execution":{"maxIterations":1.0,"maxExecutionTime":1e3}}`))
	if err != nil {
		t.Fatal(err)
	}
	if caps.Execution.MaxIterations == nil || *caps.Execution.MaxIterations != 1 || caps.Execution.MaxExecutionTime == nil || *caps.Execution.MaxExecutionTime != 1000 {
		t.Fatal(caps.Execution)
	}
}
