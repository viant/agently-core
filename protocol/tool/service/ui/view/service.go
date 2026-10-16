package view

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	svc "github.com/viant/agently-core/protocol/tool/service"
	viewproto "github.com/viant/agently-core/protocol/ui/view"
	workspaceproto "github.com/viant/agently-core/protocol/ui/workspace"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	repo "github.com/viant/agently-core/workspace/repository/forgewindow"
	forgetypes "github.com/viant/forge/backend/types"
)

const (
	Name                              = "ui/view"
	uiWindowOpenRejectionFallbackText = "UI client rejected the window without providing an error message"
)

type ListInput struct{}

type ListItem struct {
	// Discovery provenance is metadata only. Get/Open must obtain a fresh pin.
	ResourceURI        string                     `json:"resourceUri,omitempty"`
	ProviderIdentity   string                     `json:"providerIdentity,omitempty"`
	Target             *forgetypes.WindowTarget   `json:"target,omitempty"`
	Resource           *identity.ResolvedResource `json:"resource,omitempty"`
	ID                 string                     `json:"id,omitempty"`
	Title              string                     `json:"title,omitempty"`
	Description        string                     `json:"description,omitempty"`
	WindowKey          string                     `json:"windowKey,omitempty"`
	Presentation       string                     `json:"presentation,omitempty"`
	Region             string                     `json:"region,omitempty"`
	OpenMode           string                     `json:"openMode,omitempty"`
	IdentityScope      string                     `json:"identityScope,omitempty"`
	QuickSearch        *viewproto.QuickSearch     `json:"quickSearch,omitempty"`
	IdentityParameters []string                   `json:"identityParameters,omitempty"`
	WorkspaceSharePct  int                        `json:"workspaceSharePct,omitempty"`
	WorkspaceMinHeight int                        `json:"workspaceMinHeight,omitempty"`
	RefreshOnOpen      *bool                      `json:"refreshOnOpen,omitempty"`
	ReportBuilderRef   string                     `json:"reportBuilderRef,omitempty"`
	Parameters         []viewproto.Parameter      `json:"parameters,omitempty"`
	ReportPresets      []viewproto.ReportPreset   `json:"reportPresets,omitempty"`
	Capabilities       viewproto.Capabilities     `json:"capabilities,omitempty"`
	Navigation         *viewproto.Navigation      `json:"navigation,omitempty"`
}

type ListOutput struct {
	Items []ListItem `json:"items,omitempty"`
}

type GetInput struct {
	Target   *forgetypes.WindowTarget `json:"target,omitempty"`
	ID       string                   `json:"id,omitempty"`
	Resource *identity.ResourceRef    `json:"resource,omitempty"`
}

type GetOutput struct {
	Item *ListItem `json:"item,omitempty"`
}

type OpenInput struct {
	Target     *forgetypes.WindowTarget `json:"target,omitempty"`
	Resource   *identity.ResourceRef    `json:"resource,omitempty"`
	ID         string                   `json:"id,omitempty"`
	Parameters map[string]interface{}   `json:"parameters,omitempty"`
	SearchText string                   `json:"searchText,omitempty"`
	OpenMode   string                   `json:"openMode,omitempty"`
	Items      []OpenItem               `json:"items,omitempty"`
	ClientID   string                   `json:"clientId,omitempty"`
	TimeoutMs  int                      `json:"timeoutMs,omitempty"`
}

type OpenItem struct {
	Target     *forgetypes.WindowTarget `json:"target,omitempty"`
	ID         string                   `json:"id,omitempty"`
	Resource   *identity.ResourceRef    `json:"resource,omitempty"`
	Parameters map[string]interface{}   `json:"parameters"`
	SearchText string                   `json:"searchText,omitempty"`
	OpenMode   string                   `json:"openMode,omitempty"`
}

type OpenOutput struct {
	Resource               *identity.ResolvedResource        `json:"resource,omitempty"`
	WorkspaceObject        *workspaceproto.Object            `json:"workspaceObject,omitempty"`
	ClientID               string                            `json:"clientId,omitempty"`
	WindowID               string                            `json:"windowId,omitempty"`
	SelectedWindowID       string                            `json:"selectedWindowId,omitempty"`
	WindowKey              string                            `json:"windowKey,omitempty"`
	WindowTitle            string                            `json:"windowTitle,omitempty"`
	ConversationID         string                            `json:"conversationId,omitempty"`
	Presentation           string                            `json:"presentation,omitempty"`
	Region                 string                            `json:"region,omitempty"`
	ParentKey              string                            `json:"parentKey,omitempty"`
	WorkspaceSharePct      int                               `json:"workspaceSharePct,omitempty"`
	WorkspaceMinHeight     int                               `json:"workspaceMinHeight,omitempty"`
	Navigation             *viewproto.Navigation             `json:"navigation,omitempty"`
	Parameters             map[string]interface{}            `json:"parameters,omitempty"`
	ReportPresetResolution *viewproto.ReportPresetResolution `json:"reportPresetResolution,omitempty"`
	Items                  []OpenResultItem                  `json:"items,omitempty"`
	OK                     bool                              `json:"ok,omitempty"`
	Error                  string                            `json:"error,omitempty"`
}

