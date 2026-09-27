package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/ui/permittedview"
	windowloader "github.com/viant/agently-core/service/ui/window"
	ws "github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/config"
	forgetypes "github.com/viant/forge/backend/types"
	"gopkg.in/yaml.v3"
)

// Layout is the workspace-owned navigation shell. Window definitions remain
// owned by Forge's existing workspace/embedded window loaders.
type Layout struct {
	Version          int                           `json:"version" yaml:"version"`
	ID               string                        `json:"id" yaml:"id"`
	Topbar           *LayoutTopbar                 `json:"topbar,omitempty" yaml:"topbar,omitempty"`
	Left             *LayoutLeft                   `json:"left,omitempty" yaml:"left,omitempty"`
	Right            *LayoutRight                  `json:"right,omitempty" yaml:"right,omitempty"`
	WindowProviders  []windowloader.RemoteProvider `json:"windowProviders,omitempty" yaml:"windowProviders,omitempty"`
	CatalogRevisions map[string]string             `json:"-" yaml:"-"`
	Applications     []LayoutApplication           `json:"applications" yaml:"applications"`
}

type LayoutLeft struct {
	Width      *LayoutWidth      `json:"width,omitempty" yaml:"width,omitempty"`
	Split      *LayoutSplit      `json:"split,omitempty" yaml:"split,omitempty"`
	Navigation *LayoutNavigation `json:"navigation,omitempty" yaml:"navigation,omitempty"`
	History    *LayoutHistory    `json:"history,omitempty" yaml:"history,omitempty"`
}

type LayoutNavigation struct {
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}
type LayoutHistory struct {
	Enabled         *bool                    `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	NewConversation *bool                    `json:"newConversation,omitempty" yaml:"newConversation,omitempty"`
	Search          *LayoutHistorySearch     `json:"search,omitempty" yaml:"search,omitempty"`
	List            *LayoutHistoryList       `json:"list,omitempty" yaml:"list,omitempty"`
	Pagination      *LayoutHistoryPagination `json:"pagination,omitempty" yaml:"pagination,omitempty"`
}
type LayoutHistorySearch struct {
	Placeholder   string `json:"placeholder,omitempty" yaml:"placeholder,omitempty"`
	DebounceMs    int    `json:"debounceMs,omitempty" yaml:"debounceMs,omitempty"`
	ShowClear     *bool  `json:"showClear,omitempty" yaml:"showClear,omitempty"`
	EnterToSearch *bool  `json:"enterToSearch,omitempty" yaml:"enterToSearch,omitempty"`
}
type LayoutHistoryList struct {
	Virtualized   *bool  `json:"virtualized,omitempty" yaml:"virtualized,omitempty"`
	EmptyText     string `json:"emptyText,omitempty" yaml:"emptyText,omitempty"`
	NoMatchesText string `json:"noMatchesText,omitempty" yaml:"noMatchesText,omitempty"`
}
type LayoutHistoryPagination struct {
	Mode     string `json:"mode,omitempty" yaml:"mode,omitempty"`
	AutoLoad *bool  `json:"autoLoad,omitempty" yaml:"autoLoad,omitempty"`
}
type LayoutTopbar struct {
	Actions []LayoutMenu `json:"actions,omitempty" yaml:"actions,omitempty"`
}
type LayoutRight struct {
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

type LayoutWidth struct {
	Default         int    `json:"default" yaml:"default"`
	Min             int    `json:"min" yaml:"min"`
	Max             int    `json:"max" yaml:"max"`
	PreferenceScope string `json:"preferenceScope,omitempty" yaml:"preferenceScope,omitempty"`
	PreferenceKey   string `json:"preferenceKey,omitempty" yaml:"preferenceKey,omitempty"`
}

type LayoutSplit struct {
	Initial          float64 `json:"initial" yaml:"initial"`
	Resizable        bool    `json:"resizable" yaml:"resizable"`
	MinSectionHeight int     `json:"minSectionHeight" yaml:"minSectionHeight"`
}

type LayoutApplication struct {
	ID            string                        `json:"id" yaml:"id"`
	Title         string                        `json:"title" yaml:"title"`
	Icon          string                        `json:"icon,omitempty" yaml:"icon,omitempty"`
	ClassName     string                        `json:"className,omitempty" yaml:"className,omitempty"`
	Parameters    map[string]any                `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Authorization *forgetypes.AuthorizationSpec `json:"authorization,omitempty" yaml:"authorization,omitempty"`
	VisibleWhen   any                           `json:"visibleWhen,omitempty" yaml:"visibleWhen,omitempty"`
	HiddenWhen    any                           `json:"hiddenWhen,omitempty" yaml:"hiddenWhen,omitempty"`
	DisabledWhen  any                           `json:"disabledWhen,omitempty" yaml:"disabledWhen,omitempty"`
	ReadOnlyWhen  any                           `json:"readOnlyWhen,omitempty" yaml:"readOnlyWhen,omitempty"`
	Disabled      bool                          `json:"disabled,omitempty" yaml:"-"`
	ReadOnly      bool                          `json:"readOnly,omitempty" yaml:"-"`
	Menus         []LayoutMenu                  `json:"menus" yaml:"menus"`
	WindowCatalog *LayoutWindowCatalog          `json:"windowCatalog,omitempty" yaml:"windowCatalog,omitempty"`
}

