package datasource

import (
	"fmt"
	"strings"

	"github.com/viant/forge/backend/types"
)

// project applies the forge Selectors to the backend payload and returns
// (rows, dataInfo). Rows are always []map[string]interface{}; dataInfo is the
// optional pagination metadata payload.
func project(raw interface{}, ds *types.DataSource) ([]map[string]interface{}, map[string]interface{}) {
	rowsRaw := raw
	var dataInfo map[string]interface{}
	if ds != nil && ds.Selectors != nil {
		if ds.Selectors.Data != "" {
			rowsRaw = selectPath(ds.Selectors.Data, raw)
		}
		if ds.Selectors.DataInfo != "" {
			if di, ok := selectPath(ds.Selectors.DataInfo, raw).(map[string]interface{}); ok {
				dataInfo = di
			}
		}
	}
	rows := coerceRows(rowsRaw)
	return rows, dataInfo
}

// coerceRows turns whatever the selector returned into []map[string]interface{}.
func coerceRows(v interface{}) []map[string]interface{} {
	switch arr := v.(type) {
	case []map[string]interface{}:
		return arr
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(arr))
		for _, it := range arr {
			if m, ok := it.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out
	case map[string]interface{}:
		// Single-row payload.
		return []map[string]interface{}{arr}
	case nil:
		return nil
	default:
		return nil
	}
}

// selectPath mirrors the forge/feedextract dot+[idx] path walker.
// Supports: "a.b.c" and "a[0].b" and "a.0.b".
func selectPath(selector string, root interface{}) interface{} {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return root
	}
	cur := root
	norm := strings.ReplaceAll(selector, "[", ".")
	norm = strings.ReplaceAll(norm, "]", "")
	norm = strings.TrimPrefix(norm, ".")
	for _, token := range strings.Split(norm, ".") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if cur == nil {
			return nil
		}
		switch actual := cur.(type) {
		case map[string]interface{}:
			if value, ok := actual[token]; ok {
				cur = value
			} else {
				return nil
			}
		case []interface{}:
			idx := -1
			for _, r := range token {
				if r < '0' || r > '9' {
					return nil
				}
			}
			if token != "" {
				fmt.Sscanf(token, "%d", &idx)
			}
			if idx < 0 || idx >= len(actual) {
				return nil
			}
			cur = actual[idx]
		default:
			return nil
		}
	}
	return cur
}
