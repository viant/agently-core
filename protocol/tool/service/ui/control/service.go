package control

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	"github.com/viant/forge/backend/uicontext"
	"github.com/viant/forge/backend/uimetadata"
)

const Name = "ui/control"

type SetValueInput struct {
	Target           *uimetadata.Target    `json:"target,omitempty"`
	Resource         *identity.ResourceRef `json:"resource,omitempty"`
	ExpectedRevision *uint32               `json:"expectedRevision,omitempty"`
	ConversationID   string                `json:"conversationId,omitempty"`
	ClientID         string                `json:"clientId,omitempty"`
	WindowID         string                `json:"windowId,omitempty"`
	WindowKey        string                `json:"windowKey,omitempty"`
	ControlID        string                `json:"controlId"`
	DataSourceRef    string                `json:"dataSourceRef,omitempty"`
	Scope            string                `json:"scope,omitempty"`
	Value            interface{}           `json:"value"`
	BindingPath      string                `json:"bindingPath,omitempty"`
	DataField        string                `json:"dataField,omitempty"`
}

type ClickInput struct {
	Target           *uimetadata.Target    `json:"target,omitempty"`
	Resource         *identity.ResourceRef `json:"resource,omitempty"`
	ExpectedRevision *uint32               `json:"expectedRevision,omitempty"`
	ConversationID   string                `json:"conversationId,omitempty"`
	ClientID         string                `json:"clientId,omitempty"`
	WindowID         string                `json:"windowId,omitempty"`
	WindowKey        string                `json:"windowKey,omitempty"`
	ControlID        string                `json:"controlId"`
	DataSourceRef    string                `json:"dataSourceRef,omitempty"`
}