type LayoutWindowCatalog struct {
	Provider string `json:"provider" yaml:"provider"`
	Group    string `json:"group,omitempty" yaml:"group,omitempty"`
}

type LayoutMenu struct {
	ID            string                        `json:"id" yaml:"id"`
	Title         string                        `json:"title" yaml:"title"`
	Icon          string                        `json:"icon,omitempty" yaml:"icon,omitempty"`
	ClassName     string                        `json:"className,omitempty" yaml:"className,omitempty"`
	Parameters    map[string]any                `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Authorization *forgetypes.AuthorizationSpec `json:"authorization,omitempty" yaml:"authorization,omitempty"`
	VisibleWhen   any                           `json:"visibleWhen,omitempty" yaml:"visibleWhen,omitempty"`
	HiddenWhen    any                           `json:"hiddenWhen,omitempty" yaml:"hiddenWhen,omitempty"`
	DisabledWhen  any                           `json:"disabledWhen,omitempty" yaml:"disabledWhen,omitempty"`
	ReadOnlyWhen  any                           `json:"readOnlyWhen,omitempty" yaml:"readOnlyWhen,omitempty"`
	Disabled      bool                          `json:"disabled,omitempty" yaml:"-"`
	ReadOnly      bool                          `json:"readOnly,omitempty" yaml:"-"`
	Action        *LayoutAction                 `json:"action,omitempty" yaml:"action,omitempty"`
	Children      []LayoutMenu                  `json:"children,omitempty" yaml:"children,omitempty"`
}

type LayoutAction struct {
	Type               string         `json:"type" yaml:"type"`
	Provider           string         `json:"provider,omitempty" yaml:"provider,omitempty"`
	WindowKey          string         `json:"windowKey,omitempty" yaml:"windowKey,omitempty"`
	Parameters         map[string]any `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	RefreshDataSources []string       `json:"refreshDataSources,omitempty" yaml:"refreshDataSources,omitempty"`
}

type layoutResponse struct {
	SchemaVersion    int               `json:"schemaVersion"`
	LayoutRevision   string            `json:"layoutRevision"`
	WorkspaceID      string            `json:"workspaceId"`
	CatalogRevisions map[string]string `json:"catalogRevisions,omitempty"`
	Layout           *Layout           `json:"layout"`
}

// SetLayoutDefault supplies the host application's embedded default.
func (h *MetadataHandler) SetLayoutDefault(data []byte) {
	h.layoutDefault = append([]byte(nil), data...)
}

