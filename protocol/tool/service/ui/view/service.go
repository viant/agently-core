package view

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	svc "github.com/viant/agently-core/protocol/tool/service"
	viewproto "github.com/viant/agently-core/protocol/ui/view"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	repo "github.com/viant/agently-core/workspace/repository/forgewindow"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

const (
	Name                              = "ui/view"
	uiWindowOpenRejectionFallbackText = "UI client rejected the window without providing an error message"
)

type ListInput struct{}

type ListItem struct {
	ID                 string                   `json:"id,omitempty"`
	Title              string                   `json:"title,omitempty"`
	Description        string                   `json:"description,omitempty"`
	WindowKey          string                   `json:"windowKey,omitempty"`
	Presentation       string                   `json:"presentation,omitempty"`
	Region             string                   `json:"region,omitempty"`
	OpenMode           string                   `json:"openMode,omitempty"`
	IdentityScope      string                   `json:"identityScope,omitempty"`
	WorkspaceSharePct  int                      `json:"workspaceSharePct,omitempty"`
	WorkspaceMinHeight int                      `json:"workspaceMinHeight,omitempty"`
	ReportBuilderRef   string                   `json:"reportBuilderRef,omitempty"`
	Parameters         []viewproto.Parameter    `json:"parameters,omitempty"`
	ReportPresets      []viewproto.ReportPreset `json:"reportPresets,omitempty"`
	Capabilities       viewproto.Capabilities   `json:"capabilities,omitempty"`
}

type ListOutput struct {
	Items []ListItem `json:"items,omitempty"`
}

type GetInput struct {
	ID string `json:"id"`
}

type GetOutput struct {
	Item *ListItem `json:"item,omitempty"`
}

type OpenInput struct {
	ID         string                 `json:"id,omitempty"`
	Parameters map[string]interface{} `json:"parameters,omitempty"`
	OpenMode   string                 `json:"openMode,omitempty"`
	Items      []OpenItem             `json:"items,omitempty"`
	ClientID   string                 `json:"clientId,omitempty"`
	TimeoutMs  int                    `json:"timeoutMs,omitempty"`
}

type OpenItem struct {
	ID         string                 `json:"id"`
	Parameters map[string]interface{} `json:"parameters"`
	OpenMode   string                 `json:"openMode,omitempty"`
}

type OpenOutput struct {
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
	Parameters             map[string]interface{}            `json:"parameters,omitempty"`
	ReportPresetResolution *viewproto.ReportPresetResolution `json:"reportPresetResolution,omitempty"`
	Items                  []OpenResultItem                  `json:"items,omitempty"`
	OK                     bool                              `json:"ok,omitempty"`
	Error                  string                            `json:"error,omitempty"`
}

type OpenResultItem struct {
	WindowID               string                            `json:"windowId,omitempty"`
	WindowKey              string                            `json:"windowKey,omitempty"`
	WindowTitle            string                            `json:"windowTitle,omitempty"`
	ConversationID         string                            `json:"conversationId,omitempty"`
	Presentation           string                            `json:"presentation,omitempty"`
	Region                 string                            `json:"region,omitempty"`
	ParentKey              string                            `json:"parentKey,omitempty"`
	WorkspaceSharePct      int                               `json:"workspaceSharePct,omitempty"`
	WorkspaceMinHeight     int                               `json:"workspaceMinHeight,omitempty"`
	Parameters             map[string]interface{}            `json:"parameters,omitempty"`
	ReportPresetResolution *viewproto.ReportPresetResolution `json:"reportPresetResolution,omitempty"`
}

type preparedOpenItem struct {
	item                   *ListItem
	openMode               string
	windowParameters       map[string]interface{}
	reportPresetResolution *viewproto.ReportPresetResolution
}

type Service struct {
	repo         *repo.Repository
	bridge       *forgeuisvc.Service
	reg          *uireg.Registry
	itemEnricher ListItemEnricher
}

type ListItemEnricher func(context.Context, *ListItem) error

type Option func(*Service)

func WithListItemEnricher(enricher ListItemEnricher) Option {
	return func(service *Service) {
		service.itemEnricher = enricher
	}
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
	return result
}

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "list", Description: "List workspace-defined dynamic UI views that can be opened for the user.", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "get", Description: "Get a workspace-defined dynamic UI view by id.", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&GetOutput{})},
		{Name: "open", Description: "Open one or more workspace-defined dynamic UI views for the active conversation and wait for the UI to acknowledge the request. For a single open, provide id plus parameters. For ordered multi-open, provide items[] where each item includes id, parameters, and optional openMode. parameters.reportStarterId may be a canonical ReportPresets id, a unique human label, or the reserved __blank__ value.", Input: reflect.TypeOf(&OpenInput{}), Output: reflect.TypeOf(&OpenOutput{})},
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
	items, err := s.loadAll(ctx)
	if err != nil {
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
	item, err := s.loadOne(ctx, strings.TrimSpace(input.ID))
	if err != nil {
		return err
	}
	output.Item = item
	return nil
}

