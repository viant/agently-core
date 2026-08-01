package events

import (
	"context"
	"reflect"
	"sort"
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
	Order     string   `json:"order,omitempty"`
}

type ListOutput struct {
	ConversationID string          `json:"conversationId,omitempty"`
	ClientID       string          `json:"clientId,omitempty"`
	Events         []uireg.UIEvent `json:"events,omitempty"`
}

type RecordInput struct {
	ClientID  string                 `json:"clientId,omitempty"`
	WindowID  string                 `json:"windowId,omitempty"`
	WindowKey string                 `json:"windowKey,omitempty"`
	Kind      string                 `json:"kind,omitempty"`
	Detail    map[string]interface{} `json:"detail,omitempty"`
}

type RecordOutput struct {
	Recorded bool          `json:"recorded"`
	Event    uireg.UIEvent `json:"event"`
}

type DurableReportRunValidator interface {
	ValidateDurableUIEvent(ctx context.Context, conversationID, kind string, detail map[string]interface{}) error
}

type Option func(*Service)

func WithDurableReportRuns(validator DurableReportRunValidator) Option {
	return func(service *Service) {
		service.durableReportRuns = validator
	}
}

type Service struct {
	reg               *uireg.Registry
	durableReportRuns DurableReportRunValidator
}

func New(bridge *forgeuisvc.Service, options ...Option) *Service {
	service := &Service{reg: uireg.New(bridge)}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "list", Description: "List recent structured UI events for the current conversation and optional window/client scope. Defaults to the latest 10 events in newest-first order.", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "record", Description: "Record an authenticated browser-originated UI event for the current conversation and visible window.", Input: reflect.TypeOf(&RecordInput{}), Output: reflect.TypeOf(&RecordOutput{})},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "list":
		return s.list, nil
	case "record":
		return s.record, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

func (s *Service) record(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*RecordInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*RecordOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	kind := strings.TrimSpace(input.Kind)
	if conversationID == "" || kind == "" {
		return svc.NewInvalidInputError(in)
	}
	items, err := s.reg.ListReadableByConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	clientID := normalizeOptionalClientID(input.ClientID)
	windowID := strings.TrimSpace(input.WindowID)
	windowKey := strings.TrimSpace(input.WindowKey)
	if s.durableReportRuns != nil && isDurableReportRunEvent(kind) {
		if windowID == "" {
			return svc.NewInvalidInputError(in)
		}
		if err := s.durableReportRuns.ValidateDurableUIEvent(ctx, conversationID, kind, input.Detail); err != nil {
			return err
		}
		// Durable run lifecycle events are routed only by their exact windowId.
		// A stale or reused windowKey must never select a different window.
		windowKey = ""
	}
	record := func(clientID, resolvedWindowID, resolvedWindowKey string) {
		event := uireg.UIEvent{
			ConversationID: conversationID,
			ClientID:       strings.TrimSpace(clientID),
			WindowID:       strings.TrimSpace(resolvedWindowID),
			WindowKey:      strings.TrimSpace(resolvedWindowKey),
			Kind:           kind,
			Actor:          "user",
			Detail:         input.Detail,
		}
		event = s.reg.RecordConversationEvent(conversationID, event)
		output.Recorded = true
		output.Event = event
	}
	for _, item := range items {
		if clientID != "" && strings.TrimSpace(item.ClientID) != clientID {
			continue
		}
		var matched *uireg.WindowSnapshot
		if item.Snapshot != nil {
			for index := range item.Snapshot.Windows {
				window := &item.Snapshot.Windows[index]
				if windowID != "" && strings.TrimSpace(window.WindowID) != windowID {
					continue
				}
				if windowKey != "" && strings.TrimSpace(window.WindowKey) != windowKey {
					continue
				}
				matched = window
				break
			}
		}
		if (windowID != "" || windowKey != "") && matched == nil {
			continue
		}
		if matched != nil {
			windowID = strings.TrimSpace(matched.WindowID)
			windowKey = strings.TrimSpace(matched.WindowKey)
		}
		record(item.ClientID, windowID, windowKey)
		return nil
	}
	// A browser bridge can reconnect between two lifecycle events. Reuse only an
	// exact identity established by a trusted command, snapshot, or previously
	// accepted event; never infer ownership from a window-name or ID suffix.
	if windowID != "" || windowKey != "" {
		if authorized, found := s.reg.FindAuthorizedConversationWindow(conversationID, clientID, windowID, windowKey); found {
			record(authorized.ClientID, authorized.WindowID, authorized.WindowKey)
			return nil
		}
	}
	return svc.NewInvalidInputError(in)
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
	if !strings.EqualFold(strings.TrimSpace(input.Order), "asc") {
		sort.SliceStable(events, func(i, j int) bool { return events[i].Seq > events[j].Seq })
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

func isDurableReportRunEvent(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "report.run_start", "report.run":
		return true
	default:
		return false
	}
}
