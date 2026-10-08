package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/viant/agently-core/app/executor/config"
	llmprovider "github.com/viant/agently-core/genai/llm/provider"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	policy "github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/ui/permittedview"
	uistyle "github.com/viant/agently-core/service/ui/style"
	ws "github.com/viant/agently-core/workspace"
	wscodec "github.com/viant/agently-core/workspace/codec"
	wscfg "github.com/viant/agently-core/workspace/config"
)

// MetadataResponse is the response for the workspace metadata endpoint.
type MetadataResponse struct {
	Composer           wscfg.Composer      `json:"composer"`
	WorkspaceID        string              `json:"workspaceId,omitempty"`
	UIStyles           *uistyle.Descriptor `json:"uiStyles,omitempty"`
	UIThemes           *uistyle.Descriptor `json:"uiThemes,omitempty"`
	UIStyleDiagnostics []string            `json:"uiStyleDiagnostics,omitempty"`
	WorkspaceRoot      string              `json:"workspaceRoot,omitempty"`
	WorkspaceVersion   string              `json:"workspaceVersion,omitempty"`
	MetadataVersion    string              `json:"metadataVersion,omitempty"`
	DefaultAgent       string              `json:"defaultAgent,omitempty"`
	DefaultModel       string              `json:"defaultModel,omitempty"`
	DefaultEmbedder    string              `json:"defaultEmbedder,omitempty"`
	AppName            string              `json:"appName,omitempty"`
	AppIconRef         string              `json:"appIconRef,omitempty"`
	Defaults           *Defaults           `json:"defaults,omitempty"`
	Capabilities       Capabilities        `json:"capabilities,omitempty"`
	Agents             []string            `json:"agents,omitempty"`
	Models             []string            `json:"models,omitempty"`
	AgentInfos         []AgentInfo         `json:"agentInfos,omitempty"`
	ModelInfos         []ModelInfo         `json:"modelInfos,omitempty"`
	Version            string              `json:"version,omitempty"`
}

type PublicAgentsResponse struct {
	AgentInfos []AgentInfo `json:"agentInfos"`
}

// Defaults captures UI-facing runtime defaults in a stable nested shape.
type Defaults struct {
	AppName         string `json:"appName,omitempty"`
	AppIconRef      string `json:"appIconRef,omitempty"`
	Agent           string `json:"agent,omitempty"`
	Model           string `json:"model,omitempty"`
	Embedder        string `json:"embedder,omitempty"`
	AutoSelectTools bool   `json:"autoSelectTools,omitempty"`
	// ElicitationTimeoutSec is the per-prompt response timeout applied by
	// interactive clients (CLI, UI) when waiting for a user to respond to an
	// elicitation. Zero means the client should use its built-in default.
	ElicitationTimeoutSec int `json:"elicitationTimeoutSec,omitempty"`
}

// Capabilities advertises optional backend contracts so the UI can avoid
// inventing client-only sentinels and endpoint probes.
type Capabilities struct {
	WorkspaceLayout       bool `json:"workspaceLayout,omitempty"`
	AgentAutoSelection    bool `json:"agentAutoSelection,omitempty"`
	ModelAutoSelection    bool `json:"modelAutoSelection,omitempty"`
	ToolAutoSelection     bool `json:"toolAutoSelection,omitempty"`
	Goals                 bool `json:"goals,omitempty"`
	Reporting             bool `json:"reporting,omitempty"`
	CompactConversation   bool `json:"compactConversation,omitempty"`
	PruneConversation     bool `json:"pruneConversation,omitempty"`
	AnonymousSession      bool `json:"anonymousSession,omitempty"`
	MessageCursor         bool `json:"messageCursor,omitempty"`
	StructuredElicitation bool `json:"structuredElicitation,omitempty"`
	TurnStartedEvent      bool `json:"turnStartedEvent,omitempty"`
}

