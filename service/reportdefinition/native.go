package reportdefinition

import (
	"fmt"
	"strings"
)

// validateNativeReport checks identity and all authored dataset bindings before
// Forge receives the native block source. Forge remains the block/schema owner.
func (d *Definition) validateNativeReport(fieldsByDataset map[string]map[string]Column) error {
	report := d.Report
	for key := range report {
		switch key {
		case "id", "title", "subtitle", "blocks", "layout", "theme", "metadata":
		default:
			return fmt.Errorf("report has unsupported field %q", key)
		}
	}
	if report["id"] != d.Metadata.ID || report["title"] != d.Metadata.Title {
		return fmt.Errorf("report id and title must match metadata")
	}
	blocks, ok := report["blocks"].([]any)
	if !ok || len(blocks) == 0 || len(blocks) > maxBlocks {
		return fmt.Errorf("report.blocks must contain 1..%d native Forge blocks", maxBlocks)
	}
	seen := map[string]bool{}
	for index, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("report.blocks[%d] must be an object", index)
		}
		id, _ := block["id"].(string)
		kind, _ := block["kind"].(string)
		if !validID(id) || seen[id] || strings.TrimSpace(kind) == "" {
			return fmt.Errorf("report.blocks[%d] requires a unique id and kind", index)
		}
		seen[id] = true
		if err := validateNativeDatasetRefs(block, fieldsByDataset); err != nil {
			return fmt.Errorf("report.blocks[%d] %s: %w", index, id, err)
		}
		ref, _ := block["datasetRef"].(string)
		if oneOf(kind, "tableBlock", "chartBlock", "kpiBlock", "badgesBlock", "collectionBlock", "geoMapBlock") && ref == "" {
			return fmt.Errorf("report.blocks[%d] %s requires an explicit datasetRef", index, id)
		}
		if ref == "" {
			continue
		}
		fields := fieldsByDataset[ref]
		for _, name := range []string{"valueField", "valueKey", "secondaryField", "timeField", "titleField", "descriptionField", "itemTitleField", "itemSubtitleField"} {
			if err := validateNativeField(fields, block[name], name); err != nil {
				return fmt.Errorf("report.blocks[%d] %s: %w", index, id, err)
			}
		}
		if kind == "tableBlock" {
			columns, ok := block["columns"].([]any)
			if !ok || len(columns) == 0 {
				return fmt.Errorf("report.blocks[%d] %s requires columns", index, id)
			}
			for _, rawColumn := range columns {
				column, ok := rawColumn.(map[string]any)
				if !ok {
					return fmt.Errorf("report.blocks[%d] %s has invalid column", index, id)
				}
				if err := validateNativeField(fields, column["key"], "columns.key"); err != nil {
					return fmt.Errorf("report.blocks[%d] %s: %w", index, id, err)
				}
			}
		}
		if kind == "chartBlock" {
			chart, ok := block["chartSpec"].(map[string]any)
			if !ok {
				return fmt.Errorf("report.blocks[%d] %s requires chartSpec", index, id)
			}
			if chart["xField"] == nil {
				return fmt.Errorf("report.blocks[%d] %s requires chartSpec.xField", index, id)
			}
			if err := validateNativeField(fields, chart["xField"], "chartSpec.xField"); err != nil {
				return fmt.Errorf("report.blocks[%d] %s: %w", index, id, err)
			}
			series, ok := chart["yFields"].([]any)
			if !ok || len(series) == 0 {
				return fmt.Errorf("report.blocks[%d] %s requires chartSpec.yFields", index, id)
			}
			for _, field := range series {
				if err := validateNativeField(fields, field, "chartSpec.yFields"); err != nil {
					return fmt.Errorf("report.blocks[%d] %s: %w", index, id, err)
				}
			}
		}
	}
	return nil
}

func validateNativeField(fields map[string]Column, value any, path string) error {
	if value == nil {
		return nil
	}
	name, ok := value.(string)
	if !ok || name == "" || fields[name].Name == "" {
		return fmt.Errorf("%s references an unknown projected field %q", path, value)
	}
	return nil
}

func validateNativeDatasetRefs(value any, fieldsByDataset map[string]map[string]Column) error {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if key == "datasetRef" {
				id, ok := child.(string)
				if !ok || id == "" || fieldsByDataset[id] == nil {
					return fmt.Errorf("datasetRef %q is not a declared dataset", child)
				}
				continue
			}
			if err := validateNativeDatasetRefs(child, fieldsByDataset); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range item {
			if err := validateNativeDatasetRefs(child, fieldsByDataset); err != nil {
				return err
			}
		}
	}
	return nil
}
