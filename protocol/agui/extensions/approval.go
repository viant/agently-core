package extensions

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"sync"
)

//go:embed approval-v1.schema.json
var approvalSchema []byte
var approvalOnce sync.Once
var approvalDefinitions map[string]*jsonschema.Schema
var approvalSchemaError error

func ValidateApprovalEnvelope(data []byte) error { return validateApprovalDefinition("Envelope", data) }
func ValidateApprovalPayload(operation string, data []byte) error {
	return validateApprovalDefinition(operation, data)
}
func validateApprovalDefinition(name string, data []byte) error {
	approvalOnce.Do(func() {
		var source any
		if approvalSchemaError = json.Unmarshal(approvalSchema, &source); approvalSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		const resource = "https://agently.local/approval-v1.schema.json"
		if approvalSchemaError = compiler.AddResource(resource, source); approvalSchemaError != nil {
			return
		}
		approvalDefinitions = map[string]*jsonschema.Schema{}
		for _, name := range []string{"Envelope", "approval.decide"} {
			schema, err := compiler.Compile(resource + "#/$defs/" + name)
			if err != nil {
				approvalSchemaError = err
				return
			}
			approvalDefinitions[name] = schema
		}
	})
	if approvalSchemaError != nil {
		return approvalSchemaError
	}
	schema, ok := approvalDefinitions[name]
	if !ok {
		return fmt.Errorf("unsupported approval operation %q", name)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return schema.Validate(value)
}
