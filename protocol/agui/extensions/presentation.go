package extensions

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed presentation-v1.schema.json
var presentationSchema []byte
var presentationOnce sync.Once
var presentationDefinitions map[string]*jsonschema.Schema
var presentationSchemaError error

func ValidatePresentationMetadata(data []byte) error {
	return ValidatePresentation("Presentation", data)
}
func ValidatePresentation(kind string, data []byte) error {
	presentationOnce.Do(func() {
		var document any
		if presentationSchemaError = json.Unmarshal(presentationSchema, &document); presentationSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		const resource = "https://agently.local/presentation-v1.schema.json"
		if presentationSchemaError = compiler.AddResource(resource, document); presentationSchemaError != nil {
			return
		}
		presentationDefinitions = map[string]*jsonschema.Schema{}
		for _, name := range []string{"Presentation", "agently.turn", "agently.feed", "agently.planner", "agently.tools-planned", "agently.narration", "agently.user-identity", "agently.tool", "FeedActivationSnapshot", "FeedActivationFact", "FeedActivationSource"} {
			schema, err := compiler.Compile(resource + "#/$defs/" + name)
			if err != nil {
				presentationSchemaError = err
				return
			}
			presentationDefinitions[name] = schema
		}
	})
	if presentationSchemaError != nil {
		return presentationSchemaError
	}
	schema := presentationDefinitions[kind]
	if schema == nil {
		return fmt.Errorf("unsupported presentation kind %q", kind)
	}
	if !json.Valid(data) {
		return fmt.Errorf("presentation requires one JSON value")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	return schema.Validate(value)
}