type OpenResultItem struct {
	Resource               *identity.ResolvedResource        `json:"resource,omitempty"`
	WorkspaceObject        *workspaceproto.Object            `json:"workspaceObject,omitempty"`
	WindowID               string                            `json:"windowId,omitempty"`
	WindowKey              string                            `json:"windowKey,omitempty"`
	WindowTitle            string                            `json:"windowTitle,omitempty"`
	ConversationID         string                            `json:"conversationId,omitempty"`
	Presentation           string                            `json:"presentation,omitempty"`
	Region                 string                            `json:"region,omitempty"`
	ParentKey              string                            `json:"parentKey,omitempty"`
	WorkspaceSharePct      int                               `json:"workspaceSharePct,omitempty"`
	WorkspaceMinHeight     int                               `json:"workspaceMinHeight,omitempty"`
	Navigation             *viewproto.Navigation             `json:"navigation,omitempty"`
	Parameters             map[string]interface{}            `json:"parameters,omitempty"`
	ReportPresetResolution *viewproto.ReportPresetResolution `json:"reportPresetResolution,omitempty"`
}

type preparedOpenItem struct {
	item                   *ListItem
	openMode               string
	searchText             string
	windowParameters       map[string]interface{}
	reportPresetResolution *viewproto.ReportPresetResolution
}

type Service struct {
	repo             *repo.Repository
	bridge           *forgeuisvc.Service
	reg              *uireg.Registry
	itemEnricher     ListItemEnricher
	viewAuthorizer   func(context.Context, string) (bool, error)
	windowAuthorizer func(context.Context, string) (bool, error)
	metadataScope    forgeuisvc.MetadataReadScope
}

type ListItemEnricher func(context.Context, *ListItem) error

type Option func(*Service)

func WithListItemEnricher(enricher ListItemEnricher) Option {
	return func(service *Service) {
		service.itemEnricher = enricher
	}
}

// WithWindowAuthorizer applies legacy whole-window admission. Canonical
// catalogs use their shared resource resolver for discovery and explicit reads.
func WithWindowAuthorizer(authorize func(context.Context, string) (bool, error)) Option {
	return func(service *Service) { service.windowAuthorizer = authorize }
}

// WithMetadataScope requires verified metadata scope finalization for view
// list/get. It does not affect open or runtime datasource work.
func WithMetadataScope(scope forgeuisvc.MetadataReadScope) Option {
	return func(service *Service) { service.metadataScope = scope }
}

// WithViewAuthorizer checks legacy workspace IDs. Canonical view visibility is
// authorized by the resource's namespace/name and requested candidate.
func WithViewAuthorizer(authorize func(context.Context, string) (bool, error)) Option {
	return func(service *Service) { service.viewAuthorizer = authorize }
}

type viewNotFoundError struct {
	id        string
	available []string
}

func (e *viewNotFoundError) Error() string {
	if len(e.available) == 0 {
		return fmt.Sprintf("ui view %q not found; no workspace Forge windows are loaded", e.id)
	}
	return fmt.Sprintf("ui view %q not found; available views: %s", e.id, strings.Join(e.available, ", "))
}

func New(repository *repo.Repository, bridge *forgeuisvc.Service, options ...Option) *Service {
	result := &Service{
		repo:   repository,
		bridge: bridge,
		reg:    uireg.New(bridge),
	}
	for _, option := range options {
		if option != nil {
			option(result)
		}
	}
	if result.metadataScope == nil && bridge != nil {
		result.metadataScope = bridge.MetadataScope()
	}
	if result.windowAuthorizer == nil && bridge != nil {
		if ready, ok := any(bridge).(interface{ AuthzReady() bool }); ok && ready.AuthzReady() {
			// The protected Forge catalog is the authority for this host. A
			// workspace view is discoverable only if its referenced window
			// passes the same catalog admission used for direct opens.
			if admission, ok := any(bridge).(interface {
				WindowAuthorize(context.Context, string) (bool, error)
			}); ok {
				result.windowAuthorizer = admission.WindowAuthorize
			} else {
				result.windowAuthorizer = func(ctx context.Context, key string) (bool, error) {
					if key == "" {
						return false, nil
					}
					definition, err := bridge.WindowDefinitionGet(ctx, &forgeuisvc.WindowDefinitionGetInput{WindowID: key})
					return err == nil && definition != nil && definition.Definition != nil, nil
				}
			}
		}
	}
	return result
}

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "list", Description: "List workspace-defined dynamic UI views that can be opened for the user.", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "get", Description: "Get a workspace-defined dynamic UI view by id.", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&GetOutput{})},
		{Name: "open", Description: "Open one or more workspace-defined dynamic UI views for the active conversation and wait for the UI to acknowledge the request. For a single open, provide id plus parameters. A list view with declared quickSearch also accepts searchText to seed its name filter before loading. For ordered multi-open, provide items[] where each item includes id, parameters, and optional openMode. parameters.reportStarterId may be a canonical ReportPresets id, a unique human label, or the reserved __blank__ value.", Input: reflect.TypeOf(&OpenInput{}), Output: reflect.TypeOf(&OpenOutput{})},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "list":
		return s.list, nil
	case "get":
		return s.get, nil
	case "open":
		return s.open, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

