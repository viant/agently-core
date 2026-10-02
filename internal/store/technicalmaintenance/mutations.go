package technicalmaintenance

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	artifactread "github.com/viant/agently-core/internal/datly/reporting/artifact/read"
	artifactwrite "github.com/viant/agently-core/internal/datly/reporting/artifact/write"
	auditread "github.com/viant/agently-core/internal/datly/reporting/audit/read"
	auditwrite "github.com/viant/agently-core/internal/datly/reporting/audit/write"
	contextread "github.com/viant/agently-core/internal/datly/reporting/context/read"
	contextwrite "github.com/viant/agently-core/internal/datly/reporting/context/write"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
	jobwrite "github.com/viant/agently-core/internal/datly/reporting/job/write"
	runread "github.com/viant/agently-core/internal/datly/reporting/run/read"
	runwrite "github.com/viant/agently-core/internal/datly/reporting/run/write"
	sessionread "github.com/viant/agently-core/internal/datly/session/read"
	sessionwrite "github.com/viant/agently-core/internal/datly/session/write"
	"github.com/viant/agently-core/internal/store/maintenancebatch"

	datlypredicate "github.com/viant/agently-core/internal/datly/predicate"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

func (s *Store) lockRecord(ctx context.Context, kind, id string) (bool, error) {
	switch kind {
	case ReportRun:
		input := &runread.Input{}
		input.SetReportRunID(id)
		out, err := retentionRead[runread.Output](ctx, s, input, "/v1/internal/forge/reporting/run", providers("reportaccess", nil), []string{"report_run_id"}, "", 0, true)
		return out != nil && len(out.Data) > 0, err
	case ExportJob:
		input := &jobread.Input{}
		input.SetJobID(id)
		out, err := retentionRead[jobread.Output](ctx, s, input, "/v1/internal/forge/reporting/job", providers("reportaccess", nil), []string{"job_id"}, "", 0, true)
		return out != nil && len(out.Data) > 0, err
	case Audit:
		input := &auditread.Input{}
		input.SetEventID(id)
		out, err := retentionRead[auditread.Output](ctx, s, input, "/v1/internal/forge/reporting/audit", providers("reportauditaccess", nil), []string{"event_id"}, "", 0, true)
		return out != nil && len(out.Data) > 0, err
	case Session:
		input := &sessionread.SessionInput{}
		input.SetId(id)
		out, err := retentionRead[sessionread.SessionOutput](ctx, s, input, "/v1/api/agently/user/session", providers("sessionmaintenance", nil), []string{"id"}, "", 0, true)
		return out != nil && len(out.Data) > 0, err
	default:
		return false, fmt.Errorf("%w: unsupported technical maintenance kind", ErrInvalidRequest)
	}
}
func (s *Store) deleteRecord(ctx context.Context, request Request) (int64, error) {
	policy := &datlypredicate.TechnicalRetention{Scope: request.Scope, OlderThan: request.OlderThan, EvaluatedAt: request.EvaluatedAt}
	switch request.Kind {
	case ReportRun:
		query := &runread.Input{}
		query.SetReportRunID(request.RecordID)
		runs, err := retentionRead[runread.Output](ctx, s, query, "/v1/internal/forge/reporting/run", providers("reportaccess", nil), []string{"report_run_id", "owner_id", "revision"}, "", 0, true)
		if err != nil {
			return 0, err
		}
		jobsQuery := &jobread.Input{}
		jobsQuery.SetReportRunID(request.RecordID)
		jobs, err := retentionRead[jobread.Output](ctx, s, jobsQuery, "/v1/internal/forge/reporting/job", providers("reportaccess", nil), []string{"job_id", "owner_id"}, "", 0, true)
		if err != nil {
			return 0, err
		}
		deleted, err := s.deleteJobs(ctx, jobs.Data, policy)
		if err != nil {
			return 0, err
		}
		contextsQuery := &contextread.Input{}
		contextsQuery.SetActiveReportRunID(request.RecordID)
		contexts, err := retentionRead[contextread.Output](ctx, s, contextsQuery, "/v1/internal/forge/reporting/conversation-context", providers("reportaccess", nil), []string{"owner_id", "conversation_id", "revision"}, "", 0, true)
		if err != nil {
			return 0, err
		}
		for _, snapshot := range contexts.Data {
			if snapshot == nil {
				return 0, fmt.Errorf("nil technical report context")
			}
			row := &contextwrite.Context{}
			row.SetOwnerId(snapshot.OwnerId)
			row.SetConversationId(snapshot.ConversationId)
			row.SetRevision(snapshot.Revision)
			row.SetShouldDelete(true)
			input := &contextwrite.Input{}
			input.SetContexts([]*contextwrite.Context{row})
			if err := retentionWrite[contextwrite.Output](ctx, s, input, "/v1/internal/forge/reporting/conversation-context", ownerProviders(snapshot.OwnerId)...); err != nil {
				return 0, err
			}
			deleted++
		}
		for _, snapshot := range runs.Data {
			if snapshot == nil {
				return 0, fmt.Errorf("nil technical report run")
			}
			row := &runwrite.Run{}
			row.SetReportRunId(snapshot.ReportRunId)
			row.SetOwnerId(snapshot.OwnerId)
			row.SetRevision(snapshot.Revision)
			row.SetShouldDelete(true)
			input := &runwrite.Input{}
			input.SetMode("delete")
			input.SetRuns([]*runwrite.Run{row})
			if err := retentionWrite[runwrite.Output](ctx, s, input, "/v1/internal/forge/reporting/run", ownerProviders(snapshot.OwnerId)...); err != nil {
				return 0, err
			}
			deleted++
		}
		return deleted, nil
	case ExportJob:
		query := &jobread.Input{}
		query.SetJobID(request.RecordID)
		jobs, err := retentionRead[jobread.Output](ctx, s, query, "/v1/internal/forge/reporting/job", providers("reportaccess", nil), []string{"job_id", "owner_id"}, "", 0, true)
		if err != nil {
			return 0, err
		}
		return s.deleteJobs(ctx, jobs.Data, policy)
	case Audit:
		row := &auditwrite.AuditEvent{}
		row.SetEventId(request.RecordID)
		row.SetShouldDelete(true)
		input := &auditwrite.Input{}
		input.SetEvents([]*auditwrite.AuditEvent{row})
		if err := retentionWrite[auditwrite.Output](ctx, s, input, "/v1/internal/forge/reporting/audit", providers("reportauditaccess", policy)...); err != nil {
			return 0, err
		}
		return 1, nil
	case Session:
		row := &sessionwrite.Session{}
		row.SetId(request.RecordID)
		row.SetShouldDelete(true)
		input := &sessionwrite.Input{}
		input.SetSession([]*sessionwrite.Session{row})
		if err := retentionWrite[sessionwrite.Output](ctx, s, input, "/v1/api/agently/user/session"); err != nil {
			return 0, err
		}
		return 1, nil
	default:
		return 0, fmt.Errorf("%w: unsupported technical maintenance kind", ErrInvalidRequest)
	}
}
func (s *Store) deleteJobs(ctx context.Context, jobs []*jobread.Job, policy *datlypredicate.TechnicalRetention) (int64, error) {
	if len(jobs) == 0 {
		return 0, nil
	}
	ids := []string{}
	for _, job := range jobs {
		if job == nil {
			return 0, fmt.Errorf("nil technical export job")
		}
		ids = append(ids, job.JobId)
	}
	artifactQuery := &artifactread.Input{}
	artifactQuery.SetJobIDs(ids)
	artifacts, err := retentionRead[artifactread.Output](ctx, s, artifactQuery, "/v1/internal/forge/reporting/artifact", providers("reportaccess", nil), []string{"artifact_id", "job_id", "owner_id"}, "", 0, true)
	if err != nil {
		return 0, err
	}
	auditByID := map[string]*auditread.AuditEvent{}
	auditQuery := &auditread.Input{}
	auditQuery.SetJobIDs(ids)
	audits, err := retentionRead[auditread.Output](ctx, s, auditQuery, "/v1/internal/forge/reporting/audit", providers("reportauditaccess", nil), []string{"event_id"}, "", 0, true)
	if err != nil {
		return 0, err
	}
	for _, row := range audits.Data {
		if row != nil {
			auditByID[row.EventId] = row
		}
	}
	artifactIDs := []string{}
	for _, artifact := range artifacts.Data {
		if artifact == nil {
			return 0, fmt.Errorf("nil technical export artifact")
		}
		artifactIDs = append(artifactIDs, artifact.ArtifactId)
	}
	if len(artifactIDs) > 0 {
		query := &auditread.Input{}
		query.SetArtifactIDs(artifactIDs)
		rows, err := retentionRead[auditread.Output](ctx, s, query, "/v1/internal/forge/reporting/audit", providers("reportauditaccess", nil), []string{"event_id"}, "", 0, true)
		if err != nil {
			return 0, err
		}
		for _, row := range rows.Data {
			if row != nil {
				auditByID[row.EventId] = row
			}
		}
	}
	auditIDs := []string{}
	for id := range auditByID {
		auditIDs = append(auditIDs, id)
	}
	sort.Strings(auditIDs)
	var deleted int64
	err = maintenancebatch.Groups(ctx, auditIDs, func(id string) (bool, error) { return true, nil }, func(_ bool, ids []string) error {
		rows := make([]*auditwrite.AuditEvent, 0, len(ids))
		for _, id := range ids {
			row := &auditwrite.AuditEvent{}
			row.SetEventId(id)
			row.SetShouldDelete(true)
			rows = append(rows, row)
		}
		input := &auditwrite.Input{}
		input.SetEvents(rows)
		if err := retentionWrite[auditwrite.Output](ctx, s, input, "/v1/internal/forge/reporting/audit", providers("reportauditaccess", policy)...); err != nil {
			return err
		}
		deleted += int64(len(rows))
		return nil
	})
	if err != nil {
		return 0, err
	}
	type artifactGuard struct{ OwnerID, JobID string }
	err = maintenancebatch.Groups(ctx, artifacts.Data, func(snapshot *artifactread.Artifact) (artifactGuard, error) {
		if snapshot == nil {
			return artifactGuard{}, fmt.Errorf("nil technical export artifact")
		}
		return artifactGuard{snapshot.OwnerId, snapshot.JobId}, nil
	}, func(guard artifactGuard, snapshots []*artifactread.Artifact) error {
		rows := make([]*artifactwrite.Artifact, 0, len(snapshots))
		for _, snapshot := range snapshots {
			row := &artifactwrite.Artifact{}
			row.SetArtifactId(snapshot.ArtifactId)
			row.SetOwnerId(snapshot.OwnerId)
			row.SetShouldDelete(true)
			rows = append(rows, row)
		}
		input := &artifactwrite.Input{}
		input.SetMode("delete")
		input.SetExpectedJobID(guard.JobID)
		input.SetArtifacts(rows)
		if err := retentionWrite[artifactwrite.Output](ctx, s, input, "/v1/internal/forge/reporting/artifact", ownerProviders(guard.OwnerID)...); err != nil {
			return err
		}
		deleted += int64(len(rows))
		return nil
	})
	if err != nil {
		return 0, err
	}
	err = maintenancebatch.Groups(ctx, jobs, func(snapshot *jobread.Job) (string, error) {
		if snapshot == nil {
			return "", fmt.Errorf("nil technical export job")
		}
		return snapshot.OwnerId, nil
	}, func(owner string, snapshots []*jobread.Job) error {
		rows := make([]*jobwrite.Job, 0, len(snapshots))
		for _, snapshot := range snapshots {
			row := &jobwrite.Job{}
			row.SetJobId(snapshot.JobId)
			row.SetOwnerId(snapshot.OwnerId)
			row.SetShouldDelete(true)
			rows = append(rows, row)
		}
		input := &jobwrite.Input{}
		input.SetMode("delete")
		input.SetJobs(rows)
		if err := retentionWrite[jobwrite.Output](ctx, s, input, "/v1/internal/forge/reporting/job", ownerProviders(owner)...); err != nil {
			return err
		}
		deleted += int64(len(rows))
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}
func ownerProviders(owner string) []locator.Provider {
	return []locator.Provider{provider.Named("reportaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "internal" {
			return true, true, nil
		}
		return nil, false, nil
	}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil })}
}
func retentionWrite[Output any](ctx context.Context, s *Store, input any, path string, bindings ...locator.Provider) error {
	target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeOf(input).Elem().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: path}}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: target, Input: input, Providers: bindings})
	if err != nil {
		return err
	}
	out, ok := value.(*Output)
	if !ok || out == nil {
		return fmt.Errorf("technical writer %s returned %T", path, value)
	}
	return nil
}
