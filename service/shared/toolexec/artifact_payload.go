package toolexec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/viant/agently-core/internal/tool/dispatchpayload"
	"github.com/viant/agently-core/protocol/tool"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
)

var artifactPayloadMacro = regexp.MustCompile(`^\$\{artifact\[([0-9a-fA-F-]{36})\]\.payload(?:;xform=(base64))?\}$`)

type dispatchArtifactPayload struct {
	raw, encoded string
	outputSchema map[string]interface{}
}

func artifactDispatchArguments(ctx context.Context, reg tool.Registry, name string, arguments map[string]interface{}) (map[string]interface{}, []dispatchArtifactPayload, error) {
	if !dispatchpayload.HasArtifactReference(arguments) {
		return arguments, nil, nil
	}
	if err := validateArtifactArgumentBudget(ctx, arguments); err != nil {
		return nil, nil, err
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		return nil, nil, fmt.Errorf("artifact dispatch arguments invalid")
	}
	if !strings.Contains(string(raw), "${artifact[") {
		return arguments, nil, nil
	}
	definition, ok := reg.GetDefinition(name)
	if getter, enabled := reg.(tool.ContextDefinitionGetter); enabled {
		definition, ok = getter.GetDefinitionWithContext(ctx, name)
	}
	if !ok || definition == nil {
		return nil, nil, fmt.Errorf("artifact dispatch tool schema unavailable")
	}
	var cloned map[string]interface{}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&cloned) != nil {
		return nil, nil, fmt.Errorf("artifact dispatch arguments invalid")
	}
	payloads := []dispatchArtifactPayload{}
	var encodedBytes int64
	occurrences := 0
	var expand func(any, map[string]interface{}) (any, error)
	expand = func(value any, schema map[string]interface{}) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch typed := value.(type) {
		case map[string]interface{}:
			properties, _ := schema["properties"].(map[string]interface{})
			for key, child := range typed {
				childSchema, _ := properties[key].(map[string]interface{})
				next, e := expand(child, childSchema)
				if e != nil {
					return nil, e
				}
				typed[key] = next
			}
			return typed, nil
		case []interface{}:
			itemSchema, _ := schema["items"].(map[string]interface{})
			for i, child := range typed {
				next, e := expand(child, itemSchema)
				if e != nil {
					return nil, e
				}
				typed[i] = next
			}
			return typed, nil
		case string:
			if !strings.Contains(typed, "${artifact[") {
				return typed, nil
			}
			match := artifactPayloadMacro.FindStringSubmatch(typed)
			if len(match) != 3 {
				return nil, fmt.Errorf("artifact payload macro must be an exact UUID reference")
			}
			if _, e := uuid.Parse(match[1]); e != nil {
				return nil, fmt.Errorf("artifact payload UUID invalid")
			}
			if schema["type"] != "string" {
				return nil, fmt.Errorf("artifact payload requires a declared binary string field")
			}
			format, _ := schema["format"].(string)
			encoding, _ := schema["contentEncoding"].(string)
			explicitBase64 := match[2] == "base64"
			compatibleFormat := format == "" || format == "byte"
			if !compatibleFormat || (encoding != "" && encoding != "base64") || (!explicitBase64 && encoding != "base64" && format != "byte") {
				return nil, fmt.Errorf("artifact payload requires a declared binary encoding")
			}
			occurrences++
			if occurrences > 32 {
				return nil, fmt.Errorf("artifact macro occurrence limit exceeded")
			}
			descriptor, e := scratchpad.New().DescribeArtifact(ctx, scratchpad.ArtifactURI(match[1]))
			if e != nil {
				return nil, e
			}
			if descriptor.SizeBytes <= 0 || descriptor.SizeBytes > (16<<20) {
				return nil, fmt.Errorf("artifact dispatch size limit exceeded")
			}
			encodedBytes += ((descriptor.SizeBytes + 2) / 3) * 4
			if encodedBytes > (16 << 20) {
				return nil, fmt.Errorf("artifact aggregate dispatch limit exceeded")
			}
			data, e := scratchpad.New().ReadArtifactPayload(ctx, scratchpad.ArtifactURI(match[1]))
			if e != nil {
				return nil, e
			}
			encoded := string(data)
			if explicitBase64 || encoding == "base64" || format == "byte" {
				encoded = base64.StdEncoding.EncodeToString(data)
			}
			payloads = append(payloads, dispatchArtifactPayload{raw: string(data), encoded: encoded, outputSchema: definition.OutputSchema})
			return encoded, nil
		default:
			return value, nil
		}
	}
	next, err := expand(cloned, definition.Parameters)
	if err != nil {
		return nil, nil, err
	}
	return next.(map[string]interface{}), payloads, nil
}

