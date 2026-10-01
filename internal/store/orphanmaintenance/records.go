package orphanmaintenance

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	conv "github.com/viant/agently-core/internal/datly/conversation/read"
	file "github.com/viant/agently-core/internal/datly/generatedfile/read"
	goal "github.com/viant/agently-core/internal/datly/goal/read"
	investigation "github.com/viant/agently-core/internal/datly/investigation/read"
	legacy "github.com/viant/agently-core/internal/datly/legacyrun/read"
	message "github.com/viant/agently-core/internal/datly/message/read"
	modelcall "github.com/viant/agently-core/internal/datly/modelcall/read"
	payload "github.com/viant/agently-core/internal/datly/payload/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	reportArtifact "github.com/viant/agently-core/internal/datly/reporting/artifact/read"
	reportContext "github.com/viant/agently-core/internal/datly/reporting/context/read"
	reportJob "github.com/viant/agently-core/internal/datly/reporting/job/read"
	reportRun "github.com/viant/agently-core/internal/datly/reporting/run/read"
	run "github.com/viant/agently-core/internal/datly/run/read"
	schedule "github.com/viant/agently-core/internal/datly/schedule/read"
	approval "github.com/viant/agently-core/internal/datly/toolapprovalqueue/read"
	toolcall "github.com/viant/agently-core/internal/datly/toolcall/read"
	claim "github.com/viant/agently-core/internal/datly/toolexecutionclaim/read"
	turn "github.com/viant/agently-core/internal/datly/turn/read"
	queue "github.com/viant/agently-core/internal/datly/turnqueue/read"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

type recordSnapshot struct {
	RecordID       string
	Keys           []string
	OwnerID        string
	Revision       int64
	ScheduleID     string
	TurnID         string
	ConversationID string
	JobID          string
}

func componentTarget(typ reflect.Type, method, path string) dexec.ComponentTarget {
	name := "reader"
	if method == "PATCH" {
		name = "writer"
	}
	return dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: typ.PkgPath(), Name: name}, Route: spec.RouteRef{Method: method, Path: path}}
}
func lockedProviders(kind string, fields []string) []locator.Provider {
	owner := ""
	return []locator.Provider{provider.Named(kind, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "internal", "lock", "graph":
			return true, true, nil
		case "mode":
			return "rows", true, nil
		case "list", "enforceVisibility", "ascending", "checkReferences":
			return false, true, nil
		}
		return nil, false, nil
	}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }), queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: fields}}})}
}
func invokeRead[O any](ctx context.Context, invoker dexec.ComponentInvoker, typ reflect.Type, path, kind string, input any, fields ...string) (*O, error) {
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: componentTarget(typ, "GET", path), Input: input, Providers: lockedProviders(kind, fields)})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*O)
	if !ok || output == nil {
		return nil, fmt.Errorf("orphan canonical reader returned %T", value)
	}
	return output, nil
}
func oneRow[R any](rows []*R) (*R, bool, error) {
	if len(rows) == 0 {
		return nil, false, nil
	}
	if len(rows) != 1 || rows[0] == nil {
		return nil, false, fmt.Errorf("orphan identity returned %d rows", len(rows))
	}
	return rows[0], true, nil
}
func snapshotKeys(keys ...string) *recordSnapshot {
	return &recordSnapshot{RecordID: strings.Join(keys, RecordSeparator), Keys: keys}
}

