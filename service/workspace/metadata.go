package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/viant/agently-core/app/executor/config"
	llmprovider "github.com/viant/agently-core/genai/llm/provider"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	ws "github.com/viant/agently-core/workspace"
	wscodec "github.com/viant/agently-core/workspace/codec"
	wscfg "github.com/viant/agently-core/workspace/config"
)

// MetadataResponse is the response for the workspace metadata endpoint.
type MetadataResponse struct {
	WorkspaceRoot    string       `json:"workspaceRoot,omitempty"`
	WorkspaceVersion string       `json:"workspaceVersion,omitempty"`
	MetadataVersion  string       `json:"metadataVersion,omitempty"`
	DefaultAgent     string       `json:"defaultAgent,omitempty"`
	DefaultModel     string       `json:"defaultModel,omitempty"`
	DefaultEmbedder  string       `json:"defaultEmbedder,omitempty"`
	AppName          string       `json:"appName,omitempty"`
	AppIconRef       string       `json:"appIconRef,omitempty"`
	Defaults         *Defaults    `json:"defaults,omitempty"`
	Capabilities     Capabilities `json:"capabilities,omitempty"`
	Agents           []string     `json:"agents,omitempty"`
	Models           []string     `json:"models,omitempty"`
	AgentInfos       []AgentInfo  `json:"agentInfos,omitempty"`
	ModelInfos       []ModelInfo  `json:"modelInfos,omitempty"`
	Version          string       `json:"version,omitempty"`
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
	defaults          *config.Defaults
	store             ws.Store
	version           string
	reportingOverride *bool
}

// NewMetadataHandler creates a metadata handler.
func NewMetadataHandler(defaults *config.Defaults, store ws.Store, version string) *MetadataHandler {
	return &MetadataHandler{
		defaults: defaults,
		store:    store,
		version:  version,
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
	mux.HandleFunc("GET /v1/workspace/metadata", h.handleMetadata())
	mux.HandleFunc("GET /v1/workspace/metadata/publicagents", h.handlePublicAgents())
}

func (h *MetadataHandler) handlePublicAgents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result := PublicAgentsResponse{AgentInfos: []AgentInfo{}}
		if h.store != nil {
			if agents, err := h.store.List(r.Context(), ws.KindAgent); err == nil {
				for _, agent := range h.loadAgentInfos(r.Context(), agents) {
					if !agent.Internal {
						result.AgentInfos = append(result.AgentInfos, agent)
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(result)
	}
}

func (h *MetadataHandler) handleMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		resp := MetadataResponse{
			WorkspaceRoot:    ws.Root(),
			WorkspaceVersion: resolveWorkspaceVersion(ws.Root()),
			Version:          h.version,
			Capabilities: Capabilities{
				AgentAutoSelection:    true,
				ModelAutoSelection:    false,
				ToolAutoSelection:     h.defaults != nil && h.defaults.ToolAutoSelection.Enabled,
				CompactConversation:   true,
				PruneConversation:     true,
				AnonymousSession:      true,
				MessageCursor:         true,
				StructuredElicitation: true,
				TurnStartedEvent:      true,
			},
		}
		if h.defaults != nil {
			resp.DefaultAgent = h.defaults.Agent
			resp.DefaultModel = h.defaults.Model
			resp.DefaultEmbedder = h.defaults.Embedder
			resp.AppName = h.defaults.AppName
			resp.AppIconRef = h.defaults.AppIconRef
			resp.Defaults = &Defaults{
				AppName:               h.defaults.AppName,
				AppIconRef:            h.defaults.AppIconRef,
				Agent:                 h.defaults.Agent,
				Model:                 h.defaults.Model,
				Embedder:              h.defaults.Embedder,
				AutoSelectTools:       h.defaults.ToolAutoSelection.Enabled,
				ElicitationTimeoutSec: h.defaults.ElicitationTimeoutSec,
			}
			resp.Capabilities.Reporting = h.defaults.Reporting.Enabled
		}
		if h.reportingOverride != nil {
			resp.Capabilities.Reporting = *h.reportingOverride
		}
		if h.store != nil {
			if agents, err := h.store.List(ctx, ws.KindAgent); err == nil {
				resp.AgentInfos = h.loadAgentInfos(ctx, agents)
				resp.Agents = agentInfoIDs(resp.AgentInfos)
			}
			if models, err := h.store.List(ctx, ws.KindModel); err == nil {
				resp.ModelInfos = h.loadModelInfos(ctx, models)
				resp.Models = modelInfoIDs(resp.ModelInfos)
			}
		}
		resp.Capabilities.Goals = goalsCapabilityEnabled(resp.AgentInfos)
		resp.MetadataVersion = resolveMetadataVersion(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
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
