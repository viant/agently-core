package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	messagemodel "github.com/viant/agently-core/model/message"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

type aguiInvocationInspector interface {
	aguiInspectInvocation(context.Context, *aguistore.Run, requestctx.Invocation) (*aguiRecoveredNative, error)
}

// Every child is read under the existing trusted causal registration. No Query,
// model execution, dependency effect, or invented terminal occurs here.
func aguiReconcileRecoveredChildren(ctx context.Context, runtime aguiRuntime, writer *aguiJournalWriter, translator *agui.Translator, root *aguiRecoveredNative) (bool, error) {
	invocations := translator.RecoverableInvocations()
	if len(invocations) == 0 {
		return true, nil
	}
	inspector, ok := runtime.(aguiInvocationInspector)
	if !ok {
		return false, fmt.Errorf("native causal invocation inspection unavailable")
	}
	byID := map[string]requestctx.Invocation{}
	for _, inv := range invocations {
		byID[inv.ID] = inv
	}
	depth := func(inv requestctx.Invocation) int {
		n := 0
		seen := map[string]bool{}
		for inv.ParentInvocationID != "" {
			if seen[inv.ID] {
				return len(invocations) + 1
			}
			seen[inv.ID] = true
			parent, ok := byID[inv.ParentInvocationID]
			if !ok {
				break
			}
			n++
			inv = parent
		}
		return n
	}
	sort.SliceStable(invocations, func(i, j int) bool {
		di, dj := depth(invocations[i]), depth(invocations[j])
		if di == dj {
			return invocations[i].ID < invocations[j].ID
		}
		return di < dj
	})
	states := map[string]*aguiRecoveredNative{}
	active := map[string]bool{}
	for _, inv := range translator.ActiveInvocations() {
		active[inv.ID] = true
	}
	settled := true
	for _, inv := range invocations {
		if inv.ConversationID == "" || inv.TurnID == "" {
			return false, fmt.Errorf("journal subagent lacks recoverable native identity")
		}
		child, err := inspector.aguiInspectInvocation(ctx, writer.run, inv)
		if err != nil {
			return false, err
		}
		states[inv.ID] = child
		if child == nil {
			settled = false
			continue
		}
		status := strings.ToLower(strings.TrimSpace(child.Status))
		if status == "queued" || status == "pending" || status == "" {
			settled = false
			continue
		}
		if !aguiRecoveredInvocationTerminal(child) {
			settled = false
		}
		// Scope raw native calls to their original logical turn, then carry the
		// invocation's real ownership on materialized messages and pending input.
		scoped := *child
		scoped.Messages = make([]aguistate.Object, 0, len(child.Messages))
		for _, original := range child.Messages {
			message := aguistate.Object{}
			for key, value := range original {
				message[key] = value
			}
			message["subagentRunId"] = inv.ID
			if calls, ok := original["toolCalls"].([]any); ok {
				copied := make([]any, 0, len(calls))
				for _, value := range calls {
					raw := aguiRecoveryObject(value)
					call := aguistate.Object{}
					for k, v := range raw {
						call[k] = v
					}
					call["id"] = agui.ProtocolToolCallID(inv.TurnID, fmt.Sprint(raw["id"]))
					copied = append(copied, call)
				}
				message["toolCalls"] = copied
			}
			if original["role"] == "tool" {
				message["toolCallId"] = agui.ProtocolToolCallID(inv.TurnID, fmt.Sprint(original["toolCallId"]))
			}
			scoped.Messages = append(scoped.Messages, message)
		}
		scoped = *aguiSanitizeRecoveredMessages(writer, &scoped)
		knownResults := map[string]bool{}
		for _, m := range writer.projection.Messages {
			if m["role"] == "tool" {
				knownResults[fmt.Sprint(m["toolCallId"])] = true
			}
		}
		var results []json.RawMessage
		var snapshot []aguistate.Object
		for _, m := range scoped.Messages {
			for _, call := range aguiRecoveryCalls(m) {
				publicID := fmt.Sprint(call["id"])
				rawID := ""
				for _, nativeMessage := range child.Messages {
					for _, nativeCall := range aguiRecoveryCalls(nativeMessage) {
						candidate := fmt.Sprint(nativeCall["id"])
						if agui.ProtocolToolCallID(inv.TurnID, candidate) == publicID {
							rawID = candidate
						}
					}
				}
				if !translator.HasToolCall(inv.TurnID, rawID) {
					function := aguiRecoveryObject(call["function"])
					var arguments map[string]interface{}
					if err := decodeAGUIValue([]byte(fmt.Sprint(function["arguments"])), &arguments); err != nil {
						return false, err
					}
					event := &streaming.Event{Type: streaming.EventTypeToolCallCompleted, ConversationID: inv.ConversationID, TurnID: inv.TurnID, ToolCallID: rawID, ToolName: fmt.Sprint(function["name"]), AssistantMessageID: fmt.Sprint(m["id"]), Arguments: arguments}
					if err := writer.write(encodeAGUIEvents(translator.TranslateSubagent(inv, event)), nil); err != nil {
						return false, err
					}
				}
			}
			if m["role"] == "tool" && !knownResults[fmt.Sprint(m["toolCallId"])] {
				result := map[string]any{"type": "TOOL_CALL_RESULT", "messageId": m["id"], "toolCallId": m["toolCallId"], "content": m["content"], "subagentRunId": inv.ID}
				if metadata, present := m["metadata"]; present {
					result["metadata"] = metadata
				}
				results = append(results, rawAGUI(result))
				continue
			}
			snapshot = append(snapshot, m)
		}
		merged := aguiMergeRecoveryMessages(writer.projection.Messages, snapshot)
		if merged == nil {
			merged = []aguistate.Object{}
		}
		event := &streaming.Event{Type: streaming.EventTypeProtocol, ConversationID: inv.ConversationID, TurnID: inv.TurnID, ProtocolEvent: rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": merged})}
		reconcile := func(raw json.RawMessage) error {
			wire, err := agui.DecodeEvent(raw)
			if err != nil {
				return err
			}
			events, err := translator.ReconcileSubagentEvent(inv, wire)
			if err != nil {
				return err
			}
			return writer.write(encodeAGUIEvents(events), nil)
		}
		if !active[inv.ID] || !bytes.Equal(rawAGUI(merged), rawAGUI(writer.projection.Messages)) {
			if err := reconcile(event.ProtocolEvent); err != nil {
				return false, err
			}
		}
		for _, result := range results {
			event.ProtocolEvent = result
			if err := reconcile(event.ProtocolEvent); err != nil {
				return false, err
			}
		}
	}
	// Parent starts precede child starts; actual child terminals precede their
	// parents' terminals. Running/queued or missing native work stays unresolved.
	for i := len(invocations) - 1; i >= 0; i-- {
		inv := invocations[i]
		child := states[inv.ID]
		if !aguiRecoveredInvocationTerminal(child) {
			continue
		}
		if err := writer.write(encodeAGUIEvents(translator.InvocationReturned(requestctx.InvocationResult{Invocation: inv, NativeStatus: child.Status, Error: child.Error, ClientToolCalls: child.Pending.ClientTools, ClientToolDependencies: child.Pending.Dependencies})), nil); err != nil {
			return false, err
		}
		if root != nil {
			for _, call := range child.Pending.ClientTools {
				call.ProtocolID = agui.ProtocolToolCallID(inv.TurnID, call.ID)
				root.Pending.ClientTools = append(root.Pending.ClientTools, call)
				interrupt := aguiClientToolInterrupt(call)
				owner := inv.ID
				interrupt.SubagentRunID = &owner
				root.Pending.Interrupts = append(root.Pending.Interrupts, interrupt)
			}
			for _, interrupt := range child.Pending.Interrupts {
				owner := inv.ID
				interrupt.SubagentRunID = &owner
				root.Pending.Interrupts = append(root.Pending.Interrupts, interrupt)
			}
			root.Pending.Dependencies = append(root.Pending.Dependencies, child.Pending.Dependencies...)
		}
	}
	return settled, nil
}

func (c *backendClient) aguiInspectInvocation(ctx context.Context, root *aguistore.Run, inv requestctx.Invocation) (*aguiRecoveredNative, error) {
	if c == nil || c.conv == nil || root == nil || inv.Detached {
		return nil, fmt.Errorf("native invocation scope is unavailable")
	}
	child, err := c.conv.GetConversation(ctx, inv.ConversationID)
	if err != nil {
		return nil, err
	}
	if child == nil {
		return nil, nil
	}
	if child.ConversationParentId == nil || *child.ConversationParentId != inv.ParentConversationID || child.ConversationParentTurnId == nil || *child.ConversationParentTurnId != inv.ParentTurnID {
		// Conversation metadata is mutable when a child conversation is reused.
		// The framework-owned registration journal retains the original parent.
		registered, err := c.aguiRegisteredInvocationRecorded(ctx, root, inv)
		if err != nil {
			return nil, err
		}
		if !registered {
			return nil, fmt.Errorf("native child parent does not match invocation checkpoint")
		}
	}
	if child.CreatedByUserId != nil && *child.CreatedByUserId != "" && *child.CreatedByUserId != root.Principal {
		return nil, fmt.Errorf("native child principal does not match invocation checkpoint")
	}
	scoped := &aguistore.Run{Principal: root.Principal, ThreadID: inv.ConversationID, ConversationID: inv.ConversationID, TurnID: inv.TurnID, RunID: root.RunID, Input: root.Input}
	result, err := c.aguiInspectRun(ctx, scoped)
	if err != nil || result == nil {
		return result, err
	}
	if (result.Status == "waiting_for_user" || result.Status == "blocked") && inv.ParentToolCallID != "" {
		edge, err := c.aguiInvocationDependency(ctx, inv)
		if err != nil {
			return nil, err
		}
		result.Pending.Dependencies = append(result.Pending.Dependencies, edge)
	}
	return result, nil
}
func aguiRecoveredInvocationTerminal(child *aguiRecoveredNative) bool {
	return aguiNativeObservationTerminal(child) || child != nil && (child.Status == "waiting_for_user" || child.Status == "blocked") && len(child.Pending.Dependencies) > 0
}

// Reconstruct only the original native parent call named by the recorded
// invocation; the existing dependency adapter uses this exact checkpoint.
func (c *backendClient) aguiInvocationDependency(ctx context.Context, inv requestctx.Invocation) (clienttool.Dependency, error) {
	cursor := ""
	for {
		in := &messagemodel.MessageRowsInput{ConversationId: inv.ParentConversationID, TurnId: inv.ParentTurnID, Roles: []string{"tool"}, Has: &messagemodel.MessageRowsInputHas{ConversationId: true, TurnId: true, Roles: true}}
		page, err := c.data.GetMessagesPage(ctx, in, &data.PageInput{Limit: 500, Direction: data.DirectionBefore, Cursor: cursor}, principalDataOpts(ctx)...)
		if err != nil {
			return clienttool.Dependency{}, err
		}
		if page == nil {
			return clienttool.Dependency{}, fmt.Errorf("native invocation parent page unavailable")
		}
		for _, row := range page.Rows {
			if row == nil || row.ConversationId != inv.ParentConversationID || valueOrEmpty(row.TurnId) != inv.ParentTurnID {
				return clienttool.Dependency{}, fmt.Errorf("native invocation parent row scope mismatch")
			}
			message, err := c.conv.GetMessage(ctx, row.Id, conversation.WithIncludeToolCall(true))
			if err != nil {
				return clienttool.Dependency{}, err
			}
			if message == nil || message.MessageToolCall == nil || message.MessageToolCall.OpId != inv.ParentToolCallID {
				continue
			}
			call := message.MessageToolCall
			if call.Status != "waiting_for_user" {
				return clienttool.Dependency{}, fmt.Errorf("native invocation parent call is not waiting")
			}
			arguments := map[string]interface{}{}
			if payload := call.MessageRequestPayload; payload != nil && payload.InlineBody != nil {
				if err := decodeAGUIValue([]byte(conversation.DecodeInlineBody(*payload.InlineBody, payload.Compression)), &arguments); err != nil {
					return clienttool.Dependency{}, err
				}
			}
			parent := clienttool.PendingCall{ID: call.OpId, Name: call.ToolName, Arguments: arguments, ToolMessageID: message.Id, AssistantMessageID: valueOrEmpty(message.ParentMessageId), ConversationID: inv.ParentConversationID, TurnID: inv.ParentTurnID, Iteration: aguiRecoveryInt(call.Iteration)}
			return clienttool.Dependency{ID: inv.TurnID, ParentCall: parent, ChildConversationID: inv.ConversationID, ChildTurnID: inv.TurnID, ChildAgentID: inv.Name, ExecutionMode: inv.ExecutionMode, ResultAdapter: clienttool.AgentRunResultV1, Waiting: true}, nil
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			return clienttool.Dependency{}, fmt.Errorf("native invocation parent cursor did not advance")
		}
		cursor = page.NextCursor
	}
	return clienttool.Dependency{}, fmt.Errorf("native invocation parent call checkpoint missing")
}

func (c *backendClient) aguiRegisteredInvocationRecorded(ctx context.Context, root *aguistore.Run, inv requestctx.Invocation) (bool, error) {
	if root.LastSequence == 0 || c.goalInvoker == nil {
		return false, nil
	}
	var after int64
	verifiedFrame := false
	for after < root.LastSequence {
		page, err := c.aguiStore().Replay(ctx, root.Principal, root.ThreadID, root.RunID, after, 500)
		if err != nil {
			return false, err
		}
		if len(page) == 0 {
			return false, fmt.Errorf("native invocation registration journal incomplete")
		}
		for _, entry := range page {
			if entry.Sequence != after+1 {
				return false, fmt.Errorf("native invocation registration journal gap")
			}
			var event struct {
				Type     string `json:"type"`
				Name     string `json:"name"`
				Metadata struct {
					Agently struct {
						Version string `json:"identityVersion"`
						Turn    string `json:"nativeTurnId"`
					} `json:"agently"`
				} `json:"metadata"`
				Value struct {
					Version    string                `json:"version"`
					Phase      string                `json:"phase"`
					Invocation requestctx.Invocation `json:"invocation"`
				} `json:"value"`
			}
			if err := json.Unmarshal(entry.Event, &event); err != nil {
				return false, err
			}
			if entry.Sequence == 1 {
				verifiedFrame = event.Type == "RUN_STARTED" && event.Metadata.Agently.Version == "1" && event.Metadata.Agently.Turn == root.TurnID
			}
			if verifiedFrame && event.Type == "CUSTOM" && event.Name == "agently.invocation" && event.Value.Version == "1" && event.Value.Phase == "registered" && event.Value.Invocation == inv {
				return true, nil
			}
			after = entry.Sequence
		}
	}
	return false, nil
}
