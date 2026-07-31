package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	forgefenced "github.com/viant/forge/backend/reporting/fenced"
)

// CompileFencedReport compiles progressive Forge fences into an export-ready
// canonical reporting envelope.
func (s *Service) CompileFencedReport(_ context.Context, request *CompileFencedReportRequest) (*CompileFencedReportResult, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting fenced compile: request is required")
	}
	fences := make([]forgefenced.Fence, 0, len(request.Fences))
	for _, fence := range request.Fences {
		payload, err := normalizeFencedPayload(fence.Payload)
		if err != nil {
			return nil, fmt.Errorf("reporting fenced compile: fence %d payload: %w", fence.Index, err)
		}
		fences = append(fences, forgefenced.Fence{
			Kind: strings.TrimSpace(fence.Kind), Index: fence.Index, Payload: payload,
		})
	}
	compiled, err := forgefenced.Compile(&forgefenced.CompileRequest{
		Content: request.Content, Fences: fences, ReportID: strings.TrimSpace(request.ReportID),
	})
	if err != nil {
		return nil, fmt.Errorf("reporting fenced compile: %w", err)
	}
	if compiled == nil || compiled.Assembly == nil {
		return nil, fmt.Errorf("reporting fenced compile: compiler returned no report assembly")
	}
	format := request.Format
	if format == "" {
		format = ExportFormatPDF
	}
	if format != ExportFormatPDF && format != ExportFormatCSV && format != ExportFormatXLSX {
		return nil, fmt.Errorf("reporting fenced compile: unsupported format %q", format)
	}
	reportID := strings.TrimSpace(compiled.Assembly.ID)
	title := jsonObjectText(compiled.ReportSpec, "title")
	if title == "" {
		title = reportID
	}
	envelope := &ReportExportRequest{
		Version: 1,
		Kind:    "reportExportRequest",
		Target:  ReportExportTarget{Format: format},
		Source: ReportExportSource{
			From: "draft", ArtifactKind: "dashboard.reportBuilder",
			ArtifactRef: "dashboard.reportBuilder://" + reportID,
			Title:       title, ReportID: reportID,
		},
		ReportSpec: cloneJSON(compiled.ReportSpec), ReportFill: cloneJSON(compiled.ReportFill),
		ReportPrint: cloneJSON(compiled.ReportPrint),
	}
	if normalized, err := normalizeSubmitExportRequest(&SubmitExportRequest{ReportExportRequest: envelope}); err != nil {
		return nil, fmt.Errorf("reporting fenced compile produced invalid export request: %w", err)
	} else if err := validateSubmitExportRequest(normalized); err != nil {
		return nil, fmt.Errorf("reporting fenced compile produced invalid export request: %w", err)
	}
	diagnostics := make([]Diagnostic, 0, len(compiled.Diagnostics))
	for _, item := range compiled.Diagnostics {
		diagnostics = append(diagnostics, Diagnostic{
			Code: item.Code, Severity: item.Severity, Path: item.Path,
			Message: item.Message, SuggestedFix: item.SuggestedFix,
		})
	}
	return &CompileFencedReportResult{
		ReportID: reportID, ReportDocument: cloneJSON(compiled.ReportDocument),
		ReportSpec: cloneJSON(compiled.ReportSpec), ReportFill: cloneJSON(compiled.ReportFill),
		ReportPrint: cloneJSON(compiled.ReportPrint), ReportExportRequest: envelope,
		Diagnostics: diagnostics,
	}, nil
}

// normalizeFencedPayload accepts both a raw JSON object and a JSON string
// containing that object. Tool callers commonly use the latter because
// FencedReportFence.Payload is exposed as json.RawMessage in the Go schema.
func normalizeFencedPayload(payload json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("is required")
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, fmt.Errorf("decode JSON string: %w", err)
		}
		trimmed = bytes.TrimSpace([]byte(text))
	}
	if !json.Valid(trimmed) {
		return nil, fmt.Errorf("must be valid JSON")
	}
	return cloneJSON(trimmed), nil
}

func (s *Service) compileFencedReportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*CompileFencedReportRequest)
	if !ok {
		return fmt.Errorf("invalid reporting fenced compile input %T", in)
	}
	output, ok := out.(*CompileFencedReportResult)
	if !ok {
		return fmt.Errorf("invalid reporting fenced compile output %T", out)
	}
	result, err := s.CompileFencedReport(ctx, input)
	if err != nil {
		return err
	}
	*output = *result
	return nil
}

// CompileAndExportFencedReport compiles progressive Forge fences, submits the
// canonical envelope directly, runs the configured exporter, and returns a
// compact artifact projection.
func (s *Service) CompileAndExportFencedReport(ctx context.Context, request *CompileAndExportFencedReportRequest) (*CompileAndExportFencedReportResult, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting fenced export: request is required")
	}
	compiled, err := s.CompileFencedReport(ctx, &CompileFencedReportRequest{
		Content:  request.Content,
		Fences:   request.Fences,
		ReportID: request.ReportID,
		Format:   request.Format,
	})
	if err != nil {
		return nil, err
	}
	envelope := compiled.ReportExportRequest
	if envelope == nil {
		return nil, fmt.Errorf("reporting fenced export: compiler returned no export request")
	}
	envelope.Metadata = buildExportContextMetadata(request.ConversationID, request.WorkspaceID, envelope.Metadata, nil)
	job, err := s.SubmitExport(ctx, &SubmitExportRequest{ReportExportRequest: envelope})
	if err != nil {
		return nil, fmt.Errorf("reporting fenced export submit: %w", err)
	}
	job, err = s.RunExport(ctx, job.JobID)
	if err != nil {
		return nil, fmt.Errorf("reporting fenced export run: %w", err)
	}
	artifact, err := s.GetArtifact(ctx, job.ArtifactID)
	if err != nil {
		return nil, fmt.Errorf("reporting fenced export artifact: %w", err)
	}
	artifact.Data = nil
	return &CompileAndExportFencedReportResult{
		ReportID: compiled.ReportID,
		Job: &ExportJob{
			JobID: job.JobID, ArtifactRef: job.ArtifactRef,
			ConversationID: job.ConversationID, WorkspaceID: job.WorkspaceID,
			Format: job.Format, Scope: job.Scope, Status: job.Status,
			ArtifactID: job.ArtifactID, Error: job.Error,
			Diagnostics: job.Diagnostics, SubmittedAt: job.SubmittedAt,
			StartedAt: job.StartedAt, CompletedAt: job.CompletedAt,
			RetentionTTL: job.RetentionTTL,
		},
		Artifact:    artifact,
		Diagnostics: compiled.Diagnostics,
	}, nil
}

func (s *Service) compileAndExportFencedReportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*CompileAndExportFencedReportRequest)
	if !ok {
		return fmt.Errorf("invalid reporting fenced export input %T", in)
	}
	output, ok := out.(*CompileAndExportFencedReportResult)
	if !ok {
		return fmt.Errorf("invalid reporting fenced export output %T", out)
	}
	result, err := s.CompileAndExportFencedReport(ctx, input)
	if err != nil {
		return err
	}
	*output = *result
	return nil
}
