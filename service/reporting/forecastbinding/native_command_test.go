package forecastbinding_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	workspaceproto "github.com/viant/agently-core/protocol/ui/workspace"
	runtimeevidence "github.com/viant/agently-core/runtime/evidence"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	reporting "github.com/viant/agently-core/service/reporting"
	fb "github.com/viant/agently-core/service/reporting/forecastbinding"
)

type nativeCommandProof struct {
	reference string
	link      json.RawMessage
	artifacts runtimeevidence.ReportArtifacts
}

// Exercise the real native receipt/proof documents using the captured converter
// projection and twenty immutable cube records, without executing any provider.
func exerciseNativeCommand(t *testing.T, ctx context.Context, conv apiconv.Client, store *fb.NativeSourceStore, runtime *fb.Runtime, admission fb.Admission, sources fb.PlanSources, bindings fb.Bindings) nativeCommandProof {
	t.Helper()
	controller, err := fb.NewController(runtime, admission.Scope, func() string { return "fixture-worker" })
	require.NoError(t, err)
	var request map[string]interface{}
	require.NoError(t, json.Unmarshal(sources.Conversion.Request, &request))
	args := request["Request"].(map[string]interface{})
	delete(args, "evidenceProfile")
	delete(args, "evidenceAudienceId")
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	effective, handled, err := controller.Prepare(ctx, "steward/ForecastingTargetingConvert", "controller-convert-op", raw)
	require.NoError(t, err)
	require.True(t, handled)
	conversion := sources.Conversion
	conversion.OpID = "controller-convert-op"
	conversion.Request = effective
	persistCapturedNativeCall(t, ctx, conv, conversion, "controller-convert-message")
	projected, handled, err := controller.Completed(ctx, conversion.Tool, conversion.OpID)
	require.NoError(t, err)
	require.True(t, handled)
	var result struct {
		Plan fb.PlanReceipt `json:"_agentlyForecastPlan"`
	}
	require.NoError(t, json.Unmarshal(projected, &result))
	require.NotEmpty(t, result.Plan.PlanID)

	windowID := "forecast-builder__" + admission.ConversationID
	viewCtx := requestctx.WithToolMessageID(ctx, "view-origin-message")
	workspace := workspaceproto.New(viewCtx, windowID, admission.ConversationID)
	workspace.Kind = "report"
	workspace.Content.WindowID = windowID
	workspace.Content.WindowKey = "forecast-builder"
	workspace.Content.Parameters = map[string]interface{}{"reportBuilderRef": "forecast-builder"}
	workspace.Lifecycle.State = "ready"
	viewBody, _ := json.Marshal(map[string]interface{}{"workspaceObject": workspace})
	persistCapturedNativeCall(t, ctx, conv, fb.Call{Scope: admission.Scope, OpID: "view-origin-op", Tool: "ui/view:open", Status: "completed", Request: json.RawMessage(`{"viewId":"forecast-builder"}`), Response: viewBody}, "view-origin-message")
	pendingNativeCommand(t, ctx, conv, admission.Scope, "command-op", "command-message", windowID)
	commandCtx := requestctx.WithToolMessageID(ctx, "command-message")
	workspaceRaw, _ := json.Marshal(workspace)
	command, err := controller.IssueReportCommand(commandCtx, runtimeevidence.ReportCommandTarget{WindowID: windowID, Workspace: workspaceRaw})
	require.NoError(t, err)
	require.NotEmpty(t, command.RequestID)
	replay, err := controller.IssueReportCommand(commandCtx, runtimeevidence.ReportCommandTarget{WindowID: windowID, Workspace: workspaceRaw})
	require.NoError(t, err)
	require.Equal(t, command, replay)
	forgedWorkspace := *workspace
	forgedWorkspace.Revision++
	forgedRaw, _ := json.Marshal(forgedWorkspace)
	_, err = controller.IssueReportCommand(commandCtx, runtimeevidence.ReportCommandTarget{WindowID: windowID, Workspace: forgedRaw})
	require.Error(t, err, "stale/forged workspace revision")
	backend, err := fb.NewReportCommandBackend(runtime, store)
	require.NoError(t, err)
	admissionInput := runtimeevidence.ReportAdmissionInput{ConversationID: admission.ConversationID, BuilderRef: "forecast-builder", RequestID: command.RequestID, AdmissionRef: command.AdmissionRef}
	link, err := backend.AdmitReport(ctx, admissionInput)
	require.NoError(t, err)
	wrongConversation := admissionInput
	wrongConversation.ConversationID = "foreign-conversation"
	_, err = backend.AdmitReport(ctx, wrongConversation)
	require.Error(t, err)

	wrong := admissionInput
	wrong.RequestID = "substituted-request"
	_, err = backend.AdmitReport(ctx, wrong)
	require.Error(t, err)
	wrong = admissionInput
	wrong.BuilderRef = "other-builder"
	_, err = backend.AdmitReport(ctx, wrong)
	require.Error(t, err)

	dataBinding := fb.DataBindings{PlanID: result.Plan.PlanID, Profile: bindings.Profile, Columns: bindings.Columns}
	start, _ := json.Marshal(map[string]interface{}{"version": 1, "id": command.RequestID, "sequence": 1, "mode": "start", "grammar": "report-document-v1", "blocks": []interface{}{map[string]interface{}{"id": "timeline", "kind": "tableBlock", "datasetRef": "timeline", "columns": []interface{}{map[string]interface{}{"key": "date"}, map[string]interface{}{"key": "overall"}}}}})
	data, _ := json.Marshal(map[string]interface{}{"version": 2, "id": "timeline", "reportRef": command.RequestID, "sequence": 2, "mode": "replace", "format": "json", "sourceBindings": dataBinding})
	commit, _ := json.Marshal(map[string]interface{}{"version": 1, "id": command.RequestID, "sequence": 3, "mode": "commit"})
	compiler := reporting.New(reporting.Options{Store: reporting.NewStoreAdapter(reportmemory.New()), ReportCompilation: backend})
	compileInput := &reporting.CompileFencedReportRequest{ReportAdmissionRef: command.AdmissionRef, ReportID: command.RequestID, Fences: []reporting.FencedReportFence{{Kind: "forge-report", Payload: start}, {Kind: "forge-data", Payload: data}, {Kind: "forge-report", Payload: commit}}}
	compiled, err := compiler.CompileFencedReport(ctx, compileInput)
	require.NoError(t, err)
	artifacts := runtimeevidence.ReportArtifacts{Spec: compiled.ReportSpec, Fill: compiled.ReportFill, Print: compiled.ReportPrint}
	require.NoError(t, backend.VerifyReport(ctx, admission.ConversationID, link, artifacts))
	again, err := compiler.CompileFencedReport(ctx, compileInput)
	require.NoError(t, err)
	require.JSONEq(t, string(compiled.ReportFill), string(again.ReportFill))
	changed := artifacts
	changed.Fill = json.RawMessage(`{"datasets":[]}`)
	require.Error(t, backend.VerifyReport(ctx, admission.ConversationID, link, changed))
	changed = artifacts
	changed.Print = json.RawMessage(`{"pages":[]}`)
	require.Error(t, backend.VerifyReport(ctx, admission.ConversationID, link, changed))
	require.Error(t, backend.RecordCompiled(ctx, command.AdmissionRef, artifacts), "caller cannot skip server compiler context")
	unbound := *compileInput
	unbound.Fences = append([]reporting.FencedReportFence(nil), compileInput.Fences...)
	unbound.Fences[1].Payload = json.RawMessage(`{"version":2,"id":"other","data":[{"madeUp":1}]}`)
	_, err = compiler.CompileFencedReport(ctx, &unbound)
	require.Error(t, err)
	return nativeCommandProof{reference: command.AdmissionRef, link: link, artifacts: artifacts}
}