func (s *Service) list(ctx context.Context, in, out interface{}) error {
	_, ok := in.(*ListInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ListOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	output.Items = nil
	readCtx, finish, err := forgeuisvc.BeginMetadataReadScope(ctx, s.metadataScope)
	if err != nil {
		return err
	}
	items, readErr := s.loadAll(readCtx)
	if err := forgeuisvc.FinishMetadataReadScope(finish, readErr); err != nil {
		return err
	}
	output.Items = items
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
	output.Item = nil
	readCtx, finish, err := forgeuisvc.BeginMetadataReadScope(ctx, s.metadataScope)
	if err != nil {
		return err
	}
	item, readErr := s.loadRequested(readCtx, strings.TrimSpace(input.ID), input.Resource, input.Target)
	if err := forgeuisvc.FinishMetadataReadScope(finish, readErr); err != nil {
		return err
	}
	output.Item = item
	return nil
}

func (s *Service) open(ctx context.Context, in, out interface{}) (resultErr error) {
	input, ok := in.(*OpenInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*OpenOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	ctx = forgeuisvc.WithoutMetadataReadScope(ctx, s.metadataScope)
	baseCtx := ctx
	readCtx, finish, err := s.bridge.BeginWindowReadDecision(ctx)
	if err != nil {
		return err
	}
	ctx = readCtx
	defer func() {
		if finish != nil {
			if err := finish(); err != nil {
				*output = OpenOutput{}
				resultErr = err
			}
		}
	}()
	clientID, namespace, conversationID, err := s.resolveOpenClient(ctx, input.ClientID)
	if err != nil {
		return err
	}
	items := make([]OpenItem, 0, max(1, len(input.Items)))
	if len(input.Items) > 0 {
		items = append(items, input.Items...)
	} else {
		items = append(items, OpenItem{
			Target:     input.Target,
			Resource:   input.Resource,
			ID:         input.ID,
			Parameters: input.Parameters,
			SearchText: input.SearchText,
			OpenMode:   input.OpenMode,
		})
	}
	if len(items) == 0 {
		return fmt.Errorf("id or items are required")
	}
	timeout := effectiveOpenTimeout(input.TimeoutMs)
	output.ClientID = clientID
	output.OK = true
	output.Items = make([]OpenResultItem, 0, len(items))
	preparedItems := make([]*preparedOpenItem, 0, len(items))
	for _, item := range items {
		prepared, prepareErr := s.prepareOpenItem(ctx, item)
		if prepareErr != nil {
			var notFound *viewNotFoundError
			if errors.As(prepareErr, &notFound) {
				s.recordInvalidWorkspaceIDEvent(namespace, clientID, conversationID, notFound)
			}
			output.OK = false
			output.Error = prepareErr.Error()
			return prepareErr
		}
		preparedItems = append(preparedItems, prepared)
	}
	if finish != nil {
		checkpoint := finish
		finish = nil
		if err := checkpoint(); err != nil {
			*output = OpenOutput{}
			return err
		}
		ctx = baseCtx
	}
	for _, prepared := range preparedItems {
		resolved, openErr := s.openPreparedItem(ctx, clientID, namespace, conversationID, prepared, timeout)
		if openErr != nil {
			output.OK = false
			output.Error = openErr.Error()
			return openErr
		}
		output.Items = append(output.Items, OpenResultItem{
			Resource:               resolved.Resource,
			WorkspaceObject:        resolved.WorkspaceObject,
			WindowID:               resolved.WindowID,
			WindowKey:              resolved.WindowKey,
			WindowTitle:            resolved.WindowTitle,
			ConversationID:         resolved.ConversationID,
			Presentation:           resolved.Presentation,
			Region:                 resolved.Region,
			ParentKey:              resolved.ParentKey,
			WorkspaceSharePct:      resolved.WorkspaceSharePct,
			WorkspaceMinHeight:     resolved.WorkspaceMinHeight,
			Navigation:             resolved.Navigation,
			Parameters:             resolved.Parameters,
			ReportPresetResolution: resolved.ReportPresetResolution,
		})
	}
	if len(output.Items) > 0 {
		selected := output.Items[len(output.Items)-1]
		output.WorkspaceObject = selected.WorkspaceObject
		output.Resource = selected.Resource
		output.WindowID = selected.WindowID
		output.SelectedWindowID = selected.WindowID
		output.WindowKey = selected.WindowKey
		output.WindowTitle = selected.WindowTitle
		output.ConversationID = selected.ConversationID
		output.Presentation = selected.Presentation
		output.Region = selected.Region
		output.ParentKey = selected.ParentKey
		output.WorkspaceSharePct = selected.WorkspaceSharePct
		output.WorkspaceMinHeight = selected.WorkspaceMinHeight
		output.Navigation = selected.Navigation
		output.Parameters = selected.Parameters
		output.ReportPresetResolution = selected.ReportPresetResolution
	}
	return nil
}

func (s *Service) resolveOpenClient(ctx context.Context, requestedClientID string) (string, string, string, error) {
	if s.bridge == nil {
		return "", "", "", fmt.Errorf("ui bridge not configured")
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	if conversationID == "" {
		return "", "", "", fmt.Errorf("conversation id is required")
	}
	clients, err := s.reg.ListAttachedByConversation(ctx, conversationID)
	if err != nil {
		return "", "", "", err
	}
	clientID := normalizeOptionalClientID(requestedClientID)
	preferredClientID := normalizeOptionalClientID(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	if clientID == "" {
		clientID = preferredClientID
	}
	namespace := ""
	if len(clients) == 0 {
		return "", "", "", fmt.Errorf("no active ui client attached to conversation %q", conversationID)
	}
	if clientID == "" {
		clientID = clients[0].ClientID
		namespace = clients[0].Namespace
	} else {
		namespace = clientNamespaceFromSnapshots(clients, clientID)
		if namespace == "" {
			return "", "", "", fmt.Errorf("ui client %q is not attached to conversation %q", clientID, conversationID)
		}
	}
	return clientID, namespace, conversationID, nil
}

func clientNamespaceFromSnapshots(clients []uireg.ClientSnapshot, clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ""
	}
	for _, item := range clients {
		if strings.TrimSpace(item.ClientID) == clientID {
			return strings.TrimSpace(item.Namespace)
		}
	}
	return ""
}

func (s *Service) openResolvedItem(ctx context.Context, clientID, namespace, conversationID string, input OpenItem, timeout int) (*OpenOutput, error) {
	prepared, err := s.prepareOpenItem(ctx, input)
	if err != nil {
		var notFound *viewNotFoundError
		if errors.As(err, &notFound) {
			s.recordInvalidWorkspaceIDEvent(namespace, clientID, conversationID, notFound)
		}
		return nil, err
	}
	return s.openPreparedItem(ctx, clientID, namespace, conversationID, prepared, timeout)
}

func (s *Service) prepareOpenItem(ctx context.Context, input OpenItem) (*preparedOpenItem, error) {
	item, err := s.loadRequested(ctx, strings.TrimSpace(input.ID), input.Resource, input.Target)
	if err != nil {
		return nil, err
	}
	rawParameters := cloneMap(input.Parameters)
	reportPresetResolution, err := resolveReportStarterID(rawParameters, item.ReportPresets)
	if err != nil {
		return nil, fmt.Errorf("invalid parameters for view %q: %w", item.ID, err)
	}
	if missing := missingRequiredParameters(item.Parameters, rawParameters); len(missing) > 0 {
		return nil, fmt.Errorf("missing required view parameter(s) for %q: %s; retry ui/view:open with a parameters object that includes those keys", item.ID, strings.Join(missing, ", "))
	}
	windowParameters := expandOpenParameters(item.Parameters, rawParameters)
	if input.SearchText != "" {
		if strings.TrimSpace(input.SearchText) == "" || item.QuickSearch == nil ||
			strings.TrimSpace(item.QuickSearch.DataSourceRef) == "" || strings.TrimSpace(item.QuickSearch.Field) == "" {
			return nil, fmt.Errorf("view %q does not support name quick search", item.ID)
		}
		ref := item.QuickSearch.DataSourceRef
		seed, _ := windowParameters[ref].(map[string]interface{})
		if seed == nil {
			seed = map[string]interface{}{}
		}
		filter, _ := seed["filter"].(map[string]interface{})
		if filter == nil {
			filter = map[string]interface{}{}
		}
		filter[item.QuickSearch.Field] = input.SearchText
		seed["filter"] = filter
		windowParameters[ref] = seed
	}
	if reportPresetResolution != nil {
		// Forge consumes reportStarterId as a top-level window parameter even
		// when a workspace parameter declaration also binds it elsewhere.
		windowParameters["reportStarterId"] = reportPresetResolution.ResolvedID
	}
	if builderRef := strings.TrimSpace(item.ReportBuilderRef); builderRef != "" {
		if _, ok := windowParameters["reportBuilderRef"]; !ok {
			windowParameters["reportBuilderRef"] = builderRef
		}
	}
	return &preparedOpenItem{
		item:                   item,
		openMode:               input.OpenMode,
		searchText:             input.SearchText,
		windowParameters:       windowParameters,
		reportPresetResolution: reportPresetResolution,
	}, nil
}

func (s *Service) workspaceDescriptor(ctx context.Context, windowID, conversationID string, item *ListItem, windowParameters map[string]interface{}) *workspaceproto.Object {
	descriptor := workspaceproto.New(ctx, windowID, conversationID)
	// Repeated opens retain the server-recorded original owner; activation is
	// associated with this request separately.
	events, _ := s.reg.ListConversationEventsContext(ctx, conversationID)
	for _, event := range events {
		if event.Kind != "view.open" || event.Actor != "agent" || event.WindowID != windowID {
			continue
		}
		raw, marshalErr := json.Marshal(event.Detail["workspaceObject"])
		var previous workspaceproto.Object
		if marshalErr == nil && json.Unmarshal(raw, &previous) == nil && previous.ObjectID == descriptor.ObjectID {
			descriptor.Origin = previous.Origin
			descriptor.Lifecycle.CreatedAt = previous.Lifecycle.CreatedAt
			descriptor.Revision = previous.Revision + 1
		}
	}
	if item.ReportBuilderRef != "" {
		descriptor.Kind = "report"
	}
	descriptor.Content.WindowID = windowID
	descriptor.Content.WindowKey = item.WindowKey
	descriptor.Content.Parameters = windowParameters
	descriptor.Capabilities["refresh"] = item.Capabilities.Datasource
	if item.Navigation != nil {
		descriptor.Navigation = map[string]string{"label": item.Navigation.Label, "icon": item.Navigation.Icon}
	}
	return descriptor
}

func (s *Service) openPreparedItem(ctx context.Context, clientID, namespace, conversationID string, prepared *preparedOpenItem, timeout int) (*OpenOutput, error) {
	if prepared == nil || prepared.item == nil {
		return nil, fmt.Errorf("prepared view item is required")
	}
	item := prepared.item
	allowed, err := s.authorizeView(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, &viewNotFoundError{id: item.ID}
	}
	windowParameters := prepared.windowParameters
	windowID := computeWindowID(item.WindowKey, windowParameters, conversationID, item)
	descriptor := s.workspaceDescriptor(ctx, windowID, conversationID, item, windowParameters)
	options := buildOpenWindowOptions(item, conversationID, prepared.openMode)
	options["workspaceObject"] = descriptor
	options["waitForReady"] = item.RefreshOnOpen != nil && *item.RefreshOnOpen
	params := map[string]interface{}{
		"windowId": windowID, "windowKey": item.WindowKey,
		"windowTitle": item.Title, "parameters": windowParameters,
		"options": options,
	}
	if item.Resource != nil {
		params["target"] = item.Target
		params["resource"] = &identity.ResourceRef{URI: item.Resource.URI, Revision: item.Resource.Selector()}
		params["resolvedResource"] = item.Resource
		ctx = runtimerequestctx.WithResolvedResource(ctx, *item.Resource)
	}
	if prepared.searchText != "" && item.QuickSearch != nil {
		params["initialFilters"] = map[string]interface{}{
			item.QuickSearch.DataSourceRef: map[string]interface{}{item.QuickSearch.Field: prepared.searchText},
		}
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  clientID,
		Namespace: namespace,
		Method:    "ui.window.open",
		Params:    params,
		TimeoutMs: timeout,
	})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		rejection := strings.TrimSpace(resp.Error)
		if rejection == "" {
			rejection = uiWindowOpenRejectionFallbackText
		}
		return nil, fmt.Errorf("ui.window.open rejected for view %q: %s", strings.TrimSpace(item.ID), rejection)
	}
	descriptor.Lifecycle.State = "ready"
	// A modern browser acknowledges navigation before protected metadata loads.
	// Keep the server-owned identity and origin; only adopt its lifecycle state.
	var acknowledgement struct {
		WorkspaceObject *workspaceproto.Object `json:"workspaceObject"`
	}
	if json.Unmarshal(resp.Result, &acknowledgement) == nil && acknowledgement.WorkspaceObject != nil {
		switch acknowledgement.WorkspaceObject.Lifecycle.State {
		case "opening", "ready", "failed":
			descriptor.Lifecycle.State = acknowledgement.WorkspaceObject.Lifecycle.State
		}
	}
	output := &OpenOutput{
		Resource:               item.Resource,
		WorkspaceObject:        descriptor,
		ClientID:               clientID,
		WindowKey:              item.WindowKey,
		WindowTitle:            item.Title,
		ConversationID:         conversationID,
		Presentation:           strings.TrimSpace(item.Presentation),
		Region:                 strings.TrimSpace(item.Region),
		ParentKey:              parentKeyForPresentation(item),
		WorkspaceSharePct:      item.WorkspaceSharePct,
		WorkspaceMinHeight:     item.WorkspaceMinHeight,
		Navigation:             item.Navigation,
		Parameters:             windowParameters,
		ReportPresetResolution: prepared.reportPresetResolution,
		OK:                     resp.OK,
		Error:                  resp.Error,
	}
	if len(resp.Result) > 0 {
		var payload map[string]interface{}
		if jsonErr := json.Unmarshal(resp.Result, &payload); jsonErr == nil {
			output.WindowID = strings.TrimSpace(stringValue(payload["windowId"]))
		}
	}
	if output.WindowID == "" {
		output.WindowID = windowID
	}
	if descriptor.Lifecycle.State == "ready" && shouldRefreshOpenedWindow(item, output.WindowID) {
		if _, refreshErr := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
			ClientID:  clientID,
			Namespace: namespace,
			Method:    "ui.data.fetch",
			Params: map[string]interface{}{
				"windowId": output.WindowID,
			},
			TimeoutMs: timeout,
		}); refreshErr != nil {
			return nil, refreshErr
		}
	}
	eventDetail := map[string]interface{}{
		"resource":        item.Resource,
		"workspaceObject": descriptor,
		"viewId":          strings.TrimSpace(item.ID),
		"parameters":      windowParameters,
	}
	if prepared.reportPresetResolution != nil {
		eventDetail["reportPresetResolution"] = prepared.reportPresetResolution
	}
	if _, err := s.reg.RecordConversationEventContext(ctx, conversationID, namespace, uireg.UIEvent{
		ConversationID: conversationID,
		ClientID:       clientID,
		WindowID:       strings.TrimSpace(output.WindowID),
		WindowKey:      strings.TrimSpace(item.WindowKey),
		Kind:           "view.open",
		Actor:          "agent",
		Detail:         eventDetail,
	}); err != nil {
		return nil, err
	}
	return output, nil
}

