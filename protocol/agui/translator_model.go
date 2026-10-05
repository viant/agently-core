package agui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/viant/agently-core/runtime/streaming"
)

type reasoningState struct {
	subagent             string
	span, owner, content string
	ended                bool
}
type stepState struct {
	subagent string
	name     string
	ended    bool
}
type usageState struct {
	provider, model                         string
	input, output, cached, reasoning, write int
}

func modelIdentity(e *streaming.Event) string {
	for _, id := range []string{e.ModelCallID, e.AssistantMessageID, e.MessageID, e.ID, e.ResponseID} {
		if id != "" {
			return id
		}
	}
	return ""
}
func (t *Translator) modelStep(e *streaming.Event, complete bool) []Event {
	id := modelIdentity(e)
	if id == "" {
		return t.Fail("Native model lifecycle has no call identity", "MISSING_MODEL_CALL_ID")
	}
	state := t.steps[id]
	var events []Event
	if state == nil {
		state = &stepState{name: "model/" + id}
		t.steps[id] = state
		t.stepOrder = append(t.stepOrder, id)
		t.activeModel = id
		metadata := map[string]any{"agently": map[string]any{"provider": e.Provider, "model": e.ModelName, "iteration": e.Iteration}}
		events = append(events, standardEvent(map[string]any{"type": "STEP_STARTED", "stepName": state.name, "metadata": metadata}))
	}
	if t.recordUsage(e, id) {
		events = append(events, t.usageEvent(id))
	}
	if complete && !state.ended {
		events = append(events, t.closeReasoning(id)...)
		state.ended = true
		events = append(events, standardEvent(map[string]any{"type": "STEP_FINISHED", "stepName": state.name}))
		if t.activeModel == id {
			t.activeModel = ""
		}
	}
	return events
}
func (t *Translator) reasoning(e *streaming.Event) []Event {
	if e.EncryptedValue != "" && e.EncryptedSubtype == "tool-call" {
		return []Event{standardEvent(map[string]any{"type": "REASONING_ENCRYPTED_VALUE", "subtype": "tool-call", "entityId": t.toolID(e.TurnID, e.EncryptedEntityID), "encryptedValue": e.EncryptedValue})}
	}

	id := e.ReasoningMessageID
	if id == "" {
		id = e.ID
	}
	if id == "" {
		id = e.MessageID
	}
	if id == "" {
		id = modelIdentity(e) + "/reasoning"
	}
	span := e.ReasoningSpanID
	if span == "" {
		span = id + "/span"
	}
	state := t.reasonings[id]
	var events []Event
	if state == nil {
		state = &reasoningState{span: span, owner: t.activeModel}
		t.reasonings[id] = state
		t.reasoningOrder = append(t.reasoningOrder, id)
		if !t.reasoningSpans[span] {
			t.reasoningSpans[span] = true
			events = append(events, standardEvent(map[string]any{"type": "REASONING_START", "messageId": span}))
		}
		events = append(events, standardEvent(map[string]any{"type": "REASONING_MESSAGE_START", "messageId": id, "role": "reasoning"}))
	}
	if state.ended {
		return nil
	}
	if e.Content != "" {
		state.content += e.Content
		events = append(events, standardEvent(map[string]any{"type": "REASONING_MESSAGE_CONTENT", "messageId": id, "delta": e.Content}))
	}
	if e.EncryptedValue != "" {
		subtype := e.EncryptedSubtype
		if subtype == "" {
			subtype = "message"
		}
		entity := e.EncryptedEntityID
		if entity == "" {
			entity = id
		}
		events = append(events, standardEvent(map[string]any{"type": "REASONING_ENCRYPTED_VALUE", "subtype": subtype, "entityId": entity, "encryptedValue": e.EncryptedValue}))
	}
	return events
}
func (t *Translator) closeReasoning(owner string) []Event {
	var events []Event
	spans := map[string]bool{}
	for _, id := range t.reasoningOrder {
		state := t.reasonings[id]
		if owner == "" || state.owner == owner {
			spans[state.span] = true
			if !state.ended {
				state.ended = true
				events = append(events, scopedBoundary("REASONING_MESSAGE_END", "messageId", id, state.subagent))
			}
		}
	}
	for span := range spans {
		open := false
		for _, state := range t.reasonings {
			if state.span == span && !state.ended {
				open = true
			}
		}
		if !open && t.reasoningSpans[span] {
			t.reasoningSpans[span] = false
			owner := ""
			for _, state := range t.reasonings {
				if state.span == span {
					owner = state.subagent
					break
				}
			}
			events = append(events, scopedBoundary("REASONING_END", "messageId", span, owner))
		}
	}
	return events
}
func (t *Translator) closeReasoningItem(id string) []Event {
	state := t.reasonings[id]
	if state == nil || state.ended {
		return nil
	}
	state.ended = true
	events := []Event{scopedBoundary("REASONING_MESSAGE_END", "messageId", id, state.subagent)}
	open := false
	for _, other := range t.reasonings {
		if other.span == state.span && !other.ended {
			open = true
		}
	}
	if !open && t.reasoningSpans[state.span] {
		t.reasoningSpans[state.span] = false
		events = append(events, scopedBoundary("REASONING_END", "messageId", state.span, state.subagent))
	}
	return events
}
func (t *Translator) recordUsage(e *streaming.Event, id string) bool {
	if e.Usage == nil || e.Usage.Scope != "model_call" || id == "" {
		return false
	}
	if e.Usage.InputTokens < 0 || e.Usage.OutputTokens < 0 || e.Usage.CachedInputTokens < 0 || e.Usage.ReasoningTokens < 0 || e.Usage.CacheWriteInputTokens < 0 {
		t.historyErr = fmt.Errorf("negative native token usage")
		return false
	}
	provider, model := e.Provider, e.ModelName
	if e.Model != nil {
		if provider == "" {
			provider = e.Model.Provider
		}
		if model == "" {
			model = e.Model.Model
		}
	}
	state := usageState{provider: provider, model: model, input: e.Usage.InputTokens, output: e.Usage.OutputTokens, cached: e.Usage.CachedInputTokens, reasoning: e.Usage.ReasoningTokens, write: e.Usage.CacheWriteInputTokens}
	if prior, ok := t.usage[id]; ok {
		if provider == "" {
			state.provider = prior.provider
		}
		if model == "" {
			state.model = prior.model
		}
		if state.write == 0 {
			state.write = prior.write
		}
	}
	if prior, ok := t.usage[id]; ok && prior == state {
		return false
	}
	t.usage[id] = state
	return true
}
func (t *Translator) runUsage() []map[string]any {
	groups := map[string]usageState{}
	for _, state := range t.usage {
		key := state.provider + "\x00" + state.model
		sum := groups[key]
		sum.provider = state.provider
		sum.model = state.model
		sum.input += state.input
		sum.output += state.output
		sum.cached += state.cached
		sum.reasoning += state.reasoning
		sum.write += state.write
		groups[key] = sum
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var output []map[string]any
	for _, key := range keys {
		state := groups[key]
		entry := map[string]any{"inputTokens": state.input, "outputTokens": state.output, "totalTokens": state.input + state.output}
		if state.provider != "" {
			entry["provider"] = state.provider
		}
		if state.model != "" {
			entry["model"] = state.model
		}
		if state.cached != 0 {
			entry["cachedInputTokens"] = state.cached
		}
		if state.reasoning != 0 {
			entry["reasoningTokens"] = state.reasoning
		}
		if state.write != 0 {
			entry["cacheWriteInputTokens"] = state.write
		}
		output = append(output, entry)
	}
	return output
}
func (t *Translator) closeSteps() []Event {
	var out []Event
	for _, id := range t.stepOrder {
		step := t.steps[id]
		if !step.ended {
			step.ended = true
			out = append(out, scopedBoundary("STEP_FINISHED", "stepName", step.name, step.subagent))
		}
	}
	return out
}
func (t *Translator) renderedActivity(e *streaming.Event) []Event {
	id := e.AssistantMessageID
	if id == "" {
		id = e.MessageID
	}
	if id == "" {
		id = e.ID
	}
	if id == "" {
		return nil
	}
	if e.RenderedContent != nil {
		return []Event{standardEvent(map[string]any{"type": "ACTIVITY_SNAPSHOT", "messageId": id + "/activity", "activityType": "agently.rendered-content", "content": map[string]any{"version": "1", "renderedContent": e.RenderedContent}})}
	}
	if e.FeedData != nil && strings.TrimSpace(e.FeedID) != "" {
		if e.Type == streaming.EventTypeToolFeedActive || e.Type == streaming.EventTypeToolFeedInactive {
			return nil
		}
		return []Event{standardEvent(map[string]any{"type": "ACTIVITY_SNAPSHOT", "messageId": e.FeedID + "/activity", "activityType": "agently.feed", "content": map[string]any{"version": "1", "feedId": e.FeedID, "data": e.FeedData}})}
	}
	return nil
}
func (t *Translator) publishStandard(raw []byte) ([]Event, error) {
	if reservedNativePresentationEvent(raw) {
		return nil, fmt.Errorf("native presentation identity belongs to trusted producers")
	}
	if reservedInvocationEvent(raw) {
		return nil, fmt.Errorf("native invocation ancestry belongs to trusted registration")
	}
	event, err := StandardEvent(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid standard runtime event: %w", err)
	}
	if event.Type == "RUN_STARTED" || event.Type == "RUN_FINISHED" || event.Type == "RUN_ERROR" {
		return nil, fmt.Errorf("run lifecycle belongs to the translator")
	}
	if t.done {
		return nil, fmt.Errorf("cannot publish after run completion")
	}
	events := append(t.Start(), event)
	t.capture(events)
	if t.historyErr != nil {
		return nil, t.historyErr
	}
	return events, nil
}

// EmitStandard is the explicit bridge for runtime producers of standard RAW,
// ACTIVITY_DELTA, chunks, state, reasoning and extension events. It validates the
// true standard variant and never relabels native implementation events as RAW.
func (t *Translator) EmitStandard(event *WireEvent) ([]Event, error) {
	raw, err := EncodeEvent(event)
	if err != nil {
		return nil, err
	}
	return t.publishStandard(raw)
}

func usageFields(state usageState) map[string]any {
	fields := map[string]any{"inputTokens": state.input, "outputTokens": state.output, "totalTokens": state.input + state.output, "cachedInputTokens": state.cached, "reasoningTokens": state.reasoning, "cacheWriteInputTokens": state.write}
	if state.provider != "" {
		fields["provider"] = state.provider
	}
	if state.model != "" {
		fields["model"] = state.model
	}
	return fields
}
func (t *Translator) usageEvent(id string) Event {
	return Event{Type: "CUSTOM", Name: "agently.usage", Value: map[string]any{"version": "1", "scope": "model_call", "modelCallId": id, "usage": usageFields(t.usage[id])}}
}

func scopedBoundary(kind, key, id, owner string) Event {
	fields := map[string]any{"type": kind, key: id}
	if owner != "" {
		fields["subagentRunId"] = owner
	}
	return standardEvent(fields)
}