func (h *MetadataHandler) handleLayout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		root := ws.Root()
		cfg, err := config.Load(root)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ref := "ui/layout.yaml"
		explicit := false
		if cfg != nil {
			if ui, ok := cfg.Raw["ui"].(map[string]any); ok {
				if layout, ok := ui["layout"].(map[string]any); ok {
					if value, ok := layout["ref"].(string); ok && strings.TrimSpace(value) != "" {
						ref, explicit = value, true
					}
				}
			}
		}
		layoutPath, pathErr := ws.ResolveChildPath(root, ref)
		if pathErr != nil {
			http.Error(w, pathErr.Error(), http.StatusBadRequest)
			return
		}
		data, err := os.ReadFile(layoutPath)
		source := ref
		if os.IsNotExist(err) && !explicit {
			data, err, source = h.layoutDefault, nil, "embedded"
		}
		if err != nil {
			http.Error(w, "load workspace layout: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if len(data) == 0 {
			http.Error(w, "layout default is unavailable", http.StatusServiceUnavailable)
			return
		}
		var node yaml.Node
		if err = yaml.Unmarshal(data, &node); err != nil {
			http.Error(w, "invalid layout YAML: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err = validateLayoutYAML(&node); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var layout Layout
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err = decoder.Decode(&layout); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err = validateLayout(&layout); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		permitted, err := filterLayout(r, &layout)
		if err != nil {
			http.Error(w, "layout authorization unavailable", http.StatusServiceUnavailable)
			return
		}
		digest := sha256.Sum256(append(append([]byte(source), 0), data...))
		catalogRevisions := permitted.CatalogRevisions
		permitted.CatalogRevisions = nil
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(layoutResponse{SchemaVersion: 1, LayoutRevision: hex.EncodeToString(digest[:]), WorkspaceID: root, CatalogRevisions: catalogRevisions, Layout: permitted})
	}
}

func validateLayoutYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if seen[key] {
				return fmt.Errorf("duplicate layout key %q at line %d", key, node.Content[i].Line)
			}
			seen[key] = true
		}
	}
	for _, child := range node.Content {
		if err := validateLayoutYAML(child); err != nil {
			return err
		}
	}
	return nil
}

func validateLayout(layout *Layout) error {
	if layout.Version != 1 || strings.TrimSpace(layout.ID) == "" {
		return fmt.Errorf("layout requires version 1 and id")
	}
	if layout.Right != nil && layout.Right.Mode != "" && layout.Right.Mode != "existing" {
		return fmt.Errorf("unsupported right layout mode %q", layout.Right.Mode)
	}
	if layout.Left != nil {
		if width := layout.Left.Width; width != nil && (width.Min < 64 || width.Max < width.Min || width.Default < width.Min || width.Default > width.Max) {
			return fmt.Errorf("invalid sidebar width limits")
		}
		if width := layout.Left.Width; width != nil && width.PreferenceScope != "" && width.PreferenceScope != "global" && width.PreferenceScope != "workspace" {
			return fmt.Errorf("unsupported width preference scope")
		}
		if width := layout.Left.Width; width != nil && width.PreferenceScope == "global" && strings.TrimSpace(width.PreferenceKey) == "" {
			return fmt.Errorf("global width preference requires a key")
		}
		if history := layout.Left.History; history != nil {
			if history.Search != nil && (history.Search.DebounceMs < 0 || history.Search.DebounceMs > 2000) {
				return fmt.Errorf("invalid history search debounce")
			}
			if history.Pagination != nil && history.Pagination.Mode != "" && history.Pagination.Mode != "pages" && history.Pagination.Mode != "continuous" {
				return fmt.Errorf("unsupported history pagination mode")
			}
		}
		if split := layout.Left.Split; split != nil && (split.Initial <= 0 || split.Initial >= 1 || split.MinSectionHeight < 80) {
			return fmt.Errorf("invalid sidebar split")
		}
	}
	apps := map[string]bool{}
	providers := map[string]bool{}
	for _, provider := range layout.WindowProviders {
		if provider.ID == "" || providers[provider.ID] || provider.ID == "workspace" || provider.Type != "mcp" {
			return fmt.Errorf("invalid or duplicate window provider %q", provider.ID)
		}
		providers[provider.ID] = true
	}
	if layout.Topbar != nil {
		if err := validateMenus(layout.Topbar.Actions, map[string]bool{}, providers); err != nil {
			return fmt.Errorf("topbar: %w", err)
		}
		for _, action := range layout.Topbar.Actions {
			if action.Action == nil {
				return fmt.Errorf("topbar action %s must open a destination", action.ID)
			}
		}
	}
	for _, app := range layout.Applications {
		if app.ID == "" || app.Title == "" || apps[app.ID] {
			return fmt.Errorf("invalid or duplicate application %q", app.ID)
		}
		apps[app.ID] = true
		if err := validateLayoutParameters(app.Parameters); err != nil {
			return fmt.Errorf("application %s parameters: %w", app.ID, err)
		}
		if app.WindowCatalog != nil && !providers[app.WindowCatalog.Provider] {
			return fmt.Errorf("application %s references unknown window provider", app.ID)
		}
		if app.Authorization != nil && app.Authorization.Resource != nil {
			return fmt.Errorf("application %s: entity authorization requires a resource binding", app.ID)
		}
		if err := validateCondition(app.VisibleWhen); err != nil {
			return fmt.Errorf("application %s: %w", app.ID, err)
		}
		for _, condition := range []any{app.HiddenWhen, app.DisabledWhen, app.ReadOnlyWhen} {
			if err := validateCondition(condition); err != nil {
				return fmt.Errorf("application %s: %w", app.ID, err)
			}
		}
		ids := map[string]bool{}
		if err := validateMenus(app.Menus, ids, providers); err != nil {
			return fmt.Errorf("application %s: %w", app.ID, err)
		}
	}
	return nil
}

