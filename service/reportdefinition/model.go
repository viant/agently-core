// Package reportdefinition compiles authored Agently ReportDefinition YAML into
// Forge's canonical report artifacts. It never opens a datasource itself.
package reportdefinition

import (
	"context"
	"encoding/json"

	agnproto "github.com/viant/agently-core/protocol/datasource"
)

// Definition is the renderer-neutral authoring contract used by Agently reports.
type Definition struct {
	APIVersion  string                `yaml:"apiVersion" json:"apiVersion"`
	Kind        string                `yaml:"kind" json:"kind"`
	Metadata    Metadata              `yaml:"metadata" json:"metadata"`
	Parameters  []Parameter           `yaml:"parameters" json:"parameters,omitempty"`
	Endpoints   map[string]Endpoint   `yaml:"endpoints" json:"endpoints,omitempty"`
	DataSources map[string]DataSource `yaml:"dataSources" json:"dataSources"`
	Datasets    map[string]Dataset    `yaml:"datasets" json:"datasets"`
	Sections    []Section             `yaml:"sections" json:"sections"`
	// Report is Forge's native report-document-v1 source. It is an alternative
	// to Sections, not another block grammar implemented by this package.
	Report  map[string]any `yaml:"report" json:"report,omitempty"`
	Exports Exports        `yaml:"exports" json:"exports,omitempty"`
}

type Metadata struct {
	ID          string `yaml:"id" json:"id"`
	Title       string `yaml:"title" json:"title"`
	Description string `yaml:"description" json:"description,omitempty"`
	Revision    int    `yaml:"revision" json:"revision"`
}

type Parameter struct {
	Name     string `yaml:"name" json:"name"`
	Type     string `yaml:"type" json:"type"`
	Label    string `yaml:"label" json:"label,omitempty"`
	Multiple bool   `yaml:"multiple" json:"multiple,omitempty"`
	Default  any    `yaml:"default" json:"default,omitempty"`
	Values   []any  `yaml:"values" json:"values,omitempty"`
}

type Endpoint struct {
	Type      string `yaml:"type" json:"type"`
	Transport string `yaml:"transport" json:"transport"`
	BaseURL   string `yaml:"baseURL" json:"baseURL"`
}

type Service struct {
	Endpoint string `yaml:"endpoint" json:"endpoint"`
	URI      string `yaml:"uri" json:"uri"`
	Method   string `yaml:"method" json:"method"`
}

type ResultContract struct {
	Shape       string `yaml:"shape" json:"shape"`
	ResultsPath string `yaml:"resultsPath" json:"resultsPath,omitempty"`
	ResultName  string `yaml:"resultName" json:"resultName,omitempty"`
	RowPath     string `yaml:"rowPath" json:"rowPath,omitempty"`
	HasMorePath string `yaml:"hasMorePath" json:"hasMorePath,omitempty"`
}

type Column struct {
	Name         string `yaml:"name" json:"name"`
	Type         string `yaml:"type" json:"type"`
	Role         string `yaml:"role" json:"role"`
	Label        string `yaml:"label" json:"label,omitempty"`
	Format       string `yaml:"format" json:"format,omitempty"`
	Nullable     bool   `yaml:"nullable" json:"nullable"`
	SemanticType string `yaml:"semanticType" json:"semanticType,omitempty"`
}

type Binding struct {
	Parameter string `yaml:"parameter" json:"parameter"`
	Field     string `yaml:"field" json:"field"`
	Operator  string `yaml:"operator" json:"operator"`
}

type DataSource struct {
	Service Service `yaml:"service" json:"service"`
	// MCPRequest is the explicit per-report tool argument mapping. The host
	// resolves Service.Endpoint at execution time; this never carries a URL or
	// credential and is not a substitute for current source authorization.
	MCPRequest        *agnproto.MCPRequestBinding `yaml:"mcpRequest,omitempty" json:"mcpRequest,omitempty"`
	Description       string                      `yaml:"description" json:"description,omitempty"`
	ResultContract    ResultContract              `yaml:"resultContract" json:"resultContract"`
	Columns           []Column                    `yaml:"columns" json:"columns"`
	ParameterBindings []Binding                   `yaml:"parameterBindings" json:"parameterBindings,omitempty"`
}

type Field struct {
	Field       string `yaml:"field" json:"field"`
	Alias       string `yaml:"alias" json:"alias,omitempty"`
	Aggregation string `yaml:"aggregation" json:"aggregation,omitempty"`
}

// UnmarshalYAML permits both "field" and {field: name} projection entries.
func (f *Field) UnmarshalYAML(unmarshal func(any) error) error {
	var name string
	if err := unmarshal(&name); err == nil {
		*f = Field{Field: name}
		return nil
	}
	type plain Field
	return unmarshal((*plain)(f))
}

func (f Field) OutputName() string {
	if f.Alias != "" {
		return f.Alias
	}
	return f.Field
}

type Projection struct {
	Fields     []Field `yaml:"fields" json:"fields,omitempty"`
	Dimensions []Field `yaml:"dimensions" json:"dimensions,omitempty"`
	Measures   []Field `yaml:"measures" json:"measures,omitempty"`
}

