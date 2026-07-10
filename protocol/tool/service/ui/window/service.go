package window

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

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
	ClientID           string                 `json:"clientId,omitempty"`
	WindowID           string                 `json:"windowId,omitempty"`
	WindowKey          string                 `json:"windowKey,omitempty"`
	WindowTitle        string                 `json:"windowTitle,omitempty"`
	ConversationID     string                 `json:"conversationId,omitempty"`
	Presentation       string                 `json:"presentation,omitempty"`
	Region             string                 `json:"region,omitempty"`
	ParentKey          string                 `json:"parentKey,omitempty"`
	WorkspaceSharePct  int                    `json:"workspaceSharePct,omitempty"`
	WorkspaceMinHeight int                    `json:"workspaceMinHeight,omitempty"`
	Parameters         map[string]interface{} `json:"parameters,omitempty"`
	InTab              bool                   `json:"inTab,omitempty"`
	IsModal            bool                   `json:"isModal,omitempty"`
	IsMinimized        bool                   `json:"isMinimized,omitempty"`
	DataSourceRefs     []string               `json:"dataSourceRefs,omitempty"`
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
	ClientID       string                  `json:"clientId,omitempty"`
	Window         *uireg.WindowSnapshot   `json:"window,omitempty"`
	Selected       *uireg.SnapshotSelected `json:"selected,omitempty"`
	DataSourceRefs []string                `json:"dataSourceRefs,omitempty"`
	Surface        *WindowSurface          `json:"surface,omitempty"`
}

type WindowSurface struct {
	Tabs     []WindowTabHint     `json:"tabs,omitempty"`
	Controls []WindowControlHint `json:"controls,omitempty"`
}

type WindowTabHint struct {
	ContainerID string `json:"containerId,omitempty"`
	TabID       string `json:"tabId,omitempty"`
	Title       string `json:"title,omitempty"`
	Selected    bool   `json:"selected,omitempty"`
}

type WindowControlHint struct {
	ID          string                `json:"id,omitempty"`
	Label       string                `json:"label,omitempty"`
	Type        string                `json:"type,omitempty"`
	Scope       string                `json:"scope,omitempty"`
	BindingPath string                `json:"bindingPath,omitempty"`
	DataField   string                `json:"dataField,omitempty"`
	Value       interface{}           `json:"value,omitempty"`
	Options     []WindowControlOption `json:"options,omitempty"`
}

type WindowControlOption struct {
	Value interface{} `json:"value,omitempty"`
	Label string      `json:"label,omitempty"`
}

type ActivateInput struct {
	ClientID string `json:"clientId,omitempty"`
	WindowID string `json:"windowId,omitempty"`
}

type SelectTabInput struct {
	ClientID string `json:"clientId,omitempty"`
	WindowID string `json:"windowId,omitempty"`
	TabID    string `json:"tabId,omitempty"`
}

type HideInput struct {
	ClientID string `json:"clientId,omitempty"`
	WindowID string `json:"windowId,omitempty"`
}

type SetFormDataInput struct {
	ClientID  string                 `json:"clientId,omitempty"`
	WindowID  string                 `json:"windowId,omitempty"`
	WindowKey string                 `json:"windowKey,omitempty"`
	Values    map[string]interface{} `json:"values,omitempty"`
	Replace   bool                   `json:"replace,omitempty"`
}

type CommandOutput struct {
	ClientID string `json:"clientId,omitempty"`
	OK       bool   `json:"ok,omitempty"`
	Error    string `json:"error,omitempty"`
}

type resolvedWindowTarget struct {
	ClientID  string
	Namespace string
	Snapshot  *uireg.Snapshot
	Window    *uireg.WindowSnapshot
}

type Service struct {
	bridge *forgeuisvc.Service
	reg    *uireg.Registry
}

const setFormDataWindowResolveTimeout = 2 * time.Second
const setFormDataWindowResolvePollInterval = 50 * time.Millisecond

