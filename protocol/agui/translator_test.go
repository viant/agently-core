package agui

import (
	"encoding/json"
	"github.com/viant/agently-core/runtime/streaming"
	"reflect"
	"testing"
)

func kinds(events []Event) []string {
	var result []string
	for _, e := range events {
		result = append(result, e.Type)
	}
	return result
}
func TestTextLifecycleSnapshotsAndOffsets(t *testing.T) {
	tr := NewTranslator("thread", "run")
	offset := 0
	got := tr.Translate(&streaming.Event{Type: streaming.EventTypeTextDelta, MessageID: "a", Content: "hello", ContentOffset: &offset, EventSeq: 1})
	if !reflect.DeepEqual(kinds(got), []string{"RUN_STARTED", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT"}) {
		t.Fatal(got)
	}
	if events := tr.Translate(&streaming.Event{Type: streaming.EventTypeTextDelta, MessageID: "a", Content: "hello", ContentOffset: &offset, EventSeq: 2}); len(events) != 0 {
		t.Fatal(events)
	}
	if events := tr.Translate(&streaming.Event{Type: streaming.EventTypeAssistant, MessageID: "a", Content: "hello world"}); len(events) != 1 || events[0].Delta != " world" {
		t.Fatal(events)
	}
	got = tr.Translate(&streaming.Event{Type: streaming.EventTypeTurnCompleted})
	if !reflect.DeepEqual(kinds(got), []string{"TEXT_MESSAGE_END", "RUN_FINISHED"}) || got[1].Outcome.Type != "success" {
		t.Fatal(got)
	}
	if len(tr.Fail("late", "x")) != 0 || len(tr.Translate(&streaming.Event{Type: streaming.EventTypeTextDelta, Content: "late"})) != 0 {
		t.Fatal("events after terminal")
	}
}
func TestInterleavedToolsSeparateArgumentsAndExecution(t *testing.T) {
	tr := NewTranslator("t", "r")
	tr.Start()
	for _, id := range []string{"one", "two"} {
		got := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallStarted, ToolCallID: id, ToolName: "search", AssistantMessageID: "owner", ParentMessageID: "user"})
		if got[len(got)-1].ParentMessageID != "owner" {
			t.Fatal(got)
		}
	}
	got := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, ToolCallID: "two", Arguments: map[string]any{"q": "abc"}})
	if !reflect.DeepEqual(kinds(got), []string{"TOOL_CALL_ARGS", "TOOL_CALL_END"}) {
		t.Fatal(got)
	}
	got = tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, ToolCallID: "two", ToolMessageID: "result-two", ResponsePayload: map[string]any{"ok": true}, Status: "completed"})
	if !reflect.DeepEqual(kinds(got), []string{"TOOL_CALL_RESULT", "ACTIVITY_SNAPSHOT"}) || *got[0].Content != "{\"ok\":true}" {
		t.Fatal(got)
	}
	assertToolExecutionPresentation(t, got[1], "two", "completed")
	if got = tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, ToolCallID: "two", ToolMessageID: "result-two", Status: "completed"}); len(got) != 0 {
		t.Fatal(got)
	}
	got = tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallFailed, ToolCallID: "one", Error: "recoverable"})
	if !reflect.DeepEqual(kinds(got), []string{"TOOL_CALL_END", "TOOL_CALL_RESULT"}) || tr.Done() {
		t.Fatal(got)
	}
}
func TestCancellationAndError(t *testing.T) {
	for _, typ := range []streaming.EventType{streaming.EventTypeTurnCanceled, streaming.EventTypeTurnFailed} {
		tr := NewTranslator("t", "r")
		got := tr.Translate(&streaming.Event{Type: typ, Error: "broken"})
		if len(got) != 2 || got[0].Type != "RUN_STARTED" {
			t.Fatal(got)
		}
		if typ == streaming.EventTypeTurnCanceled && (got[1].Type != "RUN_FINISHED" || got[1].Outcome.Type != "cancelled") {
			t.Fatal(got)
		}
		if typ == streaming.EventTypeTurnFailed && (got[1].Type != "RUN_ERROR" || got[1].Message != "broken") {
			t.Fatal(got)
		}
		if !tr.Done() || len(tr.Finish("success")) != 0 {
			t.Fatal("terminal must be unique")
		}
	}
}
func TestEmptyToolResultRequiredContent(t *testing.T) {
	tr := NewTranslator("t", "r")
	got := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, ToolCallID: "call", ToolName: "empty", ToolMessageID: "result", Status: "completed"})
	if !reflect.DeepEqual(kinds(got), []string{"RUN_STARTED", "TEXT_MESSAGE_START", "TOOL_CALL_START", "TOOL_CALL_END", "TOOL_CALL_RESULT", "ACTIVITY_SNAPSHOT"}) {
		t.Fatal(kinds(got))
	}
	assertToolExecutionPresentation(t, got[len(got)-1], "call", "completed")
	b, err := json.Marshal(got[len(got)-2])
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	json.Unmarshal(b, &wire)
	if content, ok := wire["content"]; !ok || content != "" {
		t.Fatalf("required content missing: %s", b)
	}
}
func TestQueueSequenceStringAndNarration(t *testing.T) {
	tr := NewTranslator("t", "r")
	tr.Start()
	got := tr.Translate(&streaming.Event{Type: streaming.EventTypeTurnQueued, QueueSeq: 9007199254740993})
	if got[0].Value.(QueueValue).Sequence != "9007199254740993" {
		t.Fatal(got)
	}
	got = tr.Translate(&streaming.Event{Type: streaming.EventTypeNarration, MessageID: "a", Content: "working"})
	if got[0].Type != "CUSTOM" {
		t.Fatal(got)
	}
	got = tr.Translate(&streaming.Event{Type: streaming.EventTypeAssistant, MessageID: "a", Content: "answer"})
	if got[len(got)-1].Delta != "answer" {
		t.Fatal(got)
	}
}