// AgentInfo describes a UI-facing agent entry with its preferred model.
type AgentInfo struct {
	ID                    string                         `json:"id,omitempty"`
	Name                  string                         `json:"name,omitempty"`
	Internal              bool                           `json:"internal,omitempty"`
	ModelRef              string                         `json:"modelRef,omitempty"`
	Tools                 []string                       `json:"tools,omitempty"`
	StarterTasks          []agentmdl.StarterTask         `json:"starterTasks,omitempty"`
	StarterTaskCategories []agentmdl.StarterTaskCategory `json:"starterTaskCategories,omitempty"`
}

type metadataAgentProjection struct {
	ID                    string                         `yaml:"id,omitempty"`
	Name                  string                         `yaml:"name,omitempty"`
	Internal              bool                           `yaml:"internal,omitempty"`
	ModelRef              string                         `yaml:"modelRef,omitempty"`
	Model                 string                         `yaml:"model,omitempty"`
	Profile               *metadataProfile               `yaml:"profile,omitempty"`
	StarterTasks          []agentmdl.StarterTask         `yaml:"starterTasks,omitempty"`
	StarterTaskCategories []agentmdl.StarterTaskCategory `yaml:"starterTaskCategories,omitempty"`
}

type metadataProfile struct {
	Name string `yaml:"name,omitempty"`
}

// ModelInfo describes a UI-facing model entry.
type ModelInfo struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// MetadataHandler serves the workspace metadata endpoint.
type MetadataHandler struct {
	windowResourceCatalog WindowResourceCatalog
	styles                *uistyle.Service
	defaults              *config.Defaults
	store                 ws.Store
	version               string
	reportingOverride     *bool
	authorizationPolicy   *policy.Runtime
	permittedRuntime      *permittedview.Runtime
	legacyDefaults        bool
	layoutDefault         []byte
	toolDefinitions       func(context.Context) ([]ToolDefinition, error)
}

func (h *MetadataHandler) SetAuthorizationPolicy(runtime *policy.Runtime) {
	if h != nil {
		h.authorizationPolicy = runtime
	}
}

func (h *MetadataHandler) SetPermittedRuntime(runtime *permittedview.Runtime) {
	if h != nil {
		h.permittedRuntime = runtime
	}
}

// SetAuthorizationRuntimes binds a host's decision sources, including explicit
// nil values, without inheriting another host's process-global defaults.
func (h *MetadataHandler) SetAuthorizationRuntimes(admission *policy.Runtime, capabilities *permittedview.Runtime) {
	if h == nil {
		return
	}
	h.authorizationPolicy = admission
	h.permittedRuntime = capabilities
	h.legacyDefaults = false
}

// NewMetadataHandler creates a metadata handler.
func NewMetadataHandler(defaults *config.Defaults, store ws.Store, version string) *MetadataHandler {
	return &MetadataHandler{
		styles:         uistyle.Workspace(),
		defaults:       defaults,
		store:          store,
		version:        version,
		legacyDefaults: true,
	}
}

// SetReportingCapabilityEnabled overrides reporting capability signaling with
// the effective runtime registration state when available.
func (h *MetadataHandler) SetReportingCapabilityEnabled(enabled bool) {
	if h == nil {
		return
	}
	value := enabled
	h.reportingOverride = &value
}

// Register mounts the metadata endpoint.
func (h *MetadataHandler) Register(mux *http.ServeMux) {
	h.styles.Register(mux)
	mux.HandleFunc("GET /v1/workspace/metadata", h.handleMetadata())
	mux.HandleFunc("GET /v1/workspace/metadata/publicagents", h.handlePublicAgents())
	mux.HandleFunc("GET /v1/workspace/layout", h.handleLayout())
	mux.HandleFunc("GET /v1/workspace/tool", h.handleWorkspaceTools())
	mux.HandleFunc("GET /v1/workspace/models", h.handleWorkspaceModels())
	mux.HandleFunc("GET /v1/workspace/models/{id}", h.handleGetWorkspaceModel())
	mux.HandleFunc("PUT /v1/workspace/models/{id}", h.handleSaveWorkspaceModel())
	mux.HandleFunc("POST /v1/workspace/ui/providers/{provider}/windows/{key}/datasources/{id}/fetch", h.handleRemoteDatasource())
}

