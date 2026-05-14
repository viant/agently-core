package control

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

const Name = "ui/control"

type SetValueInput struct {
	ClientID    string      `json:"clientId,omitempty"`
	WindowID    string      `json:"windowId,omitempty"`
	WindowKey   string      `json:"windowKey,omitempty"`
	ControlID   string      `json:"controlId"`
	Scope       string      `json:"scope,omitempty"`
	Value       interface{} `json:"value"`
	BindingPath string      `json:"bindingPath,omitempty"`
	DataField   string      `json:"dataField,omitempty"`
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
		{Name: "setValue", Description: "Set a visible live UI control value on an existing window.", Input: reflect.TypeOf(&SetValueInput{}), Output: reflect.TypeOf(&CommandOutput{})},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "setvalue":
		return s.setValue, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

func (s *Service) setValue(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*SetValueInput)
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
	controlID := strings.TrimSpace(input.ControlID)
	if controlID == "" {
		return fmt.Errorf("controlId is required")
	}
	windowID := strings.TrimSpace(input.WindowID)
	windowKey := strings.TrimSpace(input.WindowKey)
	if windowID == "" && windowKey == "" {
		return fmt.Errorf("windowId or windowKey is required")
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	preferredClientID := normalizeOptionalClientID(input.ClientID)
	if preferredClientID == "" {
		preferredClientID = normalizeOptionalClientID(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	}
	clientID, namespace, _, win, err := s.reg.FindWindow(ctx, conversationID, preferredClientID, windowID, windowKey)
	if err != nil {
		return err
	}
	targetWindowID := strings.TrimSpace(win.WindowID)
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  clientID,
		Namespace: namespace,
		Method:    "ui.control.setValue",
		Params: map[string]interface{}{
			"windowId":    targetWindowID,
			"controlId":   controlID,
			"scope":       strings.TrimSpace(input.Scope),
			"value":       input.Value,
			"bindingPath": strings.TrimSpace(input.BindingPath),
			"dataField":   strings.TrimSpace(input.DataField),
		},
	})
	if err != nil {
		return err
	}
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
