package agui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

func presentationFields(t *testing.T, event Event) map[string]any {
	t.Helper()
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	require.NoError(t, ValidateEvent(raw))
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	return fields
}

func assertToolExecutionPresentation(t *testing.T, event Event, id, status string) {
	t.Helper()
	fields := presentationFields(t, event)
	require.Equal(t, "ACTIVITY_SNAPSHOT", fields["type"])
	require.Equal(t, "agently.tool", fields["activityType"])
	content := fields["content"].(map[string]any)
	require.Equal(t, id, content["toolCallId"])
	require.Equal(t, status, content["status"])
	require.NoError(t, extensions.ValidatePresentation("agently.tool", rawJSON(content)))
}
func TestNativePresentationMetadataPreservesIdentityTimingAndZeroWithoutPrivatePayloads(t *testing.T) {
	tr := NewTranslator("thread", "run")
	require.NoError(t, tr.SetNativeIdentity("native-turn"))
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	event := &streaming.Event{Type: streaming.EventTypeModelStarted, ConversationID: "thread", TurnID: "native-turn", AssistantMessageID: "assistant", PageID: "page", ModelCallID: "model", Provider: "provider", ModelName: "model-name", Iteration: 0, ExecutionRole: "react", Phase: "main", StartedAt: &at, CreatedAt: at, RequestPayloadID: "request-ref", Patch: map[string]any{"private": "raw-native-secret"}, ResponsePayload: map[string]any{"_meta": "host-receipt-secret"}, Content: "authoring-json-secret", Usage: &streaming.UsageSnapshot{Scope: "model_call"}}
	var journal []json.RawMessage
	for _, output := range tr.Translate(event) {
		fields := presentationFields(t, output)
		journal = append(journal, rawJSON(fields))
		if fields["type"] != "STEP_STARTED" {
			continue
		}
		metadata := fields["metadata"].(map[string]any)["agently"].(map[string]any)["presentation"]
		require.NoError(t, extensions.ValidatePresentationMetadata(rawJSON(metadata)))
		encoded := string(rawJSON(metadata))
		require.NotContains(t, encoded, "raw-native-secret")
		require.NotContains(t, encoded, "host-receipt-secret")
		require.NotContains(t, encoded, "authoring-json-secret")
		require.Contains(t, encoded, `"iteration":0`)
		require.Contains(t, encoded, `"inputTokens":0`)
		require.Contains(t, encoded, "request-ref")
		require.Contains(t, encoded, "2026-10-03T12:00:00Z")
	}
	for _, output := range tr.Translate(&streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "thread", TurnID: "native-turn", MessageID: "assistant", PageID: "page", Content: "hello", CreatedAt: at}) {
		journal = append(journal, rawJSON(presentationFields(t, output)))
	}
	for _, output := range tr.Translate(&streaming.Event{Type: streaming.EventTypeModelCompleted, ConversationID: "thread", TurnID: "native-turn", AssistantMessageID: "assistant", PageID: "page", ModelCallID: "model", Status: "completed", CompletedAt: &at}) {
		journal = append(journal, rawJSON(presentationFields(t, output)))
	}
	restored, err := RestoreTranslator("thread", "run", journal)
	require.NoError(t, err)
	require.JSONEq(t, string(rawJSON(tr.messages)), string(rawJSON(restored.messages)))
	require.Contains(t, string(rawJSON(restored.messages)), "native-turn")
	require.Contains(t, string(rawJSON(restored.messages)), "page")
}
func TestNativePresentationFeedLifecycleNeedsNoAssistantMessageAndPlannerFalseSurvives(t *testing.T) {
	tr := NewTranslator("thread", "run")
	tr.Start()
	active := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolFeedActive, ConversationID: "thread", TurnID: "turn", FeedID: "feed", FeedTitle: "Report", FeedTarget: "rail", FeedItemCount: 0, FeedData: map[string]any{"rows": []any{}}})
	require.Len(t, active, 1)
	fields := presentationFields(t, active[0])
	require.Equal(t, "agently.feed", fields["activityType"])
	content := fields["content"].(map[string]any)
	require.Equal(t, true, content["active"])
	require.EqualValues(t, 0, content["itemCount"])
	require.NoError(t, extensions.ValidatePresentation("agently.feed", rawJSON(content)))
	inactive := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolFeedInactive, ConversationID: "thread", FeedID: "feed"})
	require.Len(t, inactive, 1)
	content = presentationFields(t, inactive[0])["content"].(map[string]any)
	require.Equal(t, false, content["active"])
	validated := false
	planner := tr.Translate(&streaming.Event{Type: streaming.EventTypePlannerValidated, ConversationID: "thread", TurnID: "turn", PlannerValidated: &validated, PlannerOutputPayloadID: "planner-ref", Patch: map[string]any{"output": "private-planner-json"}})
	require.Len(t, planner, 1)
	content = presentationFields(t, planner[0])["content"].(map[string]any)
	require.Equal(t, false, content["validated"])
	require.EqualValues(t, 0, content["attempt"])
	require.NoError(t, extensions.ValidatePresentation("agently.planner", rawJSON(content)))
	require.NotContains(t, string(rawJSON(planner)), "private-planner-json")
}
func TestNativePresentationPlannedToolsDoNotCreateCallsAndCustomAliasesMatchStandardCalls(t *testing.T) {
	tr := NewTranslator("thread", "run")
	require.NoError(t, tr.SetNativeIdentity("turn"))
	tr.Start()
	planned := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallsPlanned, TurnID: "turn", PageID: "page", ToolCallsPlanned: []streaming.PlannedToolCall{{ToolCallID: "call", ToolName: "lookup"}}})
	require.Len(t, planned, 1)
	require.Empty(t, tr.tools)
	require.Empty(t, tr.toolOrder)
	require.Contains(t, string(rawJSON(planned)), ProtocolToolCallID("turn", "call"))
	require.NotContains(t, string(rawJSON(planned)), "TOOL_CALL_START")
	tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallStarted, TurnID: "turn", ToolCallID: "call", ToolName: "lookup", AssistantMessageID: "assistant"})
	waiting := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallWaiting, TurnID: "turn", ToolCallID: "call", Status: "waiting_for_user"})
	require.Equal(t, ProtocolToolCallID("turn", "call"), waiting[0].Value.(ProgressValue).ToolCallID)
	linked := tr.Translate(&streaming.Event{Type: streaming.EventTypeLinkedConversationAttached, TurnID: "turn", ToolCallID: "call", LinkedConversationID: "child"})
	require.Equal(t, ProtocolToolCallID("turn", "call"), linked[0].Value.(map[string]any)["parentToolCallId"])
}
func TestNativePresentationQueuedTurnIsNotNativeStartedAndAggregateUsageDoesNotPolluteModelTotals(t *testing.T) {
	tr := NewTranslator("thread", "run")
	tr.Start()
	queued := tr.Translate(&streaming.Event{Type: streaming.EventTypeTurnQueued, ConversationID: "thread", TurnID: "turn", QueueSeq: 0, GoalID: "goal"})
	require.Len(t, queued, 2)
	fields := presentationFields(t, queued[1])
	require.Equal(t, "queued", fields["content"].(map[string]any)["status"])
	require.Equal(t, "0", fields["content"].(map[string]any)["queueSequence"])
	usage := tr.Translate(&streaming.Event{Type: streaming.EventTypeUsage, TurnID: "turn", Usage: &streaming.UsageSnapshot{Scope: "conversation", InputTokens: 0, OutputTokens: 0}})
	require.Len(t, usage, 1)
	require.Equal(t, "conversation", usage[0].Value.(map[string]any)["scope"])
	journal := []json.RawMessage{rawJSON(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": "run"}), rawJSON(usage[0])}
	restored, err := RestoreTranslator("thread", "run", journal)
	require.NoError(t, err)
	require.Empty(t, restored.usage)
}
func TestNativePresentationNestedEventsPreserveSourceAttribution(t *testing.T) {
	tr := NewTranslator("root", "run")
	require.NoError(t, tr.SetNativeIdentity("parent-turn"))
	tr.Start()
	inv := requestctx.Invocation{ID: "child-run", ConversationID: "child", TurnID: "child-turn", Name: "worker", ParentConversationID: "root", ParentTurnID: "parent-turn"}
	outputs := tr.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "child", TurnID: "child-turn", MessageID: "child-message", PageID: "child-page", Content: "answer"})
	found := false
	for _, output := range outputs {
		fields := presentationFields(t, output)
		if fields["type"] != "TEXT_MESSAGE_CONTENT" {
			continue
		}
		found = true
		require.Equal(t, "child-run", fields["subagentRunId"])
		metadata := fields["metadata"].(map[string]any)["agently"].(map[string]any)["presentation"].(map[string]any)
		require.Equal(t, "child", metadata["conversationId"])
		require.Equal(t, "child-turn", metadata["nativeTurnId"])
	}
	require.True(t, found)
}