func pendingNativeCommand(t *testing.T, ctx context.Context, conv apiconv.Client, scope fb.Scope, op, messageID, windowID string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]interface{}{"windowId": windowID})
	payload := apiconv.NewPayload()
	payload.SetId(messageID + "-request")
	payload.SetKind("tool_request")
	payload.SetStorage("inline")
	payload.SetMimeType("application/json")
	payload.SetInlineBody(raw)
	payload.SetSizeBytes(len(raw))
	require.NoError(t, conv.PatchPayload(ctx, payload))
	message := apiconv.NewMessage()
	message.SetId(messageID)
	message.SetConversationID(scope.ConversationID)
	message.SetTurnID(scope.TurnID)
	message.SetRole("tool")
	message.SetType("tool_op")
	message.SetStatus("running")
	require.NoError(t, conv.PatchMessage(ctx, message))
	call := apiconv.NewToolCall()
	call.SetMessageID(messageID)
	call.SetTurnID(scope.TurnID)
	call.SetRunID(scope.TurnID)
	call.SetOpID(op)
	call.SetToolName("ui/report:run")
	call.SetToolKind("general")
	call.SetStatus("running")
	call.SetAttempt(1)
	payloadID := messageID + "-request"
	call.RequestPayloadID = &payloadID
	call.Has.RequestPayloadID = true
	require.NoError(t, conv.PatchToolCall(ctx, call))
}
