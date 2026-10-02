package conversationtree

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	artifactread "github.com/viant/agently-core/internal/datly/reporting/artifact/read"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

type ExportReferences struct {
	ReportRunIDs []string
	JobIDs       []string
	ArtifactIDs  []string
}

var exportJobReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[jobread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/job"},
}
var exportArtifactReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[artifactread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/artifact"},
}

// ValidateExportReferences gathers the export dependencies for a graph,
// checks ownership and blocks queued or running exports before mutation.
func (d *Discoverer) ValidateExportReferences(ctx context.Context, graph *Graph) (*ExportReferences, error) {
	if d == nil || d.Invoker == nil || d.OwnerID == nil {
		return nil, fmt.Errorf("conversation graph reader is not configured")
	}
	userID := strings.TrimSpace(d.OwnerID(ctx))
	if err := d.authorize(ctx, graph); err != nil {
		return nil, err
	}
	reportRunIDs, err := d.ValidateReportReferences(ctx, graph)
	if err != nil {
		return nil, err
	}
	result := &ExportReferences{ReportRunIDs: reportRunIDs}
	if len(graph.Nodes) == 0 {
		return result, nil
	}
	hasJobs, err := d.hasTable(ctx, "report_export_job")
	if err != nil {
		return nil, err
	}
	if !hasJobs {
		return result, nil
	}
	jobByID := map[string]*jobread.Job{}
	byConversation := &jobread.Input{}
	byConversation.SetConversationIDs(sortedMapKeys(graph.Nodes))
	rows, err := d.exportJobs(ctx, byConversation, userID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row != nil {
			jobByID[row.JobId] = row
		}
	}
	if len(reportRunIDs) > 0 {
		byReportRun := &jobread.Input{}
		byReportRun.SetReportRunIDs(reportRunIDs)
		rows, err = d.exportJobs(ctx, byReportRun, userID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row != nil {
				jobByID[row.JobId] = row
			}
		}
	}
	for _, id := range sortedMapKeys(jobByID) {
		row := jobByID[id]
		if !d.systemRetention && strings.TrimSpace(row.OwnerId) != userID {
			return nil, ErrPermissionDenied
		}
		switch strings.ToLower(strings.TrimSpace(row.Status)) {
		case "queued", "running":
			// System retention evaluates recency and run activity before export
			// activity so its existing eligibility-reason precedence is retained.
			if !d.systemRetention {
				return nil, ErrConversationActive
			}
		}
		result.JobIDs = append(result.JobIDs, id)
	}
	hasArtifacts, err := d.hasTable(ctx, "report_export_artifact")
	if err != nil {
		return nil, err
	}
	if !hasArtifacts || len(result.JobIDs) == 0 {
		return result, nil
	}
	artifactQuery := &artifactread.Input{}
	artifactQuery.SetJobIDs(result.JobIDs)
	artifacts, err := d.exportArtifacts(ctx, artifactQuery, userID)
	if err != nil {
		return nil, err
	}
	for _, row := range artifacts {
		if row == nil {
			continue
		}
		if !d.systemRetention && strings.TrimSpace(row.OwnerId) != userID {
			return nil, ErrPermissionDenied
		}
		result.ArtifactIDs = append(result.ArtifactIDs, row.ArtifactId)
	}
	result.ArtifactIDs = normalizeIDs(result.ArtifactIDs)
	return result, nil
}

func (d *Discoverer) exportJobs(ctx context.Context, input *jobread.Input, owner string) ([]*jobread.Job, error) {
	value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: exportJobReaderTarget, Input: input, Providers: reportProviders(owner)})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*jobread.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("export job reader returned %T", value)
	}
	return out.Data, nil
}

func (d *Discoverer) exportArtifacts(ctx context.Context, input *artifactread.Input, owner string) ([]*artifactread.Artifact, error) {
	value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: exportArtifactReaderTarget, Input: input, Providers: reportProviders(owner)})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*artifactread.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("export artifact reader returned %T", value)
	}
	return out.Data, nil
}
