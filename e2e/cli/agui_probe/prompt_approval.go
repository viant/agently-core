package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/sdk/api"
)

// promptOriginal binds the pending prompt to its original native parent and tool.
// It does not infer a later operation or manufacture an approved dispatch receipt.
func promptOriginal(snapshot *sdk.ConversationStateResponse, actual identity, pending *sdk.PendingElicitation) (*api.TurnState, *api.ToolStepState, error) {
	if pending == nil || pending.ConversationID != actual.ConversationID || pending.ElicitationID == "" || pending.MessageID == "" {
		return nil, nil, fmt.Errorf("original prompt identity absent")
	}
	var root *api.TurnState
	if snapshot != nil && snapshot.Conversation != nil && snapshot.Conversation.ConversationID == actual.ConversationID {
		for _, turn := range snapshot.Conversation.Turns {
			if turn != nil && turn.TurnID == actual.TurnID {
				if root != nil {
					return nil, nil, fmt.Errorf("duplicate original turn")
				}
				root = turn
			}
		}
	}
	if root == nil || root.Elicitation == nil || root.Elicitation.ElicitationID != pending.ElicitationID || root.Elicitation.Status != api.ElicitationStatusPending || root.Execution == nil {
		return nil, nil, fmt.Errorf("original pending prompt absent")
	}
	var selected *api.ToolStepState
	for _, page := range root.Execution.Pages {
		if page == nil {
			continue
		}
		for _, step := range page.ToolSteps {
			if step == nil || step.ParentMessageID != pending.MessageID {
				continue
			}
			if selected != nil {
				return nil, nil, fmt.Errorf("prompt parent has multiple original effects")
			}
			selected = step
		}
	}
	if selected == nil || selected.ToolCallID == "" || selected.ToolMessageID == "" || selected.RequestPayloadID == "" {
		return nil, nil, fmt.Errorf("original prompt tool identity absent")
	}
	return root, selected, nil
}

func promptCompleted(snapshot *sdk.ConversationStateResponse, tuple approvalTuple) (*api.TurnState, *api.ToolStepState, error) {
	var root *api.TurnState
	if snapshot != nil && snapshot.Conversation != nil && snapshot.Conversation.ConversationID == tuple.ConversationID {
		for _, turn := range snapshot.Conversation.Turns {
			if turn != nil && turn.TurnID == tuple.TurnID {
				if root != nil {
					return nil, nil, fmt.Errorf("duplicate original turn")
				}
				root = turn
			}
		}
	}
	if root == nil || root.Status != api.TurnStatusCompleted || root.Elicitation == nil || root.Elicitation.ElicitationID != tuple.ApprovalID || root.Elicitation.Status != api.ElicitationStatusAccepted {
		return nil, nil, fmt.Errorf("same original prompt not accepted and completed")
	}
	var step *api.ToolStepState
	if root.Execution != nil {
		for _, page := range root.Execution.Pages {
			if page == nil {
				continue
			}
			for _, candidate := range page.ToolSteps {
				if candidate != nil && candidate.ParentMessageID == tuple.QueueParentMessageID {
					if step != nil {
						return nil, nil, fmt.Errorf("original prompt has duplicate effects")
					}
					step = candidate
				}
			}
		}
	}
	if step == nil || step.ToolCallID != tuple.OperationID || step.ToolMessageID != tuple.MessageID || step.Status != "completed" || step.RequestPayloadID != tuple.RequestID || step.ResponsePayloadID == "" {
		return nil, nil, fmt.Errorf("original prompt effect identity differs")
	}
	return root, step, nil
}

