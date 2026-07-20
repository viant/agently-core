// Package rendering defines the dependency-light SDK wire contract for rich
// transcript content. Runtime packages may use these DTOs without importing
// the higher-level sdk or sdk/api packages.
package rendering

import "encoding/json"

// RenderedContent is the canonical rendering contract for rich transcript
// content. Raw assistant content remains available for compatibility.
type RenderedContent struct {
	SchemaVersion string                    `json:"schemaVersion"`
	Parts         []*RenderedContentPart    `json:"parts"`
	Reports       []*RenderedReportAssembly `json:"reports,omitempty"`
	Diagnostics   []*RenderedContentWarning `json:"diagnostics,omitempty"`
}

type RenderedContentPart struct {
	Kind     string          `json:"kind"`
	Text     string          `json:"text,omitempty"`
	Language string          `json:"language,omitempty"`
	Source   string          `json:"source,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	Data     *RenderedData   `json:"data,omitempty"`
	Report   *RenderedReport `json:"report,omitempty"`
}

type RenderedData struct {
	Version   int             `json:"version,omitempty"`
	Scope     string          `json:"scope,omitempty"`
	ReportRef string          `json:"reportRef,omitempty"`
	Sequence  int             `json:"sequence,omitempty"`
	ID        string          `json:"id"`
	Format    string          `json:"format,omitempty"`
	Mode      string          `json:"mode,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// RenderedReport is one normalized forge-report transaction. Payload retains
// the complete authoring envelope so newer clients can preserve unknown safe
// metadata while the shared assembler applies the versioned contract.
type RenderedReport struct {
	Version  int                   `json:"version"`
	Scope    string                `json:"scope,omitempty"`
	ID       string                `json:"id"`
	Sequence int                   `json:"sequence"`
	Mode     string                `json:"mode"`
	Grammar  string                `json:"grammar,omitempty"`
	Target   *RenderedReportTarget `json:"target,omitempty"`
	Payload  json.RawMessage       `json:"payload"`
}

type RenderedReportTarget struct {
	Kind     string `json:"kind,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Slot     string `json:"slot,omitempty"`
	Position string `json:"position,omitempty"`
}

// RenderedReportAssembly is the latest immutable progressive report snapshot.
// Source is dashboard-v1 or report-document-v1 authoring JSON; Forge remains
// responsible for lowering it to the canonical ReportDocument runtime.
type RenderedReportAssembly struct {
	Scope        string                   `json:"scope"`
	ID           string                   `json:"id"`
	Grammar      string                   `json:"grammar,omitempty"`
	Status       string                   `json:"status"`
	Sequence     int                      `json:"sequence,omitempty"`
	ResetVersion int                      `json:"resetVersion,omitempty"`
	Source       json.RawMessage          `json:"source,omitempty"`
	DataSources  map[string]*RenderedData `json:"dataSources,omitempty"`
}

type RenderedContentWarning struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	ReportID     string `json:"reportId,omitempty"`
	BlockID      string `json:"blockId,omitempty"`
	DataSourceID string `json:"dataSourceId,omitempty"`
	Sequence     int    `json:"sequence,omitempty"`
	Fence        string `json:"fence,omitempty"`
	Path         string `json:"path,omitempty"`
	SuggestedFix string `json:"suggestedFix,omitempty"`
}