func (h *MetadataHandler) handlePublicAgents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := h.PublicAgents(r.Context())
		if err != nil {
			writeMetadataError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(result)
	}
}
func (h *MetadataHandler) handleMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := h.Metadata(r.Context())
		if err != nil {
			writeMetadataError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(result)
	}
}

func resolveMetadataVersion(resp MetadataResponse) string {
	resp.MetadataVersion = ""
	data, err := json.Marshal(resp)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func resolveWorkspaceVersion(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return "0.0.0"
	}
	data, err := os.ReadFile(filepath.Join(root, "Version"))
	if err != nil {
		return "0.0.0"
	}
	version := strings.TrimSpace(string(data))
	if version == "" {
		return "0.0.0"
	}
	return version
}

func goalsCapabilityEnabled(entries []AgentInfo) bool {
	if !hasGoalCapability(entries) {
		return false
	}
	cfg, err := wscfg.Load(ws.Root())
	if err != nil {
		return false
	}
	if cfg == nil {
		return true
	}
	if !cfg.GoalsEnabled() {
		return false
	}
	if services, ok := cfg.InternalServiceList(); ok && len(services) > 0 {
		for _, service := range services {
			normalized := strings.ToLower(strings.TrimSpace(service))
			if normalized == "system/goal" || normalized == "system:goal" {
				return true
			}
		}
		return false
	}
	return true
}

func (h *MetadataHandler) loadAgentInfos(ctx context.Context, names []string) []AgentInfo {
	if h == nil || h.store == nil || len(names) == 0 {
		return nil
	}
	names = append([]string(nil), names...)
	sort.Strings(names)
	var result []AgentInfo
	for _, name := range names {
		raw, err := h.store.Load(ctx, ws.KindAgent, name)
		if err != nil || len(raw) == 0 {
			result = append(result, AgentInfo{ID: name, Name: name})
			continue
		}
		cfg := &metadataAgentProjection{}
		rawMap := map[string]interface{}{}
		if err := wscodec.DecodeData(name+".yaml", raw, cfg); err != nil {
			result = append(result, AgentInfo{ID: name, Name: name})
			continue
		}
		_ = wscodec.DecodeData(name+".yaml", raw, &rawMap)
		id := cfg.ID
		if id == "" {
			id = name
		}
		label := cfg.Name
		if label == "" && cfg.Profile != nil {
			label = cfg.Profile.Name
		}
		if label == "" {
			label = id
		}
		result = append(result, AgentInfo{
			ID:                    id,
			Name:                  label,
			Internal:              cfg.Internal,
			ModelRef:              firstNonEmpty(cfg.ModelRef, cfg.Model, stringValue(rawMap["modelRef"]), stringValue(rawMap["model"])),
			Tools:                 agentToolDefaults(rawMap),
			StarterTasks:          append([]agentmdl.StarterTask(nil), cfg.StarterTasks...),
			StarterTaskCategories: append([]agentmdl.StarterTaskCategory(nil), cfg.StarterTaskCategories...),
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(firstNonEmpty(result[i].Name, result[i].ID)))
		right := strings.ToLower(strings.TrimSpace(firstNonEmpty(result[j].Name, result[j].ID)))
		if left != right {
			return left < right
		}
		return strings.TrimSpace(result[i].ID) < strings.TrimSpace(result[j].ID)
	})
	return result
}

