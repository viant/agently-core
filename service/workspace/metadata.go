package workspace

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/viant/agently-core/app/executor/config"
	llmprovider "github.com/viant/agently-core/genai/llm/provider"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	ws "github.com/viant/agently-core/workspace"
	"gopkg.in/yaml.v3"
)

// MetadataResponse is the response for the workspace metadata endpoint.
type MetadataResponse struct {
	WorkspaceRoot   string        `json:"workspaceRoot,omitempty"`
	DefaultAgent    string        `json:"defaultAgent,omitempty"`
	DefaultModel    string        `json:"defaultModel,omitempty"`
	DefaultEmbedder string        `json:"defaultEmbedder,omitempty"`
	Defaults        *Defaults     `json:"defaults,omitempty"`
	Capabilities    Capabilities  `json:"capabilities,omitempty"`
	Agents          []string      `json:"agents,omitempty"`
	Models          []string      `json:"models,omitempty"`
	AgentInfos      []AgentInfo   `json:"agentInfos,omitempty"`
	ModelInfos      []ModelInfo   `json:"modelInfos,omitempty"`
	StarterTasks    []StarterTask `json:"starterTasks,omitempty"`
	Version         string        `json:"version,omitempty"`
}

// Defaults captures UI-facing runtime defaults in a stable nested shape.
type Defaults struct {
	Agent           string `json:"agent,omitempty"`
	Model           string `json:"model,omitempty"`
	Embedder        string `json:"embedder,omitempty"`
	AutoSelectTools bool   `json:"autoSelectTools,omitempty"`
}

// Capabilities advertises optional backend contracts so the UI can avoid
// inventing client-only sentinels and endpoint probes.
type Capabilities struct {
	AgentAutoSelection    bool `json:"agentAutoSelection,omitempty"`
	ModelAutoSelection    bool `json:"modelAutoSelection,omitempty"`
	ToolAutoSelection     bool `json:"toolAutoSelection,omitempty"`
	CompactConversation   bool `json:"compactConversation,omitempty"`
	PruneConversation     bool `json:"pruneConversation,omitempty"`
	AnonymousSession      bool `json:"anonymousSession,omitempty"`
	MessageCursor         bool `json:"messageCursor,omitempty"`
	StructuredElicitation bool `json:"structuredElicitation,omitempty"`
	TurnStartedEvent      bool `json:"turnStartedEvent,omitempty"`
}

// AgentInfo describes a UI-facing agent entry with its preferred model.
type AgentInfo struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	ModelRef string `json:"modelRef,omitempty"`
}

// ModelInfo describes a UI-facing model entry.
type ModelInfo struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// StarterTask describes a suggested starter prompt for empty chat state.
type StarterTask struct {
	ID          string `json:"id,omitempty"`
	Title       string `json:"title,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

// MetadataHandler serves the workspace metadata endpoint.
type MetadataHandler struct {
	defaults     *config.Defaults
	store        ws.Store
	starterTasks []StarterTask
	version      string
}

// NewMetadataHandler creates a metadata handler.
func NewMetadataHandler(defaults *config.Defaults, store ws.Store, version string) *MetadataHandler {
	return &MetadataHandler{
		defaults: defaults,
		store:    store,
		version:  version,
	}
}

// SetStarterTasks configures starter tasks returned by the metadata endpoint.
func (h *MetadataHandler) SetStarterTasks(tasks []StarterTask) *MetadataHandler {
	if h == nil {
		return h
	}
	h.starterTasks = append([]StarterTask(nil), tasks...)
	return h
}

// Register mounts the metadata endpoint.
func (h *MetadataHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/workspace/metadata", h.handleMetadata())
}

func (h *MetadataHandler) handleMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		resp := MetadataResponse{
			WorkspaceRoot: ws.Root(),
			Version:       h.version,
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
			resp.Defaults = &Defaults{
				Agent:           h.defaults.Agent,
				Model:           h.defaults.Model,
				Embedder:        h.defaults.Embedder,
				AutoSelectTools: h.defaults.ToolAutoSelection.Enabled,
			}
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
		if len(h.starterTasks) > 0 {
			resp.StarterTasks = append([]StarterTask(nil), h.starterTasks...)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func (h *MetadataHandler) loadAgentInfos(ctx context.Context, names []string) []AgentInfo {
	if h == nil || h.store == nil || len(names) == 0 {
		return nil
	}
	var result []AgentInfo
	for _, name := range names {
		raw, err := h.store.Load(ctx, ws.KindAgent, name)
		if err != nil || len(raw) == 0 {
			result = append(result, AgentInfo{ID: name, Name: name})
			continue
		}
		cfg := &agentmdl.Agent{}
		rawMap := map[string]interface{}{}
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			result = append(result, AgentInfo{ID: name, Name: name})
			continue
		}
		_ = yaml.Unmarshal(raw, &rawMap)
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
			ID:       id,
			Name:     label,
			ModelRef: firstNonEmpty(cfg.Model, stringValue(rawMap["modelRef"]), stringValue(rawMap["model"])),
		})
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

func (h *MetadataHandler) loadModelInfos(ctx context.Context, names []string) []ModelInfo {
	if h == nil || h.store == nil || len(names) == 0 {
		return nil
	}
	var result []ModelInfo
	for _, name := range names {
		raw, err := h.store.Load(ctx, ws.KindModel, name)
		if err != nil || len(raw) == 0 {
			result = append(result, ModelInfo{ID: name, Name: name})
			continue
		}
		cfg := &llmprovider.Config{}
		if err := yaml.Unmarshal(raw, cfg); err != nil {
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
