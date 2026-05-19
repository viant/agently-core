package context

import (
	"context"
	"reflect"
	"strings"

	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

const Name = "ui/context"

type GetInput struct {
	ClientID  string `json:"clientId,omitempty"`
	WindowID  string `json:"windowId,omitempty"`
	WindowKey string `json:"windowKey,omitempty"`
}

type WindowContext struct {
	Window         *uireg.WindowSnapshot `json:"window,omitempty"`
	DataSourceRefs []string              `json:"dataSourceRefs,omitempty"`
	Surface        *uireg.WindowSurface  `json:"surface,omitempty"`
}

type GetOutput struct {
	ConversationID  string                  `json:"conversationId,omitempty"`
	ClientID        string                  `json:"clientId,omitempty"`
	FocusedWindowID string                  `json:"focusedWindowId,omitempty"`
	Selected        *uireg.SnapshotSelected `json:"selected,omitempty"`
	Windows         []WindowContext         `json:"windows,omitempty"`
	RecentEvents    []uireg.UIEvent         `json:"recentEvents,omitempty"`
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
		{Name: "get", Description: "Return the combined current-conversation workspace UI snapshot including visible windows, surfaces, datasource refs, and recent events.", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&GetOutput{})},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "get":
		return s.get, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

func (s *Service) get(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*GetInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*GetOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	preferredClientID := normalizeOptionalClientID(input.ClientID)
	items, err := s.reg.ListByConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if preferredClientID != "" {
		filtered := make([]uireg.ClientSnapshot, 0, len(items))
		for _, item := range items {
			if strings.TrimSpace(item.ClientID) == preferredClientID {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if len(items) == 0 {
		output.ConversationID = conversationID
		return nil
	}
	client := items[0]
	output.ConversationID = conversationID
	output.ClientID = client.ClientID
	if client.Snapshot != nil {
		output.FocusedWindowID = strings.TrimSpace(client.Snapshot.Selected.WindowID)
		selected := client.Snapshot.Selected
		output.Selected = &selected
		for _, win := range client.Snapshot.Windows {
			windowCopy := win
			if strings.TrimSpace(input.WindowID) != "" && strings.TrimSpace(win.WindowID) != strings.TrimSpace(input.WindowID) {
				continue
			}
			if strings.TrimSpace(input.WindowID) == "" && strings.TrimSpace(input.WindowKey) != "" && strings.TrimSpace(win.WindowKey) != strings.TrimSpace(input.WindowKey) {
				continue
			}
			windowCopy.DataSources = nil
			output.Windows = append(output.Windows, WindowContext{
				Window:         &windowCopy,
				DataSourceRefs: uireg.ListDataSourceRefs(&win),
				Surface:        uireg.BuildWindowSurface(&win),
			})
		}
	}
	output.RecentEvents = s.reg.ListEvents(conversationID, client.ClientID, strings.TrimSpace(input.WindowID), strings.TrimSpace(input.WindowKey), 10, 0)
	return nil
}

func normalizeOptionalClientID(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "default") {
		return ""
	}
	return value
}
