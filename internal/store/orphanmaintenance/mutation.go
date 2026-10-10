package orphanmaintenance

import (
	"context"
	"fmt"
	conv "github.com/viant/agently-core/internal/datly/conversation/write"
	file "github.com/viant/agently-core/internal/datly/generatedfile/write"
	goal "github.com/viant/agently-core/internal/datly/goal/write"
	investigation "github.com/viant/agently-core/internal/datly/investigation/write"
	legacy "github.com/viant/agently-core/internal/datly/legacyrun/write"
	message "github.com/viant/agently-core/internal/datly/message/write"
	modelcall "github.com/viant/agently-core/internal/datly/modelcall/write"
	payload "github.com/viant/agently-core/internal/datly/payload/delete"
	reportArtifact "github.com/viant/agently-core/internal/datly/reporting/artifact/write"
	reportContext "github.com/viant/agently-core/internal/datly/reporting/context/write"
	reportJob "github.com/viant/agently-core/internal/datly/reporting/job/write"
	reportRun "github.com/viant/agently-core/internal/datly/reporting/run/write"
	run "github.com/viant/agently-core/internal/datly/run/write"
	schedule "github.com/viant/agently-core/internal/datly/schedule/write"
	approval "github.com/viant/agently-core/internal/datly/toolapprovalqueue/write"
	toolcall "github.com/viant/agently-core/internal/datly/toolcall/write"
	claim "github.com/viant/agently-core/internal/datly/toolexecutionclaim/write"
	turn "github.com/viant/agently-core/internal/datly/turn/write"
	queue "github.com/viant/agently-core/internal/datly/turnqueue/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"reflect"
)

func mutationProviders(rule Rule, snapshot *recordSnapshot) []locator.Provider {
	owner := snapshot.OwnerID
	return []locator.Provider{
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
		provider.Named("reportaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return true, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("claimaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return true, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("investigationaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return true, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("schedulerunaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return true, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("payloadaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "deleteUnreferenced" {
				return rule.ID == "call_payload.unused", true, nil
			}
			return nil, false, nil
		}),
		provider.Named("orphandetach", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "enabledDetach":
				return rule.Action == SafeDetach, true, nil
			case "column":
				return rule.DetachColumn, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("orphandelete", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "enabledDelete" {
				return rule.Action == SafeDelete, true, nil
			}
			return nil, false, nil
		}),
	}
}
func invokeWriter[O any](ctx context.Context, invoker dexec.ComponentInvoker, typ reflect.Type, path string, input any, rule Rule, snapshot *recordSnapshot) error {
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: componentTarget(typ, "PATCH", path), Input: input, Providers: mutationProviders(rule, snapshot)})
	if err != nil {
		return err
	}
	if output, ok := value.(*O); !ok || output == nil {
		return fmt.Errorf("orphan canonical writer returned %T", value)
	}
	return nil
}

func mutateBulkPayload(ctx context.Context, invoker dexec.ComponentInvoker, rule Rule, snapshot *recordSnapshot) (dexec.MutationResult, error) {
	row := &payload.PayloadDelete{}
	row.SetId(snapshot.Keys[0])
	row.SetShouldDelete(true)
	input := &payload.Input{}
	input.SetPayloads([]*payload.PayloadDelete{row})
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{
		Target: dexec.ComponentTarget{
			Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[payload.BulkDeleteComponent]().PkgPath(), Name: "PayloadBulkDelete"},
			Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/payload/delete-bulk"},
		}, Input: input, Providers: mutationProviders(rule, snapshot),
	})
	if err != nil {
		return dexec.MutationResult{}, err
	}
	output, ok := value.(*payload.Output)
	if !ok || output == nil {
		return dexec.MutationResult{}, fmt.Errorf("orphan bulk payload handler returned %T", value)
	}
	return dexec.MutationResult{Table: "call_payload", Operation: "delete", Records: len(output.Data), Affected: int64(len(output.Data))}, nil
}