func (h *handler) promptApproval(w http.ResponseWriter, r *http.Request, capture bool) {
	prefix := "/prompt-approval-receipt/"
	if capture {
		prefix = "/prompt-approval-capture/"
	}
	probe := strings.TrimPrefix(r.URL.Path, prefix)
	h.mu.Lock()
	record := h.runs[probe]
	h.mu.Unlock()
	if record == nil || record.ownerHash != digest(token(r)) {
		http.NotFound(w, r)
		return
	}
	record.mu.Lock()
	actual, target := record.identity, record.target
	record.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	client, err := newClient(target, token(r), &observer{next: http.DefaultTransport})
	fail := func(stage string) {
		write(w, 409, &probeError{"prompt-approval", stage, "original prompt evidence unavailable or differs"})
	}
	if err != nil {
		fail("client")
		return
	}
	if _, err = client.GetConversation(ctx, actual.ConversationID); err != nil {
		write(w, 403, &probeError{"prompt-approval", "read-denied", "same owner cannot read original conversation"})
		return
	}
	snapshot, err := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: actual.ConversationID, IncludeModelCalls: true, IncludeToolCalls: true})
	if err != nil {
		fail("transcript")
		return
	}
	id := r.URL.Query().Get("id")
	if capture {
		pending, err := client.ListPendingElicitations(ctx, &sdk.ListPendingElicitationsInput{ConversationID: actual.ConversationID})
		if err != nil {
			fail("pending")
			return
		}
		var original *sdk.PendingElicitation
		for _, row := range pending {
			if row != nil && row.ElicitationID == id {
				if original != nil {
					fail("duplicate-prompt")
					return
				}
				original = row
			}
		}
		_, step, err := promptOriginal(snapshot, actual, original)
		if err != nil {
			fail("original-tuple")
			return
		}
		request, _, err := rawOwnedPayload(ctx, target, token(r), step.RequestPayloadID)
		if err != nil {
			fail("original-request")
			return
		}
		args, ok := request.(map[string]any)
		if !ok {
			fail("request-shape")
			return
		}
		tuple := approvalTuple{ApprovalID: id, ConversationID: actual.ConversationID, TurnID: actual.TurnID, OperationID: step.ToolCallID, MessageID: step.ToolMessageID, QueueParentMessageID: original.MessageID, RequestID: step.RequestPayloadID, OriginalArgs: args}
		record.mu.Lock()
		if record.approvals == nil {
			record.approvals = map[string]approvalTuple{}
		}
		if _, exists := record.approvals[id]; exists {
			record.mu.Unlock()
			fail("duplicate-capture")
			return
		}
		record.approvals[id] = tuple
		record.mu.Unlock()
		publicTuple := tuple
		publicTuple.OriginalArgs = nil
		write(w, 200, map[string]any{"tuple": publicTuple, "originalRequest": promptOriginalNameSet(args), "sameOwner": true})
		return
	}
	record.mu.Lock()
	tuple, exists := record.approvals[id]
	record.mu.Unlock()
	if !exists {
		fail("not-captured")
		return
	}
	root, step, err := promptCompleted(snapshot, tuple)
	if err != nil {
		fail("original-effect")
		return
	}
	request, _, err := rawOwnedPayload(ctx, target, token(r), tuple.RequestID)
	if err != nil || !reflect.DeepEqual(request, tuple.OriginalArgs) {
		fail("original-request-drift")
		return
	}
	response, _, err := rawOwnedPayload(ctx, target, token(r), step.ResponsePayloadID)
	if err != nil {
		fail("response")
		return
	}
	var accepted any
	if json.Unmarshal(root.Elicitation.ResponsePayload, &accepted) != nil {
		fail("accepted-edit")
		return
	}
	acceptedNames := promptAcceptedNames(accepted)
	if acceptedNames == nil {
		fail("accepted-edit-shape")
		return
	}
	// Expose actual typed evidence, never substitute the fixture's expected edits.
	publicTuple := tuple
	publicTuple.OriginalArgs = nil
	write(w, 200, map[string]any{"tuple": publicTuple, "sameOwner": true, "nativeStatus": root.Status, "elicitationStatus": root.Elicitation.Status, "acceptedResponse": acceptedNames, "originalRequest": promptOriginalNameSet(tuple.OriginalArgs), "actualResponse": promptResultKeys(response), "effectCount": 1, "responsePayloadId": step.ResponsePayloadID})
}

// Projection deliberately retains names and key-presence, never environment values.
func promptAcceptedNames(actual any) any {
	body, _ := actual.(map[string]any)
	fields, _ := body["editedFields"].(map[string]any)
	if names, ok := fields["names"].([]any); !ok || len(names) == 0 {
		return nil
	}
	return map[string]any{"editedFields": map[string]any{"names": fields["names"]}}
}
func promptResultKeys(actual any) any {
	body, _ := actual.(map[string]any)
	values, ok := body["values"].(map[string]any)
	if !ok {
		return nil
	}
	keys := map[string]any{}
	for _, name := range []string{"HOME", "PATH", "SHELL"} {
		if _, present := values[name]; present {
			keys[name] = true
		}
	}
	return map[string]any{"valueKeyCount": len(values), "values": keys}
}

// The original model argument is a set oracle, not an ordering contract. The
// raw captured arguments remain untouched for the exact immutable reread above.
func promptOriginalNameSet(args map[string]any) any {
	names, ok := args["names"].([]any)
	if !ok {
		return nil
	}
	present := map[string]any{}
	for _, item := range names {
		name, ok := item.(string)
		if !ok {
			return nil
		}
		switch name {
		case "HOME", "PATH", "SHELL":
			present[name] = true
		}
	}
	return map[string]any{"nameCount": len(names), "names": present}
}
