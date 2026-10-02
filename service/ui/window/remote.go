package window

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/config"
	forgetypes "github.com/viant/forge/backend/types"
	"gopkg.in/yaml.v3"
)

const RemotePrefix = "provider:"

var remoteProviderIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
var remoteWindowKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type RemoteExecutor interface {
	Execute(context.Context, string, map[string]interface{}) (string, error)
}

type RemoteProvider struct {
	ID          string `json:"id" yaml:"id"`
	Type        string `json:"type" yaml:"type"`
	Trusted     bool   `json:"trusted,omitempty" yaml:"trusted,omitempty"`
	ServerRef   string `json:"serverRef" yaml:"serverRef"`
	CatalogTool string `json:"catalogTool" yaml:"catalogTool"`
	WindowTool  string `json:"windowTool" yaml:"windowTool"`
}

type RemoteWindow struct {
	Key           string                        `json:"key"`
	Title         string                        `json:"title"`
	Icon          string                        `json:"icon,omitempty"`
	GroupID       string                        `json:"groupId,omitempty"`
	Parameters    map[string]any                `json:"parameters,omitempty"`
	Authorization *forgetypes.AuthorizationSpec `json:"authorization,omitempty"`
	VisibleWhen   any                           `json:"visibleWhen,omitempty"`
	HiddenWhen    any                           `json:"hiddenWhen,omitempty"`
	DisabledWhen  any                           `json:"disabledWhen,omitempty"`
	ReadOnlyWhen  any                           `json:"readOnlyWhen,omitempty"`
}

type RemoteGroup struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type RemoteCatalog struct {
	Revision string
	Groups   []RemoteGroup
	Windows  []RemoteWindow
}

type remoteCatalog struct {
	ContractVersion int            `json:"contractVersion"`
	CatalogRevision string         `json:"catalogRevision"`
	Groups          []RemoteGroup  `json:"groups,omitempty"`
	Windows         []RemoteWindow `json:"windows"`
	NextCursor      string         `json:"nextCursor,omitempty"`
}

type remoteConfig struct {
	WindowProviders []RemoteProvider `yaml:"windowProviders"`
}

var remoteState struct {
	sync.RWMutex
	executor      RemoteExecutor
	defaultLayout []byte
	authorize     func(context.Context, string, string) error
}

// ConfigureRemoteWindowProvider binds the current process's one workspace to
// its authenticated MCP executor. Provider names/tools still come from layout.
func ConfigureRemoteWindowProvider(executor RemoteExecutor, defaultLayout []byte) {
	remoteState.Lock()
	remoteState.executor = executor
	remoteState.defaultLayout = append([]byte(nil), defaultLayout...)
	remoteState.Unlock()
}

func ConfigureRemoteWindowAuthorization(authorize func(context.Context, string, string) error) {
	remoteState.Lock()
	remoteState.authorize = authorize
	remoteState.Unlock()
}

func remoteSettings() (RemoteExecutor, []byte) {
	remoteState.RLock()
	defer remoteState.RUnlock()
	return remoteState.executor, append([]byte(nil), remoteState.defaultLayout...)
}

