package extensions

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed run-v1.schema.json
var runSchema []byte
var runOnce sync.Once
var runDefinitions map[string]*jsonschema.Schema
var runSchemaError error

func ValidateRunEnvelope(data []byte) error { return validateRunDefinition("Envelope", data) }
func ValidateRunPayload(operation string, data []byte) error {
	return validateRunDefinition(operation, data)
}
func validateRunDefinition(operation string, data []byte) error {
	runOnce.Do(func() {
		var source any
		if runSchemaError = json.Unmarshal(runSchema, &source); runSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		const resource = "https://agently.local/run-v1.schema.json"
		if runSchemaError = compiler.AddResource(resource, source); runSchemaError != nil {
			return
		}
		runDefinitions = map[string]*jsonschema.Schema{}
		for _, op := range []string{"run.get", "run.cancel", "run.events.list", "run.attach", "Envelope"} {
			schema, err := compiler.Compile(resource + "#/$defs/" + op)
			if err != nil {
				runSchemaError = err
				return
			}
			runDefinitions[op] = schema
		}
	})
	if runSchemaError != nil {
		return runSchemaError
	}
	schema, ok := runDefinitions[operation]
	if !ok {
		return fmt.Errorf("unsupported run operation %q", operation)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return schema.Validate(value)
}