func (s *Service) authorizeView(ctx context.Context, id string) (bool, error) {
	if ctx == nil || ctx.Err() != nil {
		return false, fmt.Errorf("workspace view admission unavailable")
	}
	if s.bridge != nil && s.bridge.UsesWindowResourceResolution() {
		return true, nil
	}
	if s.viewAuthorizer == nil {
		return true, nil
	}
	allowed, err := s.viewAuthorizer(ctx, strings.TrimSpace(id))
	if err != nil || ctx.Err() != nil {
		return false, fmt.Errorf("workspace view admission unavailable")
	}
	return allowed, nil
}

func resolveReportStarterID(parameters map[string]interface{}, presets []viewproto.ReportPreset) (*viewproto.ReportPresetResolution, error) {
	raw, ok := parameters["reportStarterId"]
	if !ok {
		return nil, nil
	}
	value, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("reportStarterId must be a string; %s", reportPresetAvailability(presets))
	}
	requested := strings.TrimSpace(value)
	if requested == "" {
		delete(parameters, "reportStarterId")
		return nil, nil
	}
	if requested == "__blank__" {
		parameters["reportStarterId"] = requested
		return &viewproto.ReportPresetResolution{Requested: requested, ResolvedID: requested, MatchedBy: "reserved"}, nil
	}

	idMatches := matchingReportPresetsByID(requested, presets)
	if len(idMatches) == 1 {
		resolvedID := idMatches[0].ID
		parameters["reportStarterId"] = resolvedID
		return &viewproto.ReportPresetResolution{Requested: requested, ResolvedID: resolvedID, MatchedBy: "id"}, nil
	}
	if len(idMatches) > 1 {
		return nil, fmt.Errorf("report preset catalog is invalid/ambiguous: canonical IDs collide case-insensitively; inspect ui/view:get before retrying")
	}

	labelMatches := matchingReportPresetsByLabel(requested, presets)
	if len(labelMatches) == 1 {
		resolvedID := labelMatches[0].ID
		parameters["reportStarterId"] = resolvedID
		return &viewproto.ReportPresetResolution{Requested: requested, ResolvedID: resolvedID, MatchedBy: "label"}, nil
	}
	if len(labelMatches) > 1 {
		return nil, fmt.Errorf("reportStarterId label %q is ambiguous; inspect ui/view:get for the preset catalog and use an unambiguous canonical ID", requested)
	}
	return nil, fmt.Errorf("unknown reportStarterId %q; %s", requested, reportPresetAvailability(presets))
}

