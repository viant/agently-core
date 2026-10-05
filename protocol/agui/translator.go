package agui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/runtime/streaming"
)

type textState struct {
	subagent string
	content  string
	ended    bool
}
type toolState struct {
	executionPresentation map[string]any
	subagent              string
	args                  string
	ended, result         bool
	parent                string
}

// Translator is run-scoped and must be used by one ordered stream consumer.
// Correlation and authorization of internal turn events belongs to the handler.
type Translator struct {
	nativeTurnID          string
	threadID, runID       string
	started, done         bool
	texts                 map[string]*textState
	tools                 map[string]*toolState
	textOrder, toolOrder  []string
	seen                  map[int64]bool
	seeded                bool
	messages              []json.RawMessage
	reasonings            map[string]*reasoningState
	reasoningOrder        []string
	reasoningSpans        map[string]bool
	steps                 map[string]*stepState
	stepOrder             []string
	activeModel           string
	usage                 map[string]usageState
	historyErr            error
	currentReasoningSpan  string
	lastChunkText         string
	lastChunkTool         string
	lastChunkReasoning    string
	children              map[string]*subagentState
	activeModels          map[string]string
	currentReasoningSpans map[string]string
}

func NewTranslator(threadID, runID string) *Translator {
	return &Translator{threadID: threadID, runID: runID, texts: map[string]*textState{}, tools: map[string]*toolState{}, seen: map[int64]bool{}, reasonings: map[string]*reasoningState{}, reasoningSpans: map[string]bool{}, steps: map[string]*stepState{}, usage: map[string]usageState{}, children: map[string]*subagentState{}, activeModels: map[string]string{}, currentReasoningSpans: map[string]string{}}
}
func (t *Translator) Done() bool { return t.done }
func (t *Translator) Start() []Event {
	if t.started || t.done {
		return nil
	}
	t.started = true
	if t.nativeTurnID != "" {
		return []Event{standardEvent(map[string]any{"type": "RUN_STARTED", "threadId": t.threadID, "runId": t.runID, "protocolVersion": ProtocolVersion, "metadata": map[string]any{"agently": map[string]any{"identityVersion": "1", "nativeTurnId": t.nativeTurnID}}})}
	}
	return []Event{{Type: "RUN_STARTED", ThreadID: t.threadID, RunID: t.runID, ProtocolVersion: ProtocolVersion}}
}
func (t *Translator) closeOpen() (out []Event) {
	out = append(out, t.closeReasoning("")...)
	for _, id := range t.textOrder {
		if !t.texts[id].ended {
			t.texts[id].ended = true
			out = append(out, scopedBoundary("TEXT_MESSAGE_END", "messageId", id, t.texts[id].subagent))
		}
	}
	for _, id := range t.toolOrder {
		if !t.tools[id].ended {
			t.tools[id].ended = true
			out = append(out, scopedBoundary("TOOL_CALL_END", "toolCallId", id, t.tools[id].subagent))
		}
	}
	out = append(out, t.closeSteps()...)
	return
}
func (t *Translator) Finish(outcome string) []Event {
	if t.done {
		return nil
	}
	out := t.Start()
	out = append(out, t.closeOpen()...)
	t.done = true
	if outcome != "cancelled" {
		outcome = "success"
	}
	if usage := t.runUsage(); len(usage) > 0 {
		return append(out, standardEvent(map[string]any{"type": "RUN_FINISHED", "threadId": t.threadID, "runId": t.runID, "outcome": map[string]any{"type": outcome}, "usage": usage}))
	}
	return append(out, Event{Type: "RUN_FINISHED", ThreadID: t.threadID, RunID: t.runID, Outcome: &Outcome{Type: outcome}})
}
func (t *Translator) Fail(message, code string) []Event {
	if t.done {
		return nil
	}
	out := t.Start()
	out = append(out, t.closeOpen()...)
	t.done = true
	if message == "" {
		message = "Agent run failed"
	}
	return append(out, Event{Type: "RUN_ERROR", Message: message, Code: code})
}
func (t *Translator) text(id, content string, snapshot bool, offset *int) []Event {
	if id == "" {
		id = t.runID + "/assistant"
	}
	s := t.texts[id]
	var out []Event
	if s == nil {
		s = &textState{}
		if message := t.message(id); message != nil {
			_ = json.Unmarshal(message["content"], &s.content)
		}
		t.texts[id] = s
		t.textOrder = append(t.textOrder, id)
		out = append(out, Event{Type: "TEXT_MESSAGE_START", MessageID: id, Role: "assistant"})
	}
	if s.ended {
		if snapshot && t.seeded && content != s.content {
			return t.replaceText(id, content)
		}
		return nil
	}
	delta := content
	if snapshot {
		if strings.HasPrefix(content, s.content) {
			delta = content[len(s.content):]
		} else {
			if t.seeded {
				return append(out, t.replaceText(id, content)...)
			}
			return t.Fail("Assistant text replacement is unsupported by this chat profile", "UNSUPPORTED_TEXT_REPLACEMENT")
		}
	} else if offset != nil {
		if *offset < 0 || *offset > len(s.content) {
			return t.Fail("Assistant text offset cannot be reconciled", "UNSUPPORTED_TEXT_OFFSET")
		}
		overlap := len(s.content) - *offset
		if overlap >= len(content) {
			if s.content[*offset:*offset+len(content)] != content {
				return t.Fail("Assistant text offset conflicts with emitted content", "UNSUPPORTED_TEXT_OFFSET")
			}
			return out
		}
		if overlap > 0 {
			if s.content[*offset:] != content[:overlap] {
				return t.Fail("Assistant text offset conflicts with emitted content", "UNSUPPORTED_TEXT_OFFSET")
			}
			delta = content[overlap:]
		}
	}
	if delta != "" {
		s.content += delta
		out = append(out, Event{Type: "TEXT_MESSAGE_CONTENT", MessageID: id, Delta: delta})
	}
	return out
}
func (t *Translator) tool(e *streaming.Event) (out []Event) {
	id := e.ToolCallID
	if id == "" && e.ToolMessageID == "" {
		id = e.ID
	}
	if id == "" {
		return nil
	}
	id = t.toolID(e.TurnID, id)
	s := t.tools[id]
	if s == nil {
		parent := e.AssistantMessageID
		if parent == "" && e.ToolMessageID == "" {
			parent = e.MessageID
		}
		if parent == "" {
			parent = t.runID + "/assistant"
		}
		name := e.ToolName
		if name == "" {
			name = "unknown"
		}
		s = &toolState{parent: parent}
		t.tools[id] = s
		t.toolOrder = append(t.toolOrder, id)
		if t.texts[parent] == nil {
			out = append(out, t.text(parent, "", true, nil)...)
		}
		out = append(out, Event{Type: "TOOL_CALL_START", ToolCallID: id, ToolCallName: name, ParentMessageID: parent})
	}
	execution := e.ToolMessageID != "" || e.ResponsePayload != nil || e.CompletedAt != nil || e.Status == "completed" || e.Status == "failed" || e.Status == "canceled" || e.Type == streaming.EventTypeToolCallFailed || e.Type == streaming.EventTypeToolCallCanceled
	// Native approval queues persist a presentation payload before execution.
	// Its message/payload identity does not make it a completed tool result.
	if ToolStatusPending(e.Status) {
		execution = false
	}
	if !s.ended {
		args := ""
		if e.Type == streaming.EventTypeToolCallDelta && !execution && e.OperationID == "" {
			args = e.Content
		}
		if e.Arguments != nil {
			b, err := json.Marshal(e.Arguments)
			if err != nil {
				return t.Fail("Tool arguments cannot be serialized", "INVALID_TOOL_ARGUMENTS")
			}
			if s.args == "" {
				args = string(b)
			} else if !json.Valid([]byte(s.args)) {
				if !strings.HasPrefix(string(b), s.args) {
					return t.Fail("Tool argument snapshot cannot reconcile emitted deltas", "UNSUPPORTED_TOOL_ARGUMENTS")
				}
				args = string(b)[len(s.args):]
			}
		}
		if args != "" {
			s.args += args
			out = append(out, Event{Type: "TOOL_CALL_ARGS", ToolCallID: id, Delta: args})
		}
		if (e.Type == streaming.EventTypeToolCallCompleted && (s.args != "" || e.Arguments != nil || execution)) || (execution && e.Arguments != nil) || e.Type == streaming.EventTypeToolCallFailed || e.Type == streaming.EventTypeToolCallCanceled {
			s.ended = true
			out = append(out, Event{Type: "TOOL_CALL_END", ToolCallID: id})
		}
	}
	if execution && (e.Type == streaming.EventTypeToolCallCompleted || e.Type == streaming.EventTypeToolCallFailed || e.Type == streaming.EventTypeToolCallCanceled) && !s.result {
		content := e.Content
		if e.ResponsePayload != nil {
			if b, err := json.Marshal(e.ResponsePayload); err == nil {
				content = string(b)
			}
		}
		if e.Error != "" {
			b, _ := json.Marshal(map[string]any{"error": e.Error, "content": content})
			content = string(b)
		}
		mid := e.ToolMessageID
		if mid == "" {
			mid = id + "/result"
		}
		s.result = true
		out = append(out, Event{Type: "TOOL_CALL_RESULT", ToolCallID: id, MessageID: mid, Content: &content})
	}
	return
}
func (t *Translator) Translate(e *streaming.Event) []Event {
	return t.translate(e, false)
}
func (t *Translator) translate(e *streaming.Event, trustedRecovery bool) []Event {
	if t.done || e == nil {
		return nil
	}
	if e.EventSeq > 0 {
		if t.seen[e.EventSeq] {
			return nil
		}
		t.seen[e.EventSeq] = true
	}
	out := t.Start()
	if e.Type != streaming.EventTypeProtocol {
		if err := extensions.ValidatePresentationMetadata(rawJSON(nativePresentation(e))); err != nil {
			return append(out, t.Fail("Native presentation metadata is invalid", "INVALID_PRESENTATION")...)
		}
	}
	var events []Event
	id := e.AssistantMessageID
	if id == "" {
		id = e.MessageID
	}
	if id == "" {
		id = e.ID
	}
	switch e.Type {
	case streaming.EventTypeTurnStarted:
		events = t.turnPresentation(e)
	case streaming.EventTypeTurnCompleted:
		events = append(t.turnPresentation(e), t.Finish("success")...)
	case streaming.EventTypeTurnCanceled:
		events = append(t.turnPresentation(e), t.Finish("cancelled")...)
	case streaming.EventTypeTurnFailed, streaming.EventTypeError:
		if e.Type == streaming.EventTypeTurnFailed {
			events = t.turnPresentation(e)
		}
		events = append(events, t.Fail(e.Error, "AGENT_ERROR")...)
	case streaming.EventTypeTextDelta:
		events = t.text(id, e.Content, e.ContentMode == "snapshot", e.ContentOffset)
	case streaming.EventTypeAssistant:
		if role, ok := e.Patch["role"].(string); !ok || role == "assistant" {
			events = t.text(id, e.Content, true, nil)
		}
	case streaming.EventTypeItemCompleted:
		events = append(events, t.closeReasoningItem(e.ReasoningMessageID)...)
		if s := t.texts[id]; s != nil && !s.ended {
			s.ended = true
			events = append(events, Event{Type: "TEXT_MESSAGE_END", MessageID: id})
		}
	case streaming.EventTypeToolCallStarted, streaming.EventTypeToolCallDelta, streaming.EventTypeToolCallCompleted, streaming.EventTypeToolCallFailed, streaming.EventTypeToolCallCanceled:
		knownReceipt := t.HasToolResult(t.toolID(e.TurnID, e.ToolCallID))
		events = t.tool(e)
		if !knownReceipt {
			events = append(events, t.toolExecutionPresentation(e)...)
		}
	case streaming.EventTypeReasoningDelta, streaming.EventTypeReasoningEncrypted:
		events = t.reasoning(e)
	case streaming.EventTypeModelStarted:
		events = t.modelStep(e, false)
	case streaming.EventTypeModelCompleted:
		events = t.modelStep(e, true)
	case streaming.EventTypeUsage:
		if id := modelIdentity(e); t.recordUsage(e, id) {
			events = []Event{t.usageEvent(id)}
		}
		events = append(events, t.aggregateUsagePresentation(e)...)
	case streaming.EventTypeToolFeedActive, streaming.EventTypeToolFeedInactive:
		events = t.feedPresentation(e)
	case streaming.EventTypeToolCallsPlanned:
		events = t.plannedToolsPresentation(e)
	case streaming.EventTypePlannerSelected, streaming.EventTypePlannerOutput, streaming.EventTypePlannerValidated, streaming.EventTypePlannerFailed:
		events = t.plannerPresentation(e)
	case streaming.EventTypeProtocol:
		if !trustedRecovery && reservedNativePresentationEvent(e.ProtocolEvent) {
			return append(out, t.Fail("Native presentation identity belongs to trusted producers", "INVALID_PROTOCOL_EVENT")...)
		}
		if reservedInvocationEvent(e.ProtocolEvent) {
			return append(out, t.Fail("Native invocation ancestry belongs to trusted registration", "INVALID_PROTOCOL_EVENT")...)
		}
		event, err := StandardEvent(e.ProtocolEvent)
		if err != nil {
			return append(out, t.Fail(err.Error(), "INVALID_PROTOCOL_EVENT")...)
		}
		if event.Type == "RUN_STARTED" || event.Type == "RUN_FINISHED" || event.Type == "RUN_ERROR" {
			return append(out, t.Fail("Runtime bridge cannot control run lifecycle", "INVALID_PROTOCOL_EVENT")...)
		}
		events = []Event{event}
	case streaming.EventTypeLinkedConversationAttached:
		events = []Event{{Type: "CUSTOM", Name: "agently.linked-conversation", Value: map[string]any{"version": "1", "conversationId": e.LinkedConversationID, "agentId": e.LinkedConversationAgentID, "title": e.LinkedConversationTitle, "parentToolCallId": t.toolID(e.TurnID, e.ToolCallID), "parentMessageId": id}}}
	case streaming.EventTypeTurnQueued:
		events = []Event{{Type: "CUSTOM", Name: "agently.queue", Value: QueueValue{Version: "1", TurnID: e.TurnID, Sequence: strconv.Itoa(e.QueueSeq), Origin: e.QueueOrigin, StartedByMessageID: e.StartedByMessageID}}}
		events = append(events, t.turnPresentation(e)...)
	case streaming.EventTypeNarration, streaming.EventTypeToolCallWaiting:
		events = []Event{{Type: "CUSTOM", Name: "agently.progress", Value: ProgressValue{Version: "1", Kind: string(e.Type), MessageID: id, ToolCallID: t.toolID(e.TurnID, e.ToolCallID), Status: e.Status, Content: e.Content, Source: e.NarrationSource}}}
		if e.Type == streaming.EventTypeNarration {
			events = append(events, t.narrationPresentation(e)...)
		} else {
			events = append(events, t.toolExecutionPresentation(e)...)
		}
	}
	if !t.done {
		events = append(events, t.renderedActivity(e)...)
	}
	for i := range events {
		if !e.CreatedAt.IsZero() {
			events[i].Timestamp = e.CreatedAt.UnixMilli()
		}
		events[i] = annotateNativePresentation(events[i], e)
	}
	t.capture(events)
	if t.historyErr != nil {
		return append(out, t.Fail(t.historyErr.Error(), "INVALID_NATIVE_SOURCE")...)
	}
	return append(out, events...)
}

// CompleteText is useful for synchronous preset answers without bus text events.
func (t *Translator) CompleteText(id, content string) []Event {
	if t.done {
		return nil
	}
	out := t.Start()
	events := t.text(id, content, true, nil)
	t.capture(events)
	return append(out, events...)
}
func (t *Translator) String() string { return fmt.Sprintf("AG-UI run %s/%s", t.threadID, t.runID) }

// HasText reports whether the stream has emitted assistant text. A handler can
// use it to avoid repeating a synchronous answer under a second message ID.
func (t *Translator) HasText() bool {
	for _, s := range t.texts {
		if s.content != "" && s.subagent == "" {
			return true
		}
	}
	return false
}
