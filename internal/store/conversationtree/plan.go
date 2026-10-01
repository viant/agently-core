package conversationtree

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	fileread "github.com/viant/agently-core/internal/datly/generatedfile/read"
	goalread "github.com/viant/agently-core/internal/datly/goal/read"
	investigationread "github.com/viant/agently-core/internal/datly/investigation/read"
	legacyread "github.com/viant/agently-core/internal/datly/legacyrun/read"
	msgread "github.com/viant/agently-core/internal/datly/message/read"
	modelread "github.com/viant/agently-core/internal/datly/modelcall/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	artifactread "github.com/viant/agently-core/internal/datly/reporting/artifact/read"
	auditread "github.com/viant/agently-core/internal/datly/reporting/audit/read"
	contextread "github.com/viant/agently-core/internal/datly/reporting/context/read"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
	reportread "github.com/viant/agently-core/internal/datly/reporting/run/read"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	schedread "github.com/viant/agently-core/internal/datly/schedule/read"
	toolread "github.com/viant/agently-core/internal/datly/toolcall/read"
	claimread "github.com/viant/agently-core/internal/datly/toolexecutionclaim/read"
	turnread "github.com/viant/agently-core/internal/datly/turn/read"
	queueread "github.com/viant/agently-core/internal/datly/turnqueue/read"
	agentrun "github.com/viant/agently-core/internal/store/agentrun"
	conversation "github.com/viant/agently-core/internal/store/conversation"
	queuestore "github.com/viant/agently-core/internal/store/turnqueue"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

// DeletePlan snapshots identities and retained references before any mutation.
// Its caller owns the transaction and locks; every row comes from the existing
// canonical reader. Extra identities are authorized by the parent operation.
type DeletePlan struct {
	Graph                                                        *Graph
	Tables                                                       map[string]bool
	ConversationIDs, TurnIDs, MessageIDs, RunIDs, ScheduleRunIDs []string
	ApprovalIDs, PayloadIDs, GoalIDs, ScheduleIDs                []string
	ReportRunIDs, ReportJobIDs, ReportArtifactIDs                []string
	ExtraRunIDs, ExtraScheduleRunIDs                             []string
	Turns                                                        []*turnread.TurnRowsView
	Messages                                                     []*msgread.MessageView
	Runs                                                         []*runread.RunRowsView
	LegacyRuns                                                   []*legacyread.LegacyRun
	Goals                                                        []*goalread.GoalView
	Schedules                                                    []*schedread.ScheduleView
	ModelCalls                                                   []*modelread.ModelCallView
	ToolCalls                                                    []*toolread.ToolCallView
	Queues                                                       []*queueread.QueueRowView
	GeneratedFiles                                               []*fileread.GeneratedFileView
	Investigations                                               []*investigationread.Investigation
	Claims                                                       []*claimread.Claim
	ReportContexts                                               []*contextread.Context
	ReportRuns                                                   []*reportread.Run
	ReportJobs                                                   []*jobread.Job
	ReportArtifacts                                              []*artifactread.Artifact
	ReportAuditEvents                                            []*auditread.AuditEvent
	// Include graph-internal matches too: self references and cycles must be
	// detached before per-identity generated deletes can remove their targets.
	DetachRuns     []*runread.RunRowsView
	DetachMessages []*msgread.MessageView
	DetachTurns    []*turnread.TurnRowsView
}

