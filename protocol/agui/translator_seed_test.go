package agui

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

func seededToolHistory(alias string, receipt bool) []json.RawMessage {
	history := []json.RawMessage{rawJSON(map[string]any{"id": "assistant", "role": "assistant", "content": "draft", "subagentRunId": "owner", "toolCalls": []any{map[string]any{"id": alias, "type": "function", "function": map[string]any{"name": "lookup", "arguments": "{\"q\":\"question\"}"}, "metadata": map[string]any{"opaque": true}}}})}
	if receipt {
		history = append(history, rawJSON(map[string]any{"id": "result", "role": "tool", "toolCallId": alias, "content": "answer", "metadata": map[string]any{"receipt": true}}))
	}
	return history
}
func TestSeededToolLanesResumeWithoutDuplicatedArgumentsOrCalls(t *testing.T) {
	alias := ProtocolToolCallID("native/turn", "call")
	tr := NewTranslator("thread", "resumed")
	require.NoError(t, tr.SetNativeIdentity("native/turn"))
	require.NoError(t, tr.SeedMessages(seededToolHistory(alias, false)))
	require.Empty(t, tr.toolOrder, "historical lanes must not become current-run lanes")
	require.Equal(t, `{"q":"question"}`, tr.tools[alias].args)
	require.Equal(t, "assistant", tr.tools[alias].parent)
	require.Equal(t, "owner", tr.tools[alias].subagent)
	require.True(t, tr.tools[alias].ended)
	require.False(t, tr.HasToolResult(alias))
	require.Equal(t, []string{"RUN_STARTED"}, kinds(tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallStarted, TurnID: "native/turn", ToolCallID: "call", ToolName: "lookup", Arguments: map[string]any{"q": "question"}})))
	events := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, TurnID: "native/turn", ToolCallID: "call", ToolMessageID: "result", Status: "completed", Content: "answer", Arguments: map[string]any{"q": "question"}})
	require.Equal(t, []string{"TOOL_CALL_RESULT", "ACTIVITY_SNAPSHOT"}, kinds(events))
	assertToolExecutionPresentation(t, events[1], alias, "completed")
	require.Equal(t, alias, events[0].ToolCallID)
	require.True(t, tr.HasToolResult(alias))
	require.Empty(t, tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, TurnID: "native/turn", ToolCallID: "call", ToolMessageID: "result", Status: "completed", Content: "answer"}))
	require.Empty(t, tr.toolOrder)
	// A canonical replacement after resumption retains the aliased call and its
	// receipt rather than appending another call or repeating arguments.
	tr.Translate(&streaming.Event{Type: streaming.EventTypeTextDelta, MessageID: "assistant", Content: " suffix"})
	snapshot := tr.Translate(&streaming.Event{Type: streaming.EventTypeAssistant, MessageID: "assistant", Content: "replacement"})
	require.Equal(t, []string{"MESSAGES_SNAPSHOT"}, kinds(snapshot))
	var graph struct {
		Messages []struct {
			Role   string `json:"role"`
			CallID string `json:"toolCallId"`
			Calls  []struct {
				ID       string `json:"id"`
				Function struct {
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"toolCalls"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(snapshot[0].Standard, &graph))
	require.Len(t, graph.Messages[0].Calls, 1)
	require.Equal(t, alias, graph.Messages[0].Calls[0].ID)
	require.Equal(t, `{"q":"question"}`, graph.Messages[0].Calls[0].Function.Arguments)
	require.Equal(t, alias, graph.Messages[1].CallID)
}
func TestSeededReceiptsDeduplicateCompletionAndAllowHistoryReseed(t *testing.T) {
	tr := NewTranslator("thread", "resumed")
	history := seededToolHistory("call", true)
	// Receipts need not occur after their assistant in input serialization.
	history[0], history[1] = history[1], history[0]
	require.NoError(t, tr.SeedMessages(history))
	require.True(t, tr.HasToolResult("call"))
	tr.Start()
	require.Empty(t, tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, ToolCallID: "call", ToolMessageID: "result", Status: "completed", Content: "answer"}))
	require.NoError(t, tr.SeedMessages(history), "historical tools do not trip current-run reseed guard")
	require.False(t, tr.HasToolResult("missing"))
	require.NoError(t, tr.SeedMessages(seededToolHistory("other", false)))
	require.Nil(t, tr.tools["call"], "reseed replaces historical lane index")
	require.False(t, tr.HasToolResult("other"))
	// Validation failure is atomic for both the graph and tool index.
	before, _ := json.Marshal(tr.messages)
	require.Error(t, tr.SeedMessages([]json.RawMessage{json.RawMessage(`{"id":"invalid"}`)}))
	after, _ := json.Marshal(tr.messages)
	require.Equal(t, string(before), string(after))
	require.NotNil(t, tr.tools["other"])
	tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallStarted, ToolCallID: "new", ToolName: "lookup"})
	require.Error(t, tr.SeedMessages(history), "new current-run lanes keep the existing guard")
}
func TestSeededHistoryOwnsItsBytes(t *testing.T) {
	tr := NewTranslator("thread", "run")
	history := seededToolHistory("call", false)
	require.NoError(t, tr.SeedMessages(history))
	before := append([]byte(nil), tr.messages[0]...)
	history[0][0] = '!'
	require.Equal(t, before, []byte(tr.messages[0]))
}

