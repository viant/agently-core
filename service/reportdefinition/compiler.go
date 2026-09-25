package reportdefinition

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/viant/forge/backend/reporting/fenced"
	"gopkg.in/yaml.v3"
)

const (
	maxDefinitionBytes = 1 << 20
	maxDataSets        = 32 // Forge's fence assembly limit
	maxBlocks          = 100
	maxRows            = 10000
	maxColumns         = 500
	maxPageSize        = 1000
	maxDatasetBytes    = 8 << 20
	maxReportDataBytes = 16 << 20
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
var mcpArgumentPath = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// Parse reads one ReportDefinition document. Legacy x-mock-preview is ignored
// so the public authoring portion of an existing preview YAML can be reused.
// All other fields are decoded strictly.
func Parse(body []byte) (*Definition, error) {
	return parse(body, true)
}

// ParseStrict is for production authoring. It rejects fixture-only preview
// extensions rather than silently preserving them in a stored definition.
func ParseStrict(body []byte) (*Definition, error) {
	return parse(body, false)
}

func parse(body []byte, allowMockPreview bool) (*Definition, error) {
	if len(body) == 0 || len(body) > maxDefinitionBytes {
		return nil, fmt.Errorf("report definition must be 1..%d bytes", maxDefinitionBytes)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	var node yaml.Node
	if err := decoder.Decode(&node); err != nil {
		return nil, fmt.Errorf("parse report definition: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("report definition must contain exactly one YAML document")
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("report definition must be an object")
	}
	if err := checkYAML(&node); err != nil {
		return nil, err
	}
	root := node.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "x-mock-preview" {
			if !allowMockPreview {
				return nil, fmt.Errorf("x-mock-preview is not allowed in a production report definition")
			}
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			break
		}
	}
	clean, err := yaml.Marshal(&node)
	if err != nil {
		return nil, err
	}
	strict := yaml.NewDecoder(bytes.NewReader(clean))
	strict.KnownFields(true)
	var definition Definition
	if err := strict.Decode(&definition); err != nil {
		return nil, fmt.Errorf("decode report definition: %w", err)
	}
	if err := definition.Validate(); err != nil {
		return nil, err
	}
	return &definition, nil
}

func checkYAML(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode || (node.Tag != "" && !strings.HasPrefix(node.Tag, "!!")) {
		return fmt.Errorf("YAML aliases and custom tags are unsupported")
	}
	if node.Tag == "!!timestamp" {
		node.Tag = "!!str"
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if seen[key] {
				return fmt.Errorf("duplicate YAML key %q at line %d", key, node.Content[i].Line)
			}
			seen[key] = true
		}
	}
	for _, child := range node.Content {
		if err := checkYAML(child); err != nil {
			return err
		}
	}
	return nil
}

func validID(id string) bool { return safeID.MatchString(id) }

// ValidIdentifier reports whether an authored report ID can be used as a
// ReportDefinition map key. Catalogs may use it before offering authoring.
func ValidIdentifier(id string) bool { return validID(id) }

// Validate checks references and limits before any dataset is fetched.
func (d *Definition) Validate() error {
	if d == nil {
		return fmt.Errorf("report definition is required")
	}
	if d.APIVersion != "reporting.viant.ai/v1alpha1" || !oneOf(d.Kind, "ReportDefinition", "reporting.report") {
		return fmt.Errorf("expected reporting.viant.ai/v1alpha1 ReportDefinition or reporting.report")
	}
	if !validID(d.Metadata.ID) || strings.TrimSpace(d.Metadata.Title) == "" || d.Metadata.Revision < 1 {
		return fmt.Errorf("metadata requires a valid id, title, and positive revision")
	}
	if len(d.DataSources) == 0 || len(d.DataSources) > maxDataSets || len(d.Datasets) == 0 || len(d.Datasets) > maxDataSets {
		return fmt.Errorf("dataSources and datasets must each contain 1..%d entries", maxDataSets)
	}
	parameters := map[string]Parameter{}
	if len(d.Parameters) > 64 {
		return fmt.Errorf("report exceeds 64 parameters")
	}
	for _, p := range d.Parameters {
		if !validID(p.Name) || parameters[p.Name].Name != "" || !oneOf(p.Type, "string", "integer", "number", "boolean", "date", "datetime") {
			return fmt.Errorf("invalid or duplicate parameter %q", p.Name)
		}
		parameters[p.Name] = p
		if p.Default != nil {
			if err := validateParameterValue(p, p.Default); err != nil {
				return fmt.Errorf("parameter %s default: %w", p.Name, err)
			}
		}
	}
	for id, endpoint := range d.Endpoints {
		if !validID(id) || endpoint.Type == "" {
			return fmt.Errorf("invalid endpoint %q", id)
		}
	}
	for _, id := range sortedKeys(d.DataSources) {
		source := d.DataSources[id]
		if !validID(id) {
			return fmt.Errorf("invalid dataSource id %q", id)
		}
		if source.Service.Endpoint == "" || d.Endpoints[source.Service.Endpoint].Type == "" || source.Service.URI == "" || source.Service.Method == "" {
			return fmt.Errorf("dataSources.%s.service requires a declared endpoint, uri, and method", id)
		}
		if binding := source.MCPRequest; binding != nil {
			endpoint := d.Endpoints[source.Service.Endpoint]
			if endpoint.Type != "mcp" || endpoint.Transport != "streamable" || source.Service.Method != "POST" ||
				!validMCPArgumentPath(binding.QueryPath) || !binding.CompleteResult && binding.HasMorePath == "" ||
				binding.DataSourcePath != "" && !validMCPArgumentPath(binding.DataSourcePath) ||
				binding.AuthContextPath != "" && !validMCPArgumentPath(binding.AuthContextPath) ||
				mcpPathsOverlap(binding.QueryPath, binding.DataSourcePath) || mcpPathsOverlap(binding.QueryPath, binding.AuthContextPath) ||
				mcpPathsOverlap(binding.DataSourcePath, binding.AuthContextPath) {
				return fmt.Errorf("dataSources.%s.mcpRequest has an invalid or conflicting explicit binding", id)
			}
		}
		contract := source.ResultContract
		if !oneOf(contract.Shape, "records", "tabular") || (contract.Shape == "records" && contract.RowPath == "") {
			return fmt.Errorf("dataSources.%s.resultContract requires records with rowPath or tabular", id)
		}
		if len(source.Columns) == 0 || len(source.Columns) > maxColumns {
			return fmt.Errorf("dataSources.%s.columns must contain 1..%d entries", id, maxColumns)
		}
		columns := map[string]Column{}
		for _, column := range source.Columns {
			if !validID(column.Name) || columns[column.Name].Name != "" || !oneOf(column.Type, "string", "integer", "number", "boolean", "date", "datetime") || !oneOf(column.Role, "dimension", "measure") {
				return fmt.Errorf("dataSources.%s.columns has invalid or duplicate column %q", id, column.Name)
			}
			columns[column.Name] = column
		}
		for _, binding := range source.ParameterBindings {
			p, ok := parameters[binding.Parameter]
			if !ok || columns[binding.Field].Name == "" || !oneOf(binding.Operator, "eq", "ne", "gt", "gte", "lt", "lte", "in", "notIn") {
				return fmt.Errorf("dataSources.%s.parameterBindings has invalid reference or operator", id)
			}
			if (binding.Operator == "in" || binding.Operator == "notIn") != p.Multiple {
				return fmt.Errorf("dataSources.%s binding %s operator does not match multiplicity", id, binding.Parameter)
			}
		}
	}
	outputFields := map[string]map[string]Column{}
	for _, id := range sortedKeys(d.Datasets) {
		dataset := d.Datasets[id]
		if !validID(id) {
			return fmt.Errorf("invalid dataset id %q", id)
		}
		source, ok := d.DataSources[dataset.DataSource]
		if !ok {
			return fmt.Errorf("datasets.%s references unknown dataSource %q", id, dataset.DataSource)
		}
		if dataset.Query.Projection == nil {
			return fmt.Errorf("datasets.%s.query.projection is required", id)
		}
		columns := map[string]Column{}
		for _, c := range source.Columns {
			columns[c.Name] = c
		}
		outputs := map[string]Column{}
		groups := [][]Field{dataset.Query.Projection.Fields, dataset.Query.Projection.Dimensions, dataset.Query.Projection.Measures}
		for group, fields := range groups {
			for _, field := range fields {
				column, ok := columns[field.Field]
				if !ok || !validID(field.OutputName()) || outputs[field.OutputName()].Name != "" {
					return fmt.Errorf("datasets.%s projection has unknown or duplicate field %q", id, field.Field)
				}
				if group == 1 && column.Role != "dimension" || group == 2 && column.Role != "measure" {
					return fmt.Errorf("datasets.%s field %s has wrong role", id, field.Field)
				}
				if group == 2 {
					if !oneOf(field.Aggregation, "none", "sum", "min", "max", "count", "countDistinct", "average") {
						return fmt.Errorf("datasets.%s field %s has unsupported aggregation", id, field.Field)
					}
					if oneOf(field.Aggregation, "sum", "average") && !oneOf(column.Type, "number", "integer") {
						return fmt.Errorf("datasets.%s field %s requires numeric aggregation", id, field.Field)
					}
					if field.Aggregation == "sum" && (oneOf(column.Format, "percent", "percentFraction") || oneOf(strings.ToLower(column.SemanticType), "ratio", "rate", "lift", "roas", "share", "cost")) {
						return fmt.Errorf("datasets.%s field %s is non-additive", id, field.Field)
					}
				}
				column.Name = field.OutputName()
				outputs[column.Name] = column
			}
		}
		if len(outputs) == 0 {
			return fmt.Errorf("datasets.%s projection is empty", id)
		}
		for _, order := range dataset.Query.OrderBy {
			if outputs[order.Field].Name == "" || !oneOf(order.Direction, "asc", "desc") || !oneOf(order.Nulls, "", "first", "last") {
				return fmt.Errorf("datasets.%s has invalid orderBy field or direction", id)
			}
		}
		if page := dataset.Query.Page; page != nil && (page.Limit < 1 || page.Limit > maxPageSize || page.Offset < 0 || page.Offset >= maxRows) {
			return fmt.Errorf("datasets.%s page limit must be 1..%d and offset 0..%d", id, maxPageSize, maxRows-1)
		}
		if err := validatePredicate(dataset.Query.Filter, columns, 0); err != nil {
			return fmt.Errorf("datasets.%s.filter: %w", id, err)
		}
		outputFields[id] = outputs
	}
	if len(d.Sections) == 0 && d.Report == nil || len(d.Sections) != 0 && d.Report != nil {
		return fmt.Errorf("exactly one of sections or report is required")
	}
	if d.Kind == "reporting.report" && d.Report == nil {
		return fmt.Errorf("reporting.report requires native report blocks")
	}
	if d.Report != nil {
		return d.validateNativeReport(outputFields)
	}
	seenBlocks := map[string]bool{}
	blockCount := 0
	for _, section := range d.Sections {
		if !validID(section.ID) || seenBlocks[section.ID] || len(section.Blocks) == 0 {
			return fmt.Errorf("invalid or duplicate section %q", section.ID)
		}
		seenBlocks[section.ID] = true
		blockCount++
		for _, block := range section.Blocks {
			if !validID(block.ID) || seenBlocks[block.ID] || strings.TrimSpace(block.Title) == "" {
				return fmt.Errorf("invalid or duplicate block %q", block.ID)
			}
			seenBlocks[block.ID] = true
			fields, ok := outputFields[block.Dataset]
			if !ok {
				return fmt.Errorf("block %s references unknown dataset %q", block.ID, block.Dataset)
			}
			switch block.Type {
			case "kpiGroup":
				if len(block.Metrics) == 0 {
					return fmt.Errorf("block %s requires metrics", block.ID)
				}
				for i, metric := range block.Metrics {
					if fields[metric.Field].Name == "" {
						return fmt.Errorf("block %s references unknown metric %q", block.ID, metric.Field)
					}
					generatedID := fmt.Sprintf("%s_%d", block.ID, i)
					if seenBlocks[generatedID] {
						return fmt.Errorf("duplicate generated block id %q", generatedID)
					}
					seenBlocks[generatedID] = true
				}
				blockCount += len(block.Metrics)
			case "table":
				if len(block.Columns) == 0 {
					return fmt.Errorf("block %s requires columns", block.ID)
				}
				for _, column := range block.Columns {
					if fields[column].Name == "" {
						return fmt.Errorf("block %s references unknown column %q", block.ID, column)
					}
				}
				blockCount++
			case "chart":
				if block.Visual == nil || !oneOf(block.Visual.Type, "line", "bar", "horizontalBar", "area", "pie", "donut") || fields[block.Visual.Category].Name == "" || len(block.Visual.Series) == 0 {
					return fmt.Errorf("block %s has invalid chart visual", block.ID)
				}
				for _, series := range block.Visual.Series {
					if fields[series].Name == "" {
						return fmt.Errorf("block %s references unknown chart series %q", block.ID, series)
					}
				}
				blockCount++
			default:
				return fmt.Errorf("block %s has unsupported type %q", block.ID, block.Type)
			}
		}
	}
	if blockCount > maxBlocks {
		return fmt.Errorf("report exceeds %d Forge blocks", maxBlocks)
	}
	return nil
}

func validMCPArgumentPath(path string) bool {
	if !mcpArgumentPath.MatchString(path) {
		return false
	}
	for _, segment := range strings.Split(path, ".") {
		if segment == "__proto__" || segment == "constructor" || segment == "prototype" {
			return false
		}
	}
	return true
}

func mcpPathsOverlap(left, right string) bool {
	return left != "" && right != "" && (left == right || strings.HasPrefix(left, right+".") || strings.HasPrefix(right, left+"."))
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func validatePredicate(p *Predicate, columns map[string]Column, depth int) error {
	if p == nil {
		return nil
	}
	if depth > 16 {
		return fmt.Errorf("predicate nesting exceeds 16")
	}
	branches := 0
	if len(p.And) > 0 {
		branches++
	}
	if len(p.Or) > 0 {
		branches++
	}
	if p.Not != nil {
		branches++
	}
	if p.Field != "" {
		branches++
	}
	if branches != 1 {
		return fmt.Errorf("predicate must have exactly one operator")
	}
	if p.Field != "" && (columns[p.Field].Name == "" || !oneOf(p.Op, "eq", "ne", "gt", "gte", "lt", "lte", "in", "notIn")) {
		return fmt.Errorf("unknown predicate field or operator %q", p.Field)
	}
	if len(p.And)+len(p.Or) > 64 {
		return fmt.Errorf("predicate has too many children")
	}
	for i := range p.And {
		if err := validatePredicate(&p.And[i], columns, depth+1); err != nil {
			return err
		}
	}
	for i := range p.Or {
		if err := validatePredicate(&p.Or[i], columns, depth+1); err != nil {
			return err
		}
	}
	return validatePredicate(p.Not, columns, depth+1)
}

func validateParameterValue(p Parameter, value any) error {
	values := []any{value}
	if p.Multiple {
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("multiple value must be an array")
		}
		values = items
	}
	for _, item := range values {
		valid := false
		switch p.Type {
		case "string", "date", "datetime":
			_, valid = item.(string)
		case "boolean":
			_, valid = item.(bool)
		case "integer":
			switch n := item.(type) {
			case int:
				valid = true
			case int64:
				valid = true
			case json.Number:
				_, err := n.Int64()
				valid = err == nil
			case float64:
				valid = n == float64(int64(n))
			}
		case "number":
			switch item.(type) {
			case int, int64, float64, float32, json.Number:
				valid = true
			}
		}
		if !valid {
			return fmt.Errorf("value has wrong type %s", p.Type)
		}
		if len(p.Values) > 0 {
			matched := false
			for _, candidate := range p.Values {
				if fmt.Sprint(candidate) == fmt.Sprint(item) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("value is outside allowed values")
			}
		}
	}
	return nil
}

// Compile validates, fetches the declared datasets, and asks Forge to lower
// report-document-v1 source. Every dataset must be complete for the returned
// ReportFill/ReportPrint; offset pages are drained within Forge's row bound.
func Compile(ctx context.Context, definition *Definition, parameters map[string]any, fetcher DatasetFetcher) (*Result, error) {
	return compile(ctx, definition, parameters, fetcher, 0)
}

// CompileBounded returns complete Forge artifacts only when every dataset has
// an explicit end-of-data signal within maxRowsPerDataset rows. Every fetch is
// paged within the remaining cap, so a partial result is returned only as an
// error, never as exportable ReportPrint data.
func CompileBounded(ctx context.Context, definition *Definition, parameters map[string]any, fetcher DatasetFetcher, maxRowsPerDataset int) (*Result, error) {
	if maxRowsPerDataset < 1 || maxRowsPerDataset > maxRows {
		return nil, fmt.Errorf("bounded row cap must be 1..%d", maxRows)
	}
	return compile(ctx, definition, parameters, fetcher, maxRowsPerDataset)
}

// A zero rowCap retains Compile's original fetch contract. Positive caps
// require explicit pagination and completeness on every dataset.
func compile(ctx context.Context, definition *Definition, parameters map[string]any, fetcher DatasetFetcher, rowCap int) (*Result, error) {
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
		rows := []map[string]any{}
		datasetBytes := 0
		pageSize, offset := maxPageSize, 0
		if query.Page != nil {
			pageSize, offset = query.Page.Limit, query.Page.Offset
		}
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if rowCap > 0 {
				query.Page = &Page{Limit: min(pageSize, rowCap-len(rows), maxRows-offset), Offset: offset}
			}
			result, err := fetcher.FetchDataset(ctx, FetchRequest{DatasetID: id, DataSourceID: dataset.DataSource, Endpoint: definition.Endpoints[dataSource.Service.Endpoint], DataSource: dataSource, Query: query})
			if err != nil {
				return nil, fmt.Errorf("fetch dataset %s: %w", id, err)
			}
			if rowCap > 0 && !result.HasMoreKnown {
				return nil, fmt.Errorf("dataset %s has unknown completeness: explicit HasMore signal required", id)
			}
			if query.Page != nil && len(result.Rows) > query.Page.Limit {
				return nil, fmt.Errorf("dataset %s returned %d rows above page limit %d", id, len(result.Rows), query.Page.Limit)
			}
			if rowCap > 0 && len(result.Rows) > rowCap-len(rows) {
				return nil, fmt.Errorf("dataset %s exceeds bounded row cap %d", id, rowCap)
			}
			if len(rows)+len(result.Rows) > maxRows {
				return nil, fmt.Errorf("dataset %s exceeds %d rows", id, maxRows)
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
			if !result.HasMore {
				break
			}
			if rowCap > 0 && len(rows) == rowCap {
				return nil, fmt.Errorf("dataset %s exceeds bounded row cap %d", id, rowCap)
			}
			if query.Page == nil {
				return nil, fmt.Errorf("dataset %s reported more rows without offset pagination", id)
			}
			if len(result.Rows) == 0 {
				return nil, fmt.Errorf("dataset %s reported more rows without progress", id)
			}
			next := *query.Page
			next.Offset += len(result.Rows)
			if next.Offset >= maxRows {
				return nil, fmt.Errorf("dataset %s exceeds pagination bound", id)
			}
			query.Page = &next
			offset = next.Offset
		}
		if err := validateRows(id, dataset, dataSource, rows); err != nil {
			return nil, err
		}
		rowsByID[id] = rows
	}
	return compileRows(definition, rowsByID)
}