func (d *Discoverer) CollectDeletePlan(ctx context.Context, graph *Graph, now time.Time, extraRunIDs, extraScheduleRunIDs []string) (*DeletePlan, error) {
	if d == nil || d.Invoker == nil || d.OwnerID == nil {
		return nil, fmt.Errorf("conversation graph reader is not configured")
	}
	if err := d.authorize(ctx, graph); err != nil {
		return nil, err
	}
	plan := &DeletePlan{Tables: map[string]bool{}, Graph: graph, ConversationIDs: sortedMapKeys(graph.Nodes), ExtraRunIDs: normalizeIDs(extraRunIDs), ExtraScheduleRunIDs: normalizeIDs(extraScheduleRunIDs)}
	for _, table := range []string{"conversation", "turn", "message", "run", "schedule_run", "tool_approval_queue", "turn_queue", "model_call", "tool_call", "generated_file", "call_payload", "goal", "schedule", "investigation", "tool_execution_claim", "report_run", "conversation_report_context", "report_export_job", "report_export_artifact", "report_audit_event"} {
		present, err := d.hasTable(ctx, table)
		if err != nil {
			return nil, err
		}
		plan.Tables[table] = present
	}

	for _, node := range graph.Nodes {
		if node != nil && node.ScheduleRunID != "" {
			plan.ScheduleRunIDs = append(plan.ScheduleRunIDs, node.ScheduleRunID)
		}
	}
	plan.ScheduleRunIDs = normalizeIDs(plan.ScheduleRunIDs)
	var err error
	if len(plan.ConversationIDs) > 0 {
		if err = d.ValidateInboundLinks(ctx, graph); err != nil {
			return nil, err
		}
		if plan.Tables["schedule"] {
			plan.ScheduleIDs, err = d.ValidateScheduleReferences(ctx, graph, now)
			if err != nil {
				return nil, err
			}
		}
		plan.Goals, err = d.goalRows(ctx, plan.ConversationIDs)
		if err != nil {
			return nil, err
		}
		for _, row := range plan.Goals {
			if row != nil {
				plan.GoalIDs = append(plan.GoalIDs, row.Id)
			}
		}
		plan.GoalIDs = normalizeIDs(plan.GoalIDs)
		turnQuery := &turnread.TurnRowsInput{}
		turnQuery.SetConversationIDs(plan.ConversationIDs)
		plan.Turns, err = (&conversation.TurnStore{Invoker: d.Invoker}).ListRows(ctx, turnQuery, nil)
		if err != nil {
			return nil, err
		}
		for _, row := range plan.Turns {
			if row != nil {
				plan.TurnIDs = append(plan.TurnIDs, row.Id)
			}
		}
		plan.TurnIDs = normalizeIDs(plan.TurnIDs)
		messageQuery := &msgread.MessagesInput{}
		messageQuery.SetConversationIds(plan.ConversationIDs)
		plan.Messages, err = (&conversation.MessageStore{Invoker: d.Invoker, OwnerID: d.OwnerID}).ListRows(ctx, messageQuery, state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: conversation.BaseMessageFields()}}})
		if err != nil {
			return nil, err
		}
		for _, row := range plan.Messages {
			if row != nil {
				plan.MessageIDs = append(plan.MessageIDs, row.Id)
			}
		}
		plan.MessageIDs = normalizeIDs(plan.MessageIDs)
		plan.ApprovalIDs, err = d.CollectApprovalIDs(ctx, graph)
		if err != nil {
			return nil, err
		}
		plan.PayloadIDs, err = d.CollectPayloadIDs(ctx, graph)
		if err != nil {
			return nil, err
		}
		exports, err := d.ValidateExportReferences(ctx, graph)
		if err != nil {
			return nil, err
		}
		plan.ReportRunIDs = exports.ReportRunIDs
		plan.ReportJobIDs = exports.JobIDs
		plan.ReportArtifactIDs = exports.ArtifactIDs
		if err = d.collectPlanChildren(ctx, plan); err != nil {
			return nil, err
		}
	}
	if len(plan.ScheduleIDs) > 0 {
		query := &schedread.ScheduleInput{}
		query.SetIds(plan.ScheduleIDs)
		plan.Schedules, err = d.scheduleRows(ctx, query, d.OwnerID(ctx))
		if err != nil {
			return nil, err
		}
	}
	if err = d.RefreshDeletePlanRunEvidence(ctx, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// RefreshDeletePlanRunEvidence repeats graph run discovery after parent locks,
// retaining the earlier set and adding runs attached through goal-wakeup
// schedules. Its caller locks any new identities before validating liveness.
func (d *Discoverer) RefreshDeletePlanRunEvidence(ctx context.Context, plan *DeletePlan) error {
	if plan == nil || plan.Graph == nil {
		return fmt.Errorf("conversation deletion plan is required")
	}
	evidence, err := d.CollectRunEvidence(ctx, plan.Graph)
	if err != nil {
		return err
	}
	current := map[string]*runread.RunRowsView{}
	legacy := map[string]*legacyread.LegacyRun{}
	for _, row := range plan.Runs {
		if row != nil {
			current[row.Id] = row
		}
	}
	for _, row := range evidence.Current {
		if row != nil {
			current[row.Id] = row
		}
	}
	for _, row := range plan.LegacyRuns {
		if row != nil {
			legacy[row.Id] = row
		}
	}
	for _, row := range evidence.Legacy {
		if row != nil {
			legacy[row.Id] = row
		}
	}
	runStore := &agentrun.Store{Invoker: d.Invoker, OwnerID: d.OwnerID}
	queries := []*runread.RunRowsInput{}
	if len(plan.ScheduleIDs) > 0 {
		query := &runread.RunRowsInput{}
		query.SetScheduleIds(plan.ScheduleIDs)
		queries = append(queries, query)
	}
	explicit := append(append([]string{}, plan.ExtraRunIDs...), plan.RunIDs...)
	for _, row := range plan.Turns {
		if row != nil && row.RunId != nil {
			explicit = append(explicit, *row.RunId)
		}
	}
	if len(plan.TurnIDs) > 0 {
		for _, model := range []bool{true, false} {
			ids, err := d.callRunIDs(ctx, plan.TurnIDs, model)
			if err != nil {
				return err
			}
			explicit = append(explicit, ids...)
		}
	}
	requested := normalizeIDs(explicit)
	if len(requested) > 0 {
		query := &runread.RunRowsInput{}
		query.SetIds(requested)
		queries = append(queries, query)
	}
	for _, query := range queries {
		rows, err := runStore.ListTrusted(ctx, "rows", query, state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: RunEvidenceFields()}}})
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row != nil {
				current[row.Id] = row
			}
		}
	}
	plan.Runs = nil
	for _, id := range sortedMapKeys(current) {
		plan.Runs = append(plan.Runs, current[id])
		requested = append(requested, id)
	}
	plan.RunIDs = normalizeIDs(requested)
	present, err := d.hasTable(ctx, "schedule_run")
	if err != nil {
		return err
	}
	if present {
		legacyQueries := []*legacyread.Input{}
		if len(plan.ScheduleIDs) > 0 {
			query := &legacyread.Input{}
			query.SetScheduleIDs(plan.ScheduleIDs)
			legacyQueries = append(legacyQueries, query)
		}
		ids := normalizeIDs(append(append([]string{}, plan.ExtraScheduleRunIDs...), plan.ScheduleRunIDs...))
		if len(ids) > 0 {
			query := &legacyread.Input{}
			query.SetIDs(ids)
			legacyQueries = append(legacyQueries, query)
		}
		for _, query := range legacyQueries {
			rows, err := d.legacyRuns(ctx, query)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if row != nil {
					legacy[row.Id] = row
				}
			}
		}
		plan.LegacyRuns = nil
		for _, id := range sortedMapKeys(legacy) {
			plan.LegacyRuns = append(plan.LegacyRuns, legacy[id])
			ids = append(ids, id)
		}
		plan.ScheduleRunIDs = normalizeIDs(ids)
	} else {
		plan.ScheduleRunIDs = normalizeIDs(append(plan.ScheduleRunIDs, plan.ExtraScheduleRunIDs...))
	}
	return d.collectPlanDetachRows(ctx, plan)
}

