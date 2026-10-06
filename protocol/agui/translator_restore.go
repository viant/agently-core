package agui

import (
	"encoding/json"
	"fmt"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/viant/agently-core/runtime/requestctx"
)

// RestoreTranslator restores producer sequencing and its full message graph
// from a validated durable journal. It emits nothing and never runs an agent.
// Native sequence replay filtering still belongs to the worker's saved cursor.
func RestoreTranslator(threadID, runID string, events []json.RawMessage) (*Translator, error) {
	t := NewTranslator(threadID, runID)
	for _, raw := range events {
		if err := ValidateEvent(raw); err != nil {
			return nil, err
		}
		if err := t.captureStandard(raw, true); err != nil {
			return nil, err
		}
	}
	t.seeded = true
	for owner, state := range t.children {
		if state.done {
			continue
		}
		child := NewTranslator(threadID, owner)
		if t.nativeTurnID != "" {
			child.nativeTurnID = state.invocation.TurnID
		}
		child.started = true
		child.seeded = true
		for _, raw := range events {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			kind := fieldString(fields, "type")
			if kind == "SUBAGENT_STARTED" || kind == "SUBAGENT_FINISHED" || kind == "SUBAGENT_ERROR" {
				continue
			}
			if fieldString(fields, "subagentRunId") == owner {
				delete(fields, "subagentRunId")
				stripped, _ := json.Marshal(fields)
				if err := child.captureStandard(stripped, true); err != nil {
					return nil, err
				}
			} else if kind == "MESSAGES_SNAPSHOT" {
				if err := child.captureStandard(raw, true); err != nil {
					return nil, err
				}
			}
		}
		child.messages = append([]json.RawMessage(nil), t.messages...)
		state.translator = child
	}
	return t, nil
}
func fieldString(fields map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(fields[key], &value)
	return value
}
func mergeEventMetadata(message map[string]json.RawMessage, fields map[string]json.RawMessage) {
	if metadata := fields["metadata"]; metadata != nil {
		var before, after map[string]json.RawMessage
		_ = json.Unmarshal(message["metadata"], &before)
		_ = json.Unmarshal(metadata, &after)
		if before == nil {
			before = map[string]json.RawMessage{}
		}
		for k, v := range after {
			before[k] = v
		}
		message["metadata"] = rawJSON(before)
	}
	if owner := fields["subagentRunId"]; owner != nil {
		message["subagentRunId"] = owner
	}
}
func (t *Translator) captureStandard(raw json.RawMessage, boundary bool) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	kind := fieldString(fields, "type")
	owner := fieldString(fields, "subagentRunId")
	id := fieldString(fields, "messageId")
	callID := fieldString(fields, "toolCallId")
	if boundary {
		switch kind {
		case "RUN_STARTED":
			if fieldString(fields, "threadId") != t.threadID || fieldString(fields, "runId") != t.runID {
				return fmt.Errorf("journal run identity mismatch")
			}
			var identity struct {
				Agently struct {
					Version string `json:"identityVersion"`
					TurnID  string `json:"nativeTurnId"`
				} `json:"agently"`
			}
			_ = json.Unmarshal(fields["metadata"], &identity)
			if identity.Agently.Version == "1" {
				t.nativeTurnID = identity.Agently.TurnID
			}
			t.started = true
		case "SUBAGENT_STARTED":
			invocation := requestctx.Invocation{ID: owner, Name: fieldString(fields, "name")}
			var metadata struct {
				Agently struct {
					Invocation requestctx.Invocation `json:"invocation"`
				} `json:"agently"`
			}
			_ = json.Unmarshal(fields["metadata"], &metadata)
			if metadata.Agently.Invocation.ID != "" {
				invocation = metadata.Agently.Invocation
				if invocation.ID != owner || invocation.Name != fieldString(fields, "name") {
					return fmt.Errorf("subagent metadata does not match event identity")
				}
				if t.nativeTurnID != "" {
					if err := t.validateInvocation(invocation); err != nil {
						return err
					}
				}
			}
			state := t.subagent(invocation)
			state.started = true
		case "SUBAGENT_FINISHED", "SUBAGENT_ERROR":
			if state := t.children[owner]; state != nil {
				state.done = true
			}
		case "RUN_FINISHED", "RUN_ERROR":
			t.done = true
		case "STEP_STARTED":
			name := fieldString(fields, "stepName")
			key := strings.TrimPrefix(name, "model/")
			if owner != "" {
				key = owner + "/" + key
			}
			if t.steps[key] == nil {
				t.steps[key] = &stepState{name: name, subagent: owner}
				t.stepOrder = append(t.stepOrder, key)
			}
			t.steps[key].ended = false
			t.activeModels[owner] = key
			if owner == "" {
				t.activeModel = key
			}
		case "STEP_FINISHED":
			name := fieldString(fields, "stepName")
			key := strings.TrimPrefix(name, "model/")
			if owner != "" {
				key = owner + "/" + key
			}
			if t.steps[key] != nil {
				t.steps[key].ended = true
			}
			if t.activeModels[owner] == key {
				t.activeModels[owner] = ""
			}
			if owner == "" && t.activeModel == key {
				t.activeModel = ""
			}
		case "REASONING_START":
			t.reasoningSpans[id] = true
			t.currentReasoningSpan = id
			t.currentReasoningSpans[owner] = id
		case "REASONING_END":
			t.reasoningSpans[id] = false
		case "REASONING_MESSAGE_START":
			if t.reasonings[id] == nil {
				t.reasonings[id] = &reasoningState{span: t.currentReasoningSpans[owner], owner: t.activeModels[owner], subagent: owner}
				t.reasoningOrder = append(t.reasoningOrder, id)
			}
			t.reasonings[id].ended = false
		case "REASONING_MESSAGE_CONTENT":
			if t.reasonings[id] != nil {
				t.reasonings[id].content += fieldString(fields, "delta")
			}
		case "REASONING_MESSAGE_END":
			if t.reasonings[id] != nil {
				t.reasonings[id].ended = true
			}
		case "TEXT_MESSAGE_START":
			if t.texts[id] == nil {
				t.texts[id] = &textState{subagent: owner}
				t.textOrder = append(t.textOrder, id)
			}
			t.texts[id].ended = false
		case "TEXT_MESSAGE_CONTENT":
			if t.texts[id] != nil {
				t.texts[id].content += fieldString(fields, "delta")
			}
		case "TEXT_MESSAGE_END":
			if t.texts[id] != nil {
				t.texts[id].ended = true
			}
		case "TOOL_CALL_START":
			if t.tools[callID] == nil {
				t.tools[callID] = &toolState{parent: fieldString(fields, "parentMessageId"), subagent: owner}
			}
			current := false
			for _, id := range t.toolOrder {
				if id == callID {
					current = true
					break
				}
			}
			if !current {
				t.toolOrder = append(t.toolOrder, callID)
			}
			t.tools[callID].ended = false
		case "TOOL_CALL_ARGS":
			if t.tools[callID] != nil {
				t.tools[callID].args += fieldString(fields, "delta")
			}
		case "TOOL_CALL_END":
			if t.tools[callID] != nil {
				t.tools[callID].ended = true
			}
		case "TOOL_CALL_RESULT":
			if t.tools[callID] != nil {
				t.tools[callID].result = true
			}
		}
	}
	switch kind {
	case "CUSTOM":
		if fieldString(fields, "name") == "agently.invocation" {
			var checkpoint struct {
				Version    string                `json:"version"`
				Phase      string                `json:"phase"`
				Invocation requestctx.Invocation `json:"invocation"`
			}
			if err := json.Unmarshal(fields["value"], &checkpoint); err != nil {
				return err
			}
			if checkpoint.Version != "1" || checkpoint.Phase != "registered" {
				return fmt.Errorf("invalid invocation checkpoint version/phase")
			}
			if err := t.validateInvocation(checkpoint.Invocation); err != nil {
				return err
			}
			t.subagent(checkpoint.Invocation)
		}
		if fieldString(fields, "name") == "agently.usage" {
			var scope struct {
				Scope string `json:"scope"`
			}
			_ = json.Unmarshal(fields["value"], &scope)
			if scope.Scope != "" && scope.Scope != "model_call" {
				return nil
			}
			var envelope struct {
				Version string `json:"version"`
				CallID  string `json:"modelCallId"`
				Usage   struct {
					Provider  string `json:"provider"`
					Model     string `json:"model"`
					Input     int    `json:"inputTokens"`
					Output    int    `json:"outputTokens"`
					Cached    int    `json:"cachedInputTokens"`
					Reasoning int    `json:"reasoningTokens"`
					Write     int    `json:"cacheWriteInputTokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(fields["value"], &envelope) == nil && envelope.Version == "1" && envelope.CallID != "" {
				key := envelope.CallID
				if owner != "" {
					key = owner + "/" + key
				}
				t.usage[key] = usageState{provider: envelope.Usage.Provider, model: envelope.Usage.Model, input: envelope.Usage.Input, output: envelope.Usage.Output, cached: envelope.Usage.Cached, reasoning: envelope.Usage.Reasoning, write: envelope.Usage.Write}
			}
		}
	case "MESSAGES_SNAPSHOT":
		if err := json.Unmarshal(fields["messages"], &t.messages); err != nil {
			return err
		}
		if err := t.refreshHistoricalTools(t.messages); err != nil {
			return err
		}
		for _, raw := range t.messages {
			var m map[string]json.RawMessage
			_ = json.Unmarshal(raw, &m)
			mid := fieldString(m, "id")
			role := fieldString(m, "role")
			if role == "assistant" {
				if state := t.texts[mid]; state != nil {
					state.content = fieldString(m, "content")
				}
				var calls []map[string]json.RawMessage
				_ = json.Unmarshal(m["toolCalls"], &calls)
				for _, call := range calls {
					cid := fieldString(call, "id")
					var function map[string]json.RawMessage
					_ = json.Unmarshal(call["function"], &function)
					if state := t.tools[cid]; state != nil {
						state.args = fieldString(function, "arguments")
					}
				}
			}
			if role == "reasoning" {
				if state := t.reasonings[mid]; state != nil {
					state.content = fieldString(m, "content")
				}
			}
		}
	case "TEXT_MESSAGE_START", "REASONING_MESSAGE_START":
		m := t.message(id)
		if m == nil {
			role := fieldString(fields, "role")
			if kind == "REASONING_MESSAGE_START" {
				role = "reasoning"
			}
			m = map[string]json.RawMessage{"id": rawJSON(id), "role": rawJSON(role), "content": rawJSON("")}
		}
		mergeEventMetadata(m, fields)
		t.saveMessage(id, m)
	case "TEXT_MESSAGE_CONTENT", "REASONING_MESSAGE_CONTENT":
		m := t.message(id)
		if m == nil {
			return fmt.Errorf("content event references missing message %s", id)
		}
		content := fieldString(m, "content")
		m["content"] = rawJSON(content + fieldString(fields, "delta"))
		mergeEventMetadata(m, fields)
		t.saveMessage(id, m)
	case "TEXT_MESSAGE_CHUNK", "REASONING_MESSAGE_CHUNK":
		reasoning := kind == "REASONING_MESSAGE_CHUNK"
		last := &t.lastChunkText
		role := fieldString(fields, "role")
		if reasoning {
			last = &t.lastChunkReasoning
			role = "reasoning"
		}
		if id == "" {
			id = *last
		}
		if id == "" {
			return fmt.Errorf("chunk has no open message identity")
		}
		*last = id
		m := t.message(id)
		if m == nil {
			if role == "" {
				role = "assistant"
			}
			m = map[string]json.RawMessage{"id": rawJSON(id), "role": rawJSON(role), "content": rawJSON("")}
		}
		delta := fieldString(fields, "delta")
		m["content"] = rawJSON(fieldString(m, "content") + delta)
		mergeEventMetadata(m, fields)
		t.saveMessage(id, m)
		if boundary {
			if reasoning {
				if t.reasonings[id] == nil {
					t.reasonings[id] = &reasoningState{span: t.currentReasoningSpans[owner], owner: t.activeModels[owner], subagent: owner}
					t.reasoningOrder = append(t.reasoningOrder, id)
				}
				t.reasonings[id].content = fieldString(m, "content")
			} else {
				if t.texts[id] == nil {
					t.texts[id] = &textState{subagent: owner}
					t.textOrder = append(t.textOrder, id)
				}
				t.texts[id].content = fieldString(m, "content")
			}
		}
	case "TOOL_CALL_START", "TOOL_CALL_CHUNK":
		if kind == "TOOL_CALL_CHUNK" {
			if callID == "" {
				callID = t.lastChunkTool
			}
			if callID == "" {
				return fmt.Errorf("tool chunk has no open call identity")
			}
			t.lastChunkTool = callID
		}
		state := t.tools[callID]
		parent := fieldString(fields, "parentMessageId")
		if parent == "" && state != nil {
			parent = state.parent
		}
		if parent == "" {
			parent = t.runID + "/assistant"
		}
		m := t.message(parent)
		if m == nil {
			m = map[string]json.RawMessage{"id": rawJSON(parent), "role": rawJSON("assistant")}
		}
		var calls []map[string]json.RawMessage
		_ = json.Unmarshal(m["toolCalls"], &calls)
		found := false
		for _, call := range calls {
			if fieldString(call, "id") == callID {
				found = true
			}
		}
		if !found {
			calls = append(calls, map[string]json.RawMessage{"id": rawJSON(callID), "type": rawJSON("function"), "function": rawJSON(map[string]any{"name": fieldString(fields, "toolCallName"), "arguments": ""})})
		}
		m["toolCalls"] = rawJSON(calls)
		mergeEventMetadata(m, fields)
		t.saveMessage(parent, m)
		if state == nil {
			state = &toolState{parent: parent, subagent: owner}
			t.tools[callID] = state
			t.toolOrder = append(t.toolOrder, callID)
		}
		if kind == "TOOL_CALL_CHUNK" {
			delta := fieldString(fields, "delta")
			state.args += delta
			t.appendToolArguments(parent, callID, delta)
		}
	case "TOOL_CALL_ARGS":
		if state := t.tools[callID]; state != nil {
			t.appendToolArguments(state.parent, callID, fieldString(fields, "delta"))
		}
	case "TOOL_CALL_RESULT":
		m := map[string]json.RawMessage{"id": rawJSON(id), "role": rawJSON("tool"), "toolCallId": rawJSON(callID), "content": fields["content"]}
		if fields["error"] != nil {
			m["error"] = fields["error"]
		}
		mergeEventMetadata(m, fields)
		t.saveMessage(id, m)
	case "REASONING_ENCRYPTED_VALUE":
		entity := fieldString(fields, "entityId")
		if fieldString(fields, "subtype") == "tool-call" {
			for _, raw := range append([]json.RawMessage(nil), t.messages...) {
				var m map[string]json.RawMessage
				_ = json.Unmarshal(raw, &m)
				var calls []map[string]json.RawMessage
				_ = json.Unmarshal(m["toolCalls"], &calls)
				for _, call := range calls {
					if fieldString(call, "id") == entity {
						call["encryptedValue"] = fields["encryptedValue"]
					}
				}
				if calls != nil {
					m["toolCalls"] = rawJSON(calls)
					t.saveMessage(fieldString(m, "id"), m)
				}
			}
		} else {
			m := t.message(entity)
			if m == nil {
				return fmt.Errorf("encrypted reasoning references missing message %s", entity)
			}
			m["encryptedValue"] = fields["encryptedValue"]
			t.saveMessage(entity, m)
		}
	case "ACTIVITY_SNAPSHOT":
		if fieldString(fields, "activityType") == "agently.tool" {
			var content map[string]any
			_ = json.Unmarshal(fields["content"], &content)
			if content["version"] == "1" {
				id, _ := content["toolCallId"].(string)
				if state := t.tools[id]; state != nil {
					state.executionPresentation = content
				}
			}
		}
		m := t.message(id)
		replace := true
		_ = json.Unmarshal(fields["replace"], &replace)
		if m == nil || replace {
			oldMetadata := json.RawMessage(nil)
			if m != nil {
				oldMetadata = m["metadata"]
			}
			m = map[string]json.RawMessage{"id": rawJSON(id), "role": rawJSON("activity"), "activityType": fields["activityType"], "content": fields["content"]}
			if oldMetadata != nil {
				m["metadata"] = oldMetadata
			}
		}
		mergeEventMetadata(m, fields)
		t.saveMessage(id, m)
	case "ACTIVITY_DELTA":
		m := t.message(id)
		if m == nil {
			return fmt.Errorf("activity patch references missing message %s", id)
		}
		patch, err := jsonpatch.DecodePatch(fields["patch"])
		if err != nil {
			return err
		}
		updated, err := patch.ApplyWithOptions(m["content"], &jsonpatch.ApplyOptions{SupportNegativeIndices: false})
		if err != nil {
			return err
		}
		m["content"] = updated
		mergeEventMetadata(m, fields)
		t.saveMessage(id, m)
	}
	return nil
}
func (t *Translator) appendToolArguments(parent, id, delta string) {
	m := t.message(parent)
	var calls []map[string]json.RawMessage
	_ = json.Unmarshal(m["toolCalls"], &calls)
	for _, call := range calls {
		if fieldString(call, "id") == id {
			var f map[string]json.RawMessage
			_ = json.Unmarshal(call["function"], &f)
			f["arguments"] = rawJSON(fieldString(f, "arguments") + delta)
			call["function"] = rawJSON(f)
		}
	}
	m["toolCalls"] = rawJSON(calls)
	t.saveMessage(parent, m)
}
