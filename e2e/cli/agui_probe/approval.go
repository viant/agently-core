package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/sdk/api"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
)

type approvalTuple struct {
	ApprovalID           string         `json:"approvalId"`
	Owner                string         `json:"owner"`
	ConversationID       string         `json:"nativeConversationId"`
	TurnID               string         `json:"nativeTurnId"`
	OperationID          string         `json:"nativeOperationId"`
	PublicCallID         string         `json:"publicToolCallId"`
	QueueParentMessageID string         `json:"queueParentMessageId"`
	MessageID            string         `json:"effectMessageId"`
	RequestID            string         `json:"requestPayloadId"`
	ResponseID           string         `json:"responsePayloadId"`
	OriginalArgs         map[string]any `json:"originalArguments"`
	OriginalNames        []string       `json:"originalArgumentNames"`
}

func findExactApproval(page *sdk.PendingToolApprovalPage, id string, identity identity) (*sdk.PendingToolApproval, error) {
	if page == nil {
		return nil, fmt.Errorf("approval page absent")
	}
	for _, row := range page.Rows {
		if row != nil && row.ID == id {
			if row.ConversationID != identity.ConversationID || row.TurnID != identity.TurnID || row.UserID == "" || row.MessageID == "" {
				return nil, fmt.Errorf("approval tuple differs from observed original run")
			}
			return row, nil
		}
	}
	return nil, fmt.Errorf("exact original approval absent")
}
func originalTool(snapshot *sdk.ConversationStateResponse, turnID, op, messageID string) (*api.ToolStepState, error) {
	var found *api.ToolStepState
	if snapshot != nil && snapshot.Conversation != nil {
		for _, turn := range snapshot.Conversation.Turns {
			if turn == nil || turn.TurnID != turnID || turn.Execution == nil {
				continue
			}
			for _, page := range turn.Execution.Pages {
				if page == nil {
					continue
				}
				for _, step := range page.ToolSteps {
					if step == nil || step.ToolCallID != op || (messageID != "" && step.ToolMessageID != messageID) {
						continue
					}
					if found != nil && found.ToolMessageID != step.ToolMessageID {
						return nil, fmt.Errorf("native operation has multiple effect messages")
					}
					found = step
				}
			}
		}
	}
	if found == nil {
		return nil, fmt.Errorf("original operation/message missing from canonical transcript")
	}
	return found, nil
}
func rawOwnedPayload(ctx context.Context, target, bearer, id string) (any, string, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", target+"/v1/api/payload/"+url.PathEscape(id)+"?raw=1", nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, "", fmt.Errorf("original payload read status %d", response.StatusCode)
	}
	bytes, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, "", err
	}
	var actual any
	if json.Unmarshal(bytes, &actual) != nil {
		return nil, "", fmt.Errorf("original payload is not JSON")
	}
	return actual, string(bytes), nil
}
func exactReceiptEvents(events []json.RawMessage, tuple approvalTuple, committed any) (int, bool, error) {
	count := 0
	terminal := false
	for _, raw := range events {
		var event struct {
			Type       string `json:"type"`
			ToolCallID string `json:"toolCallId"`
			MessageID  string `json:"messageId"`
			Content    string `json:"content"`
			Outcome    struct {
				Type string `json:"type"`
			} `json:"outcome"`
		}
		if json.Unmarshal(raw, &event) != nil {
			return 0, false, fmt.Errorf("canonical event decode")
		}
		if event.Type == "RUN_FINISHED" && event.Outcome.Type == "success" {
			terminal = true
		}
		if event.Type != "TOOL_CALL_RESULT" || event.ToolCallID != tuple.PublicCallID {
			continue
		}
		if event.MessageID != tuple.MessageID {
			return 0, false, fmt.Errorf("original public operation refers to a different effect message")
		}
		var actual any
		if json.Unmarshal([]byte(event.Content), &actual) != nil || !reflect.DeepEqual(actual, committed) {
			return 0, false, fmt.Errorf("canonical original-op result differs from committed receipt")
		}
		count++
	}
	if count != 1 {
		return count, terminal, fmt.Errorf("original-op receipt event count is %d", count)
	}
	return count, terminal, nil
}
func (h *handler) approval(w http.ResponseWriter, r *http.Request, capture bool) {
	prefix := "/approval-receipt/"
	if capture {
		prefix = "/approval-capture/"
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
	if err != nil {
		write(w, 500, &probeError{"approval", "client", "client setup"})
		return
	}
	if _, err = client.GetConversation(ctx, actual.ConversationID); err != nil {
		write(w, 403, &probeError{"approval", "read-denied", "same owner cannot read original conversation"})
		return
	}
	id := r.URL.Query().Get("id")
	if capture {
		page, e := client.ListPendingToolApprovals(ctx, &sdk.ListPendingToolApprovalsInput{ConversationID: actual.ConversationID, Status: "pending"})
		if e != nil {
			write(w, 409, &probeError{"approval", "pending-read", "pending read failed"})
			return
		}
		row, e := findExactApproval(page, id, actual)
		if e != nil {
			write(w, 409, &probeError{"approval", "tuple-mismatch", e.Error()})
			return
		}
		op, _ := row.Metadata["opId"].(string)
		if op == "" {
			write(w, 409, &probeError{"approval", "operation-absent", "exact native operation id absent"})
			return
		}
		snapshot, e := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: actual.ConversationID, IncludeModelCalls: true, IncludeToolCalls: true})
		if e != nil {
			write(w, 409, &probeError{"approval", "transcript", "transcript read failed"})
			return
		}
		step, e := originalTool(snapshot, actual.TurnID, op, "")
		if e != nil || step.ParentMessageID != row.MessageID || step.ToolMessageID == "" || step.ToolMessageID == row.MessageID {
			write(w, 409, &probeError{"approval", "operation-mismatch", "exact effect message/parent relationship differs from pending approval"})
			return
		}
		argumentsRaw, _ := json.Marshal(row.Arguments)
		var original map[string]any
		_ = json.Unmarshal(argumentsRaw, &original)
		names := []string{}
		if values, ok := original["names"].([]any); ok {
			for _, value := range values {
				if name, ok := value.(string); ok {
					names = append(names, name)
				}
			}
		}
		sort.Strings(names)
		tuple := approvalTuple{ApprovalID: row.ID, Owner: row.UserID, ConversationID: actual.ConversationID, TurnID: actual.TurnID, OperationID: op, PublicCallID: agui.ProtocolToolCallID(actual.TurnID, op), QueueParentMessageID: row.MessageID, MessageID: step.ToolMessageID, RequestID: step.ToolMessageID + "/agui-approval/tool_request", ResponseID: step.ToolMessageID + "/agui-approval/tool_response", OriginalArgs: original, OriginalNames: names}
		record.mu.Lock()
		if record.approvals == nil {
			record.approvals = map[string]approvalTuple{}
		}
		if _, exists := record.approvals[id]; exists {
			record.mu.Unlock()
			write(w, 409, &probeError{"approval", "duplicate-capture", "original tuple already captured"})
			return
		}
		record.approvals[id] = tuple
		record.mu.Unlock()
		write(w, 200, map[string]any{"tuple": tuple})
		return
	}
	record.mu.Lock()
	tuple, exists := record.approvals[id]
	record.mu.Unlock()
	if !exists {
		write(w, 409, &probeError{"approval", "not-captured", "original tuple must be captured before decision"})
		return
	}
	page, e := client.ListPendingToolApprovals(ctx, &sdk.ListPendingToolApprovalsInput{ConversationID: tuple.ConversationID, Status: "executed", UserID: tuple.Owner})
	if e != nil {
		write(w, 409, &probeError{"approval", "receipt-read", "executed receipt read failed"})
		return
	}
	row, e := findExactApproval(page, tuple.ApprovalID, actual)
	if e != nil || row.Status != "executed" || row.Decision != "approve" || row.UserID != tuple.Owner || row.MessageID != tuple.QueueParentMessageID {
		write(w, 409, &probeError{"approval", "receipt-mismatch", "original approval is not executed/approve for same owner"})
		return
	}
	snapshot, e := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: tuple.ConversationID, IncludeModelCalls: true, IncludeToolCalls: true})
	if e != nil {
		write(w, 409, &probeError{"approval", "transcript", "receipt transcript read failed"})
		return
	}
	step, e := originalTool(snapshot, tuple.TurnID, tuple.OperationID, tuple.MessageID)
	if e != nil || step.Status != "completed" || step.RequestPayloadID != tuple.RequestID || step.ResponsePayloadID != tuple.ResponseID {
		write(w, 409, &probeError{"approval", "effect-mismatch", "same original effect payload identity/status mismatch"})
		return
	}
	request, _, e := rawOwnedPayload(ctx, target, token(r), tuple.RequestID)
	if e != nil {
		write(w, 409, &probeError{"approval", "request-payload", e.Error()})
		return
	}
	response, text, e := rawOwnedPayload(ctx, target, token(r), tuple.ResponseID)
	if e != nil {
		write(w, 409, &probeError{"approval", "response-payload", e.Error()})
		return
	}
	h.mu.Lock()
	continuation := h.runs[r.URL.Query().Get("runProbe")]
	h.mu.Unlock()
	if continuation == nil || continuation.ownerHash != record.ownerHash {
		write(w, 409, &probeError{"approval", "continuation-owner", "original successor observation absent"})
		return
	}
	continuation.mu.Lock()
	events := append([]json.RawMessage(nil), continuation.events...)
	observedIdentity := continuation.identity
	continuation.mu.Unlock()
	if observedIdentity.RunID != r.URL.Query().Get("runId") || observedIdentity.ConversationID != tuple.ConversationID || observedIdentity.TurnID != tuple.TurnID {
		write(w, 409, &probeError{"approval", "continuation-identity", "actual successor run/conversation/turn differs from original decision"})
		return
	}
	count, terminal, e := exactReceiptEvents(events, tuple, response)
	if e != nil || !terminal {
		message := "original successor has no success terminal"
		if e != nil {
			message = e.Error()
		}
		write(w, 409, &probeError{"approval", "event-mismatch", message})
		return
	}
	write(w, 200, map[string]any{"tuple": tuple, "queueStatus": row.Status, "decision": row.Decision, "effectCount": count, "originalResultEventCount": count, "recoveredTerminal": "success", "approvedRequest": request, "actualResponse": response, "literalEffectContent": "Native original tool result:\n" + text})
}