func matchingReportPresetsByID(requested string, presets []viewproto.ReportPreset) []viewproto.ReportPreset {
	result := make([]viewproto.ReportPreset, 0, 1)
	for _, preset := range presets {
		if strings.TrimSpace(preset.ID) == "" || !strings.EqualFold(requested, strings.TrimSpace(preset.ID)) {
			continue
		}
		result = append(result, preset)
	}
	return result
}

func matchingReportPresetsByLabel(requested string, presets []viewproto.ReportPreset) []viewproto.ReportPreset {
	normalizedRequested := normalizeReportPresetLabel(requested)
	result := make([]viewproto.ReportPreset, 0, 1)
	for _, preset := range presets {
		if strings.TrimSpace(preset.ID) == "" {
			continue
		}
		normalizedLabel := normalizeReportPresetLabel(preset.Label)
		if normalizedLabel != "" && strings.EqualFold(normalizedRequested, normalizedLabel) {
			result = append(result, preset)
		}
	}
	return result
}

func normalizeReportPresetLabel(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func reportPresetAvailability(presets []viewproto.ReportPreset) string {
	labels := reportPresetLabels(presets)
	if len(labels) == 0 {
		return "no usable report preset labels are available; inspect ui/view:get before retrying"
	}
	quoted := make([]string, 0, len(labels))
	for _, label := range labels {
		quoted = append(quoted, fmt.Sprintf("%q", label))
	}
	return fmt.Sprintf("available preset labels: %s; retry with a listed label or inspect ui/view:get", strings.Join(quoted, ", "))
}

func reportPresetLabels(presets []viewproto.ReportPreset) []string {
	result := make([]string, 0, len(presets))
	for _, preset := range presets {
		if strings.TrimSpace(preset.ID) == "" {
			continue
		}
		if label := normalizeReportPresetLabel(preset.Label); label != "" {
			if len(matchingReportPresetsByLabel(label, presets)) == 1 {
				result = append(result, label)
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		left := strings.ToLower(result[i])
		right := strings.ToLower(result[j])
		if left == right {
			return result[i] < result[j]
		}
		return left < right
	})
	return result
}

func (s *Service) recordInvalidWorkspaceIDEvent(namespace, clientID, conversationID string, notFound *viewNotFoundError) {
	if s == nil || s.reg == nil || notFound == nil {
		return
	}
	invalidID := strings.TrimSpace(notFound.id)
	if invalidID == "" {
		return
	}
	payload := map[string]interface{}{
		"invalidWorkspaceId": invalidID,
	}
	if len(notFound.available) > 0 {
		payload["availableWorkspaceIds"] = append([]string(nil), notFound.available...)
	}
	s.reg.RecordEvent(namespace, clientID, uireg.UIEvent{
		ConversationID: strings.TrimSpace(conversationID),
		ClientID:       strings.TrimSpace(clientID),
		Kind:           "error",
		Actor:          "agent",
		Detail: map[string]interface{}{
			"message": notFound.Error(),
			"payload": payload,
		},
	})
}

func shouldRefreshOpenedWindow(item *ListItem, windowID string) bool {
	if item == nil {
		return false
	}
	if strings.TrimSpace(windowID) == "" {
		return false
	}
	if !item.Capabilities.Datasource {
		return false
	}
	if item.RefreshOnOpen != nil {
		return *item.RefreshOnOpen
	}
	return strings.EqualFold(strings.TrimSpace(item.Presentation), "hosted")
}

func buildOpenWindowOptions(item *ListItem, conversationID string, openModeOverride string) map[string]interface{} {
	openMode := strings.ToLower(strings.TrimSpace(firstNonEmpty(openModeOverride, item.OpenMode)))
	if openMode == "" && strings.EqualFold(item.Presentation, "hosted") {
		openMode = "append"
	}
	options := map[string]interface{}{
		"conversationId": strings.TrimSpace(conversationID),
		"presentation":   strings.TrimSpace(item.Presentation),
		"region":         strings.TrimSpace(item.Region),
	}
	if item.WorkspaceSharePct > 0 {
		options["workspaceSharePct"] = item.WorkspaceSharePct
	}
	if item.WorkspaceMinHeight > 0 {
		options["workspaceMinHeight"] = item.WorkspaceMinHeight
	}
	if item.Navigation != nil {
		options["navigation"] = map[string]interface{}{
			"label": strings.TrimSpace(item.Navigation.Label),
			"icon":  strings.TrimSpace(item.Navigation.Icon),
		}
	}
	if len(item.IdentityParameters) > 0 {
		options["identityParameters"] = append([]string(nil), item.IdentityParameters...)
	}
	if strings.EqualFold(strings.TrimSpace(item.Presentation), "hosted") {
		// Hosted workspace windows are explicit subwindows of the main chat root.
		options["parentKey"] = "chat/new"
	}
	switch openMode {
	case "replace":
		options["replaceHostedRegion"] = true
	case "append":
		options["replaceHostedRegion"] = false
	}
	return options
}

func parentKeyForPresentation(item *ListItem) string {
	if strings.EqualFold(strings.TrimSpace(item.Presentation), "hosted") {
		return "chat/new"
	}
	return ""
}

func computeWindowID(windowKey string, parameters map[string]interface{}, conversationID string, item *ListItem) string {
	base := strings.TrimSpace(windowKey)
	if item != nil {
		if id := strings.TrimSpace(item.ID); id != "" {
			base = id
		}
	}
	if base == "" {
		return ""
	}
	if len(parameters) > 0 && !strings.EqualFold(strings.TrimSpace(item.IdentityScope), "conversation") {
		identity := parameters
		if len(item.IdentityParameters) > 0 {
			identity = make(map[string]interface{}, len(item.IdentityParameters))
			for _, name := range item.IdentityParameters {
				if value, ok := parameters[strings.TrimSpace(name)]; ok {
					identity[strings.TrimSpace(name)] = value
				}
			}
		}
		if len(identity) > 0 {
			base = fmt.Sprintf("%s_%d", base, generateIntHash(identity))
		}
	}
	if item != nil && item.Resource != nil {
		var target *forgetypes.WindowTarget
		if item.Target != nil {
			normalized, err := item.Target.Normalize()
			if err == nil {
				normalized.SelectionToken = ""
				target = &normalized
			}
		}
		identityBytes, _ := json.Marshal([]any{item.Resource.URI, item.Resource.ResourceCandidate, item.Resource.AuthorityBinding, target})
		base += "_r" + identity.ContentFingerprint(identityBytes)[:12]
	}
	if strings.EqualFold(strings.TrimSpace(item.Presentation), "hosted") {
		convID := strings.TrimSpace(conversationID)
		if convID != "" {
			return base + "__" + convID
		}
	}
	return base
}

func generateIntHash(input map[string]interface{}) uint32 {
	var serialize func(value interface{}) string
	serialize = func(value interface{}) string {
		switch actual := value.(type) {
		case nil:
			return "<nil>"
		case map[string]interface{}:
			keys := make([]string, 0, len(actual))
			for key := range actual {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, key := range keys {
				parts = append(parts, key+":"+serialize(actual[key]))
			}
			return strings.Join(parts, "|")
		case []interface{}:
			parts := make([]string, 0, len(actual))
			for _, item := range actual {
				parts = append(parts, serialize(item))
			}
			return strings.Join(parts, "|")
		default:
			return fmt.Sprint(actual)
		}
	}
	serialized := serialize(input)
	var hash int32
	for _, char := range serialized {
		hash = hash*31 + int32(char)
	}
	return uint32(hash)
}

func (s *Service) loadAll(ctx context.Context) ([]ListItem, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ui view repository not configured")
	}
	all, err := s.repo.LoadAll(ctx)
	if err != nil {
		return nil, err
	}
	if ready, ok := any(s.bridge).(interface{ AuthzReady() bool }); ok && ready.AuthzReady() && s.windowAuthorizer == nil {
		if !s.bridge.UsesWindowResourceResolution() {
			return nil, fmt.Errorf("protected window admission is not configured for workspace views")
		}
	}
	var admitted map[string]forgeuisvc.WindowDefinitionSummary
	metadataOnly := s.bridge != nil && s.bridge.WindowListMetadataOnly()
	if s.bridge != nil && s.bridge.UsesWindowResourceResolution() {
		admitted = map[string]forgeuisvc.WindowDefinitionSummary{}
		for offset := 0; ; offset += 100 {
			page, err := s.bridge.WindowDefinitionsList(ctx, &forgeuisvc.WindowDefinitionListInput{Limit: 100, Offset: offset})
			if err != nil {
				return nil, err
			}
			if page == nil {
				return nil, fmt.Errorf("window resource catalog unavailable")
			}
			for _, summary := range page.Windows {
				admitted[summary.WindowID] = summary
				if summary.ResourceURI != "" {
					admitted[summary.ResourceURI] = summary
				}
			}
			if !page.HasMore {
				break
			}
			if len(page.Windows) == 0 {
				return nil, fmt.Errorf("window resource catalog did not advance")
			}
		}
	}
	items := make([]ListItem, 0, len(all))
	for _, spec := range all {
		if spec == nil {
			continue
		}
		catalogKey := strings.TrimSpace(spec.WindowKey)
		if admitted != nil {
			var err error
			var ref identity.ResourceRef
			catalogKey, ref, err = s.canonicalCatalogReference(ctx, spec)
			if err != nil {
				continue
			}
			if admitted[catalogKey].WindowID == "" && admitted[ref.URI].WindowID != "" {
				admitted[catalogKey] = admitted[ref.URI]
			}
			if admitted[catalogKey].WindowID == "" {
				continue
			}
		}
		allowed, err := s.authorizeView(ctx, spec.ID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		if s.windowAuthorizer != nil && admitted == nil {
			allowed, err := s.windowAuthorizer(ctx, strings.TrimSpace(spec.WindowKey))
			if err != nil || ctx.Err() != nil {
				return nil, fmt.Errorf("workspace view admission unavailable")
			}
			if !allowed {
				continue
			}
		}
		item := ListItem{
			ID:                 strings.TrimSpace(spec.ID),
			Title:              strings.TrimSpace(spec.Title),
			Description:        strings.TrimSpace(spec.Description),
			WindowKey:          strings.TrimSpace(spec.WindowKey),
			Presentation:       strings.TrimSpace(spec.Presentation),
			Region:             strings.TrimSpace(spec.Region),
			OpenMode:           strings.TrimSpace(spec.OpenMode),
			IdentityScope:      strings.TrimSpace(spec.IdentityScope),
			QuickSearch:        spec.QuickSearch,
			IdentityParameters: append([]string(nil), spec.IdentityParameters...),
			WorkspaceSharePct:  spec.WorkspaceSharePct,
			WorkspaceMinHeight: spec.WorkspaceMinHeight,
			RefreshOnOpen:      spec.RefreshOnOpen,
			ReportBuilderRef:   strings.TrimSpace(spec.ReportBuilderRef),
			Parameters:         append([]viewproto.Parameter(nil), spec.Parameters...),
			ReportPresets:      append([]viewproto.ReportPreset(nil), spec.ReportPresets...),
			Capabilities:       spec.Capabilities,
			Navigation:         spec.Navigation,
		}
		if s.bridge != nil && s.bridge.UsesWindowResourceResolution() {
			item.WindowKey = catalogKey
			if metadataOnly {
				// The host's declared visibility list is sufficient for discovery;
				// it must not perform entity/bootstrap checks to mint unused pins.
				item.ResourceURI = admitted[catalogKey].ResourceURI
				item.ProviderIdentity = admitted[catalogKey].ProviderIdentity
			} else {
				definition, err := s.bridge.WindowDefinitionGet(ctx, &forgeuisvc.WindowDefinitionGetInput{WindowID: catalogKey})
				if err != nil {
					return nil, err
				}
				if definition == nil || definition.Definition == nil || definition.Definition.Resource == nil {
					return nil, fmt.Errorf("window resource resolution unavailable")
				}
				pin := *definition.Definition.Resource
				item.Resource = &pin
				item.Target = definition.Definition.ResourceTarget
			}
		}
		// Conversation-owned reports/resources use the UI workspace unless the
		// definition explicitly opts into another presentation.
		if item.Presentation == "" {
			item.Presentation = "hosted"
		}
		if strings.EqualFold(item.Presentation, "hosted") && item.Region == "" {
			item.Region = "chat.top"
		}
		if item.OpenMode == "" && strings.EqualFold(item.Presentation, "hosted") {
			item.OpenMode = "append"
		}
		if s.itemEnricher != nil {
			if err := s.itemEnricher(ctx, &item); err != nil {
				return nil, err
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Title != items[j].Title {
			return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
		}
		return strings.ToLower(items[i].ID) < strings.ToLower(items[j].ID)
	})
	return items, nil
}

func (s *Service) loadOne(ctx context.Context, id string) (*ListItem, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	items, err := s.loadAll(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if strings.EqualFold(items[i].ID, id) {
			item := items[i]
			return &item, nil
		}
	}
	available := availableViewIDs(items)
	return nil, &viewNotFoundError{id: id, available: available}
}

func availableViewIDs(items []ListItem) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func stringValue(v interface{}) string {
	switch actual := v.(type) {
	case string:
		return actual
	default:
		return fmt.Sprintf("%v", v)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeOptionalClientID(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "default") {
		return ""
	}
	return value
}

func effectiveOpenTimeout(timeoutMs int) int {
	if timeoutMs > 0 {
		if timeoutMs > 30_000 {
			return 30_000
		}
		return timeoutMs
	}
	return 15_000
}

func expandOpenParameters(specParams []viewproto.Parameter, provided map[string]interface{}) map[string]interface{} {
	if len(provided) == 0 {
		return map[string]interface{}{}
	}
	if len(specParams) == 0 {
		return cloneMap(provided)
	}

	result := map[string]interface{}{}
	for key, value := range provided {
		// Preserve the declared resource parameter at the top level for generic
		// authorization and window identity. BindTo entries fan the same value
		// out to datasource-specific parameter paths; they do not replace it.
		result[key] = value
		matches := matchingViewParameters(specParams, key)
		if len(matches) == 0 {
			continue
		}
		for _, specParam := range matches {
			bindTo := strings.TrimSpace(specParam.BindTo)
			if bindTo == "" {
				continue
			}
			setNestedValue(result, bindTo, value)
		}
	}
	return result
}

func missingRequiredParameters(specParams []viewproto.Parameter, provided map[string]interface{}) []string {
	if len(specParams) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	missing := make([]string, 0)
	for _, specParam := range specParams {
		if !specParam.Required {
			continue
		}
		name := strings.TrimSpace(specParam.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(name)]; ok {
			continue
		}
		seen[strings.ToLower(name)] = struct{}{}
		if hasRequiredParameterValue(provided, name) {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)
	return missing
}

func hasRequiredParameterValue(provided map[string]interface{}, name string) bool {
	if len(provided) == 0 {
		return false
	}
	for key, value := range provided {
		if !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		switch actual := value.(type) {
		case nil:
			return false
		case string:
			return strings.TrimSpace(actual) != ""
		case []interface{}:
			return len(actual) > 0
		case []string:
			return len(actual) > 0
		default:
			return true
		}
	}
	return false
}

func matchingViewParameters(specParams []viewproto.Parameter, key string) []viewproto.Parameter {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	result := make([]viewproto.Parameter, 0, len(specParams))
	for _, specParam := range specParams {
		if strings.EqualFold(strings.TrimSpace(specParam.Name), key) {
			result = append(result, specParam)
		}
	}
	return result
}

func setNestedValue(target map[string]interface{}, path string, value interface{}) {
	parts := compactPathParts(path)
	if len(parts) == 0 {
		return
	}
	current := target
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		next, ok := current[part]
		if !ok {
			child := map[string]interface{}{}
			current[part] = child
			current = child
			continue
		}
		existing, ok := next.(map[string]interface{})
		if !ok {
			child := map[string]interface{}{}
			current[part] = child
			current = child
			continue
		}
		current = existing
	}
	current[parts[len(parts)-1]] = value
}

func compactPathParts(path string) []string {
	raw := strings.Split(path, ".")
	result := make([]string, 0, len(raw))
	for _, entry := range raw {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	if len(input) == 0 {
		return map[string]interface{}{}
	}
	result := make(map[string]interface{}, len(input))
	for key, value := range input {
		if child, ok := value.(map[string]interface{}); ok {
			result[key] = cloneMap(child)
			continue
		}
		result[key] = value
	}
	return result
}
