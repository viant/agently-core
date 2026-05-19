package datasource

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

const Name = "ui/datasource"

type PeekInput struct {
	ClientID      string `json:"clientId,omitempty"`
	WindowID      string `json:"windowId,omitempty"`
	WindowKey     string `json:"windowKey,omitempty"`
	DataSourceRef string `json:"dataSourceRef"`
}

type PeekOutput struct {
	ClientID      string                    `json:"clientId,omitempty"`
	WindowID      string                    `json:"windowId,omitempty"`
	WindowKey     string                    `json:"windowKey,omitempty"`
	DataSourceRef string                    `json:"dataSourceRef,omitempty"`
	Snapshot      *uireg.DataSourceSnapshot `json:"snapshot,omitempty"`
}

type ListInput struct {
	ClientID  string `json:"clientId,omitempty"`
	WindowID  string `json:"windowId,omitempty"`
	WindowKey string `json:"windowKey,omitempty"`
}

type ListOutput struct {
	ClientID       string   `json:"clientId,omitempty"`
	WindowID       string   `json:"windowId,omitempty"`
	WindowKey      string   `json:"windowKey,omitempty"`
	DataSourceRefs []string `json:"dataSourceRefs,omitempty"`
}

type RefreshInput struct {
	ClientID      string `json:"clientId,omitempty"`
	WindowID      string `json:"windowId,omitempty"`
	WindowKey     string `json:"windowKey,omitempty"`
	DataSourceRef string `json:"dataSourceRef,omitempty"`
}

type CommandOutput struct {
	ClientID string `json:"clientId,omitempty"`
	OK       bool   `json:"ok,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Service struct {
	bridge *forgeuisvc.Service
	reg    *uireg.Registry
}

func New(bridge *forgeuisvc.Service) *Service {
	return &Service{bridge: bridge, reg: uireg.New(bridge)}
}

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "list", Description: "List datasource refs exposed by a live UI window.", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "peek", Description: "Return the live datasource snapshot the current UI window is showing.", Input: reflect.TypeOf(&PeekInput{}), Output: reflect.TypeOf(&PeekOutput{})},
		{Name: "refresh", Description: "Request a live datasource refresh on an existing UI window.", Input: reflect.TypeOf(&RefreshInput{}), Output: reflect.TypeOf(&CommandOutput{})},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "list":
		return s.list, nil
	case "peek":
		return s.peek, nil
	case "refresh":
		return s.refresh, nil
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
	clientID, _, _, win, err := s.reg.FindWindow(ctx, conversationID, normalizeOptionalClientID(input.ClientID), input.WindowID, input.WindowKey)
	if err != nil {
		return err
	}
	output.ClientID = clientID
	output.WindowID = win.WindowID
	output.WindowKey = win.WindowKey
	output.DataSourceRefs = uireg.ListDataSourceRefs(win)
	return nil
}

func (s *Service) peek(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*PeekInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*PeekOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	clientID, _, _, win, err := s.reg.FindWindow(ctx, conversationID, normalizeOptionalClientID(input.ClientID), input.WindowID, input.WindowKey)
	if err != nil {
		return err
	}
	ref := strings.TrimSpace(input.DataSourceRef)
	if ref == "" {
		return fmt.Errorf("dataSourceRef is required")
	}
	snap, ok := win.DataSources[ref]
	if !ok {
		return fmt.Errorf("datasource %q not found on window", ref)
	}
	output.ClientID = clientID
	output.WindowID = win.WindowID
	output.WindowKey = win.WindowKey
	output.DataSourceRef = ref
	output.Snapshot = &snap
	return nil
}

func (s *Service) refresh(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*RefreshInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*CommandOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	if s.bridge == nil {
		return fmt.Errorf("ui bridge not configured")
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	clientID, namespace, _, win, err := s.reg.FindWindow(ctx, conversationID, normalizeOptionalClientID(input.ClientID), input.WindowID, input.WindowKey)
	if err != nil {
		return err
	}
	dataSourceRef := strings.TrimSpace(input.DataSourceRef)
	if dataSourceRef == "" {
		refs := uireg.ListDataSourceRefs(win)
		if len(refs) == 0 {
			return fmt.Errorf("window has no datasource refs")
		}
		sort.Strings(refs)
		dataSourceRef = refs[0]
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  clientID,
		Namespace: namespace,
		Method:    "ui.data.fetch",
		Params: map[string]interface{}{
			"windowId":      strings.TrimSpace(win.WindowID),
			"dataSourceRef": dataSourceRef,
		},
	})
	if err != nil {
		return err
	}
	s.reg.RecordEvent(namespace, clientID, uireg.UIEvent{
		ConversationID: strings.TrimSpace(win.ConversationID),
		ClientID:       clientID,
		WindowID:       strings.TrimSpace(win.WindowID),
		WindowKey:      strings.TrimSpace(win.WindowKey),
		Kind:           "datasource.refresh",
		Actor:          "agent",
		Detail: map[string]interface{}{
			"dataSourceRef": dataSourceRef,
		},
	})
	output.ClientID = clientID
	output.OK = resp.OK
	output.Error = resp.Error
	return nil
}

func normalizeOptionalClientID(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "default") {
		return ""
	}
	return value
}