// CompileLayout verifies Forge lowering without fetching data. It is suitable
// for draft validation and publication planning, never as evidence that source
// execution or complete export data succeeded.
func CompileLayout(definition *Definition) (*Result, error) {
	if err := definition.Validate(); err != nil {
		return nil, err
	}
	rows := make(map[string][]map[string]any, len(definition.Datasets))
	for id := range definition.Datasets {
		rows[id] = []map[string]any{}
	}
	return compileRows(definition, rows)
}

func compileRows(definition *Definition, rowsByID map[string][]map[string]any) (*Result, error) {
	source, err := definition.forgeSource()
	if err != nil {
		return nil, err
	}
	fences := make([]fenced.Fence, 0, len(definition.Datasets)+2)
	sequence := 0
	add := func(kind string, payload map[string]any) error {
		sequence++
		payload["sequence"] = sequence
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		fences = append(fences, fenced.Fence{Kind: kind, Payload: raw})
		return nil
	}
	source["version"], source["scope"], source["mode"], source["grammar"] = 1, "message", "start", "report-document-v1"
	if err := add(fenced.ReportFence, source); err != nil {
		return nil, err
	}
	for _, id := range sortedKeys(definition.Datasets) {
		if err := add(fenced.DataFence, map[string]any{"version": 2, "scope": "message", "id": id, "reportRef": definition.Metadata.ID, "format": "json", "mode": "replace", "data": rowsByID[id]}); err != nil {
			return nil, err
		}
	}
	if err := add(fenced.ReportFence, map[string]any{"version": 1, "scope": "message", "id": definition.Metadata.ID, "mode": "commit"}); err != nil {
		return nil, err
	}
	compiled, err := fenced.Compile(&fenced.CompileRequest{Fences: fences, ReportID: definition.Metadata.ID})
	if err != nil {
		return nil, fmt.Errorf("Forge report compilation: %w", err)
	}
	if len(compiled.Diagnostics) > 0 {
		return nil, fmt.Errorf("Forge report diagnostics: %+v", compiled.Diagnostics)
	}
	return &Result{Source: fences[0].Payload, ReportDocument: compiled.ReportDocument, ReportSpec: compiled.ReportSpec, ReportFill: compiled.ReportFill, ReportPrint: compiled.ReportPrint}, nil
}