func redactDispatchArtifactOutput(text string, payloads []dispatchArtifactPayload) string {
	original := text
	normalized := regexp.MustCompile(`\\u([0-9a-fA-F]{4})`).ReplaceAllStringFunc(text, func(value string) string {
		n, err := strconv.ParseInt(value[2:], 16, 32)
		if err != nil {
			return value
		}
		return string(rune(n))
	})
	for _, payload := range payloads {
		if payload.raw == "" && payload.encoded == "" {
			continue
		}
		values := []string{payload.raw, payload.encoded, fmt.Sprint([]byte(payload.raw))}
		numbers := make([]int, len(payload.raw))
		for i, b := range []byte(payload.raw) {
			numbers[i] = int(b)
		}
		vector, _ := json.Marshal(numbers)
		values = append(values, string(vector))
		quoted := strconv.Quote(payload.raw)
		if len(quoted) > 2 {
			values = append(values, quoted[1:len(quoted)-1])
		}
		for _, value := range values {
			if value == "" {
				continue
			}
			escaped, _ := json.Marshal(value)
			patterns := []string{value}
			if len(escaped) > 2 {
				patterns = append(patterns, string(escaped[1:len(escaped)-1]))
			}
			for _, pattern := range patterns {
				if strings.Contains(normalized, pattern) {
					return "[artifact payload echo withheld]"
				}
				original = strings.ReplaceAll(original, pattern, "[artifact payload redacted]")
			}
		}
	}
	return original
}

func validateArtifactArgumentBudget(ctx context.Context, value any) error {
	size, nodes := 0, 0
	var visit func(any, int) error
	visit = func(value any, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		nodes++
		if depth > 32 || nodes > 10000 {
			return fmt.Errorf("artifact argument structure limit exceeded")
		}
		switch v := value.(type) {
		case string:
			size += len(v)
		case map[string]interface{}:
			for key, child := range v {
				size += len(key)
				if err := visit(child, depth+1); err != nil {
					return err
				}
			}
		case []interface{}:
			for _, child := range v {
				if err := visit(child, depth+1); err != nil {
					return err
				}
			}
		}
		if size > (1 << 20) {
			return fmt.Errorf("artifact argument size limit exceeded")
		}
		return nil
	}
	return visit(value, 0)
}

func redactDeclaredBinaryOutput(text string, schema map[string]interface{}) string {
	if len(schema) == 0 {
		return text
	}
	var value any
	if json.Unmarshal([]byte(text), &value) != nil {
		return text
	}
	var contains func(any, map[string]interface{}) bool
	contains = func(value any, shape map[string]interface{}) bool {
		if value == nil {
			return false
		}
		encoding, _ := shape["contentEncoding"].(string)
		format, _ := shape["format"].(string)
		if encoding == "base64" || format == "byte" || format == "binary" {
			return true
		}
		switch v := value.(type) {
		case map[string]interface{}:
			properties, _ := shape["properties"].(map[string]interface{})
			for key, child := range v {
				nested, _ := properties[key].(map[string]interface{})
				if contains(child, nested) {
					return true
				}
			}
		case []interface{}:
			items, _ := shape["items"].(map[string]interface{})
			for _, child := range v {
				if contains(child, items) {
					return true
				}
			}
		}
		return false
	}
	if object, ok := value.(map[string]interface{}); ok {
		if child, exists := object["structuredContent"]; exists && contains(child, schema) {
			return "[binary tool output withheld]"
		}
		if content, ok := object["content"].([]interface{}); ok {
			for _, entry := range content {
				if block, ok := entry.(map[string]interface{}); ok {
					if textValue, ok := block["text"].(string); ok {
						var nested any
						if json.Unmarshal([]byte(textValue), &nested) == nil && contains(nested, schema) {
							return "[binary tool output withheld]"
						}
					}
				}
				if block, ok := entry.(map[string]interface{}); ok && (block["type"] == "image" || block["type"] == "audio") {
					return "[binary tool output withheld]"
				}
			}
		}
	}
	if contains(value, schema) {
		return "[binary tool output withheld]"
	}
	return text
}