func validateMenus(menus []LayoutMenu, seen map[string]bool, providers map[string]bool) error {
	for _, menu := range menus {
		if menu.ID == "" || menu.Title == "" || seen[menu.ID] {
			return fmt.Errorf("invalid or duplicate menu %q", menu.ID)
		}
		seen[menu.ID] = true
		if err := validateLayoutParameters(menu.Parameters); err != nil {
			return fmt.Errorf("menu %s parameters: %w", menu.ID, err)
		}
		if menu.Authorization != nil && menu.Authorization.Resource != nil {
			return fmt.Errorf("menu %s: entity authorization requires a resource binding", menu.ID)
		}
		if err := validateCondition(menu.VisibleWhen); err != nil {
			return fmt.Errorf("menu %s: %w", menu.ID, err)
		}
		for _, condition := range []any{menu.HiddenWhen, menu.DisabledWhen, menu.ReadOnlyWhen} {
			if err := validateCondition(condition); err != nil {
				return fmt.Errorf("menu %s: %w", menu.ID, err)
			}
		}
		if (menu.Action == nil) == (len(menu.Children) == 0) {
			return fmt.Errorf("menu %s requires either action or children", menu.ID)
		}
		if menu.Action != nil && (menu.Action.Type != "window" || menu.Action.WindowKey == "") {
			return fmt.Errorf("menu %s has unsupported action", menu.ID)
		}
		if menu.Action != nil && (strings.ContainsAny(menu.Action.WindowKey, "/\\?#") || strings.HasPrefix(menu.Action.WindowKey, windowloader.RemotePrefix)) {
			return fmt.Errorf("menu %s has invalid window key", menu.ID)
		}
		if menu.Action != nil {
			if err := validateLayoutParameters(menu.Action.Parameters); err != nil {
				return fmt.Errorf("menu %s action parameters: %w", menu.ID, err)
			}
		}
		if menu.Action != nil && menu.Action.Provider != "" && menu.Action.Provider != "workspace" && !providers[menu.Action.Provider] {
			return fmt.Errorf("menu %s references unknown provider", menu.ID)
		}
		if err := validateMenus(menu.Children, seen, providers); err != nil {
			return err
		}
	}
	return nil
}