func (h *MetadataHandler) filterStarterPrompts(ctx context.Context, infos []AgentInfo) ([]AgentInfo, error) {
	if h == nil || h.authorizationPolicy == nil || !h.authorizationPolicy.IsEnabled(policy.OperationStarterPromptView) {
		return infos, nil
	}
	var candidates []policy.Candidate
	for _, info := range infos {
		for _, starter := range info.StarterTasks {
			candidateID := strings.TrimSpace(info.ID) + ":" + strings.TrimSpace(starter.ID)
			candidates = append(candidates, policy.Candidate{ID: candidateID, Kind: "starterPrompt", Metadata: map[string]any{
				"agentId": info.ID, "starterPromptId": starter.ID,
			}})
		}
	}
	allowedCandidates, err := h.authorizationPolicy.Filter(ctx, policy.OperationStarterPromptView, "", candidates, nil)
	if errors.Is(err, policy.ErrIdentityRejected) {
		return nil, err
	}
	if errors.Is(err, policy.ErrDenied) {
		allowedCandidates = nil
	} else if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(allowedCandidates))
	for _, candidate := range allowedCandidates {
		allowed[strings.ToLower(strings.TrimSpace(candidate.ID))] = true
	}
	result := append([]AgentInfo(nil), infos...)
	for index := range result {
		filtered := make([]agentmdl.StarterTask, 0, len(result[index].StarterTasks))
		usedCategories := map[string]bool{}
		for _, starter := range result[index].StarterTasks {
			key := strings.ToLower(strings.TrimSpace(result[index].ID) + ":" + strings.TrimSpace(starter.ID))
			if allowed[key] {
				filtered = append(filtered, starter)
				usedCategories[strings.ToLower(strings.TrimSpace(starter.CategoryID))] = true
			}
		}
		result[index].StarterTasks = filtered
		categories := make([]agentmdl.StarterTaskCategory, 0, len(result[index].StarterTaskCategories))
		for _, category := range result[index].StarterTaskCategories {
			if usedCategories[strings.ToLower(strings.TrimSpace(category.ID))] {
				categories = append(categories, category)
			}
		}
		result[index].StarterTaskCategories = categories
	}
	return result, nil
}

func agentToolDefaults(raw map[string]interface{}) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	toolBlock, _ := raw["tool"].(map[string]interface{})
	for _, bundle := range stringList(toolBlock["bundles"]) {
		add(bundle)
	}
	for _, entry := range objectList(toolBlock["items"]) {
		add(stringValue(entry["pattern"]))
		add(stringValue(entry["name"]))
		if definition, ok := entry["definition"].(map[string]interface{}); ok {
			add(stringValue(definition["name"]))
		}
	}
	return out
}

func stringList(value interface{}) []string {
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(stringValue(item))
		if text != "" {
			result = append(result, text)
		}
	}
	return result
}

func objectList(value interface{}) []map[string]interface{} {
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}
	result := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		if mapped, ok := item.(map[string]interface{}); ok {
			result = append(result, mapped)
		}
	}
	return result
}

func agentInfoIDs(entries []AgentInfo) []string {
	if len(entries) == 0 {
		return nil
	}
	result := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		id := entry.ID
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func hasGoalCapability(entries []AgentInfo) bool {
	for _, entry := range entries {
		for _, toolName := range entry.Tools {
			normalized := strings.TrimSpace(toolName)
			if normalized == "system/goal" || normalized == "system/goal:*" {
				return true
			}
		}
	}
	return false
}

func (h *MetadataHandler) loadModelInfos(ctx context.Context, names []string) []ModelInfo {
	if h == nil || h.store == nil || len(names) == 0 {
		return nil
	}
	names = append([]string(nil), names...)
	sort.Strings(names)
	var result []ModelInfo
	for _, name := range names {
		raw, err := h.store.Load(ctx, ws.KindModel, name)
		if err != nil || len(raw) == 0 {
			result = append(result, ModelInfo{ID: name, Name: name})
			continue
		}
		cfg := &llmprovider.Config{}
		if err := wscodec.DecodeData(name+".yaml", raw, cfg); err != nil {
			result = append(result, ModelInfo{ID: name, Name: name})
			continue
		}
		id := cfg.ID
		if id == "" {
			id = name
		}
		label := cfg.Name
		if label == "" {
			label = id
		}
		result = append(result, ModelInfo{ID: id, Name: label})
	}
	sort.SliceStable(result, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(firstNonEmpty(result[i].Name, result[i].ID)))
		right := strings.ToLower(strings.TrimSpace(firstNonEmpty(result[j].Name, result[j].ID)))
		if left != right {
			return left < right
		}
		return strings.TrimSpace(result[i].ID) < strings.TrimSpace(result[j].ID)
	})
	return result
}

func modelInfoIDs(entries []ModelInfo) []string {
	if len(entries) == 0 {
		return nil
	}
	result := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		id := entry.ID
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := value; trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}