func New(bridge *forgeuisvc.Service) *Service {
	return &Service{bridge: bridge, reg: uireg.New(bridge)}
}

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "list", Description: "List live UI windows for the current conversation.", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "get", Description: "Get one live UI window by windowId or windowKey for the current conversation.", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&GetOutput{})},
		{Name: "show", Description: "Activate an existing live UI window by windowId.", Input: reflect.TypeOf(&ActivateInput{}), Output: reflect.TypeOf(&CommandOutput{})},
		{Name: "setFormData", Description: "Patch the windowForm state for an existing live UI window by windowId or windowKey.", Input: reflect.TypeOf(&SetFormDataInput{}), Output: reflect.TypeOf(&CommandOutput{})},
		{Name: "selectTab", Description: "Select a tab inside an existing live UI window by windowId and tabId.", Input: reflect.TypeOf(&SelectTabInput{}), Output: reflect.TypeOf(&CommandOutput{})},
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
	case "setformdata":
		return s.setFormData, nil
	case "selecttab":
		return s.selectTab, nil
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
	items, err := s.reg.ListReadableByConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	explicitClientID := normalizeOptionalClientID(input.ClientID)
	matchedClients := 0
	singleClientID := ""
	singleFocusedWindowID := ""
	for _, item := range items {
		if explicitClientID != "" && item.ClientID != explicitClientID {
			continue
		}
		if item.Snapshot != nil {
			matchedClients++
			if matchedClients == 1 {
				singleClientID = item.ClientID
				singleFocusedWindowID = strings.TrimSpace(item.Snapshot.Selected.WindowID)
			}
			for _, win := range item.Snapshot.Windows {
				refs := make([]string, 0, len(win.DataSources))
				for ref := range win.DataSources {
					refs = append(refs, ref)
				}
				output.Items = append(output.Items, WindowItem{
					ClientID:           item.ClientID,
					WindowID:           win.WindowID,
					WindowKey:          win.WindowKey,
					WindowTitle:        win.WindowTitle,
					ConversationID:     win.ConversationID,
					Presentation:       win.Presentation,
					Region:             win.Region,
					ParentKey:          win.ParentKey,
					WorkspaceSharePct:  win.WorkspaceSharePct,
					WorkspaceMinHeight: win.WorkspaceMinHeight,
					Parameters:         compactWindowParameters(win.Parameters),
					InTab:              win.InTab,
					IsModal:            win.IsModal,
					IsMinimized:        win.IsMinimized,
					DataSourceRefs:     refs,
				})
			}
		}
	}
	if matchedClients == 1 {
		output.ClientID = singleClientID
		output.FocusedWindowID = singleFocusedWindowID
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
	preferredClientID := normalizeOptionalClientID(input.ClientID)
	if preferredClientID == "" {
		preferredClientID = normalizeOptionalClientID(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	}
	clientID, _, snap, win, err := s.reg.FindReadableWindow(ctx, conversationID, preferredClientID, input.WindowID, input.WindowKey)
	if err != nil {
		return err
	}
	output.ClientID = clientID
	output.Window = compactWindowSnapshot(win)
	output.DataSourceRefs = uireg.ListDataSourceRefs(win)
	output.Surface = buildWindowSurface(win)
	if snap != nil {
		selected := snap.Selected
		output.Selected = &selected
	}
	return nil
}

func (s *Service) resolveWindowTarget(ctx context.Context, requestedClientID, windowID, windowKey string) (*resolvedWindowTarget, error) {
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	preferredClientID := normalizeOptionalClientID(requestedClientID)
	if preferredClientID == "" {
		preferredClientID = normalizeOptionalClientID(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	}
	clientID, namespace, snap, win, err := s.reg.FindWindow(ctx, conversationID, preferredClientID, windowID, windowKey)
	if err == nil {
		return &resolvedWindowTarget{
			ClientID:  clientID,
			Namespace: namespace,
			Snapshot:  snap,
			Window:    win,
		}, nil
	}
	if readableClientID, readableNamespace, readableSnap, readableWin, readableErr := s.reg.FindReadableWindow(ctx, conversationID, preferredClientID, windowID, windowKey); readableErr == nil {
		return &resolvedWindowTarget{
			ClientID:  readableClientID,
			Namespace: readableNamespace,
			Snapshot:  readableSnap,
			Window:    readableWin,
		}, nil
	}
	if strings.TrimSpace(windowID) == "" {
		return nil, err
	}
	clientTargetID := strings.TrimSpace(preferredClientID)
	namespace = ""
	if clientTargetID != "" {
		clientSnap, findErr := s.reg.FindClient(ctx, clientTargetID)
		if findErr != nil || clientSnap == nil {
			return nil, err
		}
		namespace = strings.TrimSpace(clientSnap.Namespace)
	} else {
		items, listErr := s.reg.ListByConversation(ctx, conversationID)
		if listErr != nil || len(items) != 1 {
			return nil, err
		}
		clientTargetID = strings.TrimSpace(items[0].ClientID)
		namespace = strings.TrimSpace(items[0].Namespace)
	}
	if clientTargetID == "" || namespace == "" {
		return nil, err
	}
	return &resolvedWindowTarget{
		ClientID:  clientTargetID,
		Namespace: namespace,
		Window: &uireg.WindowSnapshot{
			WindowID:       strings.TrimSpace(windowID),
			WindowKey:      strings.TrimSpace(windowKey),
			ConversationID: conversationID,
		},
	}, nil
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
	target, err := s.resolveWindowTarget(ctx, input.ClientID, input.WindowID, "")
	if err != nil {
		return err
	}
	if windowAlreadyFocused(target.Snapshot, target.Window) {
		output.ClientID = target.ClientID
		output.OK = true
		return nil
	}
	if s.bridge == nil {
		return fmt.Errorf("ui bridge not configured")
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  target.ClientID,
		Namespace: target.Namespace,
		Method:    "ui.window.activate",
		Params:    map[string]interface{}{"windowId": strings.TrimSpace(input.WindowID)},
	})
	if err != nil {
		return err
	}
	output.ClientID = target.ClientID
	output.OK = resp.OK
	output.Error = resp.Error
	s.reg.RecordEvent(target.Namespace, target.ClientID, uireg.UIEvent{
		ConversationID: strings.TrimSpace(target.Window.ConversationID),
		ClientID:       target.ClientID,
		WindowID:       strings.TrimSpace(target.Window.WindowID),
		WindowKey:      strings.TrimSpace(target.Window.WindowKey),
		Kind:           "window.show",
		Actor:          "agent",
	})
	return nil
}

func (s *Service) setFormData(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*SetFormDataInput)
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
	if len(input.Values) == 0 {
		return fmt.Errorf("values are required")
	}
	target, err := s.resolveWindowTargetWithRetry(
		ctx,
		input.ClientID,
		input.WindowID,
		input.WindowKey,
		setFormDataWindowResolveTimeout,
	)
	if err != nil {
		return err
	}
	targetWindowID := strings.TrimSpace(target.Window.WindowID)
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  target.ClientID,
		Namespace: target.Namespace,
		Method:    "ui.window.setFormData",
		Params: map[string]interface{}{
			"windowId": targetWindowID,
			"values":   input.Values,
			"replace":  input.Replace,
		},
	})
	if err != nil {
		return err
	}
	output.ClientID = target.ClientID
	output.OK = resp.OK
	output.Error = resp.Error
	s.reg.RecordEvent(target.Namespace, target.ClientID, uireg.UIEvent{
		ConversationID: strings.TrimSpace(target.Window.ConversationID),
		ClientID:       target.ClientID,
		WindowID:       strings.TrimSpace(target.Window.WindowID),
		WindowKey:      strings.TrimSpace(target.Window.WindowKey),
		Kind:           "window.set_form_data",
		Actor:          "agent",
		Detail: map[string]interface{}{
			"replace": input.Replace,
			"values":  input.Values,
		},
	})
	return nil
}