func validateLayoutParameters(value any) error {
	if value == nil {
		return nil
	}
	switch actual := value.(type) {
	case map[string]any:
		for _, item := range actual {
			if err := validateLayoutParameters(item); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range actual {
			if err := validateLayoutParameters(item); err != nil {
				return err
			}
		}
	case int:
		if actual > 9007199254740991 || actual < -9007199254740991 {
			return fmt.Errorf("integer is outside JavaScript safe range")
		}
	case int64:
		if actual > 9007199254740991 || actual < -9007199254740991 {
			return fmt.Errorf("integer is outside JavaScript safe range")
		}
	case float64:
		if math.IsNaN(actual) || math.IsInf(actual, 0) {
			return fmt.Errorf("nonfinite number")
		}
	case string, bool:
	default:
		return fmt.Errorf("parameter has unsupported type %T", value)
	}
	return nil
}

func validateCondition(value any) error {
	if value == nil {
		return nil
	}
	condition, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("condition must be an object")
	}
	if len(condition) == 1 {
		for _, op := range []string{"all", "any"} {
			if entries, ok := condition[op]; ok {
				list, ok := entries.([]any)
				if !ok || len(list) == 0 {
					return fmt.Errorf("%s requires nonempty array", op)
				}
				for _, entry := range list {
					if err := validateCondition(entry); err != nil {
						return err
					}
				}
				return nil
			}
		}
		if entry, ok := condition["not"]; ok {
			return validateCondition(entry)
		}
	}
	if condition["source"] != "authorization" {
		return fmt.Errorf("condition source must be authorization")
	}
	field, hasField := condition["field"].(string)
	selector, hasSelector := condition["selector"].(string)
	if hasField == hasSelector || strings.TrimSpace(field+selector) == "" {
		return fmt.Errorf("condition requires exactly one field or selector")
	}
	count := 0
	for key, value := range condition {
		switch key {
		case "source", "field", "selector":
		case "equals", "notEquals", "contains":
			count++
		case "in":
			if _, ok := value.([]any); !ok {
				return fmt.Errorf("in requires array")
			}
			count++
		case "empty", "notEmpty", "exists":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%s requires boolean", key)
			}
			count++
		default:
			return fmt.Errorf("unknown condition operator %q", key)
		}
	}
	if count != 1 {
		return fmt.Errorf("condition requires exactly one operator")
	}
	return nil
}

