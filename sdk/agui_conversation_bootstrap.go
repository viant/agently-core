package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/streaming"
)

type AGUIConversationBootstrapInput struct {
	Mode              string                    `json:"mode,omitempty"`
	Since             string                    `json:"since,omitempty"`
	IncludeModelCalls *bool                     `json:"includeModelCalls,omitempty"`
	IncludeToolCalls  *bool                     `json:"includeToolCalls,omitempty"`
	IncludeFeeds      *bool                     `json:"includeFeeds,omitempty"`
	Selectors         map[string]*QuerySelector `json:"selectors,omitempty"`
}

// Only public protocol identities are exposed, never accepted input, native IDs,
// continuation internals, leases, principal, or authenticated service bindings.
type AGUIConversationRun struct {
	Kind         string `json:"kind"`
	ThreadID     string `json:"threadId"`
	RunID        string `json:"runId"`
	ParentRunID  string `json:"parentRunId,omitempty"`
	Status       string `json:"status"`
	Revision     int64  `json:"revision"`
	LastSequence int64  `json:"lastSequence"`
}
type AGUIBootstrapProjection struct {
	Lossless              bool     `json:"lossless"`
	UnavailableMessageIDs []string `json:"unavailableMessageIds"`
}
type AGUIConversationBootstrapResult struct {
	Version                    string                     `json:"version"`
	ThreadID                   string                     `json:"threadId"`
	Transcript                 *ConversationStateResponse `json:"transcript"`
	Messages                   []aguistate.Object         `json:"messages"`
	HostActivities             []aguistate.Object         `json:"hostActivities"`
	UnavailableHostActivityIDs []string                   `json:"unavailableHostActivityIds"`
	State                      any                        `json:"state"`
	Runs                       []AGUIConversationRun      `json:"runs"`
	Projection                 AGUIBootstrapProjection    `json:"projection"`
}

func (c *backendClient) aguiConversationBootstrapAvailable() bool {
	return c != nil && c.conv != nil && c.goalInvoker != nil
}
func aguiConversationBootstrapAvailable(client Client) bool {
	configured, ok := client.(interface{ aguiConversationBootstrapAvailable() bool })
	return ok && configured.aguiConversationBootstrapAvailable()
}

