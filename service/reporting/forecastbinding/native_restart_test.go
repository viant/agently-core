package forecastbinding_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	fb "github.com/viant/agently-core/service/reporting/forecastbinding"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	evidence "github.com/viant/agently-core/app/store/reportingevidence"
	authctx "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	runmodel "github.com/viant/agently-core/model/run"
	runtimeevidence "github.com/viant/agently-core/runtime/evidence"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

// This is a replay of captured evidence into a disposable native store. No tool,
// provider or business endpoint is called; requests/results retain their bytes.
func TestActualTwentyCallsMaterializeAfterNativeEvidenceRestart(t *testing.T) {
	fixture := loadFixture(t)
	admission := actualAdmission(t)
	// The captured older workflow did not record typed user-origin selection or
	// dates. Preserve that limitation instead of inventing authenticated intent.
	admission.SelectionOrigin = fb.SelectionToolEvidence
	admission.AudienceIDs = nil
	admission.DateOrigin = fb.DateToolEvidence
	admission.Dates = nil
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: admission.OwnerID})
	workspace := t.TempDir()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	t.Cleanup(func() {
		if server != nil {
			_ = server.Shutdown(context.Background())
		}
	})
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	root := apiconv.NewConversation()
	root.SetId(admission.ConversationID)
	root.SetCreatedByUserID(admission.OwnerID)
	root.SetStatus("running")
	require.NoError(t, conv.PatchConversations(ctx, root))
	turn := apiconv.NewTurn()
	turn.SetId(admission.TurnID)
	turn.SetConversationID(admission.ConversationID)
	turn.SetStatus("running")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	starter := apiconv.NewMessage()
	starter.SetId(admission.StarterMessageID)
	starter.SetConversationID(admission.ConversationID)
	starter.SetTurnID(admission.TurnID)
	starter.SetRole("user")
	starter.SetType("text")
	starter.SetContent("Captured forecast evidence fixture")
	require.NoError(t, conv.PatchMessage(ctx, starter))
	nativeData := data.NewService(server)
	run := &runmodel.MutableRunView{}
	run.SetId(admission.TurnID)
	run.SetTurnID(admission.TurnID)
	run.SetConversationID(admission.ConversationID)
	run.SetEffectiveUserID(admission.OwnerID)
	run.SetStatus("running")
	run.SetLeaseOwner("fixture-worker")
	run.SetLeaseUntil(time.Now().Add(5 * time.Minute))
	run.SetCheckpointData(`{"unrelated":{"generation":1}}`)
	_, err = nativeData.PatchRuns(ctx, []*runmodel.MutableRunView{run})
	require.NoError(t, err)
	store := fb.NewNativeSourceStore(conv, nativeData, evidence.New(server))
	factory, err := fb.NewFactory(fb.ProjectionPolicyProducer{}, store, admission.TimeZone)
	require.NoError(t, err)
	pending, err := factory.Capture(ctx, runtimeevidence.Input{ReceivedAt: admission.ReceivedAt})
	require.NoError(t, err)
	ctx = requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: admission.ConversationID, TurnID: admission.TurnID, ParentMessageID: admission.StarterMessageID})
	lifecycleTurn := runtimeevidence.Turn{ConversationID: admission.ConversationID, TurnID: admission.TurnID, StarterMessageID: admission.StarterMessageID, LeaseOwner: func() string { return "fixture-worker" }}
	ctx, err = pending.Begin(ctx, lifecycleTurn)
	require.NoError(t, err)
	require.NotNil(t, runtimeevidence.ToolsFromContext(ctx))
	sources := actualSources(t)
	all := append([]fb.Call{sources.Profile, sources.Conversion}, fixture.Records...)
	require.Len(t, fixture.Records, 20)
	for i, call := range all {
		persistCapturedNativeCall(t, ctx, conv, call, fmt.Sprintf("captured-call-%02d", i))
	}
	runtime, err := fb.NewRuntime(fb.ProjectionPolicyProducer{}, store)
	require.NoError(t, err)
	plan, err := runtime.AdmitPlan(ctx, admission.Scope, sources.Profile.OpID, sources.Conversion.OpID, "fixture-worker")
	require.NoError(t, err)
	require.Equal(t, fb.SelectionToolEvidence, plan.Admission.SelectionOrigin)
	require.Equal(t, fb.DateToolEvidence, plan.Admission.DateOrigin)
	require.NoError(t, store.SavePlan(ctx, plan, "fixture-worker"), "identical persistence is idempotent")
	planID := plan.ID
	before := materializeNativeFixture(t, ctx, store, planID, fixture)
	require.Len(t, before.Provenance, 15)
	// fb.Receipt publication changes only the printable tool-message projection.
	// It must not change either immutable source payload used by the binder.
	first := firstCall(&fixture)
	decorated, err := runtime.DecorateAndPublishCompleted(ctx, admission.Scope, planID, first.OpID)
	require.NoError(t, err)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(decorated, &envelope))
	var receipt fb.Receipt
	require.NoError(t, json.Unmarshal(envelope[fb.ReceiptKey], &receipt))
	require.Equal(t, first.OpID, receipt.OpID)
	require.Equal(t, planID, receipt.PlanID)
	require.Equal(t, fb.SelectionToolEvidence, receipt.SelectionOrigin)
	require.Equal(t, fb.DateToolEvidence, receipt.DateOrigin)
	// Generic profile/cube receipts are also persisted separately from payload
	// truth and survive restart; grouped cube reads do not acquire a plan claim.
	projected := map[string]json.RawMessage{}
	for _, op := range []string{sources.Profile.OpID, fixture.Records[1].OpID} {
		projected[op], err = runtime.ProjectCompleted(ctx, admission.Scope, op, "fixture-worker")
		require.NoError(t, err)
	}
	catalog, err := store.CompletedOperations(ctx, admission.Scope, "steward/ForecastingCube")
	require.NoError(t, err)
	require.Len(t, catalog, 20)
	commandProof := exerciseNativeCommand(t, ctx, conv, store, runtime, admission, sources, fixture.Bindings)
	// A normal checkpoint update is independent from the immutable evidence docs.
	patch := &runmodel.MutableRunView{}
	patch.SetId(admission.TurnID)
	patch.SetCheckpointData(`{"unrelated":{"generation":2}}`)
	_, err = nativeData.PatchRuns(ctx, []*runmodel.MutableRunView{patch})
	require.NoError(t, err)
	require.NoError(t, server.Shutdown(ctx))
	server = nil
	restarted, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer restarted.Shutdown(ctx)
	restartedConv, err := convservice.New(ctx, restarted)
	require.NoError(t, err)
	restartedData := data.NewService(restarted)
	restored := fb.NewNativeSourceStore(restartedConv, restartedData, evidence.New(restarted))
	restoredFactory, err := fb.NewFactory(fb.ProjectionPolicyProducer{}, restored, "Asia/Tokyo")
	require.NoError(t, err)
	ctx, err = restoredFactory.Restore(ctx, lifecycleTurn)
	require.NoError(t, err)
	require.NotNil(t, runtimeevidence.ToolsFromContext(ctx))
	loadedAdmission, err := restored.LoadAdmission(ctx, admission.Scope)
	require.NoError(t, err)
	require.Equal(t, admission, *loadedAdmission)
	after := materializeNativeFixture(t, ctx, restored, planID, fixture)
	require.Equal(t, before, after, "fresh native runtime must reproduce all fifteen date-bound cells")
	loadedRun, err := restartedData.GetRun(ctx, admission.TurnID, nil)
	require.NoError(t, err)
	require.Equal(t, `{"unrelated":{"generation":2}}`, *loadedRun.CheckpointData)
	loadedCall, err := restored.LoadCompletedCall(ctx, admission.Scope, first.OpID)
	require.NoError(t, err)
	message, err := restartedConv.GetMessage(ctx, loadedCall.MessageID)
	require.NoError(t, err)
	require.JSONEq(t, string(decorated), message.GetContentPreferContent(), "receipt survives resumed history without replacing original payload")
	for op, body := range projected {
		source, readErr := restored.LoadCompletedCall(ctx, admission.Scope, op)
		require.NoError(t, readErr)
		restoredMessage, readErr := restartedConv.GetMessage(ctx, source.MessageID)
		require.NoError(t, readErr)
		require.JSONEq(t, string(body), restoredMessage.GetContentPreferContent())
		var original map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(source.Response, &original))
		require.NotContains(t, original, fb.SourceReceiptKey)
		require.NotContains(t, original, fb.ProfileReceiptKey)
	}
	restoredRuntime, err := fb.NewRuntime(fb.ProjectionPolicyProducer{}, restored)
	require.NoError(t, err)
	commandBackend, err := fb.NewReportCommandBackend(restoredRuntime, restored)
	require.NoError(t, err)
	require.NoError(t, commandBackend.VerifyReport(ctx, admission.ConversationID, commandProof.link, commandProof.artifacts), "command artifact proof survives native restart")
	foreign := admission.Scope
	foreign.OwnerID = "other-owner"
	_, err = restored.LoadPlan(ctx, foreign, planID)
	require.Error(t, err)
	wrongTurn := admission.Scope
	wrongTurn.TurnID = "other-turn"
	_, err = restored.LoadCompletedCall(ctx, wrongTurn, first.OpID)
	require.Error(t, err)
	changed := admission
	changed.StarterMessageID = "different-starter"
	require.Error(t, restored.SaveAdmission(ctx, changed, "fixture-worker"), "restart cannot replace immutable admission")
}

