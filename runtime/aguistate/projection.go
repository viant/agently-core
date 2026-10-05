// Package aguistate projects normalized AG-UI events into durable protocol
// messages and shared state. It never derives history from presentation rows.
package aguistate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/protocol/agui"
)

type Object map[string]any

// Projection retains opaque metadata, source objects, and encrypted values in
// the same messages that will be used for subsequent protocol runs.
type Projection struct {
	State             any                          `json:"state"`
	Messages          []Object                     `json:"messages"`
	Errored           bool                         `json:"errored"`
	ReasoningMessages map[string]string            `json:"reasoningMessages"`
	Owners            map[string]map[string]string `json:"owners"`
	Running           bool                         `json:"running"`
	ThreadID          string                       `json:"threadId"`
	RunID             string                       `json:"runId"`
	Text              map[string]string            `json:"text"`
	Tools             map[string]string            `json:"tools"`
	ToolParents       map[string]string            `json:"toolParents"`
	Steps             map[string]bool              `json:"steps"`
	Reasoning         map[string]string            `json:"reasoning"`
	Subagents         map[string]bool              `json:"subagents"`
	Chunks            map[string]chunk             `json:"chunks"`
	ChunkOrder        []string                     `json:"chunkOrder"`
}
type chunk struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Fields Object `json:"fields"`
}

func New(state, messages json.RawMessage) (*Projection, error) {
	p := &Projection{State: Object{}, Messages: []Object{}, Text: map[string]string{}, Tools: map[string]string{}, ToolParents: map[string]string{}, Steps: map[string]bool{}, Reasoning: map[string]string{}, Subagents: map[string]bool{}, Chunks: map[string]chunk{}}
	if len(state) > 0 {
		if err := decode(state, &p.State); err != nil {
			return nil, err
		}
	}
	if len(messages) > 0 {
		if err := decode(messages, &p.Messages); err != nil {
			return nil, err
		}
	}
	for _, m := range p.Messages {
		data, _ := json.Marshal(m)
		if err := agui.ValidateMessage(data); err != nil {
			return nil, err
		}
		calls, _ := m["toolCalls"].([]any)
		for _, call := range calls {
			c := object(call)
			if c != nil {
				p.ToolParents[field(c, "id")] = field(m, "id")
			}
		}
	}
	return p, nil
}
func decode(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	return d.Decode(target)
}
func (p *Projection) Snapshot() (json.RawMessage, json.RawMessage, error) {
	s, err := json.Marshal(p.State)
	if err != nil {
		return nil, nil, err
	}
	m, err := json.Marshal(p.Messages)
	return s, m, err
}

// Apply validates, normalizes chunk events, and updates atomically. A failed
// patch or invalid sequence cannot partially alter the accepted projection.
func (p *Projection) Apply(raw json.RawMessage) ([]json.RawMessage, error) {
	if err := agui.ValidateEvent(raw); err != nil {
		return nil, err
	}
	var event Object
	if err := decode(raw, &event); err != nil {
		return nil, err
	}
	serialized, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var next Projection
	if err = decode(serialized, &next); err != nil {
		return nil, err
	}
	events, err := next.normalize(event)
	if err != nil {
		return nil, err
	}
	var result []json.RawMessage
	for _, e := range events {
		b, eErr := json.Marshal(e)
		if eErr != nil {
			return nil, eErr
		}
		if eErr = agui.ValidateEvent(b); eErr != nil {
			return nil, eErr
		}
		if eErr = next.apply(e); eErr != nil {
			return nil, eErr
		}
		result = append(result, b)
	}
	*p = next
	return result, nil
}
func field(o Object, key string) string { s, _ := o[key].(string); return s }
func object(v any) Object {
	switch o := v.(type) {
	case Object:
		return o
	case map[string]any:
		return o
	}
	return nil
}
func mergeMetadata(target, e Object) {
	if target == nil {
		return
	}
	incoming := object(e["metadata"])
	if incoming == nil {
		return
	}
	metadata := object(target["metadata"])
	if metadata == nil {
		metadata = Object{}
	}
	for k, v := range incoming {
		metadata[k] = v
	}
	target["metadata"] = metadata
}
func lane(o Object) string { return field(o, "subagentRunId") }