func (s *Service) open(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*OpenInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*OpenOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	clientID, namespace, conversationID, err := s.resolveOpenClient(ctx, input.ClientID)
	if err != nil {
		return err
	}
	items := make([]OpenItem, 0, max(1, len(input.Items)))
	if len(input.Items) > 0 {
		items = append(items, input.Items...)
	} else {
		items = append(items, OpenItem{
			ID:         input.ID,
			Parameters: input.Parameters,
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
	for _, prepared := range preparedItems {
		resolved, openErr := s.openPreparedItem(ctx, clientID, namespace, conversationID, prepared, timeout)
		if openErr != nil {
			output.OK = false
			output.Error = openErr.Error()
			return openErr
		}
		output.Items = append(output.Items, OpenResultItem{
			WindowID:               resolved.WindowID,
			WindowKey:              resolved.WindowKey,
			WindowTitle:            resolved.WindowTitle,
			ConversationID:         resolved.ConversationID,
			Presentation:           resolved.Presentation,
			Region:                 resolved.Region,
			ParentKey:              resolved.ParentKey,
			WorkspaceSharePct:      resolved.WorkspaceSharePct,
			WorkspaceMinHeight:     resolved.WorkspaceMinHeight,
			Parameters:             resolved.Parameters,
			ReportPresetResolution: resolved.ReportPresetResolution,
		})
	}
	if len(output.Items) > 0 {
		selected := output.Items[len(output.Items)-1]
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
	if len(clients) == 0 && clientID != "" {
		if clientSnap, findErr := s.reg.FindClient(ctx, clientID); findErr == nil && clientSnap != nil {
			clients = append(clients, *clientSnap)
		} else if preferredClientID != "" && preferredClientID != clientID {
			if preferredSnap, preferredErr := s.reg.FindClient(ctx, preferredClientID); preferredErr == nil && preferredSnap != nil {
				clientID = preferredClientID
				clients = append(clients, *preferredSnap)
			}
		}
	}
	if clientID == "" {
		if len(clients) == 0 {
			return "", "", "", fmt.Errorf("no active ui client attached to conversation %q", conversationID)
		}
		clientID = clients[0].ClientID
		namespace = clients[0].Namespace
	} else {
		namespace = clientNamespaceFromSnapshots(clients, clientID)
		if namespace == "" {
			if clientSnap, findErr := s.reg.FindClient(ctx, clientID); findErr == nil && clientSnap != nil {
				namespace = strings.TrimSpace(clientSnap.Namespace)
			}
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
	item, err := s.loadOne(ctx, strings.TrimSpace(input.ID))
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
		windowParameters:       windowParameters,
		reportPresetResolution: reportPresetResolution,
	}, nil
}

func (s *Service) openPreparedItem(ctx context.Context, clientID, namespace, conversationID string, prepared *preparedOpenItem, timeout int) (*OpenOutput, error) {
	if prepared == nil || prepared.item == nil {
		return nil, fmt.Errorf("prepared view item is required")
	}
	item := prepared.item
	windowParameters := prepared.windowParameters
	windowID := computeWindowID(item.WindowKey, windowParameters, conversationID, item)
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  clientID,
		Namespace: namespace,
		Method:    "ui.window.open",
		Params: map[string]interface{}{
			"windowId":    windowID,
			"windowKey":   item.WindowKey,
			"windowTitle": item.Title,
			"parameters":  windowParameters,
			"options":     buildOpenWindowOptions(item, conversationID, prepared.openMode),
		},
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
	output := &OpenOutput{
		ClientID:               clientID,
		WindowKey:              item.WindowKey,
		WindowTitle:            item.Title,
		ConversationID:         conversationID,
		Presentation:           strings.TrimSpace(item.Presentation),
		Region:                 strings.TrimSpace(item.Region),
		ParentKey:              parentKeyForPresentation(item),
		WorkspaceSharePct:      item.WorkspaceSharePct,
		WorkspaceMinHeight:     item.WorkspaceMinHeight,
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
	if shouldRefreshOpenedWindow(item, output.WindowID) {
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
		"viewId":     strings.TrimSpace(item.ID),
		"parameters": windowParameters,
	}
	if prepared.reportPresetResolution != nil {
		eventDetail["reportPresetResolution"] = prepared.reportPresetResolution
	}
	s.reg.RecordEvent(namespace, clientID, uireg.UIEvent{
		ConversationID: conversationID,
		ClientID:       clientID,
		WindowID:       strings.TrimSpace(output.WindowID),
		WindowKey:      strings.TrimSpace(item.WindowKey),
		Kind:           "view.open",
		Actor:          "agent",
		Detail:         eventDetail,
	})
	return output, nil
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
	return strings.EqualFold(strings.TrimSpace(item.Presentation), "hosted")
}

func buildOpenWindowOptions(item *ListItem, conversationID string, openModeOverride string) map[string]interface{} {
	openMode := strings.ToLower(strings.TrimSpace(firstNonEmpty(openModeOverride, item.OpenMode)))
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
	if base == "" {
		return ""
	}
	if len(parameters) > 0 && !strings.EqualFold(strings.TrimSpace(item.IdentityScope), "conversation") {
		base = fmt.Sprintf("%s_%d", base, generateIntHash(parameters))
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
	items := make([]ListItem, 0, len(all))
	for _, spec := range all {
		if spec == nil {
			continue
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
			WorkspaceSharePct:  spec.WorkspaceSharePct,
			WorkspaceMinHeight: spec.WorkspaceMinHeight,
			ReportBuilderRef:   strings.TrimSpace(spec.ReportBuilderRef),
			Parameters:         append([]viewproto.Parameter(nil), spec.Parameters...),
			ReportPresets:      append([]viewproto.ReportPreset(nil), spec.ReportPresets...),
			Capabilities:       spec.Capabilities,
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
		matches := matchingViewParameters(specParams, key)
		if len(matches) == 0 {
			result[key] = value
			continue
		}
		appliedBinding := false
		for _, specParam := range matches {
			bindTo := strings.TrimSpace(specParam.BindTo)
			if bindTo == "" {
				result[key] = value
				continue
			}
			setNestedValue(result, bindTo, value)
			appliedBinding = true
		}
		if !appliedBinding && result[key] == nil {
			result[key] = value
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
