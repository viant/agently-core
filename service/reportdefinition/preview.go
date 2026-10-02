package reportdefinition

import (
	"context"
	"encoding/json"
	"fmt"

	reportfill "github.com/viant/forge/backend/reporting/fill"
)

// CompilePreview compiles renderable report artifacts using at most rowCap rows
// per dataset. Every request has an explicit page no larger than the remaining
// cap; authored offsets and page sizes are respected. A preview never contains
// ReportPrint, even if every fetcher confirms its result is complete.
func CompilePreview(ctx context.Context, definition *Definition, parameters map[string]any, fetcher DatasetFetcher, rowCap int) (*PreviewResult, error) {
	if rowCap < 1 || rowCap > maxRows {
		return nil, fmt.Errorf("preview row cap must be 1..%d", maxRows)
	}
	if err := definition.Validate(); err != nil {
		return nil, err
	}
	if fetcher == nil {
		return nil, fmt.Errorf("dataset fetcher is required")
	}
	resolved := map[string]any{}
	for _, p := range definition.Parameters {
		if p.Default != nil {
			resolved[p.Name] = p.Default
		}
	}
	for key, value := range parameters {
		var found *Parameter
		for i := range definition.Parameters {
			if definition.Parameters[i].Name == key {
				found = &definition.Parameters[i]
				break
			}
		}
		if found == nil {
			return nil, fmt.Errorf("unknown parameter %q", key)
		}
		if err := validateParameterValue(*found, value); err != nil {
			return nil, fmt.Errorf("parameter %s: %w", key, err)
		}
		resolved[key] = value
	}

	ids := sortedKeys(definition.Datasets)
	rowsByID := make(map[string][]map[string]any, len(ids))
	status := make(map[string]PreviewDataset, len(ids))
	complete := true
	reportDataBytes := 0
	for _, id := range ids {
		dataset := definition.Datasets[id]
		dataSource := definition.DataSources[dataset.DataSource]
		query := dataset.Query
		for _, binding := range dataSource.ParameterBindings {
			if value, ok := resolved[binding.Parameter]; ok {
				leaf := Predicate{Field: binding.Field, Op: binding.Operator, Value: value}
				if query.Filter == nil {
					query.Filter = &leaf
				} else {
					query.Filter = &Predicate{And: []Predicate{*query.Filter, leaf}}
				}
			}
		}
		pageSize, offset := maxPageSize, 0
		if query.Page != nil {
			pageSize, offset = query.Page.Limit, query.Page.Offset
		}
		rows := []map[string]any{}
		datasetBytes := 0
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			limit := min(pageSize, rowCap-len(rows), maxRows-offset)
			query.Page = &Page{Limit: limit, Offset: offset}
			result, err := fetcher.FetchDataset(ctx, FetchRequest{DatasetID: id, DataSourceID: dataset.DataSource, Endpoint: definition.Endpoints[dataSource.Service.Endpoint], DataSource: dataSource, Query: query})
			if err != nil {
				return nil, fmt.Errorf("fetch dataset %s: %w", id, err)
			}
			if result.HasMore && !result.HasMoreKnown {
				return nil, fmt.Errorf("dataset %s reported more rows without an explicit completeness signal", id)
			}
			if len(result.Rows) > limit {
				return nil, fmt.Errorf("dataset %s returned %d rows above page limit %d", id, len(result.Rows), limit)
			}
			pageBytes, err := json.Marshal(result.Rows)
			if err != nil {
				return nil, fmt.Errorf("dataset %s returned non-JSON rows: %w", id, err)
			}
			if len(pageBytes) > maxDatasetBytes-datasetBytes || len(pageBytes) > maxReportDataBytes-reportDataBytes {
				return nil, fmt.Errorf("dataset %s exceeds encoded data budget", id)
			}
			datasetBytes += len(pageBytes)
			reportDataBytes += len(pageBytes)
			rows = append(rows, result.Rows...)
			offset += len(result.Rows)
			state := PreviewDataset{RowCount: len(rows)}
			switch {
			case !result.HasMoreKnown && !result.HasMore:
				state.Reason = "completenessUnknown"
			case !result.HasMore:
				state.Complete = true
			case len(rows) == rowCap:
				state.Reason = "rowCap"
			case len(result.Rows) == 0:
				return nil, fmt.Errorf("dataset %s reported more rows without progress", id)
			case offset >= maxRows:
				state.Reason = "paginationBound"
			default:
				continue
			}
			status[id] = state
			if !state.Complete {
				complete = false
			}
			break
		}
		if err := validateRows(id, dataset, dataSource, rows); err != nil {
			return nil, err
		}
		rowsByID[id] = rows
	}
	compiled, err := compileRows(definition, rowsByID)
	if err != nil {
		return nil, err
	}
	var fill map[string]json.RawMessage
	err = json.Unmarshal(compiled.ReportFill, &fill)
	if err != nil {
		return nil, fmt.Errorf("decode preview ReportFill: %w", err)
	}
	var datasets []map[string]json.RawMessage
	if err := json.Unmarshal(fill["datasets"], &datasets); err != nil {
		return nil, fmt.Errorf("decode preview datasets: %w", err)
	}
	for _, item := range datasets {
		var id string
		if err := json.Unmarshal(item["id"], &id); err != nil {
			return nil, fmt.Errorf("decode preview dataset id: %w", err)
		}
		state, ok := status[id]
		if !ok {
			return nil, fmt.Errorf("unexpected Forge dataset %q", id)
		}
		var provenance map[string]json.RawMessage
		if err := json.Unmarshal(item["provenance"], &provenance); err != nil {
			return nil, fmt.Errorf("decode preview provenance: %w", err)
		}
		provenance["truncated"], _ = json.Marshal(!state.Complete)
		provenance["hasMore"], _ = json.Marshal(state.Reason == "rowCap" || state.Reason == "paginationBound")
		item["provenance"], err = json.Marshal(provenance)
		if err != nil {
			return nil, err
		}
	}
	fill["datasets"], err = json.Marshal(datasets)
	if err != nil {
		return nil, err
	}
	fillJSON, err := json.Marshal(fill)
	if err != nil {
		return nil, fmt.Errorf("encode preview ReportFill: %w", err)
	}
	if _, err := reportfill.DecodeJSON(fillJSON); err != nil {
		return nil, fmt.Errorf("validate preview ReportFill: %w", err)
	}
	return &PreviewResult{Kind: "reportPreview", Source: compiled.Source, ReportDocument: compiled.ReportDocument, ReportSpec: compiled.ReportSpec, ReportFill: fillJSON, Complete: complete, Datasets: status}, nil
}