// An absent attribution and the legal opaque empty ID are distinct owners.
func ownerKey(o Object) string {
	if _, present := o["subagentRunId"]; present {
		return "tag:" + field(o, "subagentRunId")
	}
	return "parent:"
}

// Active-lane views retain the raw optional tag used by journal producers.
// The separate Owners map preserves absence versus a present empty string.
func attribution(owner string) string {
	if strings.HasPrefix(owner, "tag:") {
		return strings.TrimPrefix(owner, "tag:")
	}
	return ""
}
func stepKey(o Object) string {
	b, _ := json.Marshal([]string{ownerKey(o), field(o, "stepName")})
	return string(b)
}
func (p *Projection) message(id string) Object {
	for _, m := range p.Messages {
		if field(m, "id") == id {
			return m
		}
	}
	return nil
}
func inherit(dst, src Object, keys ...string) {
	for _, key := range keys {
		if v, ok := src[key]; ok {
			dst[key] = v
		}
	}
}
func (p *Projection) tool(id string) (Object, error) {
	for _, parent := range p.Messages {
		if field(parent, "role") != "assistant" {
			continue
		}
		calls, _ := parent["toolCalls"].([]any)
		for _, raw := range calls {
			if call := object(raw); call != nil && field(call, "id") == id {
				return call, nil
			}
		}
	}
	return nil, fmt.Errorf("tool call %q not found", id)
}