type CommandOutput struct {
	PendingInteraction  *uicontext.ControlOperationState `json:"pendingInteraction,omitempty"`
	PendingConfirmation bool                             `json:"pendingConfirmation,omitempty"`
	Status              string                           `json:"status,omitempty"`
	Accepted            *bool                            `json:"accepted,omitempty"`
	Resource            *identity.ResourceRef            `json:"resource,omitempty"`
	OperationRevision   uint32                           `json:"operationRevision,omitempty"`
	ClientID            string                           `json:"clientId,omitempty"`
	OK                  bool                             `json:"ok,omitempty"`
	Error               string                           `json:"error,omitempty"`
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
		{Name: "focus", Description: "Focus a registered visible enabled UI control using its normal native focus handler, including lazy option loading.", Input: reflect.TypeOf(&ClickInput{}), Output: reflect.TypeOf(&CommandOutput{})},
		{Name: "get", Description: "Read one live mounted control value, validation constraints, flags, supported actions and operation revision without fetching datasets or executing handlers.", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&ReadOutput{})},
		{Name: "list", Description: "Read a bounded page of current live controls and operation availability without fetching datasets or executing handlers.", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ReadOutput{})},
		{Name: "click", Description: "Invoke a registered visible enabled UI action with normal validation and confirmation; use expectedRevision from current UI Read.", Input: reflect.TypeOf(&ClickInput{}), Output: reflect.TypeOf(&CommandOutput{})},
		{Name: "setValue", Description: "Change a registered visible enabled UI control through its normal handler; use expectedRevision from current UI Read.", Input: reflect.TypeOf(&SetValueInput{}), Output: reflect.TypeOf(&CommandOutput{})},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "focus":
		return s.focus, nil
	case "get":
		return s.getControl, nil
	case "list":
		return s.listControls, nil
	case "click":
		return s.click, nil
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
	return s.operate(ctx, input, output, "ui.control.setValue")
}
func (s *Service) click(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ClickInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*CommandOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	return s.operate(ctx, &SetValueInput{Target: input.Target, Resource: input.Resource, ExpectedRevision: input.ExpectedRevision, ConversationID: input.ConversationID, ClientID: input.ClientID, WindowID: input.WindowID, WindowKey: input.WindowKey, ControlID: input.ControlID, DataSourceRef: input.DataSourceRef}, output, "ui.control.click")
}
func (s *Service) focus(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ClickInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*CommandOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	return s.operate(ctx, &SetValueInput{Target: input.Target, Resource: input.Resource, ExpectedRevision: input.ExpectedRevision, ConversationID: input.ConversationID, ClientID: input.ClientID, WindowID: input.WindowID, WindowKey: input.WindowKey, ControlID: input.ControlID, DataSourceRef: input.DataSourceRef}, output, "ui.control.focus")
}
func (s *Service) operate(ctx context.Context, input *SetValueInput, output *CommandOutput, method string) error {
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
	if conversationID == "" {
		return fmt.Errorf("current conversation is required")
	}
	if input.ConversationID != "" && strings.TrimSpace(input.ConversationID) != conversationID {
		return fmt.Errorf("foreign conversation is not an operation target")
	}
	preferredClientID := normalizeOptionalClientID(input.ClientID)
	if preferredClientID == "" {
		preferredClientID = normalizeOptionalClientID(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	}
	clientID, namespace, _, win, err := s.reg.FindWindow(ctx, conversationID, preferredClientID, windowID, windowKey)
	if err != nil {
		return err
	}
	if input.Resource != nil && (win.Resource == nil || input.Resource.URI != win.Resource.URI || input.Resource.Revision != "" && input.Resource.Revision != win.Resource.Selector()) {
		return identity.ErrResourceDenied
	}
	ctx, err = s.reg.WindowRequestContext(ctx, namespace, clientID, win, nil, input.Target.WindowTarget())
	if err != nil {
		return err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != win.OperationRevision {
		return fmt.Errorf("stale UI operation revision; read current UI state before retrying")
	}
	targetWindowID := strings.TrimSpace(win.WindowID)
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  clientID,
		Namespace: namespace,
		Method:    method,
		Params: map[string]interface{}{
			"windowId":         targetWindowID,
			"conversationId":   conversationID,
			"resourceTarget":   win.ResourceTarget,
			"expectedRevision": input.ExpectedRevision,
			"controlId":        controlID,
			"dataSourceRef":    strings.TrimSpace(input.DataSourceRef),
			"scope":            strings.TrimSpace(input.Scope),
			"value":            input.Value,
			"bindingPath":      strings.TrimSpace(input.BindingPath),
			"dataField":        strings.TrimSpace(input.DataField),
		},
	})
	if err != nil {
		return err
	}
	output.ClientID = clientID
	if win.Resource != nil {
		output.Resource = &identity.ResourceRef{URI: win.Resource.URI, Revision: win.Resource.Selector()}
	}
	var liveResult struct {
		PendingInteraction  *uicontext.ControlOperationState `json:"pendingInteraction"`
		PendingConfirmation bool                             `json:"pendingConfirmation"`
		Status              string                           `json:"status"`
		Accepted            *bool                            `json:"accepted"`
		OperationRevision   uint32                           `json:"operationRevision"`
	}
	if len(resp.Result) > 0 {
		_ = json.Unmarshal(resp.Result, &liveResult)
	}
	output.OperationRevision = liveResult.OperationRevision
	output.Status = liveResult.Status
	output.PendingInteraction = liveResult.PendingInteraction
	output.PendingConfirmation = liveResult.PendingConfirmation
	output.Accepted = liveResult.Accepted
	output.OK = resp.OK
	output.Error = resp.Error
	if !resp.OK {
		return fmt.Errorf("UI control change rejected: %s", resp.Error)
	}
	s.reg.RecordEvent(namespace, clientID, uireg.UIEvent{
		ConversationID: strings.TrimSpace(win.ConversationID),
		ClientID:       clientID,
		WindowID:       targetWindowID,
		WindowKey:      strings.TrimSpace(win.WindowKey),
		Kind:           method,
		Actor:          "agent",
		Detail: map[string]interface{}{
			"controlId": controlID,
			"scope":     strings.TrimSpace(input.Scope),
			"value":     input.Value,
		},
	})
	return nil
}

func normalizeOptionalClientID(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "default") {
		return ""
	}
	return value
}