func loadRemoteProvider(id string) (*RemoteProvider, RemoteExecutor, error) {
	executor, fallback := remoteSettings()
	if executor == nil {
		return nil, nil, fmt.Errorf("remote window executor unavailable")
	}
	root := workspace.Root()
	cfg, err := config.Load(root)
	if err != nil {
		return nil, nil, err
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
	layoutPath, err := workspace.ResolveChildPath(root, ref)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(layoutPath)
	if os.IsNotExist(err) && !explicit {
		data, err = fallback, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var settings remoteConfig
	if err = yaml.Unmarshal(data, &settings); err != nil {
		return nil, nil, err
	}
	for _, entry := range settings.WindowProviders {
		if entry.ID != id {
			continue
		}
		if !remoteProviderIDPattern.MatchString(entry.ID) || entry.Type != "mcp" || entry.ServerRef == "" || entry.CatalogTool == "" || entry.WindowTool == "" {
			return nil, nil, fmt.Errorf("invalid remote window provider %q", id)
		}
		if strings.ContainsAny(entry.ServerRef+entry.CatalogTool+entry.WindowTool, "/\\:\t\n ") {
			return nil, nil, fmt.Errorf("invalid remote tool reference")
		}
		return &entry, executor, nil
	}
	return nil, nil, fmt.Errorf("remote window provider %q not configured", id)
}

func RemoteWindowKey(providerID, key string) string { return RemotePrefix + providerID + ":" + key }

func ParseRemoteWindowKey(key string) (string, string, bool) {
	if !strings.HasPrefix(key, RemotePrefix) {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(key, RemotePrefix), ":", 2)
	if len(parts) != 2 || !remoteProviderIDPattern.MatchString(parts[0]) || !remoteWindowKeyPattern.MatchString(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func ListRemoteWindows(ctx context.Context, providerID, applicationID, group string) (*RemoteCatalog, error) {
	provider, executor, err := loadRemoteProvider(providerID)
	if err != nil {
		return nil, err
	}
	result := &RemoteCatalog{}
	seen := map[string]bool{}
	seenGroups := map[string]bool{}
	seenCursors := map[string]bool{}
	cursor := ""
	for page := 0; page < 10; page++ {
		raw, err := executor.Execute(ctx, provider.ServerRef+":"+provider.CatalogTool, map[string]interface{}{"contractVersion": 1, "applicationId": applicationID, "group": group, "conversationId": "", "cursor": cursor, "limit": 100})
		if err != nil {
			return nil, err
		}
		var catalog remoteCatalog
		if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
			return nil, fmt.Errorf("decode remote window catalog: %w", err)
		}
		if catalog.ContractVersion != 1 {
			return nil, fmt.Errorf("unsupported remote catalog version")
		}
		if catalog.CatalogRevision == "" || (result.Revision != "" && result.Revision != catalog.CatalogRevision) {
			return nil, fmt.Errorf("remote catalog revision missing or changed during paging")
		}
		result.Revision = catalog.CatalogRevision
		for _, entry := range catalog.Groups {
			if entry.ID == "" || entry.Title == "" {
				return nil, fmt.Errorf("invalid remote window group")
			}
			if !seenGroups[entry.ID] {
				result.Groups = append(result.Groups, entry)
				seenGroups[entry.ID] = true
				if len(seenGroups) > 100 {
					return nil, fmt.Errorf("remote catalog exceeds 100 groups")
				}
			}
		}
		for _, entry := range catalog.Windows {
			if !remoteWindowKeyPattern.MatchString(entry.Key) || entry.Title == "" || seen[entry.Key] {
				return nil, fmt.Errorf("invalid or duplicate remote window key %q", entry.Key)
			}
			seen[entry.Key] = true
			if len(seen) > 1000 {
				return nil, fmt.Errorf("remote catalog exceeds 1000 windows")
			}
			if group == "" || entry.GroupID == group {
				result.Windows = append(result.Windows, entry)
			}
		}
		if catalog.NextCursor == "" {
			return result, nil
		}
		if catalog.NextCursor == cursor || seenCursors[catalog.NextCursor] {
			return nil, fmt.Errorf("remote catalog repeated cursor")
		}
		seenCursors[catalog.NextCursor] = true
		cursor = catalog.NextCursor
	}
	return nil, fmt.Errorf("remote catalog exceeded page limit")
}

type remoteDefinition struct {
	ContractVersion int                            `json:"contractVersion"`
	Window          *forgetypes.Window             `json:"window"`
	DataSources     map[string]*dsproto.DataSource `json:"dataSources,omitempty"`
}

func loadRemoteDefinition(ctx context.Context, providerID, key string) (*remoteDefinition, error) {
	remoteState.RLock()
	authorize := remoteState.authorize
	remoteState.RUnlock()
	if authorize != nil {
		if err := authorize(ctx, providerID, key); err != nil {
			return nil, err
		}
	}
	provider, executor, err := loadRemoteProvider(providerID)
	if err != nil {
		return nil, err
	}
	raw, err := executor.Execute(ctx, provider.ServerRef+":"+provider.WindowTool, map[string]interface{}{"contractVersion": 1, "windowKey": key})
	if err != nil {
		return nil, err
	}
	var payload remoteDefinition
	if err = json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode remote window: %w", err)
	}
	if payload.ContractVersion != 1 || payload.Window == nil || payload.Window.View.Content == nil {
		return nil, fmt.Errorf("invalid remote window definition")
	}
	if !provider.Trusted {
		if payload.Window.Actions != nil && strings.TrimSpace(payload.Window.Actions.Code) != "" {
			return nil, fmt.Errorf("remote window executable actions are unsupported")
		}
		if len(payload.Window.ActionRefs) != 0 {
			return nil, fmt.Errorf("remote window action references are unsupported")
		}
		for id, source := range payload.Window.DataSource {
			if source.Service != nil {
				return nil, fmt.Errorf("remote window datasource %q must use an inline MCP backend", id)
			}
		}
	}
	return &payload, nil
}

func LoadRemoteWindow(ctx context.Context, providerID, key string) (*forgetypes.Window, error) {
	payload, err := loadRemoteDefinition(ctx, providerID, key)
	if err != nil {
		return nil, err
	}
	provider, _, err := loadRemoteProvider(providerID)
	if err != nil {
		return nil, err
	}
	win := payload.Window
	if win.DataSource == nil {
		win.DataSource = map[string]forgetypes.DataSource{}
	}
	for id, source := range payload.DataSources {
		if id == "" || strings.ContainsAny(id, "/\\:") || source == nil || source.Backend == nil || source.Backend.Kind != dsproto.BackendMCPTool || source.Backend.Method == "" || strings.ContainsAny(source.Backend.Method, "/\\: \t\n") {
			return nil, fmt.Errorf("invalid remote datasource %q", id)
		}
		if source.Backend.Service != provider.ServerRef {
			return nil, fmt.Errorf("remote datasource %q uses unapproved MCP service", id)
		}
		if _, exists := win.DataSource[id]; exists {
			return nil, fmt.Errorf("duplicate remote datasource %q", id)
		}
		entry := source.DataSource
		entry.Service = &forgetypes.Service{Endpoint: "agentlyAPI", URI: "/v1/workspace/ui/providers/" + providerID + "/windows/" + key + "/datasources/" + id + "/fetch", Method: "POST"}
		win.DataSource[id] = entry
	}
	return win, nil
}

type oneRemoteDatasource struct{ datasource *dsproto.DataSource }

func (s oneRemoteDatasource) Get(id string) (*dsproto.DataSource, bool) {
	if s.datasource != nil && s.datasource.ID == id {
		return s.datasource, true
	}
	return nil, false
}

func FetchRemoteDatasource(ctx context.Context, providerID, key, id string, inputs map[string]interface{}) (*dsproto.FetchResult, error) {
	payload, err := loadRemoteDefinition(ctx, providerID, key)
	if err != nil {
		return nil, err
	}
	source := payload.DataSources[id]
	if source == nil || source.Backend == nil || source.Backend.Kind != dsproto.BackendMCPTool || source.Backend.Method == "" || strings.ContainsAny(source.Backend.Method, "/\\: \t\n") {
		return nil, fmt.Errorf("remote datasource %q not found", id)
	}
	provider, executor, err := loadRemoteProvider(providerID)
	if err != nil {
		return nil, err
	}
	if source.Backend.Service != provider.ServerRef {
		return nil, fmt.Errorf("remote datasource %q uses unapproved MCP service", id)
	}
	copySource := *source
	copySource.ID = id
	noCache := false
	copySource.Cache = &dsproto.CachePolicy{Enabled: &noCache}
	service := dssvc.New(dssvc.Options{Store: oneRemoteDatasource{datasource: &copySource}, Executor: executor})
	return service.Fetch(ctx, id, inputs, dssvc.FetchOptions{BypassCache: true})
}
