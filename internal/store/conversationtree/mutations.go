package conversationtree

import (
	"context"
	"fmt"
	filewrite "github.com/viant/agently-core/internal/datly/generatedfile/write"
	goalwrite "github.com/viant/agently-core/internal/datly/goal/write"
	investigationwrite "github.com/viant/agently-core/internal/datly/investigation/write"
	legacywrite "github.com/viant/agently-core/internal/datly/legacyrun/write"
	messagewrite "github.com/viant/agently-core/internal/datly/message/write"
	modelwrite "github.com/viant/agently-core/internal/datly/modelcall/write"
	artifactwrite "github.com/viant/agently-core/internal/datly/reporting/artifact/write"
	auditwrite "github.com/viant/agently-core/internal/datly/reporting/audit/write"
	contextwrite "github.com/viant/agently-core/internal/datly/reporting/context/write"
	jobwrite "github.com/viant/agently-core/internal/datly/reporting/job/write"
	reportwrite "github.com/viant/agently-core/internal/datly/reporting/run/write"
	runwrite "github.com/viant/agently-core/internal/datly/run/write"
	schedulewrite "github.com/viant/agently-core/internal/datly/schedule/write"
	toolwrite "github.com/viant/agently-core/internal/datly/toolcall/write"
	claimwrite "github.com/viant/agently-core/internal/datly/toolexecutionclaim/write"
	turnwrite "github.com/viant/agently-core/internal/datly/turn/write"
	queuewrite "github.com/viant/agently-core/internal/datly/turnqueue/write"
	"github.com/viant/agently-core/internal/store/agentrun"
	conversation "github.com/viant/agently-core/internal/store/conversation"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"reflect"
	"sort"
	"strings"
)

// InvestigationPolicy selects the explicit legacy relationship policy.
type InvestigationPolicy uint8

const (
	InvestigationDelete InvestigationPolicy = iota
	InvestigationRetainAndDetach
)

// Mutator applies a validated, locked plan inside its parent's managed
// transaction. It invokes canonical generated writers and never starts or
// completes transactions. The parent owns authorization and final outcome.
type Mutator struct {
	Invoker dexec.ComponentInvoker
	OwnerID func(context.Context) string
}

