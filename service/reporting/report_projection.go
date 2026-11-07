package reporting

import (
	"context"
	"encoding/json"
	sharedregistry "github.com/viant/forge/backend/reporting/registry"
)

func (s *Service) GetReport(ctx context.Context, input *GetReportInput) (*SharedArtifact, error) {
	report, err := s.getCompleteReport(ctx, input)
	if err != nil || report == nil || input == nil || input.Include == nil {
		return report, err
	}
	raw := report.ResourceDefinition
	include := *input.Include
	if len(raw) == 0 {
		// Legacy saved artifacts have typed payload fields, not a retained raw envelope.
		include.Raw = false
		raw, err = json.Marshal(sharedregistry.ReportEnvelope{SchemaVersion: 1, ReportDocument: report.Document, ReportSpec: report.ReportSpec, ReportFill: report.ReportFill, ReportPrint: report.ReportPrint, CompileState: report.CompileState, Metadata: report.Metadata})
		if err != nil {
			return nil, err
		}
	}
	sections, err := sharedregistry.SelectReportDefinition(raw, include)
	if err != nil {
		return nil, err
	}
	out := *report
	out.ResourceDefinition = nil
	out.Document = nil
	out.ReportSpec = nil
	out.ReportFill = nil
	out.ReportPrint = nil
	out.CompileState = nil
	out.Metadata = nil
	out.SavedViewOverlay = nil
	out.Sections = sections
	return &out, nil
}
