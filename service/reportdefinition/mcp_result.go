package reportdefinition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	maxMCPJSONDepth = 64
	maxMCPJSONNodes = 1 << 20
)

// DecodeMCPResult converts one MCP structuredContent JSON body using only the
// authored result contract and columns. It performs no tool, network, or auth
// work. An absent records hasMorePath leaves HasMoreKnown false; a declared
// pagination path must resolve to a boolean, including on the final page.
func DecodeMCPResult(contract ResultContract, columns []Column, body []byte) (FetchResult, error) {
	if len(body) == 0 || len(body) > maxDatasetBytes {
		return FetchResult{}, fmt.Errorf("MCP structuredContent must be 1..%d bytes", maxDatasetBytes)
	}
	if len(columns) == 0 || len(columns) > maxColumns {
		return FetchResult{}, fmt.Errorf("MCP result requires 1..%d declared columns", maxColumns)
	}
	declared := make(map[string]Column, len(columns))
	for _, column := range columns {
		if !validID(column.Name) || !oneOf(column.Type, "string", "integer", "number", "boolean", "date", "datetime") || !oneOf(column.Role, "dimension", "measure") {
			return FetchResult{}, fmt.Errorf("invalid declared column %q", column.Name)
		}
		if _, exists := declared[column.Name]; exists {
			return FetchResult{}, fmt.Errorf("duplicate declared column %q", column.Name)
		}
		declared[column.Name] = column
	}
	if contract.Shape != "records" && contract.Shape != "tabular" {
		return FetchResult{}, fmt.Errorf("unsupported MCP result shape %q", contract.Shape)
	}
	if contract.Shape == "records" && (contract.RowPath == "" || contract.ResultsPath != "" || contract.ResultName != "") {
		return FetchResult{}, fmt.Errorf("records require rowPath and no tabular selection")
	}
	if contract.Shape == "tabular" && (contract.ResultsPath == "" || contract.ResultName == "" || contract.RowPath != "") {
		return FetchResult{}, fmt.Errorf("tabular results require resultsPath and resultName")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	nodes := 0
	root, err := decodeUniqueJSON(decoder, 0, &nodes)
	if err != nil {
		return FetchResult{}, fmt.Errorf("decode MCP structuredContent: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return FetchResult{}, fmt.Errorf("MCP structuredContent must contain exactly one JSON value")
	}
	if object, ok := root.(map[string]any); ok {
		if status, exists := object["status"]; exists && status != "ok" {
			return FetchResult{}, fmt.Errorf("MCP result status is not ok")
		}
	}

	result := FetchResult{Rows: []map[string]any{}}
	var rawRows any
	var sourceColumns []string
	var selected map[string]any
	switch contract.Shape {
	case "records":
		rawRows, err = mcpValueAt(root, contract.RowPath)
		if err != nil {
			return FetchResult{}, fmt.Errorf("rowPath: %w", err)
		}
	case "tabular":
		var results any
		results, err = mcpValueAt(root, contract.ResultsPath)
		if err != nil {
			return FetchResult{}, fmt.Errorf("resultsPath: %w", err)
		}
		items, ok := results.([]any)
		if !ok {
			return FetchResult{}, fmt.Errorf("resultsPath must resolve to an array")
		}
		seen := map[string]bool{}
		for i, item := range items {
			object, ok := item.(map[string]any)
			if !ok {
				return FetchResult{}, fmt.Errorf("result %d must be an object", i)
			}
			name, ok := object["name"].(string)
			if !ok || name == "" || seen[name] {
				return FetchResult{}, fmt.Errorf("result %d has missing or duplicate name", i)
			}
			seen[name] = true
			if name == contract.ResultName {
				selected = object
			}
		}
		if selected == nil {
			return FetchResult{}, fmt.Errorf("named result %q is missing", contract.ResultName)
		}
		sourceColumns, err = mcpTabularColumns(selected["columns"], declared)
		if err != nil {
			return FetchResult{}, err
		}
		rawRows = selected["rows"]
		if contract.HasMorePath == "" {
			if value, exists := selected["hasMore"]; exists {
				more, ok := value.(bool)
				if !ok {
					return FetchResult{}, fmt.Errorf("named result %q requires boolean hasMore", contract.ResultName)
				}
				result.HasMore, result.HasMoreKnown = more, true
			}
		} else if value, exists := selected["hasMore"]; exists {
			if _, ok := value.(bool); !ok {
				return FetchResult{}, fmt.Errorf("named result %q has nonboolean hasMore", contract.ResultName)
			}
		}
	}
	if contract.HasMorePath != "" {
		value, pathErr := mcpValueAt(root, contract.HasMorePath)
		if pathErr != nil {
			return FetchResult{}, fmt.Errorf("hasMorePath: %w", pathErr)
		}
		more, ok := value.(bool)
		if !ok {
			return FetchResult{}, fmt.Errorf("hasMorePath must resolve to a boolean")
		}
		if contract.Shape == "tabular" {
			if local, exists := selected["hasMore"]; exists && local != more {
				return FetchResult{}, fmt.Errorf("conflicting hasMore signals for %q", contract.ResultName)
			}
		}
		result.HasMore, result.HasMoreKnown = more, true
	}
	rows, ok := rawRows.([]any)
	if !ok {
		return FetchResult{}, fmt.Errorf("rows must be an array")
	}
	if len(rows) > maxPageSize {
		return FetchResult{}, fmt.Errorf("MCP result exceeds %d rows", maxPageSize)
	}
	for i, raw := range rows {
		var row map[string]any
		if contract.Shape == "records" {
			object, ok := raw.(map[string]any)
			if !ok {
				return FetchResult{}, fmt.Errorf("row %d must be an object", i)
			}
			row = make(map[string]any, len(columns))
			for name := range object {
				if _, exists := declared[name]; !exists {
					return FetchResult{}, fmt.Errorf("row %d has undeclared field %q", i, name)
				}
			}
			for _, column := range columns {
				if value, exists := object[column.Name]; exists {
					row[column.Name] = value
				}
			}
		} else {
			cells, ok := raw.([]any)
			if !ok || len(cells) != len(sourceColumns) {
				return FetchResult{}, fmt.Errorf("row %d has wrong tabular width", i)
			}
			row = make(map[string]any, len(sourceColumns))
			for j, name := range sourceColumns {
				row[name] = cells[j]
			}
		}
		for name, value := range row {
			column := declared[name]
			if value == nil && !column.Nullable || value != nil && !validColumnValue(column.Type, value) {
				return FetchResult{}, fmt.Errorf("row %d field %q has invalid value", i, column.Name)
			}
		}
		result.Rows = append(result.Rows, row)
	}
	return result, nil
}

func mcpTabularColumns(raw any, declared map[string]Column) ([]string, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 || len(items) > len(declared) {
		return nil, fmt.Errorf("tabular columns must be a nonempty subset of declared columns")
	}
	names := make([]string, 0, len(items))
	seen := map[string]bool{}
	for i, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tabular column %d must be an object", i)
		}
		name, ok := object["name"].(string)
		column, exists := declared[name]
		if !ok || !exists || seen[name] {
			return nil, fmt.Errorf("tabular column %d has unknown or duplicate name", i)
		}
		if object["type"] != column.Type || object["role"] != column.Role || object["nullable"] != column.Nullable {
			return nil, fmt.Errorf("tabular column %q differs from declaration", name)
		}
		if format, exists := object["format"]; exists && format != column.Format {
			return nil, fmt.Errorf("tabular column %q has conflicting format", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	return names, nil
}

func mcpValueAt(root any, path string) (any, error) {
	if path == "$" {
		return root, nil
	}
	if path == "" || len(path) > 256 {
		return nil, fmt.Errorf("invalid path %q", path)
	}
	parts := strings.Split(path, ".")
	if len(parts) > maxMCPJSONDepth {
		return nil, fmt.Errorf("path is too deep")
	}
	value := root
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("invalid path %q", path)
		}
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path %q traverses a nonobject", path)
		}
		var exists bool
		value, exists = object[part]
		if !exists {
			return nil, fmt.Errorf("path %q is missing", path)
		}
	}
	return value, nil
}

// decodeUniqueJSON bounds nesting and node count and rejects duplicate object
// keys, which encoding/json's ordinary map decoding would silently overwrite.
func decodeUniqueJSON(decoder *json.Decoder, depth int, nodes *int) (any, error) {
	*nodes++
	if depth > maxMCPJSONDepth || *nodes > maxMCPJSONNodes {
		return nil, fmt.Errorf("JSON nesting or value count exceeds limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("object key must be a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate JSON key %q", key)
			}
			value, err := decodeUniqueJSON(decoder, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		_, err = decoder.Token()
		return object, err
	case '[':
		array := []any{}
		for decoder.More() {
			value, err := decodeUniqueJSON(decoder, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err = decoder.Token()
		return array, err
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}