func persistCapturedNativeCall(t *testing.T, ctx context.Context, conv apiconv.Client, call fb.Call, messageID string) {
	t.Helper()
	ids := []string{messageID + "-request", messageID + "-response"}
	for i, body := range []json.RawMessage{call.Request, call.Response} {
		payload := apiconv.NewPayload()
		payload.SetId(ids[i])
		kind := "tool_request"
		if i == 1 {
			kind = "tool_response"
		}
		payload.SetKind(kind)
		payload.SetMimeType("application/json")
		payload.SetStorage("inline")
		payload.SetInlineBody(body)
		payload.SetSizeBytes(len(body))
		digest := sha256.Sum256(body)
		text := hex.EncodeToString(digest[:])
		payload.Digest = &text
		payload.Has.Digest = true
		require.NoError(t, conv.PatchPayload(ctx, payload))
	}
	message := apiconv.NewMessage()
	message.SetId(messageID)
	message.SetConversationID(call.ConversationID)
	message.SetTurnID(call.TurnID)
	message.SetRole("tool")
	message.SetType("tool_op")
	message.SetStatus("completed")
	message.SetContent(string(call.Response))
	require.NoError(t, conv.PatchMessage(ctx, message))
	tool := apiconv.NewToolCall()
	tool.SetMessageID(messageID)
	tool.SetTurnID(call.TurnID)
	tool.SetRunID(call.TurnID)
	tool.SetOpID(call.OpID)
	tool.SetToolName(call.Tool)
	tool.SetToolKind("general")
	tool.SetStatus(call.Status)
	tool.SetAttempt(1)
	tool.SetCompletedAt(time.Now())
	tool.RequestPayloadID = &ids[0]
	tool.Has.RequestPayloadID = true
	tool.ResponsePayloadID = &ids[1]
	tool.Has.ResponsePayloadID = true
	hash, err := fb.RequestHash(call.Request)
	require.NoError(t, err)
	tool.RequestHash = &hash
	tool.Has.RequestHash = true
	require.NoError(t, conv.PatchToolCall(ctx, tool))
}

