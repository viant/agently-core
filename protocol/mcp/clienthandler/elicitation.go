package clienthandler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agentplan "github.com/viant/agently-core/protocol/agent/plan"
	"github.com/viant/agently-core/runtime/memory"
	elicsvc "github.com/viant/agently-core/service/elicitation"
	"github.com/viant/jsonrpc"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

// Handler adapts MCP server-initiated elicitation/create callbacks
// into agently-core elicitation persistence + wait lifecycle.
type Handler struct {
	conversations apiconv.Client
	elicitation   *elicsvc.Service
	conversation  string
	lastRequestID int64
}

// New returns a client callback handler for MCP sessions.
func New(elicitation *elicsvc.Service, conversations apiconv.Client) *Handler {
	return &Handler{
		conversations: conversations,
		elicitation:   elicitation,
	}
}

func (h *Handler) SetConversationID(id string) {
	h.conversation = strings.TrimSpace(id)
}

func (h *Handler) Notify(_ context.Context, _ *jsonrpc.Notification) error {
	return nil
}

func (h *Handler) NextRequestID() jsonrpc.RequestId {
	return atomic.AddInt64(&h.lastRequestID, 1)
}

func (h *Handler) LastRequestID() jsonrpc.RequestId {
	return atomic.LoadInt64(&h.lastRequestID)
}

func (h *Handler) Init(_ context.Context, _ *mcpschema.ClientCapabilities) {}

func (h *Handler) Implements(method string) bool {
	return method == mcpschema.MethodElicitationCreate
}

func (h *Handler) OnNotification(_ context.Context, _ *jsonrpc.Notification) {}

func (h *Handler) ListRoots(_ context.Context, _ *jsonrpc.TypedRequest[*mcpschema.ListRootsRequest]) (*mcpschema.ListRootsResult, *jsonrpc.Error) {
	return &mcpschema.ListRootsResult{}, nil
}

func (h *Handler) CreateMessage(_ context.Context, _ *jsonrpc.TypedRequest[*mcpschema.CreateMessageRequest]) (*mcpschema.CreateMessageResult, *jsonrpc.Error) {
	return nil, jsonrpc.NewMethodNotFound("sampling/createMessage not implemented", nil)
}

func (h *Handler) Elicit(ctx context.Context, request *jsonrpc.TypedRequest[*mcpschema.ElicitRequest]) (*mcpschema.ElicitResult, *jsonrpc.Error) {
	if h == nil || h.elicitation == nil || h.conversations == nil {
		return nil, jsonrpc.NewInternalError("elicitation service not configured", nil)
	}
	if request == nil || request.Request == nil {
		return nil, jsonrpc.NewInvalidParamsError("missing elicitation request", nil)
	}
	conversationID := strings.TrimSpace(h.conversation)
	if conversationID == "" {
		conversationID = strings.TrimSpace(memory.ConversationIDFromContext(ctx))
	}

	params := request.Request.Params
	if strings.TrimSpace(params.ElicitationId) == "" {
		if request.Request.Id != 0 {
			params.ElicitationId = fmt.Sprintf("%v", request.Request.Id)
		} else {
			params.ElicitationId = uuid.NewString()
		}
	}
	waitForResolution := true
	if conversationID == "" {
		conversationID = "mcp-" + uuid.NewString()
		waitForResolution = false
	}
	conv, err := h.conversations.GetConversation(ctx, conversationID)
	if err != nil {
		fmt.Printf("[elicit-handler] GetConversation(%s) error: %v\n", conversationID, err)
		return nil, jsonrpc.NewInternalError(fmt.Sprintf("get conversation: %v", err), nil)
	}
	if conv == nil {
		fmt.Printf("[elicit-handler] conversation %s NOT found — creating ephemeral, waitForResolution=false\n", conversationID)
		seed := apiconv.NewConversation()
		seed.SetId(conversationID)
		if patchErr := h.conversations.PatchConversations(ctx, seed); patchErr != nil {
			return nil, jsonrpc.NewInternalError(fmt.Sprintf("create conversation: %v", patchErr), nil)
		}
		conv, _ = h.conversations.GetConversation(ctx, conversationID)
		// Direct tool execution (without conversation context) has no resolve callback
		// handshake; record pending elicitation and return immediately.
		waitForResolution = false
	} else {
		fmt.Printf("[elicit-handler] conversation %s found, waitForResolution=true\n", conversationID)
	}
	elic := &agentplan.Elicitation{ElicitRequestParams: params}
	turn := &memory.TurnMeta{ConversationID: conversationID}
	if conv != nil && conv.LastTurnId != nil {
		turn.TurnID = *conv.LastTurnId
		turn.ParentMessageID = *conv.LastTurnId
	}
	if _, err := h.elicitation.Record(ctx, turn, "tool", elic); err != nil {
		return nil, jsonrpc.NewInternalError(fmt.Sprintf("record elicitation: %v", err), nil)
	}
	if !waitForResolution {
		fmt.Printf("[elicit-handler] AUTO-ACCEPT (no wait) convID=%s elicitID=%s\n", conversationID, elic.ElicitationId)
		return &mcpschema.ElicitResult{Action: mcpschema.ElicitResultActionAccept}, nil
	}
	fmt.Printf("[elicit-handler] WAITING for resolution convID=%s elicitID=%s\n", conversationID, elic.ElicitationId)
	status, payload, err := h.elicitation.Wait(ctx, conversationID, elic.ElicitationId)
	if err != nil {
		fmt.Printf("[elicit-handler] Wait error convID=%s elicitID=%s err=%v\n", conversationID, elic.ElicitationId, err)
		return nil, jsonrpc.NewInternalError(fmt.Sprintf("wait elicitation: %v", err), nil)
	}
	payloadJSON, _ := json.Marshal(payload)
	fmt.Printf("[elicit-handler] resolved convID=%s elicitID=%s status=%s payload=%s\n", conversationID, elic.ElicitationId, status, string(payloadJSON))
	return &mcpschema.ElicitResult{
		Action:  mcpschema.ElicitResultAction(status),
		Content: payload,
	}, nil
}
