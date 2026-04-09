package schema

import (
	"encoding/json"
	"fmt"
	"go/format"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"

	mcpschema "github.com/viant/mcp-protocol/schema"
)

// TypeFromInputSchema converts an MCP ToolInputSchema into a dynamic Go type.
func TypeFromInputSchema(inputSchema mcpschema.ToolInputSchema) (reflect.Type, error) {
	if len(inputSchema.Properties) == 0 {
		return reflect.StructOf([]reflect.StructField{}), nil
	}
	fields, err := buildReflectFields(inputSchema.Properties, inputSchema.Required)
	if err != nil {
		return nil, err
	}
	return reflect.StructOf(fields), nil
}

// TypeFromOutputSchema converts an MCP ToolOutputSchema into a dynamic Go type.
func TypeFromOutputSchema(outputSchema mcpschema.ToolOutputSchema) (reflect.Type, error) {
	if len(outputSchema.Properties) == 0 {
		return reflect.StructOf([]reflect.StructField{}), nil
	}
	fields, err := buildReflectFields(outputSchema.Properties, outputSchema.Required)
	if err != nil {
		return nil, err
	}
	return reflect.StructOf(fields), nil
}

// GoShapeFromSchemaMap renders a schema map as a Go-like type shape by first
// converting it into a dynamic reflect.Type.
func GoShapeFromSchemaMap(schemaMap map[string]interface{}) (string, error) {
	if len(schemaMap) == 0 {
		return "struct {}", nil
	}
	data, err := json.Marshal(schemaMap)
	if err != nil {
		return "", err
	}
	var input mcpschema.ToolInputSchema
	if err := json.Unmarshal(data, &input); err == nil && input.Type == "object" {
		rt, err := TypeFromInputSchema(input)
		if err != nil {
			return "", err
		}
		return formatGoShape(rt.String() + "{}"), nil
	}
	var output mcpschema.ToolOutputSchema
	if err := json.Unmarshal(data, &output); err == nil && output.Type == "object" {
		rt, err := TypeFromOutputSchema(output)
		if err != nil {
			return "", err
		}
		return formatGoShape(rt.String() + "{}"), nil
	}
	return formatGoShape("struct {}{}"), nil
}

func buildReflectFields(props map[string]map[string]interface{}, required []string) ([]reflect.StructField, error) {
	keys := make([]string, 0, len(props))
	for name := range props {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	requiredSet := make(map[string]struct{}, len(required))
	for _, item := range required {
		requiredSet[item] = struct{}{}
	}
	fields := make([]reflect.StructField, 0, len(keys))
	for _, name := range keys {
		def := props[name]
		fieldType, err := goTypeFromDef(def)
		if err != nil {
			return nil, fmt.Errorf("failed to determine type for field %q: %w", name, err)
		}
		tagName := name
		if _, ok := requiredSet[name]; !ok {
			tagName += ",omitempty"
		}
		tagParts := []string{fmt.Sprintf("json:%q", tagName)}
		if desc, ok := def["description"].(string); ok && desc != "" {
			tagParts = append(tagParts, fmt.Sprintf("description:%q", desc))
		}
		if rawEnum, ok := def["enum"].([]interface{}); ok {
			for _, enumValue := range rawEnum {
				if text, ok := enumValue.(string); ok && text != "" {
					tagParts = append(tagParts, fmt.Sprintf("choice:%q", text))
				}
			}
		}
		fields = append(fields, reflect.StructField{
			Name: exportedFieldName(name),
			Type: fieldType,
			Tag:  reflect.StructTag(strings.Join(tagParts, " ")),
		})
	}
	return fields, nil
}

func goTypeFromDef(def map[string]interface{}) (reflect.Type, error) {
	rawType, _ := def["type"]
	var typeStr string
	switch actual := rawType.(type) {
	case string:
		typeStr = actual
	case []interface{}:
		for _, item := range actual {
			if text, ok := item.(string); ok && text != "" && text != "null" {
				typeStr = text
				break
			}
		}
	}
	switch typeStr {
	case "string":
		if format, ok := def["format"].(string); ok && (format == "date-time" || format == "date") {
			return reflect.TypeOf(time.Time{}), nil
		}
		return reflect.TypeOf(""), nil
	case "integer":
		return reflect.TypeOf(int64(0)), nil
	case "number":
		return reflect.TypeOf(float64(0)), nil
	case "boolean":
		return reflect.TypeOf(true), nil
	case "object":
		nested := map[string]map[string]interface{}{}
		var nestedRequired []string
		if rawReq, ok := def["required"].([]interface{}); ok {
			for _, item := range rawReq {
				if text, ok := item.(string); ok && text != "" {
					nestedRequired = append(nestedRequired, text)
				}
			}
		}
		if rawProps, ok := def["properties"].(map[string]interface{}); ok {
			for key, value := range rawProps {
				if m, ok := value.(map[string]interface{}); ok {
					nested[key] = m
				}
			}
		}
		if len(nested) == 0 {
			return reflect.TypeOf(map[string]interface{}{}), nil
		}
		fields, err := buildReflectFields(nested, nestedRequired)
		if err != nil {
			return nil, err
		}
		return reflect.StructOf(fields), nil
	case "array":
		if rawItems, ok := def["items"].(map[string]interface{}); ok {
			itemType, err := goTypeFromDef(rawItems)
			if err != nil {
				return nil, err
			}
			return reflect.SliceOf(itemType), nil
		}
		return reflect.TypeOf([]interface{}{}), nil
	default:
		return reflect.TypeOf((*interface{})(nil)).Elem(), nil
	}
}

func exportedFieldName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Field"
	}
	if isIdentifier(name) {
		runes := []rune(name)
		runes[0] = unicode.ToUpper(runes[0])
		return string(runes)
	}
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	})
	var builder strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		builder.WriteString(string(runes))
	}
	out := builder.String()
	if out == "" {
		return "Field"
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "Field" + out
	}
	return out
}

func isIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		switch {
		case r == '_':
			continue
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		default:
			return false
		}
	}
	return true
}

func formatGoShape(shape string) string {
	shape = strings.TrimSpace(shape)
	if shape == "" {
		return shape
	}
	src := "package p\n\nvar _ = " + shape + "\n"
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return shape
	}
	text := string(formatted)
	const marker = "var _ = "
	idx := strings.Index(text, marker)
	if idx == -1 {
		return shape
	}
	out := strings.TrimSpace(text[idx+len(marker):])
	return out
}
