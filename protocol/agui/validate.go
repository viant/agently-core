package agui

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema-1.0.json
var schemaJSON []byte
var inputSchemaOnce sync.Once
var inputSchema *jsonschema.Schema
var inputSchemaErr error

// ValidateInput checks the exact pinned wire schema before decoding into the
// supported profile's Go types. Optional null values and unknown fields are
// rejected according to AG-UI 1.0, including nested union message shapes.
func ValidateInput(data []byte) error {
	inputSchemaOnce.Do(func() {
		var doc any
		if inputSchemaErr = json.Unmarshal(schemaJSON, &doc); inputSchemaErr != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		const resource = "https://agui.local/schema-1.0.json"
		if inputSchemaErr = compiler.AddResource(resource, doc); inputSchemaErr != nil {
			return
		}
		inputSchema, inputSchemaErr = compiler.Compile(resource + "#/$defs/RunAgentInput")
	})
	if inputSchemaErr != nil {
		return fmt.Errorf("AG-UI schema unavailable: %w", inputSchemaErr)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("expected one JSON request object")
	}
	if err := inputSchema.Validate(value); err != nil {
		return fmt.Errorf("invalid AG-UI input: %w", err)
	}
	return nil
}

//go:embed wire-schema.json
var wireSchemaJSON []byte
var definitions sync.Map
var compilerOnce sync.Once
var wireCompiler *jsonschema.Compiler
var wireCompilerErr error
var compilerMutex sync.Mutex

func validateDefinition(data []byte, definition string) error {
	compilerOnce.Do(func() {
		var doc any
		if wireCompilerErr = json.Unmarshal(wireSchemaJSON, &doc); wireCompilerErr != nil {
			return
		}
		wireCompiler = jsonschema.NewCompiler()
		wireCompiler.DefaultDraft(jsonschema.Draft2020)
		wireCompilerErr = wireCompiler.AddResource("https://agui.local/wire.json", doc)
	})
	if wireCompilerErr != nil {
		return wireCompilerErr
	}
	schema, ok := definitions.Load(definition)
	if !ok {
		compilerMutex.Lock()
		compiled, err := wireCompiler.Compile("https://agui.local/wire.json#/$defs/" + definition)
		compilerMutex.Unlock()
		if err != nil {
			return err
		}
		definitions.Store(definition, compiled)
		schema = compiled
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return schema.(*jsonschema.Schema).Validate(value)
}

// ValidateEvent enforces every field of all 31 event variants against the pin.
func ValidateEvent(data []byte) error { return validateDefinition(data, "Event") }

// ValidateCapabilities preserves the difference between omitted and false.
func ValidateCapabilities(data []byte) error { return validateDefinition(data, "AgentCapabilities") }

// ValidateMessage validates all roles and multimodal source variants.
func ValidateMessage(data []byte) error { return validateDefinition(data, "Message") }