func filterLayout(r *http.Request, layout *Layout) (*Layout, error) {
	result := *layout
	result.Applications = nil
	result.WindowProviders = nil
	result.CatalogRevisions = map[string]string{}
	providers := map[string]bool{}
	for _, provider := range layout.WindowProviders {
		providers[provider.ID] = true
	}
	type preparedApp struct {
		app      LayoutApplication
		snapshot *permittedview.Snapshot
	}
	var prepared []preparedApp
	for _, app := range layout.Applications {
		allowed, snapshot, err := resolveNode(r, app.Authorization, app.VisibleWhen, nil)
		if err != nil {
			return nil, err
		}
		if snapshot == nil && (app.HiddenWhen != nil || app.DisabledWhen != nil || app.ReadOnlyWhen != nil) {
			return nil, fmt.Errorf("application %s: authorization condition has no snapshot", app.ID)
		}
		if allowed && app.HiddenWhen != nil {
			allowed = !evalSnapshotCondition(app.HiddenWhen, snapshot)
		}
		if !allowed {
			continue
		}
		app.Disabled = app.DisabledWhen != nil && evalSnapshotCondition(app.DisabledWhen, snapshot)
		app.ReadOnly = app.ReadOnlyWhen != nil && evalSnapshotCondition(app.ReadOnlyWhen, snapshot)
		if app.WindowCatalog != nil {
			entries, catalogErr := windowloader.ListRemoteWindows(r.Context(), app.WindowCatalog.Provider, app.ID, app.WindowCatalog.Group)
			if catalogErr != nil {
				return nil, catalogErr
			}
			result.CatalogRevisions[app.ID+":"+app.WindowCatalog.Provider+":"+app.WindowCatalog.Group] = entries.Revision
			groupTitles := map[string]string{}
			for _, entry := range entries.Groups {
				groupTitles[entry.ID] = entry.Title
			}
			groups := map[string][]LayoutMenu{}
			for _, entry := range entries.Windows {
				for _, condition := range []any{entry.VisibleWhen, entry.HiddenWhen, entry.DisabledWhen, entry.ReadOnlyWhen} {
					if err := validateCondition(condition); err != nil {
						return nil, fmt.Errorf("remote window %s: %w", entry.Key, err)
					}
				}
				item := LayoutMenu{ID: "catalog:" + entry.Key, Title: entry.Title, Icon: entry.Icon, Authorization: entry.Authorization, VisibleWhen: entry.VisibleWhen, HiddenWhen: entry.HiddenWhen, DisabledWhen: entry.DisabledWhen, ReadOnlyWhen: entry.ReadOnlyWhen, Action: &LayoutAction{Type: "window", Provider: app.WindowCatalog.Provider, WindowKey: entry.Key, Parameters: entry.Parameters}}
				if entry.GroupID == "" || app.WindowCatalog.Group != "" {
					app.Menus = append(app.Menus, item)
					continue
				}
				if groupTitles[entry.GroupID] == "" {
					return nil, fmt.Errorf("remote window %s references unknown group", entry.Key)
				}
				groups[entry.GroupID] = append(groups[entry.GroupID], item)
			}
			for _, group := range entries.Groups {
				if children := groups[group.ID]; len(children) > 0 {
					app.Menus = append(app.Menus, LayoutMenu{ID: "catalog:group:" + group.ID, Title: group.Title, Children: children})
				}
			}
		}
		if err := validateMenus(app.Menus, map[string]bool{}, providers); err != nil {
			return nil, err
		}
		prepared = append(prepared, preparedApp{app: app, snapshot: snapshot})
	}
	var allowedWindows map[string]bool
	if runtime := policy.DefaultRuntime(); runtime != nil && runtime.IsEnabled(policy.OperationWindowView) {
		candidates := []policy.Candidate{}
		seen := map[string]bool{}
		for _, preparedApp := range prepared {
			collectLayoutWindowCandidates(preparedApp.app.Menus, &candidates, seen)
		}
		if layout.Topbar != nil {
			collectLayoutWindowCandidates(layout.Topbar.Actions, &candidates, seen)
		}
		allowedWindows = map[string]bool{}
		if len(candidates) > 0 {
			allowed, err := runtime.Filter(r.Context(), policy.OperationWindowView, "", candidates, nil)
			if errors.Is(err, policy.ErrDenied) {
				allowed = nil
			} else if err != nil {
				return nil, err
			}
			for _, candidate := range allowed {
				allowedWindows[strings.ToLower(candidate.ID)] = true
			}
		}
	}
	for _, preparedApp := range prepared {
		app, snapshot := preparedApp.app, preparedApp.snapshot
		var err error
		app.Menus, err = filterMenus(r, app.Menus, snapshot, app.Disabled, app.ReadOnly, allowedWindows)
		if err != nil {
			return nil, err
		}
		if len(app.Menus) > 0 {
			app.Authorization, app.VisibleWhen, app.HiddenWhen, app.DisabledWhen, app.ReadOnlyWhen, app.WindowCatalog = nil, nil, nil, nil, nil, nil
			result.Applications = append(result.Applications, app)
		}
	}
	if layout.Topbar != nil {
		topbar := *layout.Topbar
		var err error
		topbar.Actions, err = filterMenus(r, layout.Topbar.Actions, nil, false, false, allowedWindows)
		if err != nil {
			return nil, err
		}
		result.Topbar = &topbar
	}
	return &result, nil
}

func layoutWindowKey(action *LayoutAction) string {
	if action == nil {
		return ""
	}
	if action.Provider != "" && action.Provider != "workspace" {
		return windowloader.RemoteWindowKey(action.Provider, action.WindowKey)
	}
	return action.WindowKey
}

