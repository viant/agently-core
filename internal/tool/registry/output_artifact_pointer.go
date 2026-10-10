package tool

import (
	"encoding/base64"
	"reflect"
	"strconv"
	"strings"

	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	schema "github.com/viant/mcp-protocol/schema"
)

func artifactPointerString(result *schema.CallToolResult, path string) (string, bool) {
	value, present, valid := artifactPointerOptionalString(result, path)
	return value, present && valid
}

func artifactPointerOptionalString(result *schema.CallToolResult, path string) (string, bool, bool) {
	tokens, err := mcpcfg.OutputArtifactPointer(path)
	if err != nil || result == nil {
		return "", false, false
	}
	var value reflect.Value
	if tokens[0] == "content" {
		value = reflect.ValueOf(result.Content)
	} else {
		value = reflect.ValueOf(result.StructuredContent)
	}
	unwrap := func(value reflect.Value) (reflect.Value, bool) {
		for i := 0; i < 64; i++ {
			if !value.IsValid() {
				return value, false
			}
			if value.Kind() != reflect.Interface && value.Kind() != reflect.Pointer {
				return value, true
			}
			if value.IsNil() {
				return value, false
			}
			value = value.Elem()
		}
		return reflect.Value{}, false
	}
	for _, token := range tokens[1:] {
		var ok bool
		value, ok = unwrap(value)
		if !ok {
			return "", false, true
		}
		switch value.Kind() {
		case reflect.Map:
			if value.Type().Key().Kind() != reflect.String {
				return "", true, false
			}
			key := reflect.ValueOf(token).Convert(value.Type().Key())
			value = value.MapIndex(key)
		case reflect.Slice, reflect.Array:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || strconv.Itoa(index) != token {
				return "", false, false
			}
			if index >= value.Len() {
				return "", false, true
			}
			value = value.Index(index)
		case reflect.Struct:
			found := false
			for i := 0; i < value.NumField(); i++ {
				field := value.Type().Field(i)
				if field.PkgPath != "" {
					continue
				}
				name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
				if name == "-" {
					continue
				}
				if name == "" {
					name = field.Name
				}
				if name == token {
					value = value.Field(i)
					found = true
					break
				}
			}
			if !found {
				return "", false, true
			}
		default:
			return "", true, false
		}
	}
	value, ok := unwrap(value)
	if !ok {
		return "", false, true
	}
	if value.Kind() != reflect.String {
		return "", true, false
	}
	return value.String(), true, true
}

func artifactMetadataEcho(value, encoded string) bool {
	if encoded != "" && strings.Contains(value, encoded) {
		return true
	}
	if len(encoded) <= 1024 {
		if data, err := base64.StdEncoding.DecodeString(encoded); err == nil && len(data) >= 8 && strings.Contains(value, string(data)) {
			return true
		}
	}
	// A long uninterrupted base64 token is inappropriate filename/MIME
	// metadata, even if a server returns only a prefix of the full payload.
	run := 0
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '+' || char == '/' || char == '=' {
			run++
		} else {
			run = 0
		}
		if run >= 64 {
			return true
		}
	}
	return false
}
