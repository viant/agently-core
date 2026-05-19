package events

import (
	"context"
	"reflect"
	"strings"

	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

const Name = "ui/events"

type ListInput struct {
	ClientID  string   `json:"clientId,omitempty"`
	WindowID  string   `json:"windowId,omitempty"`
	WindowKey string   `json:"windowKey,omitempty"`
	Kinds     []string `json:"kinds,omitempty"`
	SinceSeq  int64    `json:"sinceSeq,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}

type ListOutput struct {
	ConversationID string          `json:"conversationId,omitempty"`
	ClientID       string          `json:"clientId,omitempty"`
	Events         []uireg.UIEvent `json:"events,omitempty"`
}

type Service struct {
	reg *uireg.Registry
}

func New(bridge *forgeuisvc.Service) *Service {
	return &Service{reg: uireg.New(bridge)}
}

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "list", Description: "List recent structured UI events for the current conversation and optional window/client scope. Defaults to the latest 10 events.", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "list":
		return s.list, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

func (s *Service) list(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ListInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ListOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	output.ConversationID = conversationID
	clientID := normalizeOptionalClientID(input.ClientID)
	output.ClientID = clientID
	events := s.reg.ListEvents(conversationID, clientID, strings.TrimSpace(input.WindowID), strings.TrimSpace(input.WindowKey), input.Limit, input.SinceSeq)
	if len(input.Kinds) > 0 {
		allowed := map[string]struct{}{}
		for _, kind := range input.Kinds {
			if normalized := strings.TrimSpace(strings.ToLower(kind)); normalized != "" {
				allowed[normalized] = struct{}{}
			}
		}
		filtered := make([]uireg.UIEvent, 0, len(events))
		for _, event := range events {
			if _, ok := allowed[strings.ToLower(strings.TrimSpace(event.Kind))]; ok {
				filtered = append(filtered, event)
			}
		}
		events = filtered
	}
	output.Events = events
	return nil
}

func normalizeOptionalClientID(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "default") {
		return ""
	}
	return value
}
