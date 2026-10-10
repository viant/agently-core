package main

import (
	"fmt"
	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/sdk/api"
	"net/http"
	"strings"
)

func linkedReviewStep(snapshot *sdk.ConversationStateResponse, turnID, tool string) (*api.ToolStepState, *api.TurnState, error) {
	if snapshot == nil || snapshot.Conversation == nil {
		return nil, nil, fmt.Errorf("parent transcript missing")
	}
	var match *api.ToolStepState
	var root *api.TurnState
	for _, turn := range snapshot.Conversation.Turns {
		if turn == nil || turn.TurnID != turnID {
			continue
		}
		root = turn
		if turn.Execution == nil {
			continue
		}
		for _, page := range turn.Execution.Pages {
			if page == nil {
				continue
			}
			for _, step := range page.ToolSteps {
				if step == nil || step.ToolName != tool {
					continue
				}
				if match != nil && match.ToolMessageID != step.ToolMessageID {
					return nil, nil, fmt.Errorf("linked review operation ambiguous")
				}
				match = step
			}
		}
	}
	if match == nil || root == nil || match.Status != "completed" || match.LinkedConversationID == "" || match.RequestPayloadID == "" || match.ToolMessageID == "" || match.CompletedAt == nil || root.Assistant == nil || root.Assistant.Final == nil || root.Assistant.Final.CreatedAt.Before(*match.CompletedAt) {
		return nil, nil, fmt.Errorf("observed final is not after completed linked tool")
	}
	return match, root, nil
}
func (h *handler) linkedReview(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	record := h.runs[strings.TrimPrefix(r.URL.Path, "/linked-review/")]
	h.mu.Unlock()
	if record == nil || record.ownerHash != digest(token(r)) {
		http.NotFound(w, r)
		return
	}
	record.mu.Lock()
	actual, target := record.identity, record.target
	record.mu.Unlock()
	tool := strings.TrimSpace(r.URL.Query().Get("toolName"))
	if tool == "" {
		write(w, 400, &probeError{"linked-review", "input", "tool name required"})
		return
	}
	client, err := newClient(target, token(r), &observer{next: http.DefaultTransport})
	if err != nil {
		write(w, 500, &probeError{"linked-review", "client", "SDK client unavailable"})
		return
	}
	parent, err := client.GetTranscript(r.Context(), &sdk.GetTranscriptInput{ConversationID: actual.ConversationID, IncludeModelCalls: true, IncludeToolCalls: true})
	if err != nil {
		write(w, 403, &probeError{"linked-review", "read-denied", "parent owner cannot read observed transcript"})
		return
	}
	step, turn, err := linkedReviewStep(parent, actual.TurnID, tool)
	if err != nil {
		write(w, 409, &probeError{"linked-review", "correlation", err.Error()})
		return
	}
	request, _, err := rawOwnedPayload(r.Context(), target, token(r), step.RequestPayloadID)
	if err != nil {
		write(w, 409, &probeError{"linked-review", "request", "actual tool request unavailable"})
		return
	}
	child, err := client.GetTranscript(r.Context(), &sdk.GetTranscriptInput{ConversationID: step.LinkedConversationID, IncludeModelCalls: true, IncludeToolCalls: true})
	var childTurn *api.TurnState
	if child != nil && child.Conversation != nil {
		for _, candidate := range child.Conversation.Turns {
			if candidate != nil && candidate.Assistant != nil && candidate.Assistant.Final != nil {
				if childTurn != nil {
					write(w, 409, &probeError{"linked-review", "child", "linked child has ambiguous final turns"})
					return
				}
				childTurn = candidate
			}
		}
	}
	if err != nil || child == nil || child.Conversation == nil || child.Conversation.ConversationID != step.LinkedConversationID || childTurn == nil {
		write(w, 409, &probeError{"linked-review", "child", "actual linked child final unavailable"})
		return
	}
	childMetadata, err := client.GetConversation(r.Context(), step.LinkedConversationID)
	if err != nil || childMetadata == nil || childMetadata.ConversationParentId == nil || *childMetadata.ConversationParentId != actual.ConversationID || childMetadata.ConversationParentTurnId == nil || *childMetadata.ConversationParentTurnId != actual.TurnID {
		write(w, 409, &probeError{"linked-review", "child-parent", "actual child metadata differs from parent/turn"})
		return
	}
	found := false
	for _, linked := range turn.LinkedConversations {
		if linked != nil && linked.ConversationID == step.LinkedConversationID && linked.ParentTurnID == actual.TurnID && linked.ToolCallID == step.ToolCallID {
			found = true
		}
	}
	if !found {
		write(w, 409, &probeError{"linked-review", "link", "child relation does not match observed parent/tool operation"})
		return
	}
	write(w, 200, map[string]any{"identity": actual, "tool": map[string]any{"toolName": step.ToolName, "status": step.Status, "toolCallId": step.ToolCallID, "toolMessageId": step.ToolMessageID, "requestPayloadId": step.RequestPayloadID, "linkedConversationId": step.LinkedConversationID, "completedAt": step.CompletedAt}, "request": request, "parentFinal": turn.Assistant.Final, "child": map[string]any{"conversationId": child.Conversation.ConversationID, "turnId": childTurn.TurnID, "status": childTurn.Status, "final": childTurn.Assistant.Final}})
}