// Ownership lives for the whole run, independently of whether an entity is open.
func (p *Projection) owners(kind string) map[string]string {
	if p.Owners == nil {
		p.Owners = map[string]map[string]string{}
	}
	if p.Owners[kind] == nil {
		p.Owners[kind] = map[string]string{}
	}
	return p.Owners[kind]
}
func (p *Projection) checkOwner(kind, id string, e Object) error {
	if _, tagged := e["subagentRunId"]; !tagged {
		return nil
	}
	if owner, known := p.owners(kind)[id]; known && owner != ownerKey(e) {
		return fmt.Errorf("%s %q attribution disagrees with recorded owner", kind, id)
	}
	return nil
}
func (p *Projection) seedOwners(messages []Object, authoritative bool) {
	for _, m := range messages {
		kind := "message"
		if role := field(m, "role"); role == "reasoning" || role == "activity" {
			kind = role
		}
		id := field(m, "id")
		if _, known := p.owners(kind)[id]; authoritative || !known {
			p.owners(kind)[id] = ownerKey(m)
		}
		if calls, ok := m["toolCalls"].([]any); ok {
			for _, raw := range calls {
				c := object(raw)
				if c == nil {
					continue
				}
				cid := field(c, "id")
				if _, known := p.owners("tool")[cid]; authoritative || !known {
					p.owners("tool")[cid] = ownerKey(m)
				}
			}
		}
	}
}
func (p *Projection) indexTools() {
	p.ToolParents = map[string]string{}
	for _, m := range p.Messages {
		calls, _ := m["toolCalls"].([]any)
		for _, raw := range calls {
			if c := object(raw); c != nil {
				p.ToolParents[field(c, "id")] = field(m, "id")
			}
		}
	}
}
func (p *Projection) apply(e Object) error {
	typ, id, l := field(e, "type"), field(e, "messageId"), lane(e)
	defer func() {
		if strings.HasPrefix(typ, "TOOL_CALL_") && typ != "TOOL_CALL_RESULT" {
			if call, err := p.tool(field(e, "toolCallId")); err == nil {
				mergeMetadata(call, e)
			}
		} else if strings.HasPrefix(typ, "TEXT_MESSAGE_") || strings.HasPrefix(typ, "REASONING_MESSAGE_") || strings.HasPrefix(typ, "ACTIVITY_") {
			m := p.message(id)
			if (strings.HasPrefix(typ, "TEXT_MESSAGE_") || strings.HasPrefix(typ, "REASONING_MESSAGE_")) && field(m, "role") == "activity" {
				return
			}
			if typ == "ACTIVITY_SNAPSHOT" && field(m, "role") != "activity" {
				return
			}
			mergeMetadata(m, e)
		}
	}()
	if p.Errored && typ != "RUN_STARTED" {
		return fmt.Errorf("event after run error")
	}
	if typ == "RUN_STARTED" {
		if p.Running {
			return fmt.Errorf("run already active")
		}
		p.Errored = false
		p.Owners = map[string]map[string]string{}
		p.ReasoningMessages = map[string]string{}
		p.Text = map[string]string{}
		p.Tools = map[string]string{}
		p.Steps = map[string]bool{}
		p.Reasoning = map[string]string{}
		p.Subagents = map[string]bool{}
		p.Running = true
		p.ThreadID = field(e, "threadId")
		p.RunID = field(e, "runId")
		if input := object(e["input"]); input != nil {
			var history []Object
			b, _ := json.Marshal(input["messages"])
			if err := decode(b, &history); err != nil {
				return err
			}
			p.seedOwners(history, false)
			for _, m := range history {
				if p.message(field(m, "id")) == nil {
					p.Messages = append(p.Messages, m)
				}
			}
		}
		p.indexTools()
		return nil
	}
	if !p.Running && typ != "RUN_ERROR" {
		return fmt.Errorf("%s outside an active run", typ)
	}
	switch typ {
	case "RUN_FINISHED", "RUN_ERROR":
		if typ == "RUN_FINISHED" && (field(e, "threadId") != p.ThreadID || field(e, "runId") != p.RunID) {
			return fmt.Errorf("terminal run identity mismatch")
		}
		if typ == "RUN_FINISHED" {
			if len(p.Text) > 0 || len(p.ReasoningMessages) > 0 || len(p.Tools) > 0 || len(p.Steps) > 0 || len(p.Reasoning) > 0 {
				return fmt.Errorf("run ended with open message, tool, step, or reasoning stream")
			}
			for _, active := range p.Subagents {
				if active {
					return fmt.Errorf("run ended with active subagent")
				}
			}
		}
		p.Running = false
		p.Errored = typ == "RUN_ERROR"
	case "TEXT_MESSAGE_START", "REASONING_MESSAGE_START":
		kind, open := "message", p.Text
		if typ == "REASONING_MESSAGE_START" {
			kind, open = "reasoning", p.ReasoningMessages
		}
		if _, exists := open[id]; exists {
			return fmt.Errorf("message %q already open", id)
		}
		if err := p.checkOwner(kind, id, e); err != nil {
			return err
		}
		if _, known := p.owners(kind)[id]; !known {
			p.owners(kind)[id] = ownerKey(e)
		}
		open[id] = attribution(p.owners(kind)[id])
		m := p.message(id)
		if m != nil {
			return nil
		}
		role := field(e, "role")
		if role == "" {
			role = "assistant"
		}
		if kind == "reasoning" {
			role = "reasoning"
		}
		m = Object{"id": id, "role": role, "content": ""}
		inherit(m, e, "name", "subagentRunId")
		p.Messages = append(p.Messages, m)
	case "TEXT_MESSAGE_CONTENT", "REASONING_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "REASONING_MESSAGE_END":
		kind, open := "message", p.Text
		if strings.HasPrefix(typ, "REASONING_") {
			kind, open = "reasoning", p.ReasoningMessages
		}
		if _, exists := open[id]; !exists {
			return fmt.Errorf("event for unopened message %q", id)
		}
		if err := p.checkOwner(kind, id, e); err != nil {
			return err
		}
		if strings.HasSuffix(typ, "_END") {
			delete(open, id)
			return nil
		}
		m := p.message(id)
		if m != nil && field(m, "role") != "activity" {
			m["content"] = field(m, "content") + field(e, "delta")
		}
	case "TOOL_CALL_START":
		id = field(e, "toolCallId")
		if _, open := p.Tools[id]; open {
			return fmt.Errorf("tool call %q already open", id)
		}
		parentID := field(e, "parentMessageId")
		parentOwner, hasParentOwner := p.owners("message")[parentID]
		if _, present := e["parentMessageId"]; !present {
			hasParentOwner = false
		}
		if hasParentOwner {
			if _, tagged := e["subagentRunId"]; tagged && parentOwner != ownerKey(e) {
				return fmt.Errorf("tool owner disagrees with parent message")
			}
		}
		if err := p.checkOwner("tool", id, e); err != nil {
			return err
		}
		owner, known := p.owners("tool")[id]
		if known && hasParentOwner {
			if _, tagged := e["subagentRunId"]; !tagged && owner != parentOwner {
				return fmt.Errorf("reopened tool inherits different parent owner")
			}
		}
		if !known {
			owner = ownerKey(e)
			if _, tagged := e["subagentRunId"]; !tagged && hasParentOwner {
				owner = parentOwner
			}
			p.owners("tool")[id] = owner
		}
		p.Tools[id] = attribution(owner)
		if existing, err := p.tool(id); err == nil {
			object(existing["function"])["name"] = e["toolCallName"]
			return nil
		}
		parent := p.message(parentID)
		if parentID == "" || parent != nil && field(parent, "role") != "assistant" {
			parentID = id
			parent = nil
		}
		if parent == nil {
			parent = Object{"id": parentID, "role": "assistant", "toolCalls": []any{}}
			inherit(parent, e, "subagentRunId")
			p.Messages = append(p.Messages, parent)
		}
		calls, _ := parent["toolCalls"].([]any)
		call := Object{"id": id, "type": "function", "function": Object{"name": e["toolCallName"], "arguments": ""}}
		parent["toolCalls"] = append(calls, call)
		p.ToolParents[id] = parentID
	case "TOOL_CALL_ARGS":
		id = field(e, "toolCallId")
		_, ok := p.Tools[id]
		if !ok {
			return fmt.Errorf("arguments for unopened or wrong-owner tool %q", id)
		}
		if err := p.checkOwner("tool", id, e); err != nil {
			return err
		}
		call, err := p.tool(id)
		if err != nil {
			return nil
		}
		function := object(call["function"])
		function["arguments"] = field(function, "arguments") + field(e, "delta")
	case "TOOL_CALL_END":
		id = field(e, "toolCallId")
		_, ok := p.Tools[id]
		if !ok {
			return fmt.Errorf("end for unopened or wrong-owner tool %q", id)
		}
		if err := p.checkOwner("tool", id, e); err != nil {
			return err
		}
		delete(p.Tools, id)
	case "TOOL_CALL_RESULT":
		callID := field(e, "toolCallId")
		p.owners("message")[id] = ownerKey(e)
		m := Object{"id": id, "role": "tool", "toolCallId": callID, "content": e["content"]}
		inherit(m, e, "subagentRunId")
		mergeMetadata(m, e)
		index := len(p.Messages)
		for i, parent := range p.Messages {
			owns := false
			if field(parent, "role") == "assistant" {
				calls, _ := parent["toolCalls"].([]any)
				for _, raw := range calls {
					if c := object(raw); c != nil && field(c, "id") == callID {
						owns = true
						break
					}
				}
			}
			if owns {
				index = i + 1
				for index < len(p.Messages) && field(p.Messages[index], "role") == "tool" {
					index++
				}
				break
			}
		}
		p.Messages = append(p.Messages, nil)
		copy(p.Messages[index+1:], p.Messages[index:])
		p.Messages[index] = m
	case "STATE_SNAPSHOT":
		p.State = e["snapshot"]
	case "STATE_DELTA":
		value, err := patch(p.State, e["delta"])
		if err != nil {
			return err
		}
		p.State = value
	case "MESSAGES_SNAPSHOT":
		return p.replaceHistory(e)
	case "ACTIVITY_SNAPSHOT":
		if _, known := p.owners("activity")[id]; !known {
			p.owners("activity")[id] = ownerKey(e)
		}
		m := p.message(id)
		if m != nil {
			if replace, ok := e["replace"].(bool); ok && !replace {
				return nil
			}
			if field(m, "role") != "activity" {
				m = Object{"id": id, "role": "activity"}
				for i, prior := range p.Messages {
					if field(prior, "id") == id {
						p.Messages[i] = m
						break
					}
				}
			}
		}
		if m == nil {
			m = Object{"id": id, "role": "activity"}
			p.Messages = append(p.Messages, m)
		}
		p.owners("activity")[id] = ownerKey(e)
		delete(m, "subagentRunId")
		m["activityType"] = e["activityType"]
		m["content"] = e["content"]
		inherit(m, e, "subagentRunId", "parentSubagentRunId")
	case "ACTIVITY_DELTA":
		m := p.message(id)
		if err := p.checkOwner("activity", id, e); err != nil {
			return err
		}
		if m == nil || field(m, "role") != "activity" {
			return nil
		}
		value, err := patch(m["content"], e["patch"])
		if err != nil {
			return err
		}
		if object(value) == nil {
			return fmt.Errorf("activity content must remain an object")
		}
		m["content"] = value
		m["activityType"] = e["activityType"]
	case "REASONING_START":
		if _, open := p.Reasoning[id]; open {
			return fmt.Errorf("reasoning already started")
		}
		if err := p.checkOwner("reasoning", id, e); err != nil {
			return err
		}
		if _, known := p.owners("reasoning")[id]; !known {
			p.owners("reasoning")[id] = ownerKey(e)
		}
		p.Reasoning[id] = p.owners("reasoning")[id]
	case "REASONING_END":
		_, open := p.Reasoning[id]
		if !open {
			return fmt.Errorf("reasoning %q not started", id)
		}
		if err := p.checkOwner("reasoning", id, e); err != nil {
			return err
		}
		delete(p.Reasoning, id)
	case "REASONING_ENCRYPTED_VALUE":
		id = field(e, "entityId")
		kind := "tool"
		if field(e, "subtype") == "message" {
			kind = "message"
			if _, known := p.owners(kind)[id]; !known {
				kind = "reasoning"
			}
		}
		if err := p.checkOwner(kind, id, e); err != nil {
			return err
		}
		if field(e, "subtype") == "message" {
			m := p.message(id)
			if m != nil && field(m, "role") != "activity" {
				m["encryptedValue"] = e["encryptedValue"]
			}
		} else {
			if m, err := p.tool(id); err == nil {
				m["encryptedValue"] = e["encryptedValue"]
			}
		}
	case "STEP_STARTED":
		key := stepKey(e)
		if p.Steps[key] {
			return fmt.Errorf("step already started")
		}
		p.Steps[key] = true
	case "STEP_FINISHED":
		key := stepKey(e)
		if !p.Steps[key] {
			return fmt.Errorf("step not started")
		}
		delete(p.Steps, key)
	case "SUBAGENT_STARTED":
		if _, used := p.Subagents[l]; used {
			return fmt.Errorf("subagent ID already used in this run")
		}
		parent := field(e, "parentSubagentRunId")
		if _, known := p.Subagents[parent]; e["parentSubagentRunId"] != nil && !known {
			return fmt.Errorf("subagent parent not known")
		}
		p.Subagents[l] = true
	case "SUBAGENT_FINISHED", "SUBAGENT_ERROR":
		if !p.Subagents[l] {
			return fmt.Errorf("subagent not active")
		}
		p.Subagents[l] = false
	case "CUSTOM", "RAW": // The durable event journal retains their complete values.
	default:
		return fmt.Errorf("event %q not normalized", typ)
	}
	return nil
}