func (s *Service) resolveWindowTargetWithRetry(ctx context.Context, requestedClientID, windowID, windowKey string, timeout time.Duration) (*resolvedWindowTarget, error) {
	target, err := s.resolveWindowTarget(ctx, requestedClientID, windowID, windowKey)
	if err == nil || timeout <= 0 {
		return target, err
	}
	deadline := time.Now().Add(timeout)
	lastErr := err
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		time.Sleep(setFormDataWindowResolvePollInterval)
		target, err = s.resolveWindowTarget(ctx, requestedClientID, windowID, windowKey)
		if err == nil {
			return target, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func windowAlreadyFocused(snap *uireg.Snapshot, win *uireg.WindowSnapshot) bool {
	if snap == nil || win == nil {
		return false
	}
	return strings.TrimSpace(snap.Selected.WindowID) != "" && strings.TrimSpace(snap.Selected.WindowID) == strings.TrimSpace(win.WindowID)
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
	target, err := s.resolveWindowTarget(ctx, input.ClientID, input.WindowID, "")
	if err != nil {
		return err
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  target.ClientID,
		Namespace: target.Namespace,
		Method:    "ui.window.close",
		Params:    map[string]interface{}{"windowId": strings.TrimSpace(input.WindowID)},
	})
	if err != nil {
		return err
	}
	output.ClientID = target.ClientID
	output.OK = resp.OK
	output.Error = resp.Error
	s.reg.RecordEvent(target.Namespace, target.ClientID, uireg.UIEvent{
		ConversationID: strings.TrimSpace(target.Window.ConversationID),
		ClientID:       target.ClientID,
		WindowID:       strings.TrimSpace(target.Window.WindowID),
		WindowKey:      strings.TrimSpace(target.Window.WindowKey),
		Kind:           "window.hide",
		Actor:          "agent",
	})
	return nil
}

func (s *Service) selectTab(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*SelectTabInput)
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
	windowID := strings.TrimSpace(input.WindowID)
	tabID := strings.TrimSpace(input.TabID)
	if windowID == "" {
		return fmt.Errorf("windowId is required")
	}
	if tabID == "" {
		return fmt.Errorf("tabId is required")
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	target, err := s.resolveWindowTarget(ctx, input.ClientID, windowID, "")
	if err != nil {
		return err
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  target.ClientID,
		Namespace: target.Namespace,
		Method:    "ui.window.selectTab",
		Params: map[string]interface{}{
			"windowId": windowID,
			"tabId":    tabID,
		},
	})
	if err != nil {
		return err
	}
	output.ClientID = target.ClientID
	output.OK = resp.OK
	output.Error = resp.Error
	s.reg.RecordEvent(target.Namespace, target.ClientID, uireg.UIEvent{
		ConversationID: conversationID,
		ClientID:       target.ClientID,
		WindowID:       windowID,
		Kind:           "tab.selected",
		Actor:          "agent",
		Detail: map[string]interface{}{
			"tabId": tabID,
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

func compactWindowParameters(parameters map[string]interface{}) map[string]interface{} {
	if len(parameters) == 0 {
		return nil
	}
	result := make(map[string]interface{}, len(parameters))
	for key, value := range parameters {
		result[key] = value
	}
	return result
}

func compactWindowSnapshot(win *uireg.WindowSnapshot) *uireg.WindowSnapshot {
	if win == nil {
		return nil
	}
	copyWin := *win
	copyWin.DataSources = nil
	return &copyWin
}

func buildWindowSurface(win *uireg.WindowSnapshot) *WindowSurface {
	base := uireg.BuildWindowSurface(win)
	if base == nil {
		return nil
	}
	surface := &WindowSurface{}
	for _, tab := range base.Tabs {
		surface.Tabs = append(surface.Tabs, WindowTabHint{
			ContainerID: tab.ContainerID,
			TabID:       tab.TabID,
			Title:       tab.Title,
			Selected:    tab.Selected,
		})
	}
	for _, control := range base.Controls {
		options := make([]WindowControlOption, 0, len(control.Options))
		for _, option := range control.Options {
			options = append(options, WindowControlOption{Value: option.Value, Label: option.Label})
		}
		surface.Controls = append(surface.Controls, WindowControlHint{
			ID:          control.ID,
			Label:       control.Label,
			Type:        control.Type,
			Scope:       control.Scope,
			BindingPath: control.BindingPath,
			DataField:   control.DataField,
			Value:       control.Value,
			Options:     options,
		})
	}
	if len(surface.Tabs) == 0 && len(surface.Controls) == 0 {
		return nil
	}
	return surface
}

func stringValue(v interface{}) string {
	if v == nil {
		return ""
	}
	switch actual := v.(type) {
	case string:
		return actual
	default:
		return fmt.Sprintf("%v", v)
	}
}
