package resources

import scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"

type ResourceSelection struct {
	ComponentID string `json:"componentId,omitempty"`
	Sheet       string `json:"sheet,omitempty"`
	Range       string `json:"range,omitempty"`
	Pages       []int  `json:"pages,omitempty"`
}
type ResourceOptions struct {
	Values    string `json:"values,omitempty"`
	HeaderRow int    `json:"headerRow,omitempty"`
	Encoding  string `json:"encoding,omitempty"`
	Delimiter string `json:"delimiter,omitempty"`
	OCR       string `json:"ocr,omitempty"`
}
type ResourceLimits struct {
	MaxRows        int `json:"maxRows,omitempty"`
	MaxOutputBytes int `json:"maxOutputBytes,omitempty"`
}
type InspectInput struct {
	URI             string             `json:"uri,omitempty"`
	Path            string             `json:"path,omitempty"`
	RootID          string             `json:"rootId,omitempty"`
	Select          *ResourceSelection `json:"select,omitempty"`
	Cursor          string             `json:"cursor,omitempty"`
	Limit           int                `json:"limit,omitempty"`
	ExpectedVersion string             `json:"expectedVersion,omitempty"`
}
type ResourceComponent struct {
	UsedRangeEstimated bool   `json:"usedRangeEstimated,omitempty"`
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	Name               string `json:"name"`
	UsedRange          string `json:"usedRange,omitempty"`
}
type InspectOutput struct {
	ColumnsInferred               bool                `json:"columnsInferred,omitempty"`
	NativeRequiresProviderSupport bool                `json:"nativeRequiresProviderSupport,omitempty"`
	URI                           string              `json:"uri"`
	Name                          string              `json:"name"`
	MimeType                      string              `json:"mimeType"`
	Kind                          string              `json:"kind"`
	SizeBytes                     int                 `json:"sizeBytes"`
	Version                       string              `json:"version"`
	Components                    []ResourceComponent `json:"components,omitempty"`
	Columns                       []string            `json:"columns,omitempty"`
	PageCount                     int                 `json:"pageCount,omitempty"`
	Width                         int                 `json:"width,omitempty"`
	Height                        int                 `json:"height,omitempty"`
	Capabilities                  map[string][]string `json:"capabilities"`
	Complete                      bool                `json:"complete"`
	NextCursor                    string              `json:"nextCursor,omitempty"`
}
type ResourceTable struct {
	Sheet       string     `json:"sheet,omitempty"`
	Rows        [][]string `json:"rows"`
	RowNumbers  []int      `json:"rowNumbers"`
	StartColumn int        `json:"startColumn"`
	HeaderRow   int        `json:"headerRow,omitempty"`
}
type ResourceCoverage struct {
	Selection    *ResourceSelection `json:"selection,omitempty"`
	ReturnedRows int                `json:"returnedRows,omitempty"`
	Truncated    bool               `json:"truncated"`
	NextRow      int                `json:"nextRow,omitempty"`
}

// NativeResource is an internal presentation request emitted only by resources:read.
// The executor opens this immutable snapshot; the model sees no binary payload.
type NativeResource struct {
	URI      string `json:"uri"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	SHA256   string `json:"sha256"`
}
type ExportInput struct {
	URI             string             `json:"uri,omitempty"`
	Path            string             `json:"path,omitempty"`
	RootID          string             `json:"rootId,omitempty"`
	Select          *ResourceSelection `json:"select,omitempty"`
	Operation       string             `json:"operation"`
	Output          ExportFormat       `json:"output"`
	Options         *ResourceOptions   `json:"options,omitempty"`
	ExpectedVersion string             `json:"expectedVersion,omitempty"`
}
type ExportFormat struct {
	Format string `json:"format"`
	DPI    int    `json:"dpi,omitempty"`
}
type ExportOutput struct {
	Warnings      []string                            `json:"warnings,omitempty"`
	Resources     []*scratchpadsvc.ArtifactDescriptor `json:"resources"`
	SourceVersion string                              `json:"sourceVersion"`
	Complete      bool                                `json:"complete"`
}