func materializeNativeFixture(t *testing.T, ctx context.Context, store *fb.NativeSourceStore, planID string, fixture fixture) *fb.Result {
	t.Helper()
	plan, err := store.LoadPlan(ctx, fixture.Scope, planID)
	require.NoError(t, err)
	var calls []fb.Call
	for _, expected := range fixture.Records {
		actual, err := store.LoadCompletedCall(ctx, fixture.Scope, expected.OpID)
		require.NoError(t, err)
		require.Equal(t, expected.Request, actual.Request, "request bytes must not be rewritten")
		require.Equal(t, expected.Response, actual.Response, "source response bytes must not be rewritten")
		calls = append(calls, *actual)
	}
	result, err := fb.Materialize(fixture.Scope, plan.Produced.Policy, fixture.Bindings, calls)
	require.NoError(t, err)
	require.JSONEq(t, string(fixture.Expected), string(result.Data))
	_, err = fb.Validate(fixture.Scope, plan.Produced.Policy, fixture.Bindings, calls, fixture.Incorrect)
	require.Error(t, err, "saved evidence rejects the original swapped October 2/3 values")
	_, err = fb.Validate(fixture.Scope, plan.Produced.Policy, fixture.Bindings, calls, fixture.Expected)
	require.NoError(t, err)
	return result
}

type fixture struct {
	Scope     fb.Scope        `json:"scope"`
	Policy    fb.Policy       `json:"policy"`
	Bindings  fb.Bindings     `json:"bindings"`
	Records   []fb.Call       `json:"records"`
	Incorrect json.RawMessage `json:"incorrectAuthoredData"`
	Expected  json.RawMessage `json:"expectedData"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/actual_forecast_20_calls.json")
	require.NoError(t, err)
	var f fixture
	require.NoError(t, json.Unmarshal(raw, &f))
	return f
}
func actualAdmission(t *testing.T) fb.Admission {
	f := loadFixture(t)
	return fb.Admission{Scope: f.Scope, StarterMessageID: "fixture-starter", ReceivedAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), TimeZone: "UTC", SelectionOrigin: fb.SelectionToolEvidence, DateOrigin: fb.DateToolEvidence}
}
func actualSources(t *testing.T) fb.PlanSources {
	t.Helper()
	raw, err := os.ReadFile("testdata/actual_profile_conversion_projection.json")
	require.NoError(t, err)
	var source fb.PlanSources
	require.NoError(t, json.Unmarshal(raw, &source))
	return source
}
func firstCall(f *fixture) *fb.Call {
	op := f.Bindings.Columns[0].Calls[0].OpID
	for i := range f.Records {
		if f.Records[i].OpID == op {
			return &f.Records[i]
		}
	}
	panic("fixture source missing")
}