type Predicate struct {
	And   []Predicate `yaml:"and" json:"and,omitempty"`
	Or    []Predicate `yaml:"or" json:"or,omitempty"`
	Not   *Predicate  `yaml:"not" json:"not,omitempty"`
	Field string      `yaml:"field" json:"field,omitempty"`
	Op    string      `yaml:"op" json:"op,omitempty"`
	Value any         `yaml:"value" json:"value,omitempty"`
}

type Order struct {
	Field     string `yaml:"field" json:"field"`
	Direction string `yaml:"direction" json:"direction"`
	Nulls     string `yaml:"nulls" json:"nulls,omitempty"`
}

type Page struct {
	Limit  int `yaml:"limit" json:"limit"`
	Offset int `yaml:"offset" json:"offset"`
}

type Query struct {
	Projection *Projection `yaml:"projection" json:"projection,omitempty"`
	Filter     *Predicate  `yaml:"filter" json:"filter,omitempty"`
	OrderBy    []Order     `yaml:"orderBy" json:"orderBy,omitempty"`
	Page       *Page       `yaml:"page" json:"page,omitempty"`
}

type Dataset struct {
	DataSource string `yaml:"dataSource" json:"dataSource"`
	Query      Query  `yaml:"query" json:"query"`
}

type Metric struct {
	Field  string `yaml:"field" json:"field"`
	Label  string `yaml:"label" json:"label,omitempty"`
	Format string `yaml:"format" json:"format,omitempty"`
}

type Visual struct {
	Type     string   `yaml:"type" json:"type"`
	Category string   `yaml:"category" json:"category"`
	Series   []string `yaml:"series" json:"series"`
}

type Paging struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

type Block struct {
	ID      string   `yaml:"id" json:"id"`
	Type    string   `yaml:"type" json:"type"`
	Title   string   `yaml:"title" json:"title"`
	Dataset string   `yaml:"dataset" json:"dataset"`
	Metrics []Metric `yaml:"metrics" json:"metrics,omitempty"`
	Visual  *Visual  `yaml:"visual" json:"visual,omitempty"`
	Columns []string `yaml:"columns" json:"columns,omitempty"`
	Paging  *Paging  `yaml:"paging" json:"paging,omitempty"`
}

type Section struct {
	ID     string  `yaml:"id" json:"id"`
	Title  string  `yaml:"title" json:"title"`
	Blocks []Block `yaml:"blocks" json:"blocks"`
}

type Exports struct {
	Formats             []string `yaml:"formats" json:"formats,omitempty"`
	UseResolvedDatasets bool     `yaml:"useResolvedDatasets" json:"useResolvedDatasets,omitempty"`
}

// FetchRequest carries the validated authored query and binding predicates.
// The application chooses how to resolve Service and ResultContract; no tool
// name, transport, or database behavior is inferred by this package.
type FetchRequest struct {
	DatasetID    string
	DataSourceID string
	Endpoint     Endpoint
	DataSource   DataSource
	Query        Query
}

type FetchResult struct {
	Rows    []map[string]any
	HasMore bool
	// HasMoreKnown says the fetcher observed an explicit completeness signal.
	// Bounded compilation requires it on every page, including the final
	// page where HasMore is false. Preview marks an absent signal incomplete.
	HasMoreKnown bool
}

type DatasetFetcher interface {
	FetchDataset(context.Context, FetchRequest) (FetchResult, error)
}

type DatasetFetcherFunc func(context.Context, FetchRequest) (FetchResult, error)

func (f DatasetFetcherFunc) FetchDataset(ctx context.Context, request FetchRequest) (FetchResult, error) {
	return f(ctx, request)
}

// Result exposes both the Forge authoring source and its canonical outputs.
// ReportDocument, ReportSpec, ReportFill, and ReportPrint can be handed directly
// to the AI Studio reporting/export flow.
type Result struct {
	Source         json.RawMessage `json:"source"`
	ReportDocument json.RawMessage `json:"reportDocument"`
	ReportSpec     json.RawMessage `json:"reportSpec"`
	ReportFill     json.RawMessage `json:"reportFill"`
	ReportPrint    json.RawMessage `json:"reportPrint"`
}

// PreviewDataset describes whether all rows from the authored starting offset
// were observed. A missing completeness signal is never treated as complete.
type PreviewDataset struct {
	RowCount int    `json:"rowCount"`
	Complete bool   `json:"complete"`
	Reason   string `json:"reason,omitempty"` // rowCap, paginationBound, or completenessUnknown
}

// PreviewResult contains renderable Forge artifacts and explicit completeness
// metadata. It deliberately has no ReportPrint: callers must use Compile for
// a complete, exportable report.
type PreviewResult struct {
	Kind           string                    `json:"kind"`
	Source         json.RawMessage           `json:"source"`
	ReportDocument json.RawMessage           `json:"reportDocument"`
	ReportSpec     json.RawMessage           `json:"reportSpec"`
	ReportFill     json.RawMessage           `json:"reportFill"`
	Complete       bool                      `json:"complete"`
	Datasets       map[string]PreviewDataset `json:"datasets"`
}