func validateRows(id string, dataset Dataset, source DataSource, rows []map[string]any) error {
	columns := map[string]Column{}
	for _, c := range source.Columns {
		columns[c.Name] = c
	}
	projected := map[string]Column{}
	for _, group := range [][]Field{dataset.Query.Projection.Fields, dataset.Query.Projection.Dimensions, dataset.Query.Projection.Measures} {
		for _, field := range group {
			projected[field.OutputName()] = columns[field.Field]
		}
	}
	for i, row := range rows {
		if row == nil {
			return fmt.Errorf("dataset %s row %d is null", id, i)
		}
		for name, column := range projected {
			value, ok := row[name]
			if !ok {
				return fmt.Errorf("dataset %s row %d is missing projected field %q", id, i, name)
			}
			if value == nil && !column.Nullable {
				return fmt.Errorf("dataset %s row %d has null nonnullable field %q", id, i, name)
			}
			if value != nil && !validColumnValue(column.Type, value) {
				return fmt.Errorf("dataset %s row %d field %q has wrong type %s", id, i, name, column.Type)
			}
		}
		for name := range row {
			if projected[name].Name == "" {
				return fmt.Errorf("dataset %s row %d has undeclared field %q", id, i, name)
			}
		}
	}
	return nil
}

func validColumnValue(kind string, value any) bool {
	switch kind {
	case "string", "date", "datetime":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		switch number := value.(type) {
		case int, int32, int64:
			return true
		case float64:
			return !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number
		case json.Number:
			_, err := number.Int64()
			return err == nil
		}
	case "number":
		switch number := value.(type) {
		case int, int32, int64, float32:
			return true
		case float64:
			return !math.IsNaN(number) && !math.IsInf(number, 0)
		case json.Number:
			_, err := number.Float64()
			return err == nil
		}
	}
	return false
}

