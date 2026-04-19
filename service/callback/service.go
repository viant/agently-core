package callback

import (
	"context"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	tooldef "github.com/viant/agently-core/protocol/tool"
	callbackrepo "github.com/viant/agently-core/workspace/repository/callback"
)

// Service dispatches foreground submit events to the mapped tool. It is
// constructed once at workspace bootstrap and shared across HTTP requests.
type Service struct {
	repo *callbackrepo.Repository

	// registry resolves tool names to executable handlers.
	registry tooldef.Registry

	// conv is used to resolve agentId from conversationId; optional — the
	// dispatcher still works without it, but `.agentId` in templates will
	// be empty.
	conv apiconv.Client
}

// Option configures a Service.
type Option func(*Service)

// WithConversationClient enables `.agentId` resolution from conversationId.
func WithConversationClient(c apiconv.Client) Option {
	return func(s *Service) { s.conv = c }
}

// New builds a dispatch service. repo and registry are required; panics on nil.
func New(repo *callbackrepo.Repository, registry tooldef.Registry, opts ...Option) *Service {
	if repo == nil {
		panic("callback.New: repo is required")
	}
	if registry == nil {
		panic("callback.New: registry is required")
	}
	s := &Service{repo: repo, registry: registry}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Dispatch resolves the callback, renders the payload, and invokes the
// mapped tool. Returns a DispatchOutput carrying the tool's textual result
// or a non-empty Error when the dispatch itself failed (not when the tool
// returned an application error — those flow via Result).
func (s *Service) Dispatch(ctx context.Context, in *DispatchInput) (*DispatchOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("dispatch input is required")
	}
	eventName := strings.TrimSpace(in.EventName)
	if eventName == "" {
		return nil, fmt.Errorf("eventName is required")
	}
	cb, err := s.repo.GetByEvent(ctx, eventName)
	if err != nil {
		return nil, fmt.Errorf("lookup callback %q: %w", eventName, err)
	}
	if cb == nil {
		return nil, fmt.Errorf("no callback registered for event %q", eventName)
	}

	root := s.buildTemplateRoot(ctx, in, cb)
	args, err := renderPayload(cb.Payload.Body, root)
	if err != nil {
		return nil, err
	}
	result, err := s.registry.Execute(ctx, cb.Tool, args)
	out := &DispatchOutput{
		EventName: eventName,
		Tool:      cb.Tool,
		Result:    result,
	}
	if err != nil {
		out.Error = err.Error()
	}
	return out, nil
}

// buildTemplateRoot produces the flat map handed to the payload template.
// Layer order (later wins on conflict, except reserved keys never lose):
//  1. caller-supplied Context (flattened under root, reserved keys dropped)
//  2. time helpers (now, today)
//  3. request-derived fields (eventName, conversationId, turnId, payload,
//     selectedRows)
//  4. conversation-derived fields (agentId) — always last, authoritative
func (s *Service) buildTemplateRoot(ctx context.Context, in *DispatchInput, cb interface{}) map[string]interface{} {
	_ = cb // reserved for future callback-scoped defaults

	root := make(map[string]interface{}, 16)

	// (1) flatten Context, drop reserved-key collisions
	for k, v := range in.Context {
		if _, clash := reservedKeys[k]; clash {
			continue
		}
		root[k] = v
	}

	// (2) time helpers
	now := time.Now().UTC()
	root["now"] = now.Format(time.RFC3339)
	root["today"] = now.Format("2006-01-02")

	// (3) request-derived
	root["eventName"] = in.EventName
	root["conversationId"] = in.ConversationID
	root["turnId"] = in.TurnID
	root["payload"] = in.Payload
	if sel, ok := in.Payload["selectedRows"]; ok {
		root["selectedRows"] = sel
	} else {
		root["selectedRows"] = []interface{}{}
	}

	// (4) conversation-derived
	root["agentId"] = s.resolveAgentID(ctx, in.ConversationID)

	return root
}

// resolveAgentID looks up the conversation and returns its AgentId when
// present. Returns "" when unresolvable.
func (s *Service) resolveAgentID(ctx context.Context, conversationID string) string {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" || s.conv == nil {
		return ""
	}
	conv, err := s.conv.GetConversation(ctx, conversationID)
	if err != nil || conv == nil || conv.AgentId == nil {
		return ""
	}
	return strings.TrimSpace(*conv.AgentId)
}