func (d *Discoverer) collectPlanChildren(ctx context.Context, plan *DeletePlan) error {
	owner := strings.TrimSpace(d.OwnerID(ctx))
	var err error
	if len(plan.MessageIDs) > 0 {
		modelQuery := &modelread.ModelCallsInput{}
		modelQuery.SetMessageIds(plan.MessageIDs)
		plan.ModelCalls, err = planReaderRows[modelread.ModelCallView](ctx, d, modelQuery, "/v1/internal/agently/model-call", planProviders("modelcallaccess", owner))
		if err != nil {
			return err
		}
		toolQuery := &toolread.ToolCallsInput{}
		toolQuery.SetMessageIds(plan.MessageIDs)
		plan.ToolCalls, err = planReaderRows[toolread.ToolCallView](ctx, d, toolQuery, "/v1/internal/agently/tool-call", planProviders("toolcallaccess", owner))
		if err != nil {
			return err
		}
	}
	queueQuery := &queueread.QueueRowsInput{}
	queueQuery.SetConversationIds(plan.ConversationIDs)
	plan.Queues, err = (&queuestore.Store{Invoker: d.Invoker}).List(ctx, queueQuery)
	if err != nil {
		return err
	}
	fileQuery := &fileread.Input{}
	fileQuery.SetConversationIDs(plan.ConversationIDs)
	plan.GeneratedFiles, err = (&conversation.GeneratedFileStore{Invoker: d.Invoker}).List(ctx, fileQuery)
	if err != nil {
		return err
	}
	if present, err := d.hasTable(ctx, "investigation"); err != nil {
		return err
	} else if present {
		query := &investigationread.Input{}
		query.SetConversationIDs(plan.ConversationIDs)
		plan.Investigations, err = planReaderRows[investigationread.Investigation](ctx, d, query, "/v1/internal/agently/investigation", planProviders("investigationaccess", owner))
		if err != nil {
			return err
		}
	}
	if len(plan.TurnIDs) > 0 {
		if present, err := d.hasTable(ctx, "tool_execution_claim"); err != nil {
			return err
		} else if present {
			query := &claimread.Input{}
			query.SetTurnIDs(plan.TurnIDs)
			plan.Claims, err = planReaderRows[claimread.Claim](ctx, d, query, "/v1/internal/agently/tool-execution-claim", planProviders("claimaccess", owner))
			if err != nil {
				return err
			}
		}
	}
	if present, err := d.hasTable(ctx, "conversation_report_context"); err != nil {
		return err
	} else if present {
		query := &contextread.Input{}
		query.SetConversationIDs(plan.ConversationIDs)
		plan.ReportContexts, err = d.reportContexts(ctx, query, owner)
		if err != nil {
			return err
		}
	}
	if len(plan.ReportJobIDs) > 0 {
		query := &jobread.Input{}
		query.SetJobIDs(plan.ReportJobIDs)
		plan.ReportJobs, err = d.exportJobs(ctx, query, owner)
		if err != nil {
			return err
		}
	}
	if len(plan.ReportArtifactIDs) > 0 {
		query := &artifactread.Input{}
		query.SetJobIDs(plan.ReportJobIDs)
		plan.ReportArtifacts, err = d.exportArtifacts(ctx, query, owner)

		if err != nil {
			return err
		}
		for _, row := range plan.ReportArtifacts {
			if row != nil {
				if !d.systemRetention && strings.TrimSpace(row.OwnerId) != owner {
					return ErrPermissionDenied
				}
				plan.ReportArtifactIDs = append(plan.ReportArtifactIDs, row.ArtifactId)
			}
		}
		plan.ReportArtifactIDs = normalizeIDs(plan.ReportArtifactIDs)
	}
	if len(plan.ReportRunIDs) > 0 && plan.Tables["report_run"] {
		query := &reportread.Input{}
		query.SetReportRunIDs(plan.ReportRunIDs)
		plan.ReportRuns, err = d.reportRuns(ctx, query, owner)
		if err != nil {
			return err
		}
	}
	if present, err := d.hasTable(ctx, "report_audit_event"); err != nil {
		return err
	} else if present {
		rows := map[string]*auditread.AuditEvent{}
		queries := []*auditread.Input{}
		if len(plan.ReportJobIDs) > 0 {
			query := &auditread.Input{}
			query.SetJobIDs(plan.ReportJobIDs)
			queries = append(queries, query)
		}
		if len(plan.ReportArtifactIDs) > 0 {
			query := &auditread.Input{}
			query.SetArtifactIDs(plan.ReportArtifactIDs)
			queries = append(queries, query)
		}
		for _, query := range queries {
			found, err := planReaderRows[auditread.AuditEvent](ctx, d, query, "/v1/internal/forge/reporting/audit", planProviders("reportauditaccess", owner))
			if err != nil {
				return err
			}
			for _, row := range found {
				if row != nil {
					rows[row.EventId] = row
				}
			}
		}
		for _, id := range sortedMapKeys(rows) {
			plan.ReportAuditEvents = append(plan.ReportAuditEvents, rows[id])
		}
	}
	return nil
}

