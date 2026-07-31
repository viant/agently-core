package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	reportingsvc "github.com/viant/agently-core/service/reporting"
)

// ReportingToolExecutor is the narrow SDK seam required by ReportingClient.
// Both embedded and HTTP Agently clients satisfy it through ExecuteTool.
type ReportingToolExecutor interface {
	ExecuteTool(ctx context.Context, name string, args map[string]interface{}) (string, error)
}

// ReportingClient exposes typed saved-report lifecycle operations on top of
// the canonical reporting tool contract.
type ReportingClient struct {
	executor ReportingToolExecutor
}

func NewReportingClient(executor ReportingToolExecutor) *ReportingClient {
	return &ReportingClient{executor: executor}
}

func (c *ReportingClient) ListReports(ctx context.Context, input *reportingsvc.ListReportsInput) (*reportingsvc.ListReportsResult, error) {
	result := &reportingsvc.ListReportsResult{}
	if err := c.execute(ctx, "reporting:list_reports", input, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *ReportingClient) GetReport(ctx context.Context, input *reportingsvc.GetReportInput) (*reportingsvc.SharedArtifact, error) {
	result := &reportingsvc.SharedArtifact{}
	if err := c.execute(ctx, "reporting:get_report", input, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *ReportingClient) SaveReport(ctx context.Context, input *reportingsvc.SaveReportRequest) (*reportingsvc.SharedArtifact, error) {
	result := &reportingsvc.SharedArtifact{}
	if err := c.execute(ctx, "reporting:save_report", input, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *ReportingClient) UpdateReport(ctx context.Context, input *reportingsvc.UpdateReportRequest) (*reportingsvc.SharedArtifact, error) {
	result := &reportingsvc.SharedArtifact{}
	if err := c.execute(ctx, "reporting:update_report", input, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *ReportingClient) DuplicateReport(ctx context.Context, input *reportingsvc.DuplicateReportRequest) (*reportingsvc.SharedArtifact, error) {
	result := &reportingsvc.SharedArtifact{}
	if err := c.execute(ctx, "reporting:duplicate_report", input, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *ReportingClient) DeleteReport(ctx context.Context, input *reportingsvc.DeleteReportRequest) (*reportingsvc.DeleteReportResult, error) {
	result := &reportingsvc.DeleteReportResult{}
	if err := c.execute(ctx, "reporting:delete_report", input, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *ReportingClient) RecordReportRun(ctx context.Context, input *reportingsvc.RecordReportRunRequest) (*reportingsvc.SharedArtifact, error) {
	result := &reportingsvc.SharedArtifact{}
	if err := c.execute(ctx, "reporting:record_report_run", input, result); err != nil {
		return nil, err
	}
	return result, nil
}

// SubmitSavedReportExport queues an export from a persisted report identity.
func (c *ReportingClient) SubmitSavedReportExport(ctx context.Context, reportID string, format reportingsvc.ExportFormat) (*reportingsvc.ExportJob, error) {
	result := &reportingsvc.ExportJob{}
	input := &reportingsvc.SubmitExportRequest{
		Format: format,
		Source: &reportingsvc.ExportSource{
			Kind:     "report",
			ReportID: strings.TrimSpace(reportID),
		},
	}
	if err := c.execute(ctx, "reporting:submit_export", input, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *ReportingClient) GetExportStatus(ctx context.Context, jobID string) (*reportingsvc.ExportJob, error) {
	result := &reportingsvc.ExportJob{}
	if err := c.execute(ctx, "reporting:get_export_status", &reportingsvc.GetExportStatusInput{JobID: strings.TrimSpace(jobID)}, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *ReportingClient) GetExportArtifact(ctx context.Context, artifactID string) (*reportingsvc.Artifact, error) {
	result := &reportingsvc.Artifact{}
	if err := c.execute(ctx, "reporting:get_artifact", &reportingsvc.GetArtifactInput{ArtifactID: strings.TrimSpace(artifactID)}, result); err != nil {
		return nil, err
	}
	return result, nil
}

// WaitForSavedReportExport polls a queued export until it succeeds, fails, the
// context is canceled, or the supplied timeout elapses.
func (c *ReportingClient) WaitForSavedReportExport(ctx context.Context, jobID string, timeout time.Duration) (*reportingsvc.Artifact, error) {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := c.GetExportStatus(ctx, jobID)
		if err != nil {
			return nil, err
		}
		switch job.Status {
		case reportingsvc.JobStatusSucceeded:
			return c.GetExportArtifact(ctx, job.ArtifactID)
		case reportingsvc.JobStatusFailed:
			return nil, fmt.Errorf("report export failed: %s", strings.TrimSpace(job.Error))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("report export %s did not complete within %s", jobID, timeout)
		case <-ticker.C:
		}
	}
}

func (c *ReportingClient) execute(ctx context.Context, name string, input, output interface{}) error {
	if c == nil || c.executor == nil {
		return fmt.Errorf("reporting SDK: tool executor is required")
	}
	args := map[string]interface{}{}
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(encoded, &args); err != nil {
			return err
		}
	}
	payload, err := c.executor.ExecuteTool(ctx, name, args)
	if err != nil {
		return err
	}
	value := strings.TrimSpace(payload)
	if value == "" {
		return fmt.Errorf("reporting SDK: %s returned an empty response", name)
	}
	if strings.HasPrefix(value, `"`) {
		var decoded string
		if json.Unmarshal([]byte(value), &decoded) == nil {
			value = strings.TrimSpace(decoded)
		}
	}
	if err := json.Unmarshal([]byte(value), output); err != nil {
		return fmt.Errorf("reporting SDK: decode %s response: %w", name, err)
	}
	return nil
}
