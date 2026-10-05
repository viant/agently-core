package elicitation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	receiptstore "github.com/viant/agently-core/app/store/elicitationreceipt"
	auth "github.com/viant/agently-core/internal/auth"
	elact "github.com/viant/agently-core/service/elicitation/action"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/mcp-protocol/schema"
)

type nativeReceiptClient interface {
	NativeComponentInvoker() dexec.ComponentInvoker
	BindComponentInvoker(dexec.ComponentInvoker) apiconv.Client
}
type waiterInspector interface{ HasWaiter(string, string) bool }
type ResolutionDisposition string

const (
	ResolutionDeferred     ResolutionDisposition = "deferred"
	ResolutionWokeExisting ResolutionDisposition = "woke_existing_execution"
)

type CheckedResolution struct {
	Receipt     *receiptstore.Receipt
	Disposition ResolutionDisposition
}

func receiptID(conversationID, elicitationID string) string {
	sum := sha256.Sum256([]byte(conversationID + "\x00" + elicitationID))
	return "elicitation-receipt/" + hex.EncodeToString(sum[:])
}
func canonicalReceiptPayload(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func sameReceipt(receipt *receiptstore.Receipt, input *receiptstore.Input) bool {
	if receipt == nil || receipt.Principal != input.Principal || receipt.ConversationID != input.ConversationID || receipt.ElicitationID != input.ElicitationID || receipt.Action != input.Action || receipt.Reason != input.Reason {
		return false
	}
	before, e1 := canonicalReceiptPayload(receipt.Payload)
	after, e2 := canonicalReceiptPayload(input.Payload)
	return e1 == nil && e2 == nil && bytes.Equal(before, after)
}
func readAnswerReceipt(ctx context.Context, client apiconv.Client, conversationID, id string) (*receiptstore.Receipt, error) {
	payload, err := client.GetPayload(ctx, receiptID(conversationID, id))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(strings.ToLower(err.Error()), "no rows") {
			return nil, nil
		}
		return nil, err
	}
	if payload == nil {
		return nil, nil
	}
	if payload.Kind != "elicitation_answer_receipt" || payload.InlineBody == nil {
		return nil, fmt.Errorf("invalid elicitation receipt")
	}
	var receipt receiptstore.Receipt
	if err = json.Unmarshal(*payload.InlineBody, &receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

// InspectResolution checks exact original action/payload before protocol admission.
// It performs no writes and cannot wake an in-process waiter.
func (s *Service) InspectResolution(ctx context.Context, conversationID, id, action string, payload map[string]interface{}, reason string) (*receiptstore.Receipt, error) {
	input := receiptInput(ctx, conversationID, id, action, payload, reason)
	if input.Principal == "" {
		return nil, fmt.Errorf("elicitation resolution requires an authenticated principal")
	}
	conv, err := s.client.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if conv == nil || conv.CreatedByUserId == nil || *conv.CreatedByUserId != input.Principal {
		return nil, fmt.Errorf("elicitation conversation is not owned by caller")
	}
	receipt, err := readAnswerReceipt(ctx, s.client, conversationID, id)
	if err != nil {
		return nil, err
	}
	if receipt != nil && !sameReceipt(receipt, input) {
		return nil, fmt.Errorf("elicitation was already resolved with a different answer")
	}
	return receipt, nil
}
func receiptInput(ctx context.Context, conversationID, id, action string, payload map[string]interface{}, reason string) *receiptstore.Input {
	var raw json.RawMessage
	if payload != nil {
		raw, _ = json.Marshal(payload)
	}
	return &receiptstore.Input{ConversationID: strings.TrimSpace(conversationID), ElicitationID: strings.TrimSpace(id), Principal: strings.TrimSpace(auth.EffectiveUserID(ctx)), Action: elact.Normalize(action), Payload: raw, Reason: strings.TrimSpace(reason)}
}

// ResolveChecked commits original-answer receipt and all native answer mutations
// in one Datly transaction. Only its confirmed owner may publish/wake afterwards.
func (s *Service) ResolveChecked(ctx context.Context, conversationID, id, action string, payload map[string]interface{}, reason string) (*CheckedResolution, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("elicitation service unavailable")
	}
	native, ok := s.client.(nativeReceiptClient)
	if !ok || native.NativeComponentInvoker() == nil {
		return nil, fmt.Errorf("atomic elicitation receipt capability unavailable")
	}
	if _, err := s.InspectResolution(ctx, conversationID, id, action, payload, reason); err != nil {
		return nil, err
	}
	input := receiptInput(ctx, conversationID, id, action, payload, reason)
	receipt, _, err := receiptstore.Resolve(ctx, native.NativeComponentInvoker(), input, &receiptApplier{service: s, native: native})
	if err != nil {
		return nil, err
	}
	status := elact.ToStatus(receipt.Action)
	s.emitElicitationResolved(ctx, receipt.AuthoritativeConversationID, receipt.ElicitationID, status, payload)
	if receipt.ProxyConversationID != "" && receipt.ProxyConversationID != receipt.AuthoritativeConversationID {
		s.emitElicitationResolved(ctx, receipt.ProxyConversationID, receipt.ElicitationID, status, payload)
	}
	disposition := ResolutionDeferred
	if s.router != nil && s.router.AcceptByElicitation(receipt.AuthoritativeConversationID, receipt.ElicitationID, &schema.ElicitResult{Action: schema.ElicitResultAction(receipt.Action), Content: payload}) {
		disposition = ResolutionWokeExisting
	}
	return &CheckedResolution{Receipt: receipt, Disposition: disposition}, nil
}
func (s *Service) HasLiveWaiter(conversationID, id string) bool {
	if s == nil || s.router == nil {
		return false
	}
	inspector, ok := s.router.(waiterInspector)
	return ok && inspector.HasWaiter(conversationID, id)
}

type receiptApplier struct {
	service *Service
	native  nativeReceiptClient
}

func (a *receiptApplier) Apply(ctx context.Context, invoker dexec.ComponentInvoker, input *receiptstore.Input) (*receiptstore.Receipt, error) {
	clone := *a.service
	clone.client = a.native.BindComponentInvoker(invoker)
	clone.streamPub = nil
	clone.router = nil
	if existing, err := readAnswerReceipt(ctx, clone.client, input.ConversationID, input.ElicitationID); err != nil {
		return nil, err
	} else if existing != nil {
		if !sameReceipt(existing, input) {
			return nil, fmt.Errorf("elicitation answer receipt conflict")
		}
		return existing, nil
	}
	target, err := clone.resolveElicitationTarget(ctx, input.ConversationID, input.ElicitationID)
	if err != nil {
		return nil, err
	}
	if target.authoritative == nil {
		return nil, fmt.Errorf("elicitation request unavailable")
	}
	status := strings.ToLower(strings.TrimSpace(stringValue(target.authoritative.Status)))
	if status != "pending" && status != "waiting_for_user" {
		return nil, fmt.Errorf("resolved elicitation has no exact original answer receipt")
	}
	var payload map[string]interface{}
	if len(input.Payload) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(input.Payload))
		decoder.UseNumber()
		if err = decoder.Decode(&payload); err != nil {
			return nil, err
		}
	}
	authoritative := target.authoritative.ConversationId
	if err = clone.UpdateStatus(ctx, authoritative, input.ElicitationID, input.Action); err != nil {
		return nil, err
	}
	resultStatus := elact.ToStatus(input.Action)
	if resultStatus == elact.StatusAccepted && payload != nil {
		if err = clone.StorePayload(ctx, authoritative, input.ElicitationID, payload); err != nil {
			return nil, err
		}
	} else if resultStatus == elact.StatusRejected && input.Reason != "" {
		if err = clone.StoreDeclineReason(ctx, authoritative, input.ElicitationID, input.Reason); err != nil {
			return nil, err
		}
	} else if resultStatus == elact.StatusCancel && input.Reason != "" {
		if err = clone.StoreCancelReason(ctx, authoritative, input.ElicitationID, input.Reason); err != nil {
			return nil, err
		}
	}
	receipt := &receiptstore.Receipt{Version: "1", Principal: input.Principal, ConversationID: input.ConversationID, ElicitationID: input.ElicitationID, RequestMessageID: target.authoritative.Id, AuthoritativeConversationID: authoritative, Action: input.Action, Payload: append(json.RawMessage(nil), input.Payload...), Reason: input.Reason}
	if target.proxy != nil {
		receipt.ProxyConversationID = target.proxy.ConversationId
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	stored := apiconv.NewPayload()
	stored.SetId(receiptID(input.ConversationID, input.ElicitationID))
	stored.SetKind("elicitation_answer_receipt")
	stored.SetMimeType("application/json")
	stored.SetStorage("inline")
	stored.SetSizeBytes(len(raw))
	stored.SetInlineBody(raw)
	if err = clone.client.PatchPayload(ctx, stored); err != nil {
		return nil, err
	}
	clone.deleteResolvedProxy(ctx, target)
	return receipt, nil
}