func TestNativePresentationToolEffectStartsAfterArgumentsWithoutAnotherStandardStartAndSurvivesRestore(t *testing.T) {
	tr := NewTranslator("thread", "run")
	require.NoError(t, tr.SetNativeIdentity("turn"))
	journal := []json.RawMessage{}
	record := func(events []Event) {
		for _, event := range events {
			journal = append(journal, rawJSON(presentationFields(t, event)))
		}
	}
	record(tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallStarted, TurnID: "turn", ToolCallID: "call", ToolName: "lookup", AssistantMessageID: "assistant"}))
	record(tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, TurnID: "turn", ToolCallID: "call", Arguments: map[string]any{"q": "query"}}))
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	execution := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallStarted, TurnID: "turn", ToolCallID: "call", ToolMessageID: "native-tool", Status: "running", StartedAt: &start})
	require.Len(t, execution, 1)
	fields := presentationFields(t, execution[0])
	require.Equal(t, "agently.tool", fields["activityType"])
	require.Equal(t, "2026-10-03T12:00:00Z", fields["content"].(map[string]any)["startedAt"])
	record(execution)
	require.Empty(t, tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallStarted, TurnID: "turn", ToolCallID: "call", ToolMessageID: "native-tool", Status: "running", StartedAt: &start}), "same execution boundary must not repeat presentation callbacks")
	restored, err := RestoreTranslator("thread", "run", journal)
	require.NoError(t, err)
	end := start.Add(time.Second)
	completed := restored.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, TurnID: "turn", ToolCallID: "call", ToolMessageID: "native-tool", Status: "completed", CompletedAt: &end, Content: "result"})
	var content map[string]any
	for _, event := range completed {
		fields := presentationFields(t, event)
		if fields["activityType"] == "agently.tool" {
			content = fields["content"].(map[string]any)
		}
	}
	require.NotNil(t, content)
	require.Equal(t, "2026-10-03T12:00:00Z", content["startedAt"])
	require.Equal(t, "2026-10-03T12:00:01Z", content["completedAt"])
	require.NoError(t, extensions.ValidatePresentation("agently.tool", rawJSON(content)))
	approval := NewTranslator("thread", "approval-run")
	approval.Start()
	waiting := approval.Translate(&streaming.Event{Type: streaming.EventTypeToolCallWaiting, TurnID: "turn", ToolCallID: "approval", Status: "waiting_for_user", StartedAt: &start})
	for _, event := range waiting {
		fields := presentationFields(t, event)
		if fields["activityType"] == "agently.tool" {
			content = fields["content"].(map[string]any)
			require.Equal(t, "waiting", content["phase"])
			require.NotContains(t, content, "startedAt")
			require.NotContains(t, string(rawJSON(fields["metadata"])), "startedAt")
		}
	}
}