func (d *Discoverer) collectPlanDetachRows(ctx context.Context, plan *DeletePlan) error {
	plan.DetachRuns = nil
	plan.DetachMessages = nil
	plan.DetachTurns = nil
	if len(plan.RunIDs) > 0 {
		query := &runread.RunRowsInput{}
		query.SetResumedFromRunIds(plan.RunIDs)
		rows, err := (&agentrun.Store{Invoker: d.Invoker, OwnerID: d.OwnerID}).ListTrusted(ctx, "rows", query, state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: RunEvidenceFields()}}})
		if err != nil {
			return err
		}
		plan.DetachRuns = rows
	}
	messages := map[string]*msgread.MessageView{}
	if len(plan.MessageIDs) > 0 {
		queries := []*msgread.MessagesInput{}
		byParent := &msgread.MessagesInput{}
		byParent.SetParentMessageIds(plan.MessageIDs)
		queries = append(queries, byParent)
		bySuperseded := &msgread.MessagesInput{}
		bySuperseded.SetSupersededByIds(plan.MessageIDs)
		queries = append(queries, bySuperseded)
		for _, query := range queries {
			rows, err := planReaderRows[msgread.MessageView](ctx, d, query, "/v1/internal/agently/message", d.planDetachProviders("messageaccess", conversation.BaseMessageFields()), d.LockDetachRows)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if row != nil {
					messages[row.Id] = row
				}
			}
		}
	}
	for _, id := range sortedMapKeys(messages) {
		plan.DetachMessages = append(plan.DetachMessages, messages[id])
	}
	turns := map[string]*turnread.TurnRowsView{}
	turnQueries := []*turnread.TurnRowsInput{}
	if len(plan.MessageIDs) > 0 {
		query := &turnread.TurnRowsInput{}
		query.SetStartedByMessageIDs(plan.MessageIDs)
		turnQueries = append(turnQueries, query)
	}
	if len(plan.TurnIDs) > 0 {
		query := &turnread.TurnRowsInput{}
		query.SetRetryOfIDs(plan.TurnIDs)
		turnQueries = append(turnQueries, query)
	}
	for _, query := range turnQueries {
		rows, err := planReaderRows[turnread.TurnRowsView](ctx, d, query, "/v1/api/agently/turn/list/list", d.planDetachProviders("turnaccess", nil), d.LockDetachRows)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row != nil {
				turns[row.Id] = row
			}
		}
	}
	for _, id := range sortedMapKeys(turns) {
		plan.DetachTurns = append(plan.DetachTurns, turns[id])
	}
	return nil
}

