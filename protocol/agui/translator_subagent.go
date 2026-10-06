package agui

import (
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"strings"
)

type subagentState struct {
	invocation    requestctx.Invocation
	translator    *Translator
	started, done bool
}

func (t *Translator) subagent(invocation requestctx.Invocation) *subagentState {
	state := t.children[invocation.ID]
	if state == nil {
		state = &subagentState{invocation: invocation, translator: NewTranslator(t.threadID, invocation.ID)}
		if t.nativeTurnID != "" {
			state.translator.nativeTurnID = invocation.TurnID
		}
		state.translator.started = true
		t.children[invocation.ID] = state
	}
	return state
}
func (t *Translator) startSubagent(state *subagentState) []Event {
	if state.started || state.done {
		return nil
	}
	state.started = true
	inv := state.invocation
	fields := map[string]any{"type": "SUBAGENT_STARTED", "subagentRunId": inv.ID, "name": inv.Name, "metadata": map[string]any{"agently": map[string]any{"invocation": inv}}}
	if inv.Description != "" {
		fields["description"] = inv.Description
	}
	if inv.ParentToolCallID != "" {
		fields["parentToolCallId"] = t.toolID(inv.ParentTurnID, inv.ParentToolCallID)
	}
	if inv.ParentMessageID != "" {
		fields["parentMessageId"] = inv.ParentMessageID
	}
	if inv.ParentInvocationID != "" {
		fields["parentSubagentRunId"] = inv.ParentInvocationID
	}
	return []Event{standardEvent(fields)}
}
func attributedEvent(event Event, owner string) (Event, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return Event{}, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return Event{}, err
	}
	fields["subagentRunId"] = rawJSON(owner)
	data, err := json.Marshal(fields)
	if err != nil {
		return Event{}, err
	}
	return StandardEvent(data)
}

// TranslateSubagent consumes only a causally registered invocation's real native
// turn. Linking or queuing does not imply that the child has started/completed.
func (t *Translator) TranslateSubagent(invocation requestctx.Invocation, e *streaming.Event) []Event {
	return t.translateSubagent(invocation, e, false)
}
func (t *Translator) translateSubagent(invocation requestctx.Invocation, e *streaming.Event, trustedRecovery bool) []Event {
	if t.done || e == nil || invocation.ID == "" || e.ConversationID != invocation.ConversationID || e.TurnID != invocation.TurnID {
		return nil
	}
	if invocation.Detached {
		return nil
	}
	state := t.subagent(invocation)
	if state.done {
		return nil
	}
	if e.Type == streaming.EventTypeTurnQueued {
		return []Event{{Type: "CUSTOM", Name: "agently.subagent.queue", Value: map[string]any{"version": "1", "invocation": invocation, "status": "queued"}}}
	}
	if e.Type == streaming.EventTypeLinkedConversationAttached {
		return nil
	}
	output := t.Start()
	output = append(output, t.startSubagent(state)...)
	state.translator.messages = append([]json.RawMessage(nil), t.messages...)
	state.translator.seeded = t.seeded
	if t.seeded {
		if err := state.translator.refreshHistoricalTools(t.messages); err != nil {
			return append(output, t.Fail(err.Error(), "INVALID_AUTHORITATIVE_HISTORY")...)
		}
	}
	events := state.translator.translate(e, trustedRecovery)
	for _, event := range events {
		switch event.Type {
		case "RUN_STARTED":
			continue
		case "RUN_FINISHED":
			state.done = true
			outcome := map[string]any{"type": "success"}
			if e.Status == "waiting_for_user" || e.Status == "pending" {
				outcome["type"] = "suspended"
			}
			if e.Type == streaming.EventTypeTurnCanceled {
				output = append(output, standardEvent(map[string]any{"type": "SUBAGENT_ERROR", "subagentRunId": invocation.ID, "message": "Subagent invocation cancelled", "code": "CANCELLED"}))
			} else {
				output = append(output, standardEvent(map[string]any{"type": "SUBAGENT_FINISHED", "subagentRunId": invocation.ID, "outcome": outcome}))
			}
		case "RUN_ERROR":
			state.done = true
			output = append(output, standardEvent(map[string]any{"type": "SUBAGENT_ERROR", "subagentRunId": invocation.ID, "message": event.Message, "code": event.Code}))
		case "MESSAGES_SNAPSHOT":
			// The child copied the current parent graph before translation. Attribute
			// only messages this invocation authored; a full snapshot has no event owner.
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(event.Standard, &fields)
			var messages []map[string]json.RawMessage
			_ = json.Unmarshal(fields["messages"], &messages)
			for _, message := range messages {
				id := fieldString(message, "id")
				if _, ok := state.translator.texts[id]; ok {
					message["subagentRunId"] = rawJSON(invocation.ID)
				}
			}
			fields["messages"] = rawJSON(messages)
			raw, _ := json.Marshal(fields)
			snapshot, err := StandardEvent(raw)
			if err != nil {
				return t.Fail(err.Error(), "INVALID_SUBAGENT_EVENT")
			}
			output = append(output, snapshot)
		default:
			attributed, err := attributedEvent(event, invocation.ID)
			if err != nil {
				return t.Fail(err.Error(), "INVALID_SUBAGENT_EVENT")
			}
			output = append(output, attributed)
		}
	}
	for id, usage := range state.translator.usage {
		t.usage[invocation.ID+"/"+id] = usage
	}
	t.capture(output)
	return output
}