func TestRestoredSnapshotIndexesHistoricalToolsAndCanonicalEditedArgs(t *testing.T) {
	alias := ProtocolToolCallID("native/turn", "call")
	initial := NewTranslator("thread", "resumed")
	require.NoError(t, initial.SetNativeIdentity("native/turn"))
	events := checkedEvents(t, initial.Start())
	events = append(events, rawJSON(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": seededToolHistory(alias, false)}))
	restored, err := RestoreTranslator("thread", "resumed", events)
	require.NoError(t, err)
	require.Empty(t, restored.toolOrder)
	require.True(t, restored.tools[alias].ended)
	require.False(t, restored.HasToolResult(alias))
	edited := seededToolHistory(alias, false)
	var message map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(edited[0], &message))
	var calls []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(message["toolCalls"], &calls))
	calls[0]["function"] = rawJSON(map[string]any{"name": "lookup", "arguments": `{"q":"approved edit"}`})
	message["toolCalls"] = rawJSON(calls)
	edited[0] = rawJSON(message)
	require.NoError(t, restored.captureStandard(rawJSON(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": edited}), true))
	require.Equal(t, `{"q":"approved edit"}`, restored.tools[alias].args)
	completion := &streaming.Event{Type: streaming.EventTypeToolCallCompleted, TurnID: "native/turn", ToolCallID: "call", ToolMessageID: "result", Status: "completed", Content: "answer", Arguments: map[string]any{"q": "question"}}
	completionEvents := restored.Translate(completion)
	require.Equal(t, []string{"TOOL_CALL_RESULT", "ACTIVITY_SNAPSHOT"}, kinds(completionEvents))
	assertToolExecutionPresentation(t, completionEvents[1], alias, "completed")
	journal := append(append([]json.RawMessage(nil), events...), rawJSON(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": edited}))
	journal = append(journal, checkedEvents(t, completionEvents)...)
	observed, err := RestoreTranslator("thread", "resumed", journal)
	require.NoError(t, err)
	require.True(t, observed.HasToolResult(alias), "current-run receipt journal restores dedup even without a new tool start")
	require.Empty(t, observed.Translate(completion))
	require.Equal(t, `{"q":"approved edit"}`, restored.tools[alias].args, "late original callback cannot overwrite canonical edited args")
	require.True(t, restored.HasToolResult(alias))
	require.Empty(t, restored.Translate(completion))
	// Restart from the receipt snapshot also suppresses an old completion.
	receiptSnapshot := append([]json.RawMessage(nil), restored.messages...)
	restartJournal := []json.RawMessage{events[0], rawJSON(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": receiptSnapshot})}
	restarted, err := RestoreTranslator("thread", "resumed", restartJournal)
	require.NoError(t, err)
	require.True(t, restarted.HasToolResult(alias))
	require.Empty(t, restarted.Translate(completion))
}

func TestHistoricalSnapshotRefreshPreservesCurrentLaneStateAndAncestry(t *testing.T) {
	tr := NewTranslator("thread", "run")
	require.NoError(t, tr.SeedMessages(seededToolHistory("historical", false)))
	tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallDelta, ToolCallID: "current", ToolName: "lookup", AssistantMessageID: "current-parent", Content: `{"q":`})
	state := tr.tools["current"]
	state.subagent = "actual-owner"
	require.False(t, state.ended)
	beforeOrder := append([]string(nil), tr.toolOrder...)
	canonical := append([]json.RawMessage(nil), tr.messages...)
	require.NoError(t, tr.captureStandard(rawJSON(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": canonical}), true))
	require.Same(t, state, tr.tools["current"])
	require.False(t, state.ended)
	require.Equal(t, "actual-owner", state.subagent)
	require.Equal(t, "current-parent", state.parent)
	require.Equal(t, beforeOrder, tr.toolOrder)
	require.True(t, tr.tools["historical"].ended)
}

func TestFirstResumedSubagentUsesHistoricalLaneWithoutNewToolStart(t *testing.T) {
	tr := NewTranslator("thread", "resumed")
	require.NoError(t, tr.SetNativeIdentity("parent-turn"))
	alias := ProtocolToolCallID("child-turn", "call")
	require.NoError(t, tr.SeedMessages(seededToolHistory(alias, false)))
	inv := requestctx.Invocation{ID: "owner", ConversationID: "child", TurnID: "child-turn", Name: "child", ParentConversationID: "thread", ParentTurnID: "parent-turn"}
	completion := &streaming.Event{Type: streaming.EventTypeToolCallCompleted, ConversationID: "child", TurnID: "child-turn", ToolCallID: "call", ToolMessageID: "result", Status: "completed", Content: "answer"}
	events := tr.TranslateSubagent(inv, completion)
	require.Equal(t, []string{"RUN_STARTED", "SUBAGENT_STARTED", "TOOL_CALL_RESULT", "ACTIVITY_SNAPSHOT"}, kinds(events))
	assertToolExecutionPresentation(t, events[3], alias, "completed")
	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(events[2].Standard, &result))
	require.Equal(t, alias, fieldString(result, "toolCallId"))
	require.Equal(t, "owner", fieldString(result, "subagentRunId"))
	require.Empty(t, tr.children["owner"].translator.toolOrder)
	require.True(t, tr.HasToolResult(alias))
	require.Empty(t, tr.TranslateSubagent(inv, completion))
	terminal := tr.TranslateSubagent(inv, &streaming.Event{Type: streaming.EventTypeTurnCompleted, ConversationID: "child", TurnID: "child-turn"})
	require.Equal(t, []string{"ACTIVITY_SNAPSHOT", "SUBAGENT_FINISHED"}, kinds(terminal))
	fields := presentationFields(t, terminal[0])
	require.Equal(t, "agently.turn", fields["activityType"])
	require.Equal(t, "completed", fields["content"].(map[string]any)["status"])
}
