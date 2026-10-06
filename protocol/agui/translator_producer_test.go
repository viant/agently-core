package agui

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/sdk/rendering"
	"reflect"
	"testing"
)

func checkedEvents(t *testing.T, events []Event) []json.RawMessage {
	t.Helper()
	var output []json.RawMessage
	for _, event := range events {
		raw, err := json.Marshal(event)
		require.NoError(t, err)
		require.NoError(t, ValidateEvent(raw))
		output = append(output, raw)
	}
	return output
}
func eventFields(t *testing.T, event Event) map[string]any {
	t.Helper()
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	return fields
}

func TestNativeModelReasoningAndUsageHaveStandardBoundariesAndNoDoubleCount(t *testing.T) {
	tr := NewTranslator("thread", "run")
	var output []Event
	output = append(output, tr.Translate(&streaming.Event{Type: streaming.EventTypeModelStarted, ModelCallID: "model-1", Provider: "openai", ModelName: "model"})...)
	output = append(output, tr.Translate(&streaming.Event{Type: streaming.EventTypeReasoningDelta, ReasoningMessageID: "reason", ReasoningSpanID: "span", Content: "think"})...)
	output = append(output, tr.Translate(&streaming.Event{Type: streaming.EventTypeReasoningEncrypted, ReasoningMessageID: "reason", ReasoningSpanID: "span", EncryptedValue: "opaque"})...)
	usage := &streaming.UsageSnapshot{Scope: "model_call", InputTokens: 10, OutputTokens: 5, CachedInputTokens: 3, ReasoningTokens: 2}
	output = append(output, tr.Translate(&streaming.Event{Type: streaming.EventTypeModelCompleted, ModelCallID: "model-1", Provider: "openai", ModelName: "model", Usage: usage})...)
	require.Empty(t, tr.Translate(&streaming.Event{Type: streaming.EventTypeModelCompleted, ModelCallID: "model-1", Provider: "openai", ModelName: "model", Usage: usage}))
	output = append(output, tr.Translate(&streaming.Event{Type: streaming.EventTypeModelStarted, ModelCallID: "model-2", Provider: "other", ModelName: "model-2"})...)
	output = append(output, tr.Translate(&streaming.Event{Type: streaming.EventTypeModelCompleted, ModelCallID: "model-2", Provider: "other", ModelName: "model-2", Usage: &streaming.UsageSnapshot{Scope: "model_call", InputTokens: 4, OutputTokens: 2}})...)
	output = append(output, tr.Translate(&streaming.Event{Type: streaming.EventTypeUsage, Usage: &streaming.UsageSnapshot{Scope: "conversation", InputTokens: 999}})...)
	output = append(output, tr.Finish("success")...)
	checkedEvents(t, output)
	require.Contains(t, kinds(output), "REASONING_START")
	require.Contains(t, kinds(output), "REASONING_MESSAGE_END")
	require.Contains(t, kinds(output), "REASONING_ENCRYPTED_VALUE")
	require.Contains(t, kinds(output), "STEP_FINISHED")
	fields := eventFields(t, output[len(output)-1])
	entries := fields["usage"].([]any)
	require.Len(t, entries, 2)
	require.EqualValues(t, 15, entries[0].(map[string]any)["totalTokens"])
	require.EqualValues(t, 6, entries[1].(map[string]any)["totalTokens"])
}

func TestSeededAuthoritativeTextReplacementPreservesFullMessageGraph(t *testing.T) {
	tr := NewTranslator("thread", "run")
	history := []json.RawMessage{json.RawMessage(`{"id":"user","role":"user","content":[{"type":"image","source":{"type":"file","value":"opaque","provider":"openai"}}],"metadata":{"keep":true}}`), json.RawMessage(`{"id":"prior-tool","role":"tool","toolCallId":"old-call","content":"old"}`)}
	require.NoError(t, tr.SeedMessages(history))
	tr.Translate(&streaming.Event{Type: streaming.EventTypeTextDelta, MessageID: "assistant", Content: "draft"})
	events := tr.Translate(&streaming.Event{Type: streaming.EventTypeAssistant, MessageID: "assistant", Content: "replacement"})
	require.Equal(t, []string{"MESSAGES_SNAPSHOT"}, kinds(events))
	checkedEvents(t, events)
	fields := eventFields(t, events[0])
	messages := fields["messages"].([]any)
	require.Len(t, messages, 3)
	require.Equal(t, "user", messages[0].(map[string]any)["id"])
	require.Equal(t, "old", messages[1].(map[string]any)["content"])
	require.Equal(t, "replacement", messages[2].(map[string]any)["content"])
}