// mutate maps a static domain action to a canonical table writer or the guarded
// key-only payload delete writer. Request fields cannot select a table, column
// or component.
func mutate(ctx context.Context, invoker dexec.ComponentInvoker, rule Rule, snapshot *recordSnapshot) error {
	id := snapshot.Keys[0]
	switch rule.Table {
	case "conversation":
		row := &conv.MutableConversationView{}
		row.SetId(id)
		if rule.Action == SafeDetach {
			switch rule.DetachColumn {
			case "conversation_parent_id":
				row.SetConversationParentId(nil)
			case "conversation_parent_turn_id":
				row.SetConversationParentTurnId(nil)
			case "schedule_id":
				row.SetScheduleId(nil)
			case "schedule_run_id":
				row.SetScheduleRunId(nil)
			default:
				return fmt.Errorf("orphan detach column %q is unsupported", rule.DetachColumn)
			}
		} else {
			row.SetShouldDelete(true)
		}
		input := &conv.Input{}
		input.SetConversations([]*conv.MutableConversationView{row})
		return invokeWriter[conv.Output](ctx, invoker, reflect.TypeFor[conv.WriterComponent](), "/v1/api/agently/conversation", input, rule, snapshot)
	case "goal":
		row := &goal.Goal{}
		row.SetId(id)
		row.SetShouldDelete(true)
		input := &goal.Input{}
		input.SetGoals([]*goal.Goal{row})
		return invokeWriter[goal.Output](ctx, invoker, reflect.TypeFor[goal.WriterComponent](), "/v1/api/agently/goal", input, rule, snapshot)
	case "turn":
		row := &turn.Turn{}
		row.SetId(id)
		if rule.Action == SafeDetach {
			switch rule.DetachColumn {
			case "goal_id":
				row.SetGoalId(nil)
			case "retry_of":
				row.SetRetryOf(nil)
			case "run_id":
				row.SetRunId(nil)
			case "started_by_message_id":
				row.SetStartedByMessageId(nil)
			default:
				return fmt.Errorf("orphan detach column %q is unsupported", rule.DetachColumn)
			}
		} else {
			row.SetShouldDelete(true)
		}
		input := &turn.Input{}
		input.SetTurns([]*turn.Turn{row})
		return invokeWriter[turn.Output](ctx, invoker, reflect.TypeFor[turn.WriterComponent](), "/v1/api/agently/turn", input, rule, snapshot)
	case "turn_queue":
		row := &queue.TurnQueue{}
		row.SetId(id)
		row.SetShouldDelete(true)
		input := &queue.Input{}
		input.SetQueues([]*queue.TurnQueue{row})
		return invokeWriter[queue.Output](ctx, invoker, reflect.TypeFor[queue.WriterComponent](), "/v1/api/agently/turnqueue", input, rule, snapshot)
	case "message":
		row := &message.Message{}
		row.SetId(id)
		if rule.Action == SafeDetach {
			switch rule.DetachColumn {
			case "attachment_payload_id":
				row.SetAttachmentPayloadId(nil)
			case "elicitation_payload_id":
				row.SetElicitationPayloadId(nil)
			case "linked_conversation_id":
				row.SetLinkedConversationId(nil)
			case "parent_message_id":
				row.SetParentMessageId(nil)
			case "superseded_by":
				row.SetSupersededBy(nil)
			case "turn_id":
				row.SetTurnId(nil)
			default:
				return fmt.Errorf("orphan detach column %q is unsupported", rule.DetachColumn)
			}
		} else {
			row.SetShouldDelete(true)
		}
		input := &message.Input{}
		input.SetMessages([]*message.Message{row})
		return invokeWriter[message.Output](ctx, invoker, reflect.TypeFor[message.WriterComponent](), "/v1/api/agently/message", input, rule, snapshot)
	case "model_call":
		row := &modelcall.ModelCall{}
		row.SetMessageId(id)
		if rule.Action == SafeDetach {
			switch rule.DetachColumn {
			case "provider_request_payload_id":
				row.SetProviderRequestPayloadId(nil)
			case "provider_response_payload_id":
				row.SetProviderResponsePayloadId(nil)
			case "request_payload_id":
				row.SetRequestPayloadId(nil)
			case "response_payload_id":
				row.SetResponsePayloadId(nil)
			case "run_id":
				row.SetRunId(nil)
			case "stream_payload_id":
				row.SetStreamPayloadId(nil)
			case "turn_id":
				row.SetTurnId(nil)
			default:
				return fmt.Errorf("orphan detach column %q is unsupported", rule.DetachColumn)
			}
		} else {
			row.SetShouldDelete(true)
		}
		input := &modelcall.Input{}
		input.SetModelCalls([]*modelcall.ModelCall{row})
		return invokeWriter[modelcall.Output](ctx, invoker, reflect.TypeFor[modelcall.WriterComponent](), "/v1/api/agently/modelcall", input, rule, snapshot)
	case "tool_call":
		row := &toolcall.ToolCall{}
		row.SetMessageId(id)
		if rule.Action == SafeDetach {
			switch rule.DetachColumn {
			case "request_payload_id":
				row.SetRequestPayloadId(nil)
			case "response_payload_id":
				row.SetResponsePayloadId(nil)
			case "run_id":
				row.SetRunId(nil)
			case "turn_id":
				row.SetTurnId(nil)
			default:
				return fmt.Errorf("orphan detach column %q is unsupported", rule.DetachColumn)
			}
		} else {
			row.SetShouldDelete(true)
		}
		input := &toolcall.Input{}
		input.SetToolCalls([]*toolcall.ToolCall{row})
		return invokeWriter[toolcall.Output](ctx, invoker, reflect.TypeFor[toolcall.WriterComponent](), "/v1/api/agently/toolcall", input, rule, snapshot)
	case "generated_file":
		row := &file.GeneratedFile{}
		row.SetId(id)
		if rule.Action == SafeDetach {
			switch rule.DetachColumn {
			case "message_id":
				row.SetMessageId(nil)
			case "payload_id":
				row.SetPayloadId(nil)
			case "turn_id":
				row.SetTurnId(nil)
			default:
				return fmt.Errorf("orphan detach column %q is unsupported", rule.DetachColumn)
			}
		} else {
			row.SetShouldDelete(true)
		}
		input := &file.Input{}
		input.SetGeneratedFiles([]*file.GeneratedFile{row})
		return invokeWriter[file.Output](ctx, invoker, reflect.TypeFor[file.WriterComponent](), "/v1/api/agently/generated-file", input, rule, snapshot)
	case "tool_approval_queue":
		row := &approval.ToolApprovalQueue{}
		row.SetId(id)
		if rule.Action == SafeDetach {
			switch rule.DetachColumn {
			case "conversation_id":
				row.SetConversationId(nil)
			case "message_id":
				row.SetMessageId(nil)
			case "turn_id":
				row.SetTurnId(nil)
			default:
				return fmt.Errorf("orphan detach column %q is unsupported", rule.DetachColumn)
			}
		} else {
			row.SetShouldDelete(true)
		}
		input := &approval.Input{}
		input.SetQueues([]*approval.ToolApprovalQueue{row})
		return invokeWriter[approval.Output](ctx, invoker, reflect.TypeFor[approval.WriterComponent](), "/v1/api/agently/toolapprovalqueue", input, rule, snapshot)
	case "tool_execution_claim":
		row := &claim.Claim{}
		row.SetClaimKey(id)
		row.SetShouldDelete(true)
		input := &claim.Input{}
		input.SetClaims([]*claim.Claim{row})
		if snapshot.TurnID != "" {
			input.SetExpectedTurnID(snapshot.TurnID)
		}
		return invokeWriter[claim.Output](ctx, invoker, reflect.TypeFor[claim.WriterComponent](), "/v1/internal/agently/tool-execution-claim", input, rule, snapshot)
	case "run":
		row := &run.MutableRunView{}
		row.SetId(id)
		if rule.Action == SafeDetach {
			switch rule.DetachColumn {
			case "checkpoint_message_id":
				row.SetCheckpointMessageId(nil)
			case "conversation_id":
				row.SetConversationId(nil)
			case "resumed_from_run_id":
				row.SetResumedFromRunId(nil)
			case "schedule_id":
				row.SetScheduleId(nil)
			case "turn_id":
				row.SetTurnId(nil)
			default:
				return fmt.Errorf("orphan detach column %q is unsupported", rule.DetachColumn)
			}
		} else {
			row.SetShouldDelete(true)
		}
		input := &run.Input{}
		input.SetRuns([]*run.MutableRunView{row})
		return invokeWriter[run.Output](ctx, invoker, reflect.TypeFor[run.WriterComponent](), "/v1/api/agently/run", input, rule, snapshot)
	case "schedule":
		row := &schedule.Schedule{}
		row.SetId(id)
		if rule.Action == SafeDetach {
			switch rule.DetachColumn {
			case "conversation_id":
				row.SetConversationId(nil)
			case "goal_id":
				row.SetGoalId(nil)
			default:
				return fmt.Errorf("orphan detach column %q is unsupported", rule.DetachColumn)
			}
		} else {
			row.SetShouldDelete(true)
		}
		input := &schedule.Input{}
		input.SetSchedules([]*schedule.Schedule{row})
		return invokeWriter[schedule.Output](ctx, invoker, reflect.TypeFor[schedule.WriterComponent](), "/v1/api/agently/scheduler/", input, rule, snapshot)
	case "schedule_run":
		row := &legacy.LegacyRun{}
		row.SetId(id)
		if rule.Action == SafeDetach {
			switch rule.DetachColumn {
			case "conversation_id":
				row.SetConversationId(nil)
			default:
				return fmt.Errorf("orphan detach column %q is unsupported", rule.DetachColumn)
			}
		} else {
			row.SetShouldDelete(true)
		}
		input := &legacy.Input{}
		input.SetRuns([]*legacy.LegacyRun{row})
		if snapshot.ScheduleID != "" {
			input.SetExpectedScheduleID(snapshot.ScheduleID)
		}
		return invokeWriter[legacy.Output](ctx, invoker, reflect.TypeFor[legacy.WriterComponent](), "/v1/internal/agently/scheduler/legacy-run", input, rule, snapshot)
	case "investigation":
		row := &investigation.Investigation{}
		row.SetId(id)
		row.SetShouldDelete(true)
		input := &investigation.Input{}
		input.SetInvestigations([]*investigation.Investigation{row})
		if snapshot.ConversationID != "" {
			input.SetExpectedConversationID(snapshot.ConversationID)
		}
		return invokeWriter[investigation.Output](ctx, invoker, reflect.TypeFor[investigation.WriterComponent](), "/v1/internal/agently/investigation", input, rule, snapshot)
	case "call_payload":
		row := &payload.PayloadDelete{}
		row.SetId(id)
		row.SetShouldDelete(true)
		input := &payload.Input{}
		input.SetPayloads([]*payload.PayloadDelete{row})
		return invokeWriter[payload.Output](ctx, invoker, reflect.TypeFor[payload.WriterComponent](), "/v1/internal/agently/payload/delete", input, rule, snapshot)
	case "report_run":
		row := &reportRun.Run{}
		row.SetReportRunId(id)
		if rule.Action == SafeDetach {
			switch rule.DetachColumn {
			case "conversation_id":
				row.SetConversationId(nil)
			default:
				return fmt.Errorf("orphan detach column %q is unsupported", rule.DetachColumn)
			}
		} else {
			row.SetShouldDelete(true)
		}
		input := &reportRun.Input{}
		input.SetRuns([]*reportRun.Run{row})
		input.SetMode("orphanDetach")
		row.SetRevision(snapshot.Revision)
		return invokeWriter[reportRun.Output](ctx, invoker, reflect.TypeFor[reportRun.WriterComponent](), "/v1/internal/forge/reporting/run", input, rule, snapshot)
	case "report_export_job":
		row := &reportJob.Job{}
		row.SetJobId(id)
		if rule.Action == SafeDetach {
			switch rule.DetachColumn {
			case "conversation_id":
				row.SetConversationId(nil)
			default:
				return fmt.Errorf("orphan detach column %q is unsupported", rule.DetachColumn)
			}
		} else {
			row.SetShouldDelete(true)
		}
		input := &reportJob.Input{}
		input.SetJobs([]*reportJob.Job{row})
		input.SetMode("orphanDetach")
		return invokeWriter[reportJob.Output](ctx, invoker, reflect.TypeFor[reportJob.WriterComponent](), "/v1/internal/forge/reporting/job", input, rule, snapshot)
	case "report_export_artifact":
		row := &reportArtifact.Artifact{}
		row.SetArtifactId(id)
		row.SetShouldDelete(true)
		input := &reportArtifact.Input{}
		input.SetArtifacts([]*reportArtifact.Artifact{row})
		input.SetMode("orphanDelete")
		if snapshot.JobID != "" {
			input.SetExpectedJobID(snapshot.JobID)
		}
		return invokeWriter[reportArtifact.Output](ctx, invoker, reflect.TypeFor[reportArtifact.WriterComponent](), "/v1/internal/forge/reporting/artifact", input, rule, snapshot)
	case "conversation_report_context":
		row := &reportContext.Context{}
		row.SetOwnerId(id)
		row.SetConversationId(snapshot.Keys[1])
		row.SetRevision(snapshot.Revision)
		row.SetShouldDelete(true)
		input := &reportContext.Input{}
		input.SetContexts([]*reportContext.Context{row})
		return invokeWriter[reportContext.Output](ctx, invoker, reflect.TypeFor[reportContext.WriterComponent](), "/v1/internal/forge/reporting/conversation-context", input, rule, snapshot)
	}
	return fmt.Errorf("orphan canonical mutation table %q is unsupported", rule.Table)
}