func (m *Mutator) Apply(ctx context.Context, plan *DeletePlan, policy InvestigationPolicy) error {
	if m == nil || m.Invoker == nil || m.OwnerID == nil {
		return fmt.Errorf("conversation deletion mutation phase is not configured")
	}
	if plan == nil || plan.Graph == nil || plan.Tables == nil {
		return fmt.Errorf("conversation deletion plan is unavailable")
	}
	if policy != InvestigationDelete && policy != InvestigationRetainAndDetach {
		return fmt.Errorf("unsupported investigation deletion policy")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, table := range []string{"investigation", "report_audit_event", "report_export_artifact", "report_export_job", "conversation_report_context", "report_run", "schedule_run", "tool_approval_queue", "tool_execution_claim", "run", "turn_queue", "model_call", "tool_call", "generated_file", "message", "turn", "schedule", "goal", "conversation", "call_payload"} {
		if _, ok := plan.Tables[table]; !ok {
			return fmt.Errorf("deletion plan has no schema evidence for %s", table)
		}
	}
	if err := m.deleteAGUI(ctx, plan); err != nil {
		return err
	}
	if err := m.investigations(ctx, plan, policy); err != nil {
		return err
	}
	if err := m.reporting(ctx, plan); err != nil {
		return err
	}
	if plan.Tables["schedule_run"] {
		for _, snapshot := range plan.LegacyRuns {
			if snapshot == nil {
				return fmt.Errorf("deletion plan has a nil legacy run")
			}
			row := &legacywrite.LegacyRun{}
			row.SetId(snapshot.Id)
			row.SetShouldDelete(true)
			input := &legacywrite.Input{}
			input.SetExpectedScheduleID(snapshot.ScheduleId)
			input.SetRuns([]*legacywrite.LegacyRun{row})
			if err := treeInvoke[legacywrite.Output](ctx, m, input, "/v1/internal/agently/scheduler/legacy-run", treeProviders("schedulerunaccess", m.OwnerID(ctx))...); err != nil {
				return err
			}
		}
	}
	if plan.Tables["tool_approval_queue"] {
		if err := (&conversation.ApprovalStore{Invoker: m.Invoker, OwnerID: m.OwnerID}).DeleteTrusted(ctx, plan.ApprovalIDs...); err != nil {
			return fmt.Errorf("delete approvals: %w", err)
		}
	}
	if plan.Tables["tool_execution_claim"] {
		for _, snapshot := range plan.Claims {
			if snapshot == nil {
				return fmt.Errorf("deletion plan has a nil tool claim")
			}
			row := &claimwrite.Claim{}
			row.SetClaimKey(snapshot.ClaimKey)
			row.SetShouldDelete(true)
			input := &claimwrite.Input{}
			input.SetExpectedTurnID(snapshot.TurnId)
			input.SetClaims([]*claimwrite.Claim{row})
			if err := treeInvoke[claimwrite.Output](ctx, m, input, "/v1/internal/agently/tool-execution-claim", treeProviders("claimaccess", m.OwnerID(ctx))...); err != nil {
				return err
			}
		}
	}
	if err := m.detachRuns(ctx, plan); err != nil {
		return err
	}
	if plan.Tables["run"] {
		if err := (&agentrun.Store{Invoker: m.Invoker, OwnerID: m.OwnerID}).DeleteTrusted(ctx, plan.RunIDs...); err != nil {
			return fmt.Errorf("delete execution runs: %w", err)
		}
	}

	queueIDs := []string{}
	for _, row := range plan.Queues {
		if row == nil {
			return fmt.Errorf("deletion plan has a nil turn queue")
		}
		queueIDs = append(queueIDs, row.Id)
	}
	if plan.Tables["turn_queue"] {
		if err := treeDeleteIDs[queuewrite.TurnQueue, queuewrite.Output](ctx, m, queueIDs, "/v1/api/agently/turnqueue", func(id string) *queuewrite.TurnQueue {
			row := &queuewrite.TurnQueue{}
			row.SetId(id)
			row.SetShouldDelete(true)
			return row
		}, func(rows []*queuewrite.TurnQueue) any {
			input := &queuewrite.Input{}
			input.SetQueues(rows)
			return input
		}); err != nil {
			return err
		}
	}
	if plan.Tables["model_call"] {
		rows := make([]*modelwrite.ModelCall, 0, len(plan.ModelCalls))
		for _, snapshot := range plan.ModelCalls {
			if snapshot == nil {
				return fmt.Errorf("deletion plan has a nil model call")
			}
			row := &modelwrite.ModelCall{}
			row.SetMessageId(snapshot.MessageId)
			row.SetShouldDelete(true)
			rows = append(rows, row)
		}
		if len(rows) > 0 {
			input := &modelwrite.Input{}
			input.SetModelCalls(rows)
			if err := treeInvoke[modelwrite.Output](ctx, m, input, "/v1/api/agently/modelcall"); err != nil {
				return err
			}
		}
	}
	if plan.Tables["tool_call"] {
		rows := make([]*toolwrite.ToolCall, 0, len(plan.ToolCalls))
		for _, snapshot := range plan.ToolCalls {
			if snapshot == nil {
				return fmt.Errorf("deletion plan has a nil tool call")
			}
			row := &toolwrite.ToolCall{}
			row.SetMessageId(snapshot.MessageId)
			row.SetOpId(snapshot.OpId)
			row.SetShouldDelete(true)
			rows = append(rows, row)
		}
		if len(rows) > 0 {
			input := &toolwrite.Input{}
			input.SetToolCalls(rows)
			if err := treeInvoke[toolwrite.Output](ctx, m, input, "/v1/api/agently/toolcall"); err != nil {
				return err
			}
		}
	}
	fileIDs := []string{}
	for _, row := range plan.GeneratedFiles {
		if row == nil {
			return fmt.Errorf("deletion plan has a nil generated file")
		}
		fileIDs = append(fileIDs, row.Id)
	}
	if plan.Tables["generated_file"] {
		if err := treeDeleteIDs[filewrite.GeneratedFile, filewrite.Output](ctx, m, fileIDs, "/v1/api/agently/generated-file", func(id string) *filewrite.GeneratedFile {
			row := &filewrite.GeneratedFile{}
			row.SetId(id)
			row.SetShouldDelete(true)
			return row
		}, func(rows []*filewrite.GeneratedFile) any {
			input := &filewrite.Input{}
			input.SetGeneratedFiles(rows)
			return input
		}); err != nil {
			return err
		}
	}
	if err := m.detachMessagesAndTurns(ctx, plan); err != nil {
		return err
	}
	if plan.Tables["message"] {
		if err := treeDeleteIDs[messagewrite.Message, messagewrite.Output](ctx, m, plan.MessageIDs, "/v1/api/agently/message", func(id string) *messagewrite.Message {
			row := &messagewrite.Message{}
			row.SetId(id)
			row.SetShouldDelete(true)
			return row
		}, func(rows []*messagewrite.Message) any {
			input := &messagewrite.Input{}
			input.SetMessages(rows)
			return input
		}); err != nil {
			return err
		}
	}
	if plan.Tables["turn"] {
		if err := treeDeleteIDs[turnwrite.Turn, turnwrite.Output](ctx, m, plan.TurnIDs, "/v1/api/agently/turn", func(id string) *turnwrite.Turn {
			row := &turnwrite.Turn{}
			row.SetId(id)
			row.SetShouldDelete(true)
			return row
		}, func(rows []*turnwrite.Turn) any { input := &turnwrite.Input{}; input.SetTurns(rows); return input }); err != nil {
			return err
		}
	}
	if plan.Tables["schedule"] {
		if err := treeDeleteIDs[schedulewrite.Schedule, schedulewrite.Output](ctx, m, plan.ScheduleIDs, "/v1/api/agently/scheduler/", func(id string) *schedulewrite.Schedule {
			row := &schedulewrite.Schedule{}
			row.SetId(id)
			row.SetShouldDelete(true)
			return row
		}, func(rows []*schedulewrite.Schedule) any {
			input := &schedulewrite.Input{}
			input.SetSchedules(rows)
			return input
		}, treeProviders("visibility", m.OwnerID(ctx))...); err != nil {
			return err
		}
	}
	if plan.Tables["goal"] {
		if err := treeDeleteIDs[goalwrite.Goal, goalwrite.Output](ctx, m, plan.GoalIDs, "/v1/api/agently/goal", func(id string) *goalwrite.Goal {
			row := &goalwrite.Goal{}
			row.SetId(id)
			row.SetShouldDelete(true)
			return row
		}, func(rows []*goalwrite.Goal) any { input := &goalwrite.Input{}; input.SetGoals(rows); return input }); err != nil {
			return err
		}
	}
	if plan.Tables["conversation"] {
		ordered, err := ConversationIDsByDepthDesc(plan.Graph, plan.ConversationIDs)
		if err != nil {
			return err
		}
		for _, ids := range ordered {
			if err := (&conversation.Store{Invoker: m.Invoker, OwnerID: m.OwnerID}).DeleteTrusted(ctx, ids...); err != nil {
				return fmt.Errorf("delete conversation depth group: %w", err)
			}
		}
	}
	if plan.Tables["call_payload"] {
		if err := (&conversation.PayloadStore{Invoker: m.Invoker}).DeleteUnreferencedTrusted(ctx, plan.PayloadIDs...); err != nil {
			return fmt.Errorf("delete unreferenced payloads: %w", err)
		}
	}
	return nil
}

func (m *Mutator) investigations(ctx context.Context, plan *DeletePlan, policy InvestigationPolicy) error {
	if !plan.Tables["investigation"] {
		return nil
	}
	for _, snapshot := range plan.Investigations {
		if snapshot == nil || snapshot.ConversationId == nil || strings.TrimSpace(*snapshot.ConversationId) == "" {
			return fmt.Errorf("deletion investigation has no conversation evidence")
		}
		row := &investigationwrite.Investigation{}
		row.SetId(snapshot.Id)
		if policy == InvestigationDelete {
			row.SetShouldDelete(true)
		} else {
			row.SetConversationId(nil)
		}
		input := &investigationwrite.Input{}
		input.SetExpectedConversationID(*snapshot.ConversationId)
		input.SetInvestigations([]*investigationwrite.Investigation{row})
		if err := treeInvoke[investigationwrite.Output](ctx, m, input, "/v1/internal/agently/investigation", treeProviders("investigationaccess", m.OwnerID(ctx))...); err != nil {
			return err
		}
	}
	return nil
}

func (m *Mutator) reporting(ctx context.Context, plan *DeletePlan) error {
	jobs, artifacts := treeIDSet(plan.ReportJobIDs), treeIDSet(plan.ReportArtifactIDs)
	if plan.Tables["report_audit_event"] {
		for _, snapshot := range plan.ReportAuditEvents {
			if snapshot == nil {
				return fmt.Errorf("deletion plan has a nil report audit event")
			}
			row := &auditwrite.AuditEvent{}
			row.SetEventId(snapshot.EventId)
			row.SetShouldDelete(true)
			input := &auditwrite.Input{}
			input.SetEvents([]*auditwrite.AuditEvent{row})
			if snapshot.JobId != nil && jobs[*snapshot.JobId] {
				input.SetExpectedJobID(*snapshot.JobId)
			} else if snapshot.ArtifactId != nil && artifacts[*snapshot.ArtifactId] {
				input.SetExpectedArtifactID(*snapshot.ArtifactId)
			} else {
				return fmt.Errorf("report audit event %s has no deletion link", snapshot.EventId)
			}
			if err := treeInvoke[auditwrite.Output](ctx, m, input, "/v1/internal/forge/reporting/audit", treeProviders("reportauditaccess", m.OwnerID(ctx))...); err != nil {
				return err
			}
		}
	}
	if plan.Tables["report_export_artifact"] {
		for _, snapshot := range plan.ReportArtifacts {
			if snapshot == nil {
				return fmt.Errorf("deletion plan has a nil export artifact")
			}
			row := &artifactwrite.Artifact{}
			row.SetArtifactId(snapshot.ArtifactId)
			row.SetOwnerId(snapshot.OwnerId)
			row.SetShouldDelete(true)
			input := &artifactwrite.Input{}
			input.SetMode("delete")
			input.SetExpectedJobID(snapshot.JobId)
			input.SetArtifacts([]*artifactwrite.Artifact{row})
			if err := treeInvoke[artifactwrite.Output](ctx, m, input, "/v1/internal/forge/reporting/artifact", treeProviders("reportaccess", snapshot.OwnerId)...); err != nil {
				return err
			}
		}
	}
	if plan.Tables["report_export_job"] {
		for _, snapshot := range plan.ReportJobs {
			if snapshot == nil {
				return fmt.Errorf("deletion plan has a nil export job")
			}
			row := &jobwrite.Job{}
			row.SetJobId(snapshot.JobId)
			row.SetOwnerId(snapshot.OwnerId)
			row.SetShouldDelete(true)
			input := &jobwrite.Input{}
			input.SetMode("delete")
			input.SetJobs([]*jobwrite.Job{row})
			if err := treeInvoke[jobwrite.Output](ctx, m, input, "/v1/internal/forge/reporting/job", treeProviders("reportaccess", snapshot.OwnerId)...); err != nil {
				return err
			}
		}
	}
	if plan.Tables["conversation_report_context"] {
		for _, snapshot := range plan.ReportContexts {
			if snapshot == nil {
				return fmt.Errorf("deletion plan has a nil report context")
			}
			row := &contextwrite.Context{}
			row.SetOwnerId(snapshot.OwnerId)
			row.SetConversationId(snapshot.ConversationId)
			row.SetRevision(snapshot.Revision)
			row.SetShouldDelete(true)
			input := &contextwrite.Input{}
			input.SetContexts([]*contextwrite.Context{row})
			if err := treeInvoke[contextwrite.Output](ctx, m, input, "/v1/internal/forge/reporting/conversation-context", treeProviders("reportaccess", snapshot.OwnerId)...); err != nil {
				return err
			}
		}
	}
	if plan.Tables["report_run"] {
		for _, snapshot := range plan.ReportRuns {
			if snapshot == nil {
				return fmt.Errorf("deletion plan has a nil report run")
			}
			row := &reportwrite.Run{}
			row.SetReportRunId(snapshot.ReportRunId)
			row.SetOwnerId(snapshot.OwnerId)
			row.SetRevision(snapshot.Revision)
			row.SetShouldDelete(true)
			input := &reportwrite.Input{}
			input.SetMode("delete")
			input.SetRuns([]*reportwrite.Run{row})
			if err := treeInvoke[reportwrite.Output](ctx, m, input, "/v1/internal/forge/reporting/run", treeProviders("reportaccess", snapshot.OwnerId)...); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Mutator) detachRuns(ctx context.Context, plan *DeletePlan) error {
	targets := treeIDSet(plan.RunIDs)
	if !plan.Tables["run"] {
		return nil
	}
	rows := make([]*runwrite.MutableRunView, 0, len(plan.DetachRuns))
	for _, snapshot := range plan.DetachRuns {
		if snapshot == nil || snapshot.ResumedFromRunId == nil || !targets[*snapshot.ResumedFromRunId] {
			return fmt.Errorf("run detach has no matching deletion link")
		}
		row := &runwrite.MutableRunView{}
		row.SetId(snapshot.Id)
		row.SetResumedFromRunId(nil)
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil
	}
	input := &runwrite.Input{}
	input.SetRuns(rows)
	return treeInvoke[runwrite.Output](ctx, m, input, "/v1/api/agently/run")
}

func (m *Mutator) detachMessagesAndTurns(ctx context.Context, plan *DeletePlan) error {
	messages, turns := treeIDSet(plan.MessageIDs), treeIDSet(plan.TurnIDs)
	if plan.Tables["message"] {
		rows := make([]*messagewrite.Message, 0, len(plan.DetachMessages))
		for _, snapshot := range plan.DetachMessages {
			if snapshot == nil {
				return fmt.Errorf("deletion plan has a nil message detach")
			}
			row := &messagewrite.Message{}
			row.SetId(snapshot.Id)
			if snapshot.ParentMessageId != nil && messages[*snapshot.ParentMessageId] {
				row.SetParentMessageId(nil)
			}
			if snapshot.SupersededBy != nil && messages[*snapshot.SupersededBy] {
				row.SetSupersededBy(nil)
			}
			if row.Has == nil || (!row.Has.ParentMessageId && !row.Has.SupersededBy) {
				return fmt.Errorf("message %s has no matching deletion link", snapshot.Id)
			}
			rows = append(rows, row)
		}
		if len(rows) > 0 {
			input := &messagewrite.Input{}
			input.SetMessages(rows)
			providers := []locator.Provider{provider.Named("messageaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
				if name == "detachLinks" {
					return true, true, nil
				}
				if name == "messageIds" {
					return append([]string(nil), plan.MessageIDs...), true, nil
				}
				return nil, false, nil
			})}
			if err := treeInvoke[messagewrite.Output](ctx, m, input, "/v1/api/agently/message", providers...); err != nil {
				return err
			}
		}
	}
	if plan.Tables["turn"] {
		rows := make([]*turnwrite.Turn, 0, len(plan.DetachTurns))
		for _, snapshot := range plan.DetachTurns {
			if snapshot == nil {
				return fmt.Errorf("deletion plan has a nil turn detach")
			}
			row := &turnwrite.Turn{}
			row.SetId(snapshot.Id)
			if snapshot.StartedByMessageId != nil && messages[*snapshot.StartedByMessageId] {
				row.SetStartedByMessageId(nil)
			}
			if snapshot.RetryOf != nil && turns[*snapshot.RetryOf] {
				row.SetRetryOf(nil)
			}
			if row.Has == nil || (!row.Has.StartedByMessageId && !row.Has.RetryOf) {
				return fmt.Errorf("turn %s has no matching deletion link", snapshot.Id)
			}
			rows = append(rows, row)
		}
		if len(rows) > 0 {
			input := &turnwrite.Input{}
			input.SetTurns(rows)
			if err := treeInvoke[turnwrite.Output](ctx, m, input, "/v1/api/agently/turn"); err != nil {
				return err
			}
		}
	}
	return nil
}

func treeIDSet(ids []string) map[string]bool {
	result := map[string]bool{}
	for _, id := range ids {
		result[id] = true
	}
	return result
}
func treeProviders(kind, owner string) []locator.Provider {
	providers := []locator.Provider{provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil })}
	if kind != "visibility" {
		providers = append(providers, provider.Named(kind, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return true, true, nil
			}
			return nil, false, nil
		}))
	}
	return providers
}