func collectLayoutWindowCandidates(menus []LayoutMenu, candidates *[]policy.Candidate, seen map[string]bool) {
	for _, menu := range menus {
		if menu.Action != nil && menu.Action.Type == "window" {
			key := layoutWindowKey(menu.Action)
			if normalized := strings.ToLower(key); key != "" && !seen[normalized] {
				seen[normalized] = true
				*candidates = append(*candidates, policy.Candidate{ID: key, Kind: "window", Metadata: map[string]any{"title": menu.Title}})
			}
		}
		collectLayoutWindowCandidates(menu.Children, candidates, seen)
	}
}

func filterMenus(r *http.Request, menus []LayoutMenu, inherited *permittedview.Snapshot, inheritedDisabled, inheritedReadOnly bool, allowedWindows map[string]bool) ([]LayoutMenu, error) {
	var result []LayoutMenu
	for _, menu := range menus {
		allowed, snapshot, err := resolveNode(r, menu.Authorization, menu.VisibleWhen, inherited)
		if err != nil {
			return nil, err
		}
		if snapshot == nil && (menu.HiddenWhen != nil || menu.DisabledWhen != nil || menu.ReadOnlyWhen != nil) {
			return nil, fmt.Errorf("menu %s: authorization condition has no snapshot", menu.ID)
		}
		if !allowed {
			continue
		}
		if menu.HiddenWhen != nil && evalSnapshotCondition(menu.HiddenWhen, snapshot) {
			continue
		}
		menu.Disabled = inheritedDisabled || (menu.DisabledWhen != nil && evalSnapshotCondition(menu.DisabledWhen, snapshot))
		menu.ReadOnly = inheritedReadOnly || (menu.ReadOnlyWhen != nil && evalSnapshotCondition(menu.ReadOnlyWhen, snapshot))
		if len(menu.Children) > 0 {
			menu.Children, err = filterMenus(r, menu.Children, snapshot, menu.Disabled, menu.ReadOnly, allowedWindows)
			if err != nil {
				return nil, err
			}
			if len(menu.Children) == 0 {
				continue
			}
		}
		if menu.Action != nil && menu.Action.Type == "window" && allowedWindows != nil && !allowedWindows[strings.ToLower(layoutWindowKey(menu.Action))] {
			continue
		}
		menu.Authorization, menu.VisibleWhen, menu.HiddenWhen, menu.DisabledWhen, menu.ReadOnlyWhen = nil, nil, nil, nil, nil
		result = append(result, menu)
	}
	return result, nil
}

func evalSnapshotCondition(condition any, snapshot *permittedview.Snapshot) bool {
	if snapshot == nil {
		return false
	}
	allowed, err := permittedview.EvaluateAuthorizationCondition(condition, snapshot, nil)
	return err == nil && allowed
}

func resolveNode(r *http.Request, spec *forgetypes.AuthorizationSpec, condition any, inherited *permittedview.Snapshot) (bool, *permittedview.Snapshot, error) {
	snapshot := inherited
	if spec != nil {
		runtime := permittedview.DefaultRuntime()
		if runtime == nil || runtime.Resolver == nil {
			return false, nil, fmt.Errorf("authorization resolver unavailable")
		}
		if spec.Resource != nil {
			return false, nil, fmt.Errorf("resource-bound shell authorization requires parameter binding")
		}
		if spec.ResourceType == "" {
			return false, nil, fmt.Errorf("authorization resource type required")
		}
		var err error
		snapshot, err = runtime.Resolver.Resolve(r.Context(), &permittedview.Request{ResourceType: spec.ResourceType, RequestedGlobalCapabilities: spec.RequestedGlobalCapabilities, IncludePrincipal: true})
		if err != nil {
			return false, nil, err
		}
		if snapshot == nil || snapshot.AuthorizationVersion == "" || !time.Now().Before(snapshot.ExpiresAt) {
			return false, nil, fmt.Errorf("invalid authorization snapshot")
		}
	}
	if condition == nil {
		return true, snapshot, nil
	}
	if snapshot == nil {
		return false, nil, fmt.Errorf("condition has no authorization snapshot")
	}
	allowed, err := permittedview.EvaluateAuthorizationCondition(condition, snapshot, nil)
	return allowed, snapshot, err
}

