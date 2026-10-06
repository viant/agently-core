package extensions

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed state-v1.schema.json
var stateSchema []byte
var stateOnce sync.Once
var stateDefinitions map[string]*jsonschema.Schema
var stateSchemaError error

func ValidateStatePayload(operation string, data []byte) error {
	return validateStateDefinition(operation, data, true)
}
func ValidateStateEnvelope(data []byte) error {
	return validateStateDefinition("Envelope", data, false)
}
func ValidateStateResult(data []byte) error { return validateStateDefinition("Result", data, false) }
func ValidateStateCapabilities(data []byte) error {
	return validateStateDefinition("Capabilities", data, false)
}
func validateStateDefinition(name string, data []byte, omittedPayload bool) error {
	stateOnce.Do(func() {
		var document any
		if stateSchemaError = json.Unmarshal(stateSchema, &document); stateSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		const resource = "https://agently.local/state-v1.schema.json"
		if stateSchemaError = compiler.AddResource(resource, document); stateSchemaError != nil {
			return
		}
		stateDefinitions = map[string]*jsonschema.Schema{}
		for _, name := range []string{"state.get", "state.patch", "Envelope", "Result", "Capabilities"} {
			schema, err := compiler.Compile(resource + "#/$defs/" + strings.TrimPrefix(name, "state."))
			if err != nil {
				stateSchemaError = err
				return
			}
			stateDefinitions[name] = schema
		}
	})
	if stateSchemaError != nil {
		return stateSchemaError
	}
	schema, exists := stateDefinitions[name]
	if !exists {
		return fmt.Errorf("unsupported state command %q", name)
	}
	if omittedPayload && len(bytes.TrimSpace(data)) == 0 {
		data = []byte(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("state payload requires one JSON object")
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("invalid %s: %w", name, err)
	}
	return nil
}
