package reporting

import (
	"encoding/json"
	"fmt"
)

// Select only a server-materialized table. Preserve opaque datasource requests
// and provenance verbatim; the browser never supplies rows or definitions.
func selectExportTable(raw json.RawMessage, id string) (json.RawMessage, error) {
	var fill map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fill); err != nil {
		return nil, fmt.Errorf("reporting export: invalid report fill")
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(fill["blocks"], &blocks); err != nil {
		return nil, fmt.Errorf("reporting export: invalid report blocks")
	}
	var selected []json.RawMessage
	for _, rawBlock := range blocks {
		var block struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			return nil, err
		}
		if block.Kind == "tableBlock" && (id == "" || block.ID == id) {
			selected = append(selected, rawBlock)
		}
	}
	if len(selected) != 1 {
		return nil, fmt.Errorf("reporting export: select one available table for tabular export")
	}
	fill["blocks"], _ = json.Marshal(selected)
	return json.Marshal(fill)
}