func (h *MetadataHandler) handleRemoteDatasource() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		providerID, key, id := r.PathValue("provider"), r.PathValue("key"), r.PathValue("id")
		if providerID == "" || key == "" || id == "" {
			http.Error(w, "invalid datasource path", http.StatusBadRequest)
			return
		}
		windowKey := windowloader.RemoteWindowKey(providerID, key)
		if runtime := policy.DefaultRuntime(); runtime != nil && runtime.IsEnabled(policy.OperationWindowView) {
			if err := runtime.Authorize(r.Context(), policy.OperationWindowView, strings.TrimSpace(r.URL.Query().Get("conversationId")), policy.Candidate{ID: windowKey, Kind: "window"}, nil); err != nil {
				if errors.Is(err, policy.ErrDenied) {
					http.Error(w, "datasource not found", http.StatusNotFound)
				} else {
					http.Error(w, "authorization unavailable", http.StatusServiceUnavailable)
				}
				return
			}
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read datasource inputs", http.StatusBadRequest)
			return
		}
		inputs := map[string]interface{}{}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &inputs); err != nil {
				http.Error(w, "invalid datasource inputs", http.StatusBadRequest)
				return
			}
		}
		result, err := windowloader.FetchRemoteDatasource(r.Context(), providerID, key, id, inputs)
		if err != nil {
			if errors.Is(err, policy.ErrDenied) {
				http.Error(w, "datasource not found", http.StatusNotFound)
				return
			}
			http.Error(w, "remote datasource unavailable: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": result.Rows, "rows": result.Rows, "dataInfo": result.DataInfo})
	}
}

// AuthorizeRemoteWindow applies the workspace-authored application gate on
// direct, restored, and datasource-triggered remote definition loads.
func (h *MetadataHandler) AuthorizeRemoteWindow(ctx context.Context, providerID, key string) error {
	root := ws.Root()
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	ref, explicit := "ui/layout.yaml", false
	if cfg != nil {
		if ui, ok := cfg.Raw["ui"].(map[string]any); ok {
			if layout, ok := ui["layout"].(map[string]any); ok {
				if value, ok := layout["ref"].(string); ok && strings.TrimSpace(value) != "" {
					ref, explicit = value, true
				}
			}
		}
	}
	layoutPath, err := ws.ResolveChildPath(root, ref)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(layoutPath)
	if os.IsNotExist(err) && !explicit {
		data, err = h.layoutDefault, nil
	}
	if err != nil {
		return err
	}
	var layout Layout
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}
	if err := validateLayoutYAML(&node); err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&layout); err != nil {
		return err
	}
	if err := validateLayout(&layout); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/v1/workspace/layout", nil)
	if err != nil {
		return err
	}
	for _, app := range layout.Applications {
		if !appUsesRemoteWindow(app, providerID, key) {
			continue
		}
		allowed, snapshot, err := resolveNode(request, app.Authorization, app.VisibleWhen, nil)
		if err != nil {
			return err
		}
		if allowed && app.HiddenWhen != nil {
			allowed = !evalSnapshotCondition(app.HiddenWhen, snapshot)
		}
		if allowed {
			return nil
		}
	}
	return policy.ErrDenied
}

func appUsesRemoteWindow(app LayoutApplication, providerID, key string) bool {
	if app.WindowCatalog != nil && app.WindowCatalog.Provider == providerID {
		return true
	}
	var visit func([]LayoutMenu) bool
	visit = func(menus []LayoutMenu) bool {
		for _, menu := range menus {
			if menu.Action != nil && menu.Action.Provider == providerID && menu.Action.WindowKey == key {
				return true
			}
			if visit(menu.Children) {
				return true
			}
		}
		return false
	}
	return visit(app.Menus)
}
