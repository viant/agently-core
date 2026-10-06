package extensions

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed execution-v1.schema.json
var executionSchema []byte
var executionOnce sync.Once
var executionValidator *jsonschema.Schema
var executionSchemaError error

func ValidateExecutionEnvelope(data []byte) error {
	executionOnce.Do(func() {
		var document any
		if executionSchemaError = json.Unmarshal(executionSchema, &document); executionSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		const resource = "https://agently.local/execution-v1.schema.json"
		if executionSchemaError = compiler.AddResource(resource, document); executionSchemaError == nil {
			executionValidator, executionSchemaError = compiler.Compile(resource)
		}
	})
	if executionSchemaError != nil {
		return executionSchemaError
	}
	if !json.Valid(data) {
		return fmt.Errorf("execution selection requires one JSON object")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	return executionValidator.Validate(value)
}
