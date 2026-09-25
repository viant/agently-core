package reportdefinition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	reportfill "github.com/viant/forge/backend/reporting/fill"
	reportprint "github.com/viant/forge/backend/reporting/print"
	reportspec "github.com/viant/forge/backend/reporting/spec"
)

const maxActivatedReportBytes = 32 << 20

// ActivatedReport is a server-compiled, exact published report. An agent
// selects a group/report and parameters; the producer resolves the definition,
// executes its declared sources, and returns Forge artifacts. Consumers render
// these artifacts directly rather than reconstructing blocks or datasource
// bindings from field metadata.
type ActivatedReport struct {
	GroupID        string          `json:"groupId"`
	ReportID       string          `json:"reportId"`
	ArtifactID     string          `json:"artifactId"`
	Version        int64           `json:"version"`
	DraftRevision  int64           `json:"draftRevision"`
	ContentDigest  string          `json:"contentDigest"`
	ReportDocument json.RawMessage `json:"reportDocument"`
	ReportSpec     json.RawMessage `json:"reportSpec"`
	ReportFill     json.RawMessage `json:"reportFill"`
	ReportPrint    json.RawMessage `json:"reportPrint"`
	ComputedAt     time.Time       `json:"computedAt"`
}

func (a *ActivatedReport) Validate() error {
	if a == nil || !validID(a.GroupID) || !validID(a.ReportID) || a.ArtifactID == "" || a.Version < 1 || a.DraftRevision < 1 || a.ContentDigest == "" || a.ComputedAt.IsZero() {
		return fmt.Errorf("activated report identity or revision is invalid")
	}
	for _, raw := range []json.RawMessage{a.ReportDocument, a.ReportSpec, a.ReportFill, a.ReportPrint} {
		if len(raw) == 0 || len(raw) > maxActivatedReportBytes || !json.Valid(raw) {
			return fmt.Errorf("activated report has a missing, oversized or malformed Forge artifact")
		}
	}
	spec, err := reportspec.DecodeJSON(a.ReportSpec)
	if err != nil {
		return fmt.Errorf("activated reportSpec: %w", err)
	}
	fill, err := reportfill.DecodeJSON(a.ReportFill)
	if err != nil {
		return fmt.Errorf("activated reportFill: %w", err)
	}
	print, err := reportprint.DecodeJSON(a.ReportPrint)
	if err != nil {
		return fmt.Errorf("activated reportPrint: %w", err)
	}
	if spec.Version != fill.SpecVersion || spec.Version != print.SpecVersion || fill.Version != print.FillVersion ||
		fill.SpecHash == "" || fill.SpecHash != print.SpecHash ||
		spec.Source.Kind != fill.Source.Kind || spec.Source.ContainerID != fill.Source.ContainerID ||
		spec.Source.StateKey != fill.Source.StateKey || spec.Source.DataSourceRef != fill.Source.DataSourceRef ||
		spec.Source.Kind != print.Source.Kind || spec.Source.ContainerID != print.Source.ContainerID ||
		spec.Source.StateKey != print.Source.StateKey || spec.Source.DataSourceRef != print.Source.DataSourceRef {
		return fmt.Errorf("activated Forge artifacts do not share one exact source and version")
	}
	return nil
}

// DecodeActivatedMCPReport accepts AI Studio's ordinary MCP result envelope
// with a single ActivatedReport in data. The caller pins the expected
// group/report identity independently of the response body.
func DecodeActivatedMCPReport(body []byte, groupID, reportID string) (*ActivatedReport, error) {
	if len(body) == 0 || len(body) > maxActivatedReportBytes {
		return nil, fmt.Errorf("activated MCP response exceeds limit")
	}
	var envelope struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode activated MCP response: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF || envelope.Status != "ok" {
		return nil, fmt.Errorf("activated MCP response is incomplete or unsuccessful")
	}
	var result ActivatedReport
	data := json.NewDecoder(bytes.NewReader(envelope.Data))
	data.DisallowUnknownFields()
	if err := data.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode activated report: %w", err)
	}
	if _, err := data.Token(); err != io.EOF || result.GroupID != groupID || result.ReportID != reportID {
		return nil, fmt.Errorf("activated report identity mismatch")
	}
	if err := result.Validate(); err != nil {
		return nil, err
	}
	return &result, nil
}
