package window

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

const Name = "ui/window"

type ListInput struct {
	ClientID string `json:"clientId,omitempty"`
}

type WindowItem struct {
	ClientID       string   `json:"clientId,omitempty"`
	WindowID       string   `json:"windowId,omitempty"`
	WindowKey      string   `json:"windowKey,omitempty"`
	WindowTitle    string   `json:"windowTitle,omitempty"`
	InTab          bool     `json:"inTab,omitempty"`
	IsModal        bool     `json:"isModal,omitempty"`
	IsMinimized    bool     `json:"isMinimized,omitempty"`
	DataSourceRefs []string `json:"dataSourceRefs,omitempty"`
}

type ListOutput struct {
	ClientID        string       `json:"clientId,omitempty"`
	FocusedWindowID string       `json:"focusedWindowId,omitempty"`
	Items           []WindowItem `json:"items,omitempty"`
}

type GetInput struct {
	ClientID  string `json:"clientId,omitempty"`
	WindowID  string `json:"windowId,omitempty"`
	WindowKey string `json:"windowKey,omitempty"`
}

type GetOutput struct {
	ClientID string                  `json:"clientId,omitempty"`
	Window   *uireg.WindowSnapshot   `json:"window,omitempty"`
	Selected *uireg.SnapshotSelected `json:"selected,omitempty"`
}

type ActivateInput struct {
	ClientID string `json:"clientId,omitempty"`
	WindowID string `json:"windowId,omitempty"`
}

type HideInput struct {
	ClientID string `json:"clientId,omitempty"`
	WindowID string `json:"windowId,omitempty"`
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
		{Name: "list", Description: "List live UI windows for the current conversation.", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "get", Description: "Get one live UI window by windowId or windowKey for the current conversation.", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&GetOutput{})},
		{Name: "show", Description: "Activate an existing live UI window by windowId.", Input: reflect.TypeOf(&ActivateInput{}), Output: reflect.TypeOf(&CommandOutput{})},
		{Name: "hide", Description: "Hide or close an existing live UI window by windowId.", Input: reflect.TypeOf(&HideInput{}), Output: reflect.TypeOf(&CommandOutput{})},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "list":
		return s.list, nil
	case "get":
		return s.get, nil
	case "show":
		return s.show, nil
	case "hide":
		return s.hide, nil
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
	items, err := s.reg.ListByConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	preferred := strings.TrimSpace(input.ClientID)
	if preferred == "" && len(items) > 0 {
		preferred = items[0].ClientID
	}
	for _, item := range items {
		if preferred != "" && item.ClientID != preferred {
			continue
		}
		output.ClientID = item.ClientID
		if item.Snapshot != nil {
			output.FocusedWindowID = strings.TrimSpace(item.Snapshot.Selected.WindowID)
			for _, win := range item.Snapshot.Windows {
				refs := make([]string, 0, len(win.DataSources))
				for ref := range win.DataSources {
					refs = append(refs, ref)
				}
				output.Items = append(output.Items, WindowItem{
					ClientID:       item.ClientID,
					WindowID:       win.WindowID,
					WindowKey:      win.WindowKey,
					WindowTitle:    win.WindowTitle,
					InTab:          win.InTab,
					IsModal:        win.IsModal,
					IsMinimized:    win.IsMinimized,
					DataSourceRefs: refs,
				})
			}
		}
		break
	}
	return nil
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
	clientID, snap, win, err := s.reg.FindWindow(ctx, conversationID, input.ClientID, input.WindowID, input.WindowKey)
	if err != nil {
		return err
	}
	output.ClientID = clientID
	output.Window = win
	if snap != nil {
		selected := snap.Selected
		output.Selected = &selected
	}
	return nil
}

func (s *Service) show(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ActivateInput)
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
	clientID, _, _, err := s.reg.FindWindow(ctx, conversationID, input.ClientID, input.WindowID, "")
	if err != nil {
		return err
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID: clientID,
		Method:   "ui.window.activate",
		Params:   map[string]interface{}{"windowId": strings.TrimSpace(input.WindowID)},
	})
	if err != nil {
		return err
	}
	output.ClientID = clientID
	output.OK = resp.OK
	output.Error = resp.Error
	return nil
}

func (s *Service) hide(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*HideInput)
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
	clientID, _, _, err := s.reg.FindWindow(ctx, conversationID, input.ClientID, input.WindowID, "")
	if err != nil {
		return err
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID: clientID,
		Method:   "ui.window.close",
		Params:   map[string]interface{}{"windowId": strings.TrimSpace(input.WindowID)},
	})
	if err != nil {
		return err
	}
	output.ClientID = clientID
	output.OK = resp.OK
	output.Error = resp.Error
	return nil
}