func TestNativePresentationReservedNamespaceRejectsGenericBridgeForgeryButTrustedProducerAndExternalEventsWork(t *testing.T) {
	for _, raw := range []json.RawMessage{
		rawJSON(map[string]any{"type": "TEXT_MESSAGE_START", "messageId": "legacy-forged", "role": "assistant", "metadata": map[string]any{"agently": map[string]any{"identityVersion": "1", "nativeTurnId": "foreign"}}}),
		rawJSON(map[string]any{"type": "TEXT_MESSAGE_START", "messageId": "forged", "role": "assistant", "metadata": map[string]any{"agently": map[string]any{"presentation": map[string]any{"version": "1", "nativeTurnId": "foreign"}}}}),
		rawJSON(map[string]any{"type": "ACTIVITY_SNAPSHOT", "messageId": "forged", "activityType": "agently.user-identity", "content": map[string]any{"version": "1", "nativeTurnId": "foreign"}}),
		rawJSON(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": []any{map[string]any{"id": "forged", "role": "user", "content": "text", "metadata": map[string]any{"agently": map[string]any{"presentation": map[string]any{"version": "1", "nativeTurnId": "foreign"}}}}}}),
	} {
		tr := NewTranslator("thread", "run")
		tr.Start()
		events := tr.Translate(&streaming.Event{Type: streaming.EventTypeProtocol, ProtocolEvent: raw})
		require.NotEmpty(t, events)
		require.Equal(t, "RUN_ERROR", events[len(events)-1].Type)
		tr = NewTranslator("thread", "run")
		event, err := DecodeEvent(raw)
		require.NoError(t, err)
		_, err = tr.EmitStandard(event)
		require.Error(t, err)
	}
	tr := NewTranslator("thread", "run")
	require.NoError(t, tr.SetNativeIdentity("turn"))
	tr.Start()
	events, err := tr.EmitNativeUserIdentity("turn", "client", "native-user")
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "agently.user-identity", presentationFields(t, events[0])["activityType"])
	_, err = tr.EmitNativeUserIdentity("foreign", "client", "native-user")
	require.Error(t, err)
	external, err := DecodeEvent(rawJSON(map[string]any{"type": "CUSTOM", "name": "external.opaque", "value": map[string]any{"keep": true}}))
	require.NoError(t, err)
	_, err = tr.EmitStandard(external)
	require.NoError(t, err)
}