func TestExecutionStartDoesNotDiscardLaterArguments(t *testing.T) {
	tr := NewTranslator("t", "r")
	tr.Start()
	got := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallStarted, ToolCallID: "call", ToolMessageID: "result", ToolName: "search", Status: "running"})
	if !reflect.DeepEqual(kinds(got), []string{"TEXT_MESSAGE_START", "TOOL_CALL_START", "ACTIVITY_SNAPSHOT"}) {
		t.Fatal(got)
	}
	assertToolExecutionPresentation(t, got[2], "call", "running")
	got = tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, ToolCallID: "call", Arguments: map[string]any{"q": "abc"}})
	if !reflect.DeepEqual(kinds(got), []string{"TOOL_CALL_ARGS", "TOOL_CALL_END"}) {
		t.Fatal(got)
	}
}

func TestUnsupportedTextReplacementFailsClearly(t *testing.T) {
	tr := NewTranslator("t", "r")
	tr.Translate(&streaming.Event{Type: streaming.EventTypeTextDelta, MessageID: "a", Content: "first"})
	got := tr.Translate(&streaming.Event{Type: streaming.EventTypeAssistant, MessageID: "a", Content: "replacement"})
	if !reflect.DeepEqual(kinds(got), []string{"TEXT_MESSAGE_END", "RUN_ERROR"}) || got[1].Code != "UNSUPPORTED_TEXT_REPLACEMENT" || !tr.Done() {
		t.Fatal(got)
	}
}

func TestToolEmptyProviderCompletionWaitsForHydratedArguments(t *testing.T) {
	tr := NewTranslator("t", "r")
	tr.Start()
	got := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, ToolCallID: "call", ToolName: "system/os:getEnv", AssistantMessageID: "assistant"})
	if !reflect.DeepEqual(kinds(got), []string{"TEXT_MESSAGE_START", "TOOL_CALL_START"}) {
		t.Fatal(got)
	}
	got = tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallStarted, ToolCallID: "call", ToolMessageID: "result", Status: "running", Arguments: map[string]any{"names": []string{"AGENTLY_AGUI_FIXTURE_VALUE"}}})
	if !reflect.DeepEqual(kinds(got), []string{"TOOL_CALL_ARGS", "TOOL_CALL_END", "ACTIVITY_SNAPSHOT"}) || got[0].Delta != `{"names":["AGENTLY_AGUI_FIXTURE_VALUE"]}` {
		t.Fatal(got)
	}
	assertToolExecutionPresentation(t, got[2], "call", "running")
	got = tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, ToolCallID: "call", ToolMessageID: "result", Status: "completed", Content: "value"})
	if !reflect.DeepEqual(kinds(got), []string{"TOOL_CALL_RESULT", "ACTIVITY_SNAPSHOT"}) {
		t.Fatal(got)
	}
	assertToolExecutionPresentation(t, got[1], "call", "completed")
}

func TestToolPartialArgumentsCompleteWithoutSnapshotDuplication(t *testing.T) {
	tr := NewTranslator("t", "r")
	tr.Start()
	tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallDelta, ToolCallID: "call", ToolName: "search", Content: `{"q":`})
	got := tr.Translate(&streaming.Event{Type: streaming.EventTypeToolCallCompleted, ToolCallID: "call", Arguments: map[string]any{"q": "abc"}})
	if !reflect.DeepEqual(kinds(got), []string{"TOOL_CALL_ARGS", "TOOL_CALL_END"}) || got[0].Delta != `"abc"}` {
		t.Fatal(got)
	}
}