func planProviders(kind, owner string) []locator.Provider {
	return []locator.Provider{provider.Named(kind, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "internal":
			return true, true, nil
		case "mode":
			return "rows", true, nil
		}
		return nil, false, nil
	}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil })}
}
func planReaderRows[T any](ctx context.Context, d *Discoverer, input any, path string, providers []locator.Provider, lock ...bool) ([]*T, error) {
	target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeOf(input).Elem().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: path}}
	value, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, len(lock) > 0 && lock[0]), Target: target, Input: input, Providers: providers})
	if err != nil {
		return nil, err
	}
	output := reflect.ValueOf(value)
	if output.Kind() != reflect.Pointer || output.IsNil() {
		return nil, fmt.Errorf("deletion plan reader returned %T", value)
	}
	data := output.Elem().FieldByName("Data")
	if !data.IsValid() {
		return nil, fmt.Errorf("deletion plan reader %T has no rows", value)
	}
	rows, ok := data.Interface().([]*T)
	if !ok {
		return nil, fmt.Errorf("deletion plan reader %T has incompatible rows", value)
	}
	return rows, nil
}

func (d *Discoverer) planDetachProviders(kind string, fields []string) []locator.Provider {

	// Owner is rebound per invocation by the caller context. Row locking is
	// supplied separately through trusted invocation options.
	providers := []locator.Provider{provider.Named(kind, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "internal":
			return true, true, nil
		case "mode":
			return "rows", true, nil
		}
		return nil, false, nil
	}), provider.Named("visibility", func(ctx context.Context, _ reflect.Type, _ string) (any, bool, error) {
		owner := d.OwnerID(ctx)
		return &owner, true, nil
	})}
	if len(fields) > 0 {
		providers = append(providers, queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: fields}}}))
	}
	return providers
}