func dispatchAGUIConversationBootstrap(ctx context.Context, client Client, store aguistore.Store, record *aguistore.Run, payload json.RawMessage) (*AGUIConversationBootstrapResult, error) {
	if client == nil || store == nil || record == nil || record.ConversationID == "" || record.TurnID != "" || iauth.EffectiveUserID(ctx) != record.Principal {
		return nil, fmt.Errorf("authorized independent conversation bootstrap scope is required")
	}
	if err := extensions.ValidateConversationPayload("conversation.bootstrap", payload); err != nil {
		return nil, err
	}
	// Recheck the original native conversation at dispatch, not a protocol thread
	// created by command admission. The existing service remains the authority.
	conversation, err := client.GetConversation(ctx, record.ConversationID)
	if err != nil || conversation == nil || conversation.Id != record.ConversationID || conversation.CreatedByUserId == nil || *conversation.CreatedByUserId != record.Principal {
		return nil, fmt.Errorf("conversation unavailable")
	}
	input := AGUIConversationBootstrapInput{}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &input); err != nil {
			return nil, err
		}
	}
	include := func(value *bool) bool { return value == nil || *value }
	options := []TranscriptOption{}
	if include(input.IncludeFeeds) {
		options = append(options, WithIncludeFeeds())
	}
	if include(input.IncludeModelCalls) {
		options = append(options, WithIncludeModelCalls())
	}
	if include(input.IncludeToolCalls) {
		options = append(options, WithIncludeToolCalls())
	}
	for name, selector := range input.Selectors {
		options = append(options, WithTranscriptSelector(name, selector))
	}
	var transcript *ConversationStateResponse
	if input.Mode == "live" {
		transcript, err = client.GetLiveState(ctx, record.ConversationID, options...)
	} else {
		transcript, err = client.GetTranscript(ctx, &GetTranscriptInput{ConversationID: record.ConversationID, Since: input.Since, IncludeModelCalls: include(input.IncludeModelCalls), IncludeToolCalls: include(input.IncludeToolCalls)}, options...)
	}
	if err != nil {
		return nil, err
	}
	if transcript == nil || transcript.Conversation == nil || transcript.Conversation.ConversationID != record.ConversationID {
		return nil, fmt.Errorf("canonical conversation snapshot scope mismatch")
	}
	thread, err := store.GetThread(ctx, record.Principal, record.ThreadID)
	if err != nil {
		return nil, err
	}
	if thread.ProtocolOnly {
		return nil, fmt.Errorf("original native conversation unavailable")
	}
	projection, err := aguistate.New(thread.State, thread.Messages)
	if err != nil {
		return nil, err
	}
	result := &AGUIConversationBootstrapResult{Version: "1", ThreadID: record.ThreadID, Transcript: transcript, State: projection.State, Runs: []AGUIConversationRun{}, Projection: AGUIBootstrapProjection{Lossless: true, UnavailableMessageIDs: []string{}}}
	aliases := aguiBootstrapUserAliases(ctx, store, record, transcript, projection.Messages)
	result.Messages, result.Projection = aguiBootstrapMessagesWithAliases(ctx, client, transcript, projection.Messages, aliases)
	result.HostActivities, result.UnavailableHostActivityIDs = aguiBootstrapHostActivities(ctx, client, store, record, projection.Messages)
	pending, err := store.ListPending(ctx, record.Principal, record.ThreadID)
	if err != nil {
		return nil, err
	}
	if lister, ok := store.(aguistore.ActiveRunLister); ok {
		active, readErr := lister.ListActive(ctx, record.Principal, record.ThreadID)
		if readErr != nil {
			return nil, readErr
		}
		pending = append(pending, active...)
	} else {
		return nil, fmt.Errorf("active protocol run discovery is unavailable")
	}
	seen := map[string]bool{}
	for _, run := range pending {
		if run == nil || run.RunID == record.RunID {
			continue
		}
		if run.Principal != record.Principal || run.ThreadID != record.ThreadID {
			return nil, fmt.Errorf("pending protocol run scope mismatch")
		}
		if seen[run.RunID] {
			continue
		}
		seen[run.RunID] = true
		result.Runs = append(result.Runs, AGUIConversationRun{Kind: aguiBootstrapRunKind(run), ThreadID: run.ThreadID, RunID: run.RunID, ParentRunID: run.ParentRunID, Status: run.Status, Revision: run.Revision, LastSequence: run.LastSequence})
	}
	sort.Slice(result.Runs, func(i, j int) bool { return result.Runs[i].RunID < result.Runs[j].RunID })
	if err := agui.ValidateEvent(rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": result.Messages})); err != nil {
		return nil, err
	}
	if err := extensions.ValidateConversationResult(rawAGUI(result)); err != nil {
		return nil, err
	}
	return result, nil
}

func aguiBootstrapRunKind(run *aguistore.Run) string {
	var input struct {
		ForwardedProps map[string]json.RawMessage `json:"forwardedProps"`
	}
	if json.Unmarshal(run.Input, &input) == nil {
		if _, proxy := input.ForwardedProps["__proxiedMCPRequest"]; proxy {
			return "mcp-app"
		}
	}
	if run.TurnID != "" {
		return "chat"
	}
	return "resource"
}