// ReconcileSubagentEvent is the native inspector's restricted recovery seam.
// Unlike the generic ProtocolEvent bridge it can retain already-authenticated
// presentation metadata in snapshots/results. Registration remains authoritative.
func (t *Translator) ReconcileSubagentEvent(invocation requestctx.Invocation, event *WireEvent) ([]Event, error) {
	state := t.children[invocation.ID]
	if state == nil || state.invocation != invocation || invocation.Detached {
		return nil, fmt.Errorf("trusted child recovery requires exact causal registration")
	}
	raw, err := EncodeEvent(event)
	if err != nil {
		return nil, err
	}
	var header struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &header)
	if header.Type != "MESSAGES_SNAPSHOT" && header.Type != "TOOL_CALL_RESULT" {
		return nil, fmt.Errorf("trusted child recovery only accepts messages and tool receipts")
	}
	return t.translateSubagent(invocation, &streaming.Event{Type: streaming.EventTypeProtocol, ConversationID: invocation.ConversationID, TurnID: invocation.TurnID, ProtocolEvent: raw}, true), nil
}

// InvocationReturned carries the real Query return status. Preset success has
// completion evidence even without a bus terminal. A queued return stays open.
func (t *Translator) InvocationReturned(result requestctx.InvocationResult) []Event {
	if t.done || result.Invocation.ID == "" {
		return nil
	}
	if result.Invocation.Detached {
		return nil
	}
	state := t.subagent(result.Invocation)
	if state.done {
		return nil
	}
	if result.NativeStatus == "queued" || result.NativeStatus == "running" || result.NativeStatus == "pending" {
		return nil
	}
	if result.NativeStatus == "" && result.Error == "" {
		return nil
	}
	output := t.Start()
	output = append(output, t.startSubagent(state)...)
	if result.NativeStatus == "succeeded" || result.NativeStatus == "completed" {
		if result.Content != "" && !state.translator.HasText() {
			events := state.translator.CompleteText(result.Invocation.ID+"/result", result.Content)
			for _, event := range events {
				tagged, err := attributedEvent(event, result.Invocation.ID)
				if err != nil {
					return t.Fail(err.Error(), "INVALID_SUBAGENT_EVENT")
				}
				output = append(output, tagged)
			}
		}
	}
	for _, event := range state.translator.closeOpen() {
		tagged, err := attributedEvent(event, result.Invocation.ID)
		if err != nil {
			return t.Fail(err.Error(), "INVALID_SUBAGENT_EVENT")
		}
		output = append(output, tagged)
	}
	state.done = true
	state.translator.done = true
	if result.NativeStatus == "waiting_for_user" {
		output = append(output, standardEvent(map[string]any{"type": "SUBAGENT_FINISHED", "subagentRunId": result.Invocation.ID, "outcome": map[string]any{"type": "suspended"}}))
	} else if result.Error != "" || result.NativeStatus == "failed" || result.NativeStatus == "canceled" || result.NativeStatus == "cancelled" {
		message := result.Error
		if message == "" {
			message = fmt.Sprintf("Subagent invocation %s", result.NativeStatus)
		}
		output = append(output, standardEvent(map[string]any{"type": "SUBAGENT_ERROR", "subagentRunId": result.Invocation.ID, "message": message, "code": strings.ToUpper(result.NativeStatus)}))
	} else {
		output = append(output, standardEvent(map[string]any{"type": "SUBAGENT_FINISHED", "subagentRunId": result.Invocation.ID}))
	}
	t.capture(output)
	return output
}
func (t *Translator) ActiveInvocations() []requestctx.Invocation {
	var output []requestctx.Invocation
	for _, state := range t.children {
		if state.started && !state.done {
			output = append(output, state.invocation)
		}
	}
	return output
}

