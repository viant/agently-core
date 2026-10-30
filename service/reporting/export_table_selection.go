package reporting

import (
	"bytes"
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
	// An already single-table report is the complete snapshot. Preserve its
	// original bytes so its matching print model remains valid.
	if len(blocks) == 1 {
		return cloneJSON(raw), nil
	}
	fill["blocks"], _ = json.Marshal(selected)
	return json.Marshal(fill)
}

// selectExportTableSnapshot retains a matching print only when selection
// leaves the complete materialized report unchanged.
func selectExportTableSnapshot(fill, print json.RawMessage, id string) (json.RawMessage, json.RawMessage, error) {
	selected, err := selectExportTable(fill, id)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(selected, fill) {
		print = nil
	}
	return selected, print, nil
}
