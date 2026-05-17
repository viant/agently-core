package window

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

const Name = "ui/window"

type ListInput struct {
	ClientID string `json:"clientId,omitempty"`
}

type WindowItem struct {
	ClientID       string                 `json:"clientId,omitempty"`
	WindowID       string                 `json:"windowId,omitempty"`
	WindowKey      string                 `json:"windowKey,omitempty"`
	WindowTitle    string                 `json:"windowTitle,omitempty"`
	ConversationID string                 `json:"conversationId,omitempty"`
	Presentation   string                 `json:"presentation,omitempty"`
	Region         string                 `json:"region,omitempty"`
	ParentKey      string                 `json:"parentKey,omitempty"`
	Parameters     map[string]interface{} `json:"parameters,omitempty"`
	InTab          bool                   `json:"inTab,omitempty"`
	IsModal        bool                   `json:"isModal,omitempty"`
	IsMinimized    bool                   `json:"isMinimized,omitempty"`
	DataSourceRefs []string               `json:"dataSourceRefs,omitempty"`
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
	items, err := s.reg.ListByConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	preferred := normalizeOptionalClientID(input.ClientID)
	if preferred == "" {
		preferred = normalizeOptionalClientID(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	}
	if preferred != "" && len(items) == 0 {
		var fallback *uireg.ClientSnapshot
		if snap, findErr := s.reg.FindClient(ctx, preferred); findErr == nil && snap != nil {
			fallback = snap
		}
		items = resolveListSnapshots(items, preferred, fallback)
	}
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
					ConversationID: win.ConversationID,
					Presentation:   win.Presentation,
					Region:         win.Region,
					ParentKey:      win.ParentKey,
					Parameters:     compactWindowParameters(win.Parameters),
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

func resolveListSnapshots(items []uireg.ClientSnapshot, preferred string, fallback *uireg.ClientSnapshot) []uireg.ClientSnapshot {
	if preferred != "" && len(items) == 0 && fallback != nil && strings.TrimSpace(fallback.ClientID) == strings.TrimSpace(preferred) {
		return []uireg.ClientSnapshot{*fallback}
	}
	return items
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
	clientID, _, snap, win, err := s.reg.FindWindow(ctx, conversationID, preferredClientID, input.WindowID, input.WindowKey)
	if err != nil {
		return err
	}
	output.ClientID = clientID
	output.Window = compactWindowSnapshot(win)
	output.DataSourceRefs = listDataSourceRefs(win)
	output.Surface = buildWindowSurface(win)
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
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	preferredClientID := normalizeOptionalClientID(input.ClientID)
	if preferredClientID == "" {
		preferredClientID = normalizeOptionalClientID(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	}
	clientID, namespace, snap, win, err := s.reg.FindWindow(ctx, conversationID, preferredClientID, input.WindowID, "")
	if err != nil {
		return err
	}
	if windowAlreadyFocused(snap, win) {
		output.ClientID = clientID
		output.OK = true
		return nil
	}
	if s.bridge == nil {
		return fmt.Errorf("ui bridge not configured")
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  clientID,
		Namespace: namespace,
		Method:    "ui.window.activate",
		Params:    map[string]interface{}{"windowId": strings.TrimSpace(input.WindowID)},
	})
	if err != nil {
		return err
	}
	output.ClientID = clientID
	output.OK = resp.OK
	output.Error = resp.Error
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
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	preferredClientID := normalizeOptionalClientID(input.ClientID)
	if preferredClientID == "" {
		preferredClientID = normalizeOptionalClientID(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	}
	clientID, namespace, _, win, err := s.reg.FindWindow(ctx, conversationID, preferredClientID, input.WindowID, input.WindowKey)
	if err != nil {
		return err
	}
	targetWindowID := strings.TrimSpace(win.WindowID)
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  clientID,
		Namespace: namespace,
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
	output.ClientID = clientID
	output.OK = resp.OK
	output.Error = resp.Error
	return nil
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
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	preferredClientID := normalizeOptionalClientID(input.ClientID)
	if preferredClientID == "" {
		preferredClientID = normalizeOptionalClientID(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	}
	clientID, namespace, _, _, err := s.reg.FindWindow(ctx, conversationID, preferredClientID, input.WindowID, "")
	if err != nil {
		return err
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  clientID,
		Namespace: namespace,
		Method:    "ui.window.close",
		Params:    map[string]interface{}{"windowId": strings.TrimSpace(input.WindowID)},
	})
	if err != nil {
		return err
	}
	output.ClientID = clientID
	output.OK = resp.OK
	output.Error = resp.Error
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
	preferredClientID := normalizeOptionalClientID(input.ClientID)
	if preferredClientID == "" {
		preferredClientID = normalizeOptionalClientID(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	}
	clientID, namespace, _, _, err := s.reg.FindWindow(ctx, conversationID, preferredClientID, windowID, "")
	if err != nil {
		return err
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  clientID,
		Namespace: namespace,
		Method:    "ui.window.selectTab",
		Params: map[string]interface{}{
			"windowId": windowID,
			"tabId":    tabID,
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

func listDataSourceRefs(win *uireg.WindowSnapshot) []string {
	if win == nil || len(win.DataSources) == 0 {
		return nil
	}
	refs := make([]string, 0, len(win.DataSources))
	for ref := range win.DataSources {
		refs = append(refs, ref)
	}
	return refs
}

func buildWindowSurface(win *uireg.WindowSnapshot) *WindowSurface {
	if win == nil {
		return nil
	}
	viewMeta, _ := mapValue(win.Metadata, "view")
	tabsRaw, _ := sliceValue(viewMeta, "tabs")
	controlsRaw, _ := sliceValue(viewMeta, "controls")
	viewStateTabs := viewStateTabs(win.ViewState)

	surface := &WindowSurface{}
	for _, raw := range tabsRaw {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		containerID := strings.TrimSpace(stringValue(entry["containerId"]))
		tabID := strings.TrimSpace(stringValue(entry["tabId"]))
		if tabID == "" {
			continue
		}
		surface.Tabs = append(surface.Tabs, WindowTabHint{
			ContainerID: containerID,
			TabID:       tabID,
			Title:       strings.TrimSpace(stringValue(entry["title"])),
			Selected:    containerID != "" && viewStateTabs[containerID] == tabID,
		})
	}
	sort.SliceStable(surface.Tabs, func(i, j int) bool {
		if surface.Tabs[i].ContainerID != surface.Tabs[j].ContainerID {
			return surface.Tabs[i].ContainerID < surface.Tabs[j].ContainerID
		}
		return surface.Tabs[i].TabID < surface.Tabs[j].TabID
	})

	for _, raw := range controlsRaw {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		id := strings.TrimSpace(stringValue(entry["id"]))
		if id == "" {
			continue
		}
		scope := strings.TrimSpace(stringValue(entry["scope"]))
		bindingPath := strings.TrimSpace(stringValue(entry["bindingPath"]))
		dataField := strings.TrimSpace(stringValue(entry["dataField"]))
		value := controlValue(win.WindowForm, scope, bindingPath, dataField, id)
		options := controlOptions(entry["options"])
		surface.Controls = append(surface.Controls, WindowControlHint{
			ID:          id,
			Label:       strings.TrimSpace(stringValue(entry["label"])),
			Type:        strings.TrimSpace(stringValue(entry["type"])),
			Scope:       scope,
			BindingPath: bindingPath,
			DataField:   dataField,
			Value:       value,
			Options:     options,
		})
	}
	sort.SliceStable(surface.Controls, func(i, j int) bool {
		return surface.Controls[i].ID < surface.Controls[j].ID
	})
	if len(surface.Tabs) == 0 && len(surface.Controls) == 0 {
		return nil
	}
	return surface
}

func mapValue(source map[string]interface{}, key string) (map[string]interface{}, bool) {
	if len(source) == 0 {
		return nil, false
	}
	raw, ok := source[key]
	if !ok {
		return nil, false
	}
	value, ok := raw.(map[string]interface{})
	return value, ok
}

func sliceValue(source map[string]interface{}, key string) ([]interface{}, bool) {
	if len(source) == 0 {
		return nil, false
	}
	raw, ok := source[key]
	if !ok {
		return nil, false
	}
	value, ok := raw.([]interface{})
	return value, ok
}

func viewStateTabs(viewState map[string]interface{}) map[string]string {
	result := map[string]string{}
	raw, ok := viewState["tabs"]
	if !ok {
		return result
	}
	tabs, ok := raw.(map[string]interface{})
	if !ok {
		return result
	}
	for key, value := range tabs {
		id := strings.TrimSpace(stringValue(value))
		if key == "" || id == "" {
			continue
		}
		result[key] = id
	}
	return result
}

func controlValue(windowForm map[string]interface{}, scope, bindingPath, dataField, id string) interface{} {
	if !strings.EqualFold(strings.TrimSpace(scope), "windowForm") {
		return nil
	}
	switch {
	case bindingPath != "":
		return resolvePath(windowForm, bindingPath)
	case dataField != "":
		return resolvePath(windowForm, dataField)
	case id != "":
		return resolvePath(windowForm, id)
	default:
		return nil
	}
}

func controlOptions(raw interface{}) []WindowControlOption {
	list, ok := raw.([]interface{})
	if !ok || len(list) == 0 {
		return nil
	}
	result := make([]WindowControlOption, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		result = append(result, WindowControlOption{
			Value: entry["value"],
			Label: strings.TrimSpace(stringValue(entry["label"])),
		})
	}
	if len(result) == 0 {
		return nil
	}
	return result
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

func resolvePath(source map[string]interface{}, path string) interface{} {
	current := interface{}(source)
	for _, part := range strings.Split(strings.TrimSpace(path), ".") {
		if part == "" {
			continue
		}
		asMap, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		next, exists := asMap[part]
		if !exists {
			return nil
		}
		current = next
	}
	return current
}
