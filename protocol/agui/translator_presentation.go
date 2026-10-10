package agui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/runtime/streaming"
)

// Native presentation is a closed scalar projection. Never serialize Event,
// Patch, payload bodies, provider requests, MCP receipts, or authoring JSON here.
func nativePresentation(e *streaming.Event) map[string]any {
	value := map[string]any{"version": "1"}
	text := func(key, content string) {
		if content != "" {
			value[key] = content
		}
	}
	text("conversationId", e.ConversationID)
	text("nativeTurnId", e.TurnID)
	text("nativeMessageId", e.AssistantMessageID)
	if value["nativeMessageId"] == nil {
		text("nativeMessageId", e.MessageID)
	}
	if e.Type == streaming.EventTypeAssistant && e.ModelCallID == "" && e.PageID == "" && e.Patch["role"] != "user" && e.Patch["agentlyProjectionSnapshot"] != true && !streaming.IsInternalMessageMode(e.Mode) {
		value["messageKind"] = "standalone"
	}
	text("parentMessageId", e.ParentMessageID)
	if e.Type == streaming.EventTypeModelStarted {
		text("nativeUserMessageId", e.ParentMessageID)
	}
	text("pageId", e.PageID)
	text("modelCallId", e.ModelCallID)
	if value["modelCallId"] == nil && (e.Type == streaming.EventTypeModelStarted || e.Type == streaming.EventTypeModelCompleted) {
		text("modelCallId", modelIdentity(e))
	}
	text("nativeToolCallId", e.ToolCallID)
	text("toolMessageId", e.ToolMessageID)
	text("executionRole", e.ExecutionRole)
	text("phase", e.Phase)
	text("mode", e.Mode)
	text("status", e.Status)
	text("agentId", e.AgentIDUsed)
	text("agentName", e.AgentName)
	provider, model := e.Provider, e.ModelName
	if e.Model != nil {
		if provider == "" {
			provider = e.Model.Provider
		}
		if model == "" {
			model = e.Model.Model
		}
	}
	text("provider", provider)
	text("model", model)
	text("requestPayloadId", e.RequestPayloadID)
	text("responsePayloadId", e.ResponsePayloadID)
	text("providerRequestPayloadId", e.ProviderRequestPayloadID)
	text("providerResponsePayloadId", e.ProviderResponsePayloadID)
	text("streamPayloadId", e.StreamPayloadID)
	if !e.CreatedAt.IsZero() {
		value["createdAt"] = e.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	pendingTool := e.Type == streaming.EventTypeToolCallWaiting || e.ToolCallID != "" && ToolStatusPending(e.Status)
	if e.StartedAt != nil && !pendingTool {
		value["startedAt"] = e.StartedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if e.CompletedAt != nil && !pendingTool {
		value["completedAt"] = e.CompletedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	// Zero is an actual first iteration, page and usage count, not omission.
	value["iteration"] = e.Iteration
	if e.PageID != "" {
		value["pageIndex"] = e.PageIndex
		value["pageCount"] = e.PageCount
		value["latestPage"] = e.LatestPage
	}
	if e.Usage != nil {
		text("usageScope", e.Usage.Scope)
		value["inputTokens"] = e.Usage.InputTokens
		value["outputTokens"] = e.Usage.OutputTokens
		value["cachedInputTokens"] = e.Usage.CachedInputTokens
		value["reasoningTokens"] = e.Usage.ReasoningTokens
		value["embeddingTokens"] = e.Usage.EmbeddingTokens
		value["totalTokens"] = e.Usage.TotalTokens
		value["cacheWriteInputTokens"] = e.Usage.CacheWriteInputTokens
	}
	return value
}

func annotateNativePresentation(event Event, native *streaming.Event) Event {
	if native.Type == streaming.EventTypeProtocol {
		return event
	}
	raw, _ := json.Marshal(event)
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return event
	}
	metadata := map[string]json.RawMessage{}
	_ = json.Unmarshal(fields["metadata"], &metadata)
	agently := map[string]json.RawMessage{}
	_ = json.Unmarshal(metadata["agently"], &agently)
	presentation := nativePresentation(native)
	if event.Type == "TOOL_CALL_RESULT" && native.ToolMessageID != "" {
		presentation["nativeMessageId"] = native.ToolMessageID
	}
	agently["presentation"] = rawJSON(presentation)
	metadata["agently"] = rawJSON(agently)
	fields["metadata"] = rawJSON(metadata)
	event.Standard = rawJSON(fields)
	// Translator helpers already advanced their lanes. Capture only the public
	// message graph, not the same native boundary a second time.
	event.applied = true
	return event
}

func (t *Translator) presentationActivity(kind, id string, content map[string]any) []Event {
	if id == "" {
		return nil
	}
	content["version"] = "1"
	if err := extensions.ValidatePresentation(kind, rawJSON(content)); err != nil {
		return t.Fail("Native presentation descriptor is invalid", "INVALID_PRESENTATION")
	}
	return []Event{standardEvent(map[string]any{"type": "ACTIVITY_SNAPSHOT", "messageId": id, "activityType": kind, "content": content, "replace": true})}
}
func (t *Translator) turnPresentation(e *streaming.Event) []Event {
	if e.TurnID == "" {
		return nil
	}
	status := e.Status
	switch e.Type {
	case streaming.EventTypeTurnStarted:
		status = "running"
	case streaming.EventTypeTurnQueued:
		status = "queued"
	case streaming.EventTypeTurnCompleted:
		status = "completed"
	case streaming.EventTypeTurnCanceled:
		status = "canceled"
	case streaming.EventTypeTurnFailed:
		status = "failed"
	}
	value := map[string]any{"nativeTurnId": e.TurnID, "status": status, "queueSequence": strconv.FormatInt(int64(e.QueueSeq), 10)}
	if e.StartedByMessageID != "" {
		value["startedByMessageId"] = e.StartedByMessageID
	}
	if e.QueueOrigin != "" {
		value["origin"] = e.QueueOrigin
	}
	if e.GoalID != "" {
		value["goalId"] = e.GoalID
	}
	if e.StatusReason != "" {
		value["statusReason"] = e.StatusReason
	}
	return t.presentationActivity("agently.turn", e.TurnID+"/turn", value)
}
func (t *Translator) feedPresentation(e *streaming.Event) []Event {
	if e.FeedID == "" {
		return nil
	}
	value := map[string]any{"feedId": e.FeedID, "active": e.Type != streaming.EventTypeToolFeedInactive, "title": e.FeedTitle, "developerOnly": e.FeedDeveloperOnly, "itemCount": e.FeedItemCount}
	if e.FeedIcon != "" || e.FeedAccent != "" || e.FeedTarget != "" {
		value["presentation"] = map[string]any{"icon": e.FeedIcon, "accent": e.FeedAccent, "target": e.FeedTarget}
	}
	if e.FeedData != nil {
		value["data"] = e.FeedData
	}
	return t.presentationActivity("agently.feed", e.ConversationID+"/feed/"+e.FeedID+"/activity", value)
}
func (t *Translator) plannedToolsPresentation(e *streaming.Event) []Event {
	if len(e.ToolCallsPlanned) == 0 {
		return nil
	}
	id := e.PageID
	if id == "" {
		id = e.AssistantMessageID
	}
	if id == "" {
		id = e.MessageID
	}
	if id == "" {
		return nil
	}
	calls := make([]map[string]any, 0, len(e.ToolCallsPlanned))
	for _, call := range e.ToolCallsPlanned {
		calls = append(calls, map[string]any{"toolCallId": t.toolID(e.TurnID, call.ToolCallID), "toolName": call.ToolName})
	}
	return t.presentationActivity("agently.tools-planned", id+"/planned-tools", map[string]any{"calls": calls})
}
func (t *Translator) plannerPresentation(e *streaming.Event) []Event {
	if e.TurnID == "" {
		return nil
	}
	phase := strings.TrimPrefix(string(e.Type), "planner.")
	value := map[string]any{"status": phase, "attempt": e.PlannerAttempt, "trigger": e.PlannerTrigger, "staticProfile": e.PlannerStaticProfile, "strategyFamily": e.PlannerStrategyFamily, "secondPolicy": e.PlannerSecondPolicy}
	if e.PlannerOutputPayloadID != "" {
		value["outputPayloadId"] = e.PlannerOutputPayloadID
	}
	if e.PlannerValidated != nil {
		value["validated"] = *e.PlannerValidated
	}
	return t.presentationActivity("agently.planner", e.TurnID+"/planner", value)
}
func (t *Translator) narrationPresentation(e *streaming.Event) []Event {
	id := e.MessageID
	if id == "" {
		id = e.AssistantMessageID
	}
	if id == "" {
		id = e.ToolMessageID
	}
	if id == "" {
		return nil
	}
	text := e.Content
	if text == "" {
		text = e.Narration
	}
	return t.presentationActivity("agently.narration", id+"/narration", map[string]any{"text": text, "source": e.NarrationSource, "status": e.Status, "toolCallId": t.toolID(e.TurnID, e.ToolCallID)})
}
func (t *Translator) aggregateUsagePresentation(e *streaming.Event) []Event {
	if e.Usage != nil && e.Usage.Scope == "model_call" {
		return nil
	}
	scope := "conversation"
	if e.Usage != nil && e.Usage.Scope != "" {
		scope = e.Usage.Scope
	}
	usage := map[string]any{"inputTokens": e.UsageInputTokens, "outputTokens": e.UsageOutputTokens, "embeddingTokens": e.UsageEmbeddingTokens, "totalTokens": e.UsageTotalTokens}
	if e.Usage != nil {
		usage = map[string]any{"inputTokens": e.Usage.InputTokens, "outputTokens": e.Usage.OutputTokens, "cachedInputTokens": e.Usage.CachedInputTokens, "reasoningTokens": e.Usage.ReasoningTokens, "embeddingTokens": e.Usage.EmbeddingTokens, "totalTokens": e.Usage.TotalTokens, "cacheWriteInputTokens": e.Usage.CacheWriteInputTokens}
	}
	return []Event{{Type: "CUSTOM", Name: "agently.usage", Value: map[string]any{"version": "1", "scope": scope, "nativeTurnId": e.TurnID, "usage": usage}}}
}

// Model request/argument completion and native effect execution are different
// boundaries. A later native start/wait can have no new standard TOOL event.
func (t *Translator) toolExecutionPresentation(e *streaming.Event) []Event {
	if e.ToolCallID == "" {
		return nil
	}
	waiting := e.Type == streaming.EventTypeToolCallWaiting || ToolStatusPending(e.Status)
	if !waiting && e.ToolMessageID == "" && e.StartedAt == nil && e.CompletedAt == nil && e.OperationID == "" {
		return nil
	}
	id := t.toolID(e.TurnID, e.ToolCallID)
	value := map[string]any{"toolCallId": id, "phase": "execution", "status": e.Status}
	if state := t.tools[id]; state != nil && state.executionPresentation != nil {
		for key, field := range state.executionPresentation {
			value[key] = field
		}
	}
	value["toolCallId"] = id
	value["phase"] = "execution"
	status := e.Status
	if waiting {
		value["phase"] = "waiting"
		if status == "" {
			status = "waiting_for_user"
		}
	} else if status == "" {
		switch e.Type {
		case streaming.EventTypeToolCallStarted:
			status = "running"
		case streaming.EventTypeToolCallCompleted:
			status = "completed"
		case streaming.EventTypeToolCallFailed:
			status = "failed"
		case streaming.EventTypeToolCallCanceled:
			status = "canceled"
		}
	}
	value["status"] = status
	value["version"] = "1"
	if e.ToolMessageID != "" {
		value["toolMessageId"] = e.ToolMessageID
	}
	if !waiting && e.StartedAt != nil {
		value["startedAt"] = e.StartedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if !waiting && e.CompletedAt != nil {
		value["completedAt"] = e.CompletedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if state := t.tools[id]; state != nil {
		if state.executionPresentation != nil && string(rawJSON(state.executionPresentation)) == string(rawJSON(value)) {
			return nil
		}
		state.executionPresentation = value
	}
	return t.presentationActivity("agently.tool", id+"/execution", value)
}

func reservedNativePresentationEvent(raw []byte) bool {
	var event struct {
		Type         string                     `json:"type"`
		Name         string                     `json:"name"`
		ActivityType string                     `json:"activityType"`
		Metadata     map[string]json.RawMessage `json:"metadata"`
		Messages     []json.RawMessage          `json:"messages"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return false
	}
	var namespace map[string]json.RawMessage
	_ = json.Unmarshal(event.Metadata["agently"], &namespace)
	if _, present := namespace["presentation"]; present {
		return true
	}
	if _, present := namespace["identityVersion"]; present {
		return true
	}
	if _, present := namespace["nativeTurnId"]; present {
		return true
	}
	if event.Type == "MESSAGES_SNAPSHOT" {
		for _, rawMessage := range event.Messages {
			var message struct {
				Role         string                     `json:"role"`
				ActivityType string                     `json:"activityType"`
				Metadata     map[string]json.RawMessage `json:"metadata"`
				ToolCalls    []struct {
					Metadata map[string]json.RawMessage `json:"metadata"`
				} `json:"toolCalls"`
			}
			if json.Unmarshal(rawMessage, &message) != nil {
				continue
			}
			if reservedNativePresentationEvent(rawJSON(map[string]any{"type": "ACTIVITY_SNAPSHOT", "activityType": message.ActivityType, "metadata": message.Metadata})) {
				return true
			}
			for _, call := range message.ToolCalls {
				if reservedNativePresentationEvent(rawJSON(map[string]any{"metadata": call.Metadata})) {
					return true
				}
			}
		}
	}
	if event.Type == "ACTIVITY_SNAPSHOT" || event.Type == "ACTIVITY_DELTA" {
		switch event.ActivityType {
		case "agently.turn", "agently.tool", "agently.feed", "agently.planner", "agently.tools-planned", "agently.narration", "agently.user-identity":
			return true
		}
	}
	return event.Type == "CUSTOM" && (event.Name == "agently.queue" || event.Name == "agently.progress" || event.Name == "agently.linked-conversation" || event.Name == "agently.usage")
}

// EmitNativeUserIdentity is a restricted trusted producer, not the generic
// runtime ProtocolEvent bridge. It binds identity to this translator's native run.
func (t *Translator) EmitNativeUserIdentity(nativeTurnID, clientMessageID, nativeUserMessageID string) ([]Event, error) {
	if t.done || nativeTurnID == "" || nativeTurnID != t.nativeTurnID || clientMessageID == "" || nativeUserMessageID == "" {
		return nil, fmt.Errorf("native user identity does not match active translator scope")
	}
	content := map[string]any{"version": "1", "protocolRunId": t.runID, "nativeTurnId": nativeTurnID, "clientMessageId": clientMessageID, "clientRequestId": clientMessageID, "nativeUserMessageId": nativeUserMessageID}
	if err := extensions.ValidatePresentation("agently.user-identity", rawJSON(content)); err != nil {
		return nil, err
	}
	events := t.presentationActivity("agently.user-identity", clientMessageID+"/identity", content)
	t.capture(events)
	if t.historyErr != nil {
		return nil, t.historyErr
	}
	return events, nil
}
