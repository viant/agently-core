package extensions

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed conversation-v1.schema.json
var conversationSchema []byte
var conversationOnce sync.Once
var conversationDefinitions map[string]*jsonschema.Schema
var conversationSchemaError error

func ValidateConversationEnvelope(data []byte) error {
	return validateConversationDefinition("Envelope", data)
}
func ValidateConversationPayload(operation string, data []byte) error {
	return validateConversationDefinition(operation, data)
}
func ValidateConversationResult(data []byte) error {
	return validateConversationDefinition("Result", data)
}
func validateConversationDefinition(name string, data []byte) error {
	conversationOnce.Do(func() {
		var source any
		if conversationSchemaError = json.Unmarshal(conversationSchema, &source); conversationSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		const resource = "https://agently.local/conversation-v1.schema.json"
		if conversationSchemaError = compiler.AddResource(resource, source); conversationSchemaError != nil {
			return
		}
		conversationDefinitions = map[string]*jsonschema.Schema{}
		for _, definition := range []string{"Envelope", "conversation.bootstrap", "Result"} {
			schema, err := compiler.Compile(resource + "#/$defs/" + definition)
			if err != nil {
				conversationSchemaError = err
				return
			}
			conversationDefinitions[definition] = schema
		}
	})
	if conversationSchemaError != nil {
		return conversationSchemaError
	}
	schema := conversationDefinitions[name]
	if schema == nil {
		return fmt.Errorf("unsupported conversation operation %q", name)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte(`{}`)
	}
	if !json.Valid(data) {
		return fmt.Errorf("conversation command requires a single JSON value")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("invalid %s: %w", name, err)
	}
	return nil
}