func treeInvoke[Output any](ctx context.Context, m *Mutator, input any, path string, providers ...locator.Provider) error {
	target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeOf(input).Elem().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: path}}
	value, err := m.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: target, Input: input, Providers: providers})
	if err != nil {
		return fmt.Errorf("conversation deletion %s: %w", path, err)
	}
	output, ok := value.(*Output)
	if !ok || output == nil {
		return fmt.Errorf("conversation deletion %s returned %T", path, value)
	}
	return nil
}

func treeDeleteIDs[Row, Output any](ctx context.Context, m *Mutator, ids []string, path string, row func(string) *Row, input func([]*Row) any, providers ...locator.Provider) error {
	ids = normalizeIDs(ids)
	const batch = 400
	for begin := 0; begin < len(ids); begin += batch {
		end := begin + batch
		if end > len(ids) {
			end = len(ids)
		}
		rows := make([]*Row, 0, end-begin)
		for _, id := range ids[begin:end] {
			rows = append(rows, row(id))
		}
		if err := treeInvoke[Output](ctx, m, input(rows), path, providers...); err != nil {
			return err
		}
	}
	return nil
}

// ConversationIDsByDepthDesc preserves the legacy dependency order: deepest
// first, then oldest creation time, then lexical ID for equal timestamps.
func ConversationIDsByDepthDesc(graph *Graph, ids []string) ([][]string, error) {
	if graph == nil {
		return nil, fmt.Errorf("conversation deletion graph is unavailable")
	}
	byDepth := map[int][]string{}
	for _, id := range normalizeIDs(ids) {
		node := graph.Nodes[id]
		if node == nil {
			return nil, fmt.Errorf("deletion conversation %s has no graph evidence", id)
		}
		byDepth[node.Depth] = append(byDepth[node.Depth], id)
	}
	depths := []int{}
	for depth := range byDepth {
		depths = append(depths, depth)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(depths)))
	result := make([][]string, 0, len(depths))
	for _, depth := range depths {
		group := byDepth[depth]
		sort.Slice(group, func(i, j int) bool {
			left, right := graph.Nodes[group[i]], graph.Nodes[group[j]]
			if left.CreatedAt.Equal(right.CreatedAt) {
				return group[i] < group[j]
			}
			return left.CreatedAt.Before(right.CreatedAt)
		})
		result = append(result, group)
	}
	return result, nil
}
