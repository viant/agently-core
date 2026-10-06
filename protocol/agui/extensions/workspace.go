package extensions

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed workspace-v1.schema.json
var workspaceSchema []byte
var workspaceOnce sync.Once
var workspaceDefinitions map[string]*jsonschema.Schema
var workspaceSchemaError error

func ValidateWorkspaceEnvelope(data []byte) error {
	return validateWorkspaceDefinition("Envelope", data)
}
func ValidateWorkspacePayload(operation string, data []byte) error {
	return validateWorkspaceDefinition(operation, data)
}
func validateWorkspaceDefinition(operation string, data []byte) error {
	workspaceOnce.Do(func() {
		var document struct {
			Operations []string `json:"x-operations"`
		}
		if workspaceSchemaError = json.Unmarshal(workspaceSchema, &document); workspaceSchemaError != nil {
			return
		}
		var source any
		json.Unmarshal(workspaceSchema, &source)
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		const resource = "https://agently.local/workspace-v1.schema.json"
		if workspaceSchemaError = compiler.AddResource(resource, source); workspaceSchemaError != nil {
			return
		}
		workspaceDefinitions = map[string]*jsonschema.Schema{}
		for _, name := range append(document.Operations, "Envelope") {
			schema, err := compiler.Compile(resource + "#/$defs/" + name)
			if err != nil {
				workspaceSchemaError = err
				return
			}
			workspaceDefinitions[name] = schema
		}
	})
	if workspaceSchemaError != nil {
		return workspaceSchemaError
	}
	schema, exists := workspaceDefinitions[operation]
	if !exists {
		return fmt.Errorf("unsupported workspace operation %q", operation)
	}
	if len(bytes.TrimSpace(data)) == 0 {
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
		return fmt.Errorf("workspace payload requires one JSON object")
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("invalid %s: %w", operation, err)
	}
	return nil
}
