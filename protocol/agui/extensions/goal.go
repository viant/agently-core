// Package extensions defines versioned Agently AG-UI extension contracts.
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

//go:embed goal-v1.schema.json
var goalSchema []byte
var goalOnce sync.Once
var goalSchemas map[string]*jsonschema.Schema
var goalSchemaError error

var GoalOperations = []string{"goal.get", "goal.create", "goal.update", "goal.clear", "goal.pause", "goal.resume", "goal.subscribe"}

// ValidateGoalPayload rejects undeclared authority fields and optional nulls.
// Omitted payload is an empty object; explicit JSON null remains invalid.
func ValidateGoalPayload(operation string, data []byte) error {
	return validateGoalDefinition(operation, data, true)
}

func ValidateGoalEnvelope(data []byte) error { return validateGoalDefinition("Envelope", data, false) }
func ValidateGoalResult(data []byte) error   { return validateGoalDefinition("Result", data, false) }
func ValidateGoalCapabilities(data []byte) error {
	return validateGoalDefinition("Capabilities", data, false)
}

func validateGoalDefinition(operation string, data []byte, omittedPayload bool) error {
	goalOnce.Do(func() {
		var document any
		if goalSchemaError = json.Unmarshal(goalSchema, &document); goalSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		const resource = "https://agently.local/goal-v1.schema.json"
		if goalSchemaError = compiler.AddResource(resource, document); goalSchemaError != nil {
			return
		}
		goalSchemas = map[string]*jsonschema.Schema{}
		names := append(append([]string{}, GoalOperations...), "Envelope", "Result", "Capabilities")
		for _, name := range names {
			schema, err := compiler.Compile(resource + "#/$defs/" + strings.TrimPrefix(name, "goal."))
			if err != nil {
				goalSchemaError = err
				return
			}
			goalSchemas[name] = schema
		}
	})
	if goalSchemaError != nil {
		return goalSchemaError
	}
	schema, known := goalSchemas[operation]
	if !known {
		return fmt.Errorf("unsupported goal operation %q", operation)
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
		return fmt.Errorf("goal payload requires one JSON object")
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("invalid %s payload: %w", operation, err)
	}
	return nil
}