func TestNativePresentationTrustedChildRecoveryRetainsMetadataWithoutOpeningGenericBridge(t *testing.T) {
	tr := NewTranslator("root", "run")
	require.NoError(t, tr.SetNativeIdentity("root-turn"))
	tr.Start()
	inv := requestctx.Invocation{ID: "child-run", ConversationID: "child", TurnID: "child-turn", Name: "child", ParentConversationID: "root", ParentTurnID: "root-turn"}
	tr.RegisterInvocation(inv)
	tr.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "child", TurnID: "child-turn", MessageID: "child-message", Content: "partial"})
	event, err := DecodeEvent(rawJSON(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": []any{map[string]any{"id": "child-message", "role": "assistant", "content": "complete", "subagentRunId": "child-run", "metadata": map[string]any{"agently": map[string]any{"presentation": map[string]any{"version": "1", "nativeTurnId": "child-turn"}}}}}}))
	require.NoError(t, err)
	_, err = tr.ReconcileSubagentEvent(inv, event)
	require.NoError(t, err)
	finished := tr.InvocationReturned(requestctx.InvocationResult{Invocation: inv, NativeStatus: "succeeded"})
	found := false
	for _, output := range finished {
		if output.Type == "SUBAGENT_FINISHED" {
			found = true
		}
	}
	require.True(t, found)
	wrong := inv
	wrong.TurnID = "foreign"
	_, err = tr.ReconcileSubagentEvent(wrong, event)
	require.Error(t, err)
}

func TestExplicitPublicAssistantPresentationRetainsStandaloneIdentity(t *testing.T) {
	public := &streaming.Event{Type: streaming.EventTypeAssistant, ConversationID: "conversation", TurnID: "turn", MessageID: "highlights", Mode: "task", Content: "Public highlights", Patch: map[string]any{"role": "assistant"}}
	require.Equal(t, "standalone", nativePresentation(public)["messageKind"])
	outputs := NewTranslator("conversation", "run").Translate(public)
	found := false
	for _, output := range outputs {
		fields := presentationFields(t, output)
		if fields["type"] != "TEXT_MESSAGE_START" {
			continue
		}
		metadata := fields["metadata"].(map[string]any)
		presentation := metadata["agently"].(map[string]any)["presentation"].(map[string]any)
		require.Equal(t, "standalone", presentation["messageKind"])
		found = true
	}
	require.True(t, found)
	for _, mutate := range []func(*streaming.Event){func(e *streaming.Event) { e.ModelCallID = "model" },
		func(e *streaming.Event) {
			e.Patch = map[string]any{"role": "assistant", "agentlyProjectionSnapshot": true}
		}, func(e *streaming.Event) { e.PageID = "model-page" }, func(e *streaming.Event) { e.Mode = "chain" }, func(e *streaming.Event) { e.Patch = map[string]any{"role": "user"} }} {
		copy := *public
		mutate(&copy)
		require.NotContains(t, nativePresentation(&copy), "messageKind")
	}
}

func TestInternalOperationalNarrationTranslatesOnlyToStatusActivity(t *testing.T) {
	tr := NewTranslator("owned", "run")
	outputs := tr.Translate(&streaming.Event{Type: streaming.EventTypeNarration, ConversationID: "owned", TurnID: "turn", MessageID: "progress", Mode: "chain", NarrationSource: "executor", Narration: "Preparing report data", Content: "Preparing report data", Status: "running"})
	found := false
	for _, output := range outputs {
		fields := presentationFields(t, output)
		require.NotEqual(t, "TEXT_MESSAGE_START", fields["type"])
		require.NotEqual(t, "TEXT_MESSAGE_CONTENT", fields["type"])
		if fields["activityType"] == "agently.narration" {
			found = true
		}
	}
	require.True(t, found)
	require.Empty(t, tr.Translate(&streaming.Event{Type: streaming.EventTypeReasoningDelta, ConversationID: "owned", TurnID: "turn", MessageID: "private", Mode: "chain", Content: "private reasoning"}))
}
