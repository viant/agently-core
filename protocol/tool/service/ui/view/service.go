package view

import (
	"context"
	"encoding/json"
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

const Name = "ui/view"

type ListInput struct{}

type ListItem struct {
	ID           string                 `json:"id,omitempty"`
	Title        string                 `json:"title,omitempty"`
	Description  string                 `json:"description,omitempty"`
	WindowKey    string                 `json:"windowKey,omitempty"`
	Presentation string                 `json:"presentation,omitempty"`
	Region       string                 `json:"region,omitempty"`
	Parameters   []viewproto.Parameter  `json:"parameters,omitempty"`
	Capabilities viewproto.Capabilities `json:"capabilities,omitempty"`
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
	ID         string                 `json:"id"`
	Parameters map[string]interface{} `json:"parameters"`
	ClientID   string                 `json:"clientId,omitempty"`
	TimeoutMs  int                    `json:"timeoutMs,omitempty"`
}

type OpenOutput struct {
	ClientID  string `json:"clientId,omitempty"`
	WindowID  string `json:"windowId,omitempty"`
	WindowKey string `json:"windowKey,omitempty"`
	OK        bool   `json:"ok,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Service struct {
	repo   *repo.Repository
	bridge *forgeuisvc.Service
	reg    *uireg.Registry
}

func New(repository *repo.Repository, bridge *forgeuisvc.Service) *Service {
	return &Service{
		repo:   repository,
		bridge: bridge,
		reg:    uireg.New(bridge),
	}
}

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "list", Description: "List workspace-defined dynamic UI views that can be opened for the user.", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "get", Description: "Get a workspace-defined dynamic UI view by id.", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&GetOutput{})},
		{Name: "open", Description: "Open a workspace-defined dynamic UI view for the active conversation and wait for the UI to acknowledge the request. Always provide the view id and a parameters object. If the target view declares required parameters, include every required key in that parameters object.", Input: reflect.TypeOf(&OpenInput{}), Output: reflect.TypeOf(&OpenOutput{})},
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
	if s.bridge == nil {
		return fmt.Errorf("ui bridge not configured")
	}
	item, err := s.loadOne(ctx, strings.TrimSpace(input.ID))
	if err != nil {
		return err
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	if conversationID == "" {
		return fmt.Errorf("conversation id is required")
	}
	clients, err := s.reg.ListByConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	clientID := normalizeOptionalClientID(input.ClientID)
	preferredClientID := normalizeOptionalClientID(runtimerequestctx.PreferredUIClientIDFromContext(ctx))
	if clientID == "" {
		clientID = preferredClientID
	}
	namespace := ""
	if len(clients) == 0 && clientID != "" {
		if clientSnap, err := s.reg.FindClient(ctx, clientID); err == nil && clientSnap != nil {
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
			return fmt.Errorf("no active ui client attached to conversation %q", conversationID)
		}
		clientID = clients[0].ClientID
		namespace = clients[0].Namespace
	} else {
		for _, item := range clients {
			if item.ClientID == clientID {
				namespace = item.Namespace
				break
			}
		}
	}
	timeout := 15_000
	if input.TimeoutMs > 0 {
		timeout = input.TimeoutMs
	}
	windowParameters := expandOpenParameters(item.Parameters, input.Parameters)
	if missing := missingRequiredParameters(item.Parameters, input.Parameters); len(missing) > 0 {
		return fmt.Errorf("missing required view parameter(s) for %q: %s; retry ui/view:open with a parameters object that includes those keys", item.ID, strings.Join(missing, ", "))
	}
	resp, err := s.bridge.UICommand(ctx, &forgeuisvc.UICommandInput{
		ClientID:  clientID,
		Namespace: namespace,
		Method:    "ui.window.open",
		Params: map[string]interface{}{
			"windowKey":   item.WindowKey,
			"windowTitle": item.Title,
			"parameters":  windowParameters,
			"options": map[string]interface{}{
				"conversationId": conversationID,
				"presentation":   strings.TrimSpace(item.Presentation),
				"region":         strings.TrimSpace(item.Region),
			},
		},
		TimeoutMs: timeout,
	})
	if err != nil {
		return err
	}
	output.ClientID = clientID
	output.WindowKey = item.WindowKey
	output.OK = resp.OK
	output.Error = resp.Error
	if len(resp.Result) > 0 {
		var payload map[string]interface{}
		if jsonErr := json.Unmarshal(resp.Result, &payload); jsonErr == nil {
			output.WindowID = strings.TrimSpace(stringValue(payload["windowId"]))
		}
	}
	return nil
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
		items = append(items, ListItem{
			ID:           strings.TrimSpace(spec.ID),
			Title:        strings.TrimSpace(spec.Title),
			Description:  strings.TrimSpace(spec.Description),
			WindowKey:    strings.TrimSpace(spec.WindowKey),
			Presentation: strings.TrimSpace(spec.Presentation),
			Region:       strings.TrimSpace(spec.Region),
			Parameters:   append([]viewproto.Parameter(nil), spec.Parameters...),
			Capabilities: spec.Capabilities,
		})
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
	if len(available) == 0 {
		return nil, fmt.Errorf("ui view %q not found; no workspace Forge windows are loaded", id)
	}
	return nil, fmt.Errorf("ui view %q not found; available views: %s", id, strings.Join(available, ", "))
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