func patch(value, operations any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	ops, err := json.Marshal(operations)
	if err != nil {
		return nil, err
	}
	result, err := ApplyPatchJSON(data, ops)
	if err != nil {
		return nil, err
	}
	var out any
	err = decode(result, &out)
	return out, err
}

// normalize follows the reference client's per-subagent chunk lanes. Chunk
// metadata belongs to generated content, never to the prior stream's close.
func (p *Projection) normalize(e Object) ([]Object, error) {
	typ, l := field(e, "type"), ownerKey(e)
	var out []Object
	closeLane := func(key string) {
		c, ok := p.Chunks[key]
		if !ok {
			return
		}
		end := Object{"type": c.Kind + "_END"}
		if c.Kind == "TOOL_CALL" {
			end["toolCallId"] = c.ID
		} else {
			end["messageId"] = c.ID
		}
		inherit(end, c.Fields, "subagentRunId")
		out = append(out, end)
		delete(p.Chunks, key)
		for i, k := range p.ChunkOrder {
			if k == key {
				p.ChunkOrder = append(p.ChunkOrder[:i], p.ChunkOrder[i+1:]...)
				break
			}
		}
	}
	if !strings.HasSuffix(typ, "_CHUNK") {
		switch typ {
		case "RUN_STARTED", "RUN_FINISHED", "RUN_ERROR", "MESSAGES_SNAPSHOT":
			for _, key := range append([]string(nil), p.ChunkOrder...) {
				closeLane(key)
			}
		case "RAW", "ACTIVITY_SNAPSHOT", "ACTIVITY_DELTA", "REASONING_ENCRYPTED_VALUE", "SUBAGENT_STARTED":
		default:
			closeLane(l)
		}
		return append(out, e), nil
	}
	kind := strings.TrimSuffix(typ, "_CHUNK")
	idKey := "messageId"
	if kind == "TOOL_CALL" {
		idKey = "toolCallId"
	}
	id := field(e, idKey)
	_, hasID := e[idKey]
	if hasID {
		for owner, c := range p.Chunks {
			if c.Kind == kind && c.ID == id {
				if _, tagged := e["subagentRunId"]; tagged && l != owner {
					return nil, fmt.Errorf("chunk owner changed")
				}
				l = owner
				break
			}
		}
	} else if _, tagged := e["subagentRunId"]; !tagged {
		if c, ok := p.Chunks["parent:"]; !ok || c.Kind != kind {
			candidates := 0
			for owner, c := range p.Chunks {
				if c.Kind == kind {
					l = owner
					candidates++
				}
			}
			if candidates > 1 {
				return nil, fmt.Errorf("ambiguous unowned chunk")
			}
		}
	}
	prior, exists := p.Chunks[l]
	if !hasID && exists && prior.Kind == kind {
		id = prior.ID
		hasID = true
	}
	if !hasID {
		return nil, fmt.Errorf("initial chunk requires identity")
	}
	if exists && (prior.Kind != kind || prior.ID != id) {
		closeLane(l)
		exists = false
	}
	if exists {
		for _, key := range []string{"role", "name", "toolCallName", "parentMessageId"} {
			if value, present := e[key]; present && value != prior.Fields[key] {
				return nil, fmt.Errorf("chunk opener field %q changed", key)
			}
		}
	}
	if !exists {
		start := Object{"type": kind + "_START", idKey: id}
		inherit(start, e, "timestamp", "metadata")
		if strings.HasPrefix(l, "tag:") {
			start["subagentRunId"] = strings.TrimPrefix(l, "tag:")
		}
		if kind == "TOOL_CALL" {
			if _, present := e["toolCallName"]; !present {
				return nil, fmt.Errorf("initial tool chunk requires name")
			}
			inherit(start, e, "toolCallName", "parentMessageId")
		} else {
			role := field(e, "role")
			if role == "" {
				role = "assistant"
			}
			if kind == "REASONING_MESSAGE" {
				role = "reasoning"
			}
			start["role"] = role
			inherit(start, e, "name")
		}
		out = append(out, start)
		p.Chunks[l] = chunk{Kind: kind, ID: id, Fields: start}
		p.ChunkOrder = append(p.ChunkOrder, l)
	}
	delta, present := e["delta"]
	if !present {
		delta = ""
	}
	_, hasMetadata := e["metadata"]
	_, hasRaw := e["rawEvent"]
	if present || hasRaw || hasMetadata && exists {
		content := Object{"type": kind + "_CONTENT", idKey: id, "delta": delta}
		if kind == "TOOL_CALL" {
			content["type"] = "TOOL_CALL_ARGS"
		}
		inherit(content, e, "timestamp", "metadata", "rawEvent")
		if strings.HasPrefix(l, "tag:") {
			content["subagentRunId"] = strings.TrimPrefix(l, "tag:")
		}
		out = append(out, content)
	}
	return out, nil
}