func TestCausalSubagentsUseInvocationIDsAndQueuedOrDetachedDoNotPretendToRun(t *testing.T) {
	tr := NewTranslator("thread", "run")
	inv := requestctx.Invocation{ID: "child-turn", ConversationID: "child-conversation", TurnID: "child-turn", Name: "child", ParentConversationID: "thread", ParentTurnID: "parent"}
	require.Empty(t, tr.RegisterInvocation(inv))
	require.Empty(t, tr.InvocationReturned(requestctx.InvocationResult{Invocation: inv, NativeStatus: "queued", Content: "ignored"}))
	queued := tr.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeTurnQueued, ConversationID: inv.ConversationID, TurnID: inv.TurnID})
	require.Equal(t, []string{"CUSTOM"}, kinds(queued))
	output := tr.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeTurnStarted, ConversationID: inv.ConversationID, TurnID: inv.TurnID})
	require.Equal(t, []string{"RUN_STARTED", "SUBAGENT_STARTED", "ACTIVITY_SNAPSHOT"}, kinds(output))
	started := presentationFields(t, output[2])
	require.Equal(t, "agently.turn", started["activityType"])
	require.Equal(t, "running", started["content"].(map[string]any)["status"])
	output = append(output, tr.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: inv.ConversationID, TurnID: inv.TurnID, MessageID: "child-answer", Content: "answer"})...)
	output = append(output, tr.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeTurnCompleted, ConversationID: inv.ConversationID, TurnID: inv.TurnID})...)
	checkedEvents(t, output)
	require.False(t, tr.Done())
	require.Empty(t, tr.ActiveInvocations())
	require.False(t, tr.HasText())
	for _, event := range output {
		if event.Type == "TEXT_MESSAGE_CONTENT" {
			require.Equal(t, inv.ID, eventFields(t, event)["subagentRunId"])
		}
	}
	reused := inv
	reused.ID = "other-turn"
	reused.TurnID = reused.ID
	events := tr.InvocationReturned(requestctx.InvocationResult{Invocation: reused, NativeStatus: "succeeded", Content: "preset"})
	checkedEvents(t, events)
	require.Equal(t, "other-turn", eventFields(t, events[0])["subagentRunId"])
	detached := inv
	detached.ID = "detached"
	detached.Detached = true
	require.Equal(t, []string{"CUSTOM"}, kinds(tr.RegisterInvocation(detached)))
	require.Empty(t, tr.TranslateSubagent(detached, &streaming.Event{Type: streaming.EventTypeTurnStarted, ConversationID: detached.ConversationID, TurnID: detached.TurnID}))
}

func TestRestoreProducerKeepsOpenOffsetsArgumentsAndInvocationOwners(t *testing.T) {
	tr := NewTranslator("thread", "run")
	var journal []json.RawMessage
	appendEvents := func(events []Event) { journal = append(journal, checkedEvents(t, events)...) }
	appendEvents(tr.Translate(&streaming.Event{Type: streaming.EventTypeTextDelta, MessageID: "assistant", Content: "hello"}))
	appendEvents(tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallDelta, ToolCallID: "call", ToolName: "search", AssistantMessageID: "assistant", Content: `{"id":`}))
	restored, err := RestoreTranslator("thread", "run", journal)
	require.NoError(t, err)
	require.Empty(t, restored.Start())
	offset := 5
	events := restored.Translate(&streaming.Event{Type: streaming.EventTypeTextDelta, MessageID: "assistant", Content: " world", ContentOffset: &offset})
	require.Equal(t, []string{"TEXT_MESSAGE_CONTENT"}, kinds(events))
	events = restored.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, ToolCallID: "call", Arguments: map[string]any{"id": 7}})
	require.Equal(t, []string{"TOOL_CALL_ARGS", "TOOL_CALL_END"}, kinds(events))
	require.Equal(t, "7}", events[0].Delta)
	require.Error(t, restored.SeedMessages(nil), "history cannot be replaced after restoring open content")
}

func TestNativeRenderedContentIsActivityNotAssistantJSON(t *testing.T) {
	tr := NewTranslator("thread", "run")
	rendered := &rendering.RenderedContent{Parts: []*rendering.RenderedContentPart{{Kind: "data", Data: &rendering.RenderedData{ID: "data", Payload: json.RawMessage(`{"values":[1]}`)}}}}
	events := tr.Translate(&streaming.Event{Type: streaming.EventTypeAssistant, MessageID: "message", Content: "plain", RenderedContent: rendered})
	checkedEvents(t, events)
	require.True(t, reflect.DeepEqual(kinds(events), []string{"RUN_STARTED", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "ACTIVITY_SNAPSHOT"}))
	require.Equal(t, "plain", events[2].Delta)
}
