package agui

import (
	"encoding/json"
	"fmt"
)

// SeedMessages installs the authoritative full message graph before native run
// events. Replacement snapshots preserve every role, tool, media part, metadata
// and opaque continuation value in this graph.
func (t *Translator) SeedMessages(messages []json.RawMessage) error {
	raw, err := json.Marshal(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": messages})
	if err != nil {
		return err
	}
	if err = ValidateEvent(raw); err != nil {
		return err
	}
	if len(t.textOrder) > 0 || len(t.toolOrder) > 0 {
		return fmt.Errorf("translator history must be seeded before content events")
	}
	tools, err := historicalToolStates(messages)
	if err != nil {
		return err
	}
	t.messages = make([]json.RawMessage, len(messages))
	for i, raw := range messages {
		t.messages[i] = append(json.RawMessage(nil), raw...)
	}
	t.tools = tools
	t.seeded = true
	return nil
}

// historicalToolStates treats graph entries as closed wire lanes. Receipts are
// joined in a second pass because history ordering does not establish identity.
func historicalToolStates(messages []json.RawMessage) (map[string]*toolState, error) {
	tools := map[string]*toolState{}
	for _, raw := range messages {
		var message struct {
			ID       string `json:"id"`
			Role     string `json:"role"`
			Subagent string `json:"subagentRunId"`
			Calls    []struct {
				ID       string `json:"id"`
				Function struct {
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"toolCalls"`
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			return nil, err
		}
		if message.Role != "assistant" {
			continue
		}
		for _, call := range message.Calls {
			tools[call.ID] = &toolState{parent: message.ID, args: call.Function.Arguments, subagent: message.Subagent, ended: true}
		}
	}
	for _, raw := range messages {
		var message struct {
			Role   string `json:"role"`
			CallID string `json:"toolCallId"`
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			return nil, err
		}
		if state := tools[message.CallID]; message.Role == "tool" && state != nil {
			state.result = true
		}
	}
	return tools, nil
}

// refreshHistoricalTools reconciles canonical history without reopening it or
// changing the ancestry/open state/order of current-run producer lanes.
func (t *Translator) refreshHistoricalTools(messages []json.RawMessage) error {
	history, err := historicalToolStates(messages)
	if err != nil {
		return err
	}
	for _, id := range t.toolOrder {
		if live := t.tools[id]; live != nil {
			if canonical := history[id]; canonical != nil {
				live.args = canonical.args
				live.result = live.result || canonical.result
			}
			history[id] = live
		}
	}
	t.tools = history
	return nil
}
func (t *Translator) message(id string) map[string]json.RawMessage {
	for _, raw := range t.messages {
		var message map[string]json.RawMessage
		if json.Unmarshal(raw, &message) != nil {
			continue
		}
		var existing string
		_ = json.Unmarshal(message["id"], &existing)
		if existing == id {
			return message
		}
	}
	return nil
}
func (t *Translator) saveMessage(id string, message map[string]json.RawMessage) {
	raw, _ := json.Marshal(message)
	for i, existing := range t.messages {
		var identity struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(existing, &identity)
		if identity.ID == id {
			t.messages[i] = raw
			return
		}
	}
	t.messages = append(t.messages, raw)
}
func rawJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func (t *Translator) capture(events []Event) {
	for _, event := range events {
		if event.Standard != nil {
			if err := t.captureStandard(event.Standard, !event.applied); err != nil && t.historyErr == nil {
				t.historyErr = err
			}
			continue
		}
		switch event.Type {
		case "TEXT_MESSAGE_START":
			message := t.message(event.MessageID)
			if message == nil {
				message = map[string]json.RawMessage{"id": rawJSON(event.MessageID), "role": rawJSON(event.Role), "content": rawJSON("")}
			}
			t.saveMessage(event.MessageID, message)
		case "TEXT_MESSAGE_CONTENT":
			message := t.message(event.MessageID)
			var content string
			_ = json.Unmarshal(message["content"], &content)
			message["content"] = rawJSON(content + event.Delta)
			t.saveMessage(event.MessageID, message)
		case "TOOL_CALL_START":
			message := t.message(event.ParentMessageID)
			if message == nil {
				message = map[string]json.RawMessage{"id": rawJSON(event.ParentMessageID), "role": rawJSON("assistant")}
			}
			var calls []map[string]any
			_ = json.Unmarshal(message["toolCalls"], &calls)
			calls = append(calls, map[string]any{"id": event.ToolCallID, "type": "function", "function": map[string]any{"name": event.ToolCallName, "arguments": ""}})
			message["toolCalls"] = rawJSON(calls)
			t.saveMessage(event.ParentMessageID, message)
		case "TOOL_CALL_ARGS":
			if state := t.tools[event.ToolCallID]; state != nil {
				message := t.message(state.parent)
				var calls []map[string]any
				_ = json.Unmarshal(message["toolCalls"], &calls)
				for _, call := range calls {
					if call["id"] == event.ToolCallID {
						function := call["function"].(map[string]any)
						args, _ := function["arguments"].(string)
						function["arguments"] = args + event.Delta
					}
				}
				message["toolCalls"] = rawJSON(calls)
				t.saveMessage(state.parent, message)
			}
		case "TOOL_CALL_RESULT":
			t.saveMessage(event.MessageID, map[string]json.RawMessage{"id": rawJSON(event.MessageID), "role": rawJSON("tool"), "toolCallId": rawJSON(event.ToolCallID), "content": rawJSON(event.Content)})
		}
	}
}
func (t *Translator) replaceText(id, content string) []Event {
	message := t.message(id)
	if message == nil {
		return t.Fail("Authoritative message history is not available for text replacement", "MISSING_AUTHORITATIVE_HISTORY")
	}
	message["content"] = rawJSON(content)
	t.saveMessage(id, message)
	if state := t.texts[id]; state != nil {
		state.content = content
	}
	return []Event{standardEvent(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": t.messages})}
}

// HasToolResult reports an authoritative receipt or emitted result by public
// protocol tool-call ID. Historical calls are not current-run open lanes.
func (t *Translator) HasToolResult(publicToolCallID string) bool {
	if state := t.tools[publicToolCallID]; state != nil && state.result {
		return true
	}
	for _, child := range t.children {
		if child.translator.HasToolResult(publicToolCallID) {
			return true
		}
	}
	return false
}