// lockRecord consumes the existing canonical reader for each physical table.
// It returns only identity and immutable writer guard fields, never content.
func lockRecord(ctx context.Context, invoker dexec.ComponentInvoker, rule Rule, keys []string) (*recordSnapshot, bool, error) {
	id := keys[0]
	switch rule.Table {
	case "conversation":
		input := &conv.ConversationInput{}
		input.SetIds([]string{id})
		out, err := invokeRead[conv.ConversationOutput](ctx, invoker, reflect.TypeFor[conv.ReaderComponent](), "/v1/api/agently/conversation/{id}", "conversationaccess", input, "id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		return snapshotKeys(r.Id), true, nil
	case "goal":
		input := &goal.GoalInput{}
		input.SetIds([]string{id})
		out, err := invokeRead[goal.GoalOutput](ctx, invoker, reflect.TypeFor[goal.ReaderComponent](), "/v1/api/agently/goal/{conversationId}", "goalaccess", input, "id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		return snapshotKeys(r.Id), true, nil
	case "turn":
		input := &turn.TurnRowsInput{}
		input.SetTurnId(id)
		out, err := invokeRead[turn.TurnRowsOutput](ctx, invoker, reflect.TypeFor[turn.ReaderComponent](), "/v1/api/agently/turn/list/list", "turnaccess", input, "id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		return snapshotKeys(r.Id), true, nil
	case "turn_queue":
		input := &queue.QueueRowsInput{}
		input.SetId(id)
		out, err := invokeRead[queue.QueueRowsOutput](ctx, invoker, reflect.TypeFor[queue.ReaderComponent](), "/v1/api/agently/turnqueue/list", "turnqueueaccess", input, "id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		return snapshotKeys(r.Id), true, nil
	case "message":
		input := &message.MessagesInput{}
		input.SetId(id)
		out, err := invokeRead[message.MessagesOutput](ctx, invoker, reflect.TypeFor[message.ReaderComponent](), "/v1/internal/agently/message", "messageaccess", input, "id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		return snapshotKeys(r.Id), true, nil
	case "model_call":
		input := &modelcall.ModelCallsInput{}
		input.SetMessageId(id)
		out, err := invokeRead[modelcall.ModelCallsOutput](ctx, invoker, reflect.TypeFor[modelcall.ReaderComponent](), "/v1/internal/agently/model-call", "modelcallaccess", input, "message_id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		return snapshotKeys(r.MessageId), true, nil
	case "tool_call":
		input := &toolcall.ToolCallsInput{}
		input.SetMessageId(id)
		out, err := invokeRead[toolcall.ToolCallsOutput](ctx, invoker, reflect.TypeFor[toolcall.ReaderComponent](), "/v1/internal/agently/tool-call", "toolcallaccess", input, "message_id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		return snapshotKeys(r.MessageId), true, nil
	case "generated_file":
		input := &file.Input{}
		input.SetID(id)
		out, err := invokeRead[file.Output](ctx, invoker, reflect.TypeFor[file.ReaderComponent](), "/v2/api/agently/generated-file", "generatedfileaccess", input, "id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		return snapshotKeys(r.Id), true, nil
	case "tool_execution_claim":
		input := &claim.Input{}
		input.SetClaimKey(id)
		out, err := invokeRead[claim.Output](ctx, invoker, reflect.TypeFor[claim.ReaderComponent](), "/v1/internal/agently/tool-execution-claim", "claimaccess", input, "claim_key", "turn_id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		snapshot := snapshotKeys(r.ClaimKey)
		snapshot.TurnID = r.TurnId
		return snapshot, true, nil
	case "tool_approval_queue":
		input := &approval.ApprovalRowsInput{}
		input.SetId(id)
		out, err := invokeRead[approval.ApprovalRowsOutput](ctx, invoker, reflect.TypeFor[approval.ReaderComponent](), "/v1/internal/agently/tool-approval", "approvalaccess", input, "id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		return snapshotKeys(r.Id), true, nil
	case "run":
		input := &run.RunRowsInput{}
		input.SetId(id)
		out, err := invokeRead[run.RunRowsOutput](ctx, invoker, reflect.TypeFor[run.ReaderComponent](), "/v1/api/agently/run/{id}", "runaccess", input, "id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		return snapshotKeys(r.Id), true, nil
	case "schedule":
		input := &schedule.ScheduleInput{}
		input.SetId(id)
		out, err := invokeRead[schedule.ScheduleOutput](ctx, invoker, reflect.TypeFor[schedule.ReaderComponent](), "/v1/api/agently/scheduler/schedule/{id}", "scheduleaccess", input, "id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		return snapshotKeys(r.Id), true, nil
	case "schedule_run":
		input := &legacy.Input{}
		input.SetID(id)
		out, err := invokeRead[legacy.Output](ctx, invoker, reflect.TypeFor[legacy.ReaderComponent](), "/v1/internal/agently/scheduler/legacy-run", "schedulerunaccess", input, "id", "schedule_id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		snapshot := snapshotKeys(r.Id)
		snapshot.ScheduleID = r.ScheduleId
		return snapshot, true, nil
	case "investigation":
		input := &investigation.Input{}
		input.SetID(id)
		out, err := invokeRead[investigation.Output](ctx, invoker, reflect.TypeFor[investigation.ReaderComponent](), "/v1/internal/agently/investigation", "investigationaccess", input, "id", "conversation_id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		snapshot := snapshotKeys(r.Id)
		if r.ConversationId != nil {
			snapshot.ConversationID = *r.ConversationId
		}
		return snapshot, true, nil
	case "call_payload":
		input := &payload.PayloadRowsInput{}
		input.SetId(id)
		out, err := invokeRead[payload.PayloadRowsOutput](ctx, invoker, reflect.TypeFor[payload.ReaderComponent](), "/v1/api/agently/payload", "payloadaccess", input, "id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		return snapshotKeys(r.Id), true, nil
	case "report_run":
		input := &reportRun.Input{}
		input.SetReportRunID(id)
		out, err := invokeRead[reportRun.Output](ctx, invoker, reflect.TypeFor[reportRun.ReaderComponent](), "/v1/internal/forge/reporting/run", "reportaccess", input, "report_run_id", "owner_id", "revision")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		snapshot := snapshotKeys(r.ReportRunId)
		snapshot.OwnerID = r.OwnerId
		snapshot.Revision = r.Revision
		return snapshot, true, nil
	case "report_export_job":
		input := &reportJob.Input{}
		input.SetJobID(id)
		out, err := invokeRead[reportJob.Output](ctx, invoker, reflect.TypeFor[reportJob.ReaderComponent](), "/v1/internal/forge/reporting/job", "reportaccess", input, "job_id", "owner_id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		snapshot := snapshotKeys(r.JobId)
		snapshot.OwnerID = r.OwnerId
		return snapshot, true, nil
	case "report_export_artifact":
		input := &reportArtifact.Input{}
		input.SetArtifactID(id)
		out, err := invokeRead[reportArtifact.Output](ctx, invoker, reflect.TypeFor[reportArtifact.ReaderComponent](), "/v1/internal/forge/reporting/artifact", "reportaccess", input, "artifact_id", "owner_id", "job_id")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		snapshot := snapshotKeys(r.ArtifactId)
		snapshot.OwnerID = r.OwnerId
		snapshot.JobID = r.JobId
		return snapshot, true, nil
	case "conversation_report_context":
		input := &reportContext.Input{}
		input.SetOwnerID(keys[0])
		input.SetConversationID(keys[1])
		out, err := invokeRead[reportContext.Output](ctx, invoker, reflect.TypeFor[reportContext.ReaderComponent](), "/v1/internal/forge/reporting/conversation-context", "reportaccess", input, "owner_id", "conversation_id", "revision")
		if err != nil {
			return nil, false, err
		}
		r, found, err := oneRow(out.Data)
		if !found || err != nil {
			return nil, found, err
		}
		snapshot := snapshotKeys(r.OwnerId, r.ConversationId)
		snapshot.OwnerID = r.OwnerId
		snapshot.Revision = r.Revision
		return snapshot, true, nil
	}
	return nil, false, fmt.Errorf("orphan canonical table %q is unsupported", rule.Table)
}