// Native canonical history is the saved presentation authority. Journal entries
// supply lossless multipart/opaque protocol fields. Missing native-only history
// is explicitly marked as a presentation projection, not lossless reconstruction.
func aguiBootstrapMessages(ctx context.Context, client Client, transcript *ConversationStateResponse, journal []aguistate.Object) ([]aguistate.Object, AGUIBootstrapProjection) {
	return aguiBootstrapMessagesWithAliases(ctx, client, transcript, journal, nil)
}
func aguiBootstrapMessagesWithAliases(ctx context.Context, client Client, transcript *ConversationStateResponse, journal []aguistate.Object, aliases map[string]string) ([]aguistate.Object, AGUIBootstrapProjection) {
	result := []aguistate.Object{}
	quality := AGUIBootstrapProjection{Lossless: true, UnavailableMessageIDs: []string{}}
	unavailable := map[string]bool{}
	removeActivities := map[string]bool{}
	mark := func(id string) {
		if !unavailable[id] {
			unavailable[id] = true
			quality.UnavailableMessageIDs = append(quality.UnavailableMessageIDs, id)
		}
		quality.Lossless = false
	}
	hostTurns, hostMessageIDs := aguiCanonicalHostBoundaries(transcript)
	internalIDs := map[string]bool{}
	toolResultIDs := map[string]bool{}
	for _, turn := range transcript.Conversation.Turns {
		if turn == nil {
			continue
		}
		for _, message := range turn.Messages {
			if message != nil && message.Role == "assistant" && streaming.IsInternalMessageMode(message.Mode) {
				internalIDs[message.MessageID] = true
			}
		}
		if turn.Execution != nil {
			for _, page := range turn.Execution.Pages {
				if page == nil {
					continue
				}
				for _, step := range page.ToolSteps {
					if step != nil && step.ToolMessageID != "" {
						toolResultIDs[step.ToolMessageID] = true
					}
				}
				hidden := streaming.IsInternalMessageMode(page.Mode)
				if hidden && page.AssistantMessageID != "" {
					internalIDs[page.AssistantMessageID] = true
				}
				for _, step := range page.ModelSteps {
					if step != nil && (streaming.IsInternalMessageMode(step.Mode) || step.Mode == "" && (hidden || streaming.IsInternalMessageMode(step.ExecutionRole))) && step.AssistantMessageID != "" {
						internalIDs[step.AssistantMessageID] = true
					}
				}
			}
		}
	}
	byID := map[string]int{}
	for _, message := range journal {
		id, _ := message["id"].(string)
		if hostMessageIDs[id] || hostTurns[aguiMessageNativeTurn(message)] {
			mark(id)
			continue
		}
		// Full MCP host receipts and trusted app bindings require the scoped host
		// channel. Do not place them in bootstrap's model-facing message graph.
		if message["role"] == "activity" && message["activityType"] == "mcp-apps" {
			mark(id)
			continue
		}
		if internalIDs[id] || toolResultIDs[id] && message["role"] == "assistant" {
			// Retain a tool-call container for pairing, never its internal body.
			calls, _ := message["toolCalls"].([]any)
			if len(calls) == 0 {
				continue
			}
		}
		if message["role"] == "activity" && message["activityType"] == "agently.rendered-content" && (internalIDs[strings.TrimSuffix(id, "/activity")] || toolResultIDs[strings.TrimSuffix(id, "/activity")]) {
			continue
		}
		copy := aguistate.Object{}
		for key, value := range message {
			copy[key] = value
		}
		if internalIDs[id] || toolResultIDs[id] && message["role"] == "assistant" {
			copy["content"] = ""
		}
		byID[id] = len(result)
		result = append(result, copy)
	}
	putText := func(id, role, content string, rendered *RenderedContent) {
		if internalIDs[id] || toolResultIDs[id] && role == "assistant" {
			return
		}
		if id == "" || role != "user" && role != "assistant" && role != "system" {
			return
		}
		if role == "user" && aliases[id] != "" {
			id = aliases[id]
		}
		plain := content
		if role == "assistant" {
			plain, _ = plainAGUIContent(content, true)
		}
		if position, exists := byID[id]; exists {
			message := result[position]
			if message["role"] != role {
				mark(id)
				return
			}
			if _, text := message["content"].(string); text || message["content"] == nil {
				if role == "assistant" && message["content"] != plain && rendered == nil {
					removeActivities[id+"/activity"] = true
				}
				message["content"] = plain
			} else {
				// A canonical text DTO cannot safely replace a multipart body.
				mark(id)
			}
		} else {
			byID[id] = len(result)
			result = append(result, aguistate.Object{"id": id, "role": role, "content": plain})
			mark(id)
		}
		if role == "assistant" && rendered != nil {
			delete(removeActivities, id+"/activity")
			activity := aguistate.Object{"id": id + "/activity", "role": "activity", "activityType": "agently.rendered-content", "content": map[string]any{"version": "1", "renderedContent": rendered}}
			if position, exists := byID[id+"/activity"]; exists {
				result[position]["content"] = activity["content"]
			} else {
				byID[id+"/activity"] = len(result)
				result = append(result, activity)
			}
		}
	}
	for _, turn := range transcript.Conversation.Turns {
		if turn == nil {
			continue
		}
		if hostTurns[turn.TurnID] {
			continue
		}
		if turn.User != nil {
			putText(turn.User.MessageID, "user", turn.User.Content, nil)
		}
		for _, user := range turn.Users {
			if user != nil {
				putText(user.MessageID, "user", user.Content, nil)
			}
		}
		for _, message := range turn.Messages {
			if message != nil && !streaming.IsInternalMessageMode(message.Mode) {
				if message.Role != "user" && message.Role != "assistant" && message.Role != "system" {
					if _, exists := byID[message.MessageID]; !exists {
						mark(message.MessageID)
					}
				}
				putText(message.MessageID, message.Role, message.Content, message.RenderedContent)
			}
		}
		if turn.Assistant != nil {
			for _, message := range append(append([]*AssistantMessageState{}, turn.Assistant.Messages...), turn.Assistant.Narration, turn.Assistant.Final) {
				if message != nil {
					putText(message.MessageID, "assistant", message.Content, message.RenderedContent)
				}
			}
		}
		if turn.Execution != nil {
			for _, page := range turn.Execution.Pages {
				if page == nil {
					continue
				}
				for _, tool := range page.ToolSteps {
					if tool != nil && tool.ToolMessageID != "" {
						if _, exists := byID[tool.ToolMessageID]; !exists {
							mark(tool.ToolMessageID)
						}
					}
				}
			}
		}
	}
	if len(removeActivities) > 0 {
		kept := make([]aguistate.Object, 0, len(result))
		for _, message := range result {
			id, _ := message["id"].(string)
			if !removeActivities[id] {
				kept = append(kept, message)
			}
		}
		result = kept
	}
	// Reuse the existing authoring-to-public presentation boundary so Forge fences
	// are activities rather than raw authoring JSON in standard assistant prose.
	writer := &aguiJournalWriter{ctx: ctx, client: client}
	sanitized := aguiSanitizeRecoveredMessages(writer, &aguiRecoveredNative{Messages: result})
	return sanitized.Messages, quality
}

func runAGUIConversationBootstrap(ctx context.Context, client Client, store aguistore.Store, record *aguistore.Run, payload json.RawMessage) error {
	if record.LastSequence > 0 {
		return fmt.Errorf("bootstrap journal was interrupted; replay its accepted result or use a new read command identity")
	}
	result, err := dispatchAGUIConversationBootstrap(ctx, client, store, record, payload)
	if err != nil {
		return err
	}
	events := []json.RawMessage{
		rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": record.ThreadID, "runId": record.RunID, "protocolVersion": "1.0"}),
		rawAGUI(map[string]any{"type": "STATE_SNAPSHOT", "snapshot": result.State}),
		rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": result.Messages}),
		rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": record.ThreadID, "runId": record.RunID, "outcome": map[string]any{"type": "success"}, "result": result}),
	}
	// Journal only: bootstrap never changes saved protocol/native state/history.
	_, err = store.Append(ctx, record.Principal, record.ThreadID, record.RunID, record.Revision, events, &aguistore.Change{LeaseOwner: record.LeaseOwner})
	return err
}