func (d *Definition) forgeSource() (map[string]any, error) {
	if d.Report != nil {
		// Forge's compiler normalizes block maps in place. Clone the authored
		// source so repeated or concurrent compilation is deterministic.
		raw, err := json.Marshal(d.Report)
		if err != nil {
			return nil, fmt.Errorf("encode native report: %w", err)
		}
		var source map[string]any
		if err := json.Unmarshal(raw, &source); err != nil {
			return nil, fmt.Errorf("decode native report: %w", err)
		}
		return source, nil
	}
	blocks := []any{}
	for _, section := range d.Sections {
		blocks = append(blocks, map[string]any{"id": section.ID, "kind": "sectionBlock", "title": section.Title, "navigationLabel": section.Title})
		for _, block := range section.Blocks {
			switch block.Type {
			case "kpiGroup":
				for i, metric := range block.Metrics {
					title := metric.Label
					if title == "" {
						title = metric.Field
					}
					blocks = append(blocks, map[string]any{"id": fmt.Sprintf("%s_%d", block.ID, i), "kind": "kpiBlock", "datasetRef": block.Dataset, "title": title, "valueField": metric.Field, "valueFormat": metric.Format})
				}
			case "table":
				cols := []any{}
				dataset := d.Datasets[block.Dataset]
				source := d.DataSources[dataset.DataSource]
				for _, name := range block.Columns {
					label, format := name, ""
					for _, group := range [][]Field{dataset.Query.Projection.Fields, dataset.Query.Projection.Dimensions, dataset.Query.Projection.Measures} {
						for _, field := range group {
							if field.OutputName() == name {
								for _, col := range source.Columns {
									if col.Name == field.Field {
										if col.Label != "" {
											label = col.Label
										}
										format = col.Format
									}
								}
							}
						}
					}
					column := map[string]any{"key": name, "label": label}
					if format != "" {
						column["format"] = format
					}
					cols = append(cols, column)
				}
				blocks = append(blocks, map[string]any{"id": block.ID, "kind": "tableBlock", "title": block.Title, "datasetRef": block.Dataset, "columns": cols})
			case "chart":
				blocks = append(blocks, map[string]any{"id": block.ID, "kind": "chartBlock", "title": block.Title, "datasetRef": block.Dataset, "chartSpec": map[string]any{"type": block.Visual.Type, "xField": block.Visual.Category, "yFields": block.Visual.Series}})
			}
		}
	}
	return map[string]any{"id": d.Metadata.ID, "title": d.Metadata.Title, "subtitle": d.Metadata.Description, "blocks": blocks}, nil
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