// RegisterInvocation records causal linkage without claiming execution has
// started. Detached work belongs to an independent protocol run with parentRunId.
func (t *Translator) RegisterInvocation(invocation requestctx.Invocation) []Event {
	if t.done || invocation.ID == "" {
		return nil
	}
	if invocation.Detached {
		return []Event{{Type: "CUSTOM", Name: "agently.linked-run", Value: map[string]any{"version": "1", "parentRunId": t.runID, "invocation": invocation}}}
	}
	if t.nativeTurnID == "" {
		t.subagent(invocation)
		return nil
	} // legacy producer has no trusted native frame
	if err := t.validateInvocation(invocation); err != nil {
		return t.Fail(err.Error(), "INVALID_INVOCATION")
	}
	if prior := t.children[invocation.ID]; prior != nil {
		return nil
	}
	t.subagent(invocation)
	out := append(t.Start(), standardEvent(map[string]any{"type": "CUSTOM", "name": "agently.invocation", "value": map[string]any{"version": "1", "phase": "registered", "invocation": invocation}}))
	t.capture(out)
	return out
}

// RecoverableInvocations includes registered native work whose first event may
// not have reached the observer; it makes no claim that execution has started.
func (t *Translator) RecoverableInvocations() []requestctx.Invocation {
	var out []requestctx.Invocation
	for _, state := range t.children {
		if !state.done && !state.invocation.Detached {
			out = append(out, state.invocation)
		}
	}
	return out
}
func (t *Translator) validateInvocation(inv requestctx.Invocation) error {
	if inv.ID == "" || inv.ConversationID == "" || inv.TurnID == "" || inv.Name == "" || inv.Detached {
		return fmt.Errorf("invalid native invocation checkpoint")
	}
	if prior := t.children[inv.ID]; prior != nil && prior.invocation != inv {
		return fmt.Errorf("invocation checkpoint identity changed")
	}
	parentOK := t.nativeTurnID != "" && inv.ParentConversationID == t.threadID && inv.ParentTurnID == t.nativeTurnID && inv.ParentInvocationID == ""
	if !parentOK {
		for _, parent := range t.children {
			if inv.ParentInvocationID == parent.invocation.ID && inv.ParentConversationID == parent.invocation.ConversationID && inv.ParentTurnID == parent.invocation.TurnID {
				parentOK = true
				break
			}
		}
	}
	if !parentOK {
		return fmt.Errorf("invocation checkpoint does not match a recorded native parent")
	}
	if inv.ConversationID == inv.ParentConversationID && inv.TurnID == inv.ParentTurnID {
		return fmt.Errorf("invocation checkpoint cycles into parent")
	}
	return nil
}

// This private framework checkpoint is not a generic standard bridge surface.
// The wire codec still accepts arbitrary CUSTOM; only the runtime producer
// bridge reserves the framework ancestry namespace.
func reservedInvocationEvent(raw json.RawMessage) bool {
	var event struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Metadata struct {
			Agently struct {
				Invocation json.RawMessage `json:"invocation"`
			} `json:"agently"`
		} `json:"metadata"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return false
	}
	return event.Type == "CUSTOM" && event.Name == "agently.invocation" || len(event.Metadata.Agently.Invocation) > 0
}
