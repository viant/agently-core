package workspace

import (
	"encoding/json"
	"net/http"

	"github.com/viant/agently-core/app/executor/config"
	ws "github.com/viant/agently-core/workspace"
)

// MetadataResponse is the response for the workspace metadata endpoint.
type MetadataResponse struct {
	DefaultAgent    string   `json:"defaultAgent,omitempty"`
	DefaultModel    string   `json:"defaultModel,omitempty"`
	DefaultEmbedder string   `json:"defaultEmbedder,omitempty"`
	Agents          []string `json:"agents,omitempty"`
	Models          []string `json:"models,omitempty"`
	Version         string   `json:"version,omitempty"`
}

// MetadataHandler serves the workspace metadata endpoint.
type MetadataHandler struct {
	defaults *config.Defaults
	store    ws.Store
	version  string
}

// NewMetadataHandler creates a metadata handler.
func NewMetadataHandler(defaults *config.Defaults, store ws.Store, version string) *MetadataHandler {
	return &MetadataHandler{
		defaults: defaults,
		store:    store,
		version:  version,
	}
}

// Register mounts the metadata endpoint.
func (h *MetadataHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/workspace/metadata", h.handleMetadata())
}

func (h *MetadataHandler) handleMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		resp := MetadataResponse{
			Version: h.version,
		}
		if h.defaults != nil {
			resp.DefaultAgent = h.defaults.Agent
			resp.DefaultModel = h.defaults.Model
			resp.DefaultEmbedder = h.defaults.Embedder
		}
		if h.store != nil {
			if agents, err := h.store.List(ctx, ws.KindAgent); err == nil {
				resp.Agents = agents
			}
			if models, err := h.store.List(ctx, ws.KindModel); err == nil {
				resp.Models = models
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}
