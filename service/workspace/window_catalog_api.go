package workspace

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/viant/agently-core/service/policy"
	ws "github.com/viant/agently-core/workspace"
	"gopkg.in/yaml.v3"
)

type ToolCatalogResponse struct {
	Data []ToolDefinition `json:"data"`
}
type ModelCatalogResponse struct {
	Data []map[string]any `json:"data"`
}
type ModelResponse struct {
	Data map[string]any `json:"data"`
}

func (h *MetadataHandler) authorizeCatalog(ctx context.Context, key string) error {
	runtime := h.authorizationPolicy
	if runtime == nil || !runtime.IsEnabled(policy.OperationWindowView) {
		return nil
	}
	if err := runtime.Authorize(ctx, policy.OperationWindowView, "", policy.Candidate{ID: key, Kind: "window"}, nil); err != nil {
		if errors.Is(err, policy.ErrDenied) {
			return metadataFailure(http.StatusNotFound, "window not found", err)
		}
		return metadataFailure(http.StatusServiceUnavailable, "window authorization unavailable", err)
	}
	return nil
}
func (h *MetadataHandler) Tools(ctx context.Context, pattern string) (*ToolCatalogResponse, error) {
	if err := h.authorizeCatalog(ctx, "tool"); err != nil {
		return nil, err
	}
	if h.toolDefinitions == nil {
		return nil, metadataFailure(http.StatusServiceUnavailable, "tool catalog unavailable", nil)
	}
	definitions, err := h.toolDefinitions(ctx)
	if err != nil {
		return nil, metadataFailure(http.StatusServiceUnavailable, "tool catalog unavailable", err)
	}
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	filtered := make([]ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if pattern == "" || strings.Contains(strings.ToLower(definition.Name+" "+definition.Description), pattern) {
			filtered = append(filtered, definition)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })
	return &ToolCatalogResponse{Data: filtered}, nil
}
func (h *MetadataHandler) Models(ctx context.Context) (*ModelCatalogResponse, error) {
	if err := h.authorizeCatalog(ctx, "model"); err != nil {
		return nil, err
	}
	if h.store == nil {
		return nil, metadataFailure(http.StatusServiceUnavailable, "model catalog unavailable", nil)
	}
	names, err := h.store.List(ctx, ws.KindModel)
	if err != nil {
		return nil, metadataFailure(http.StatusServiceUnavailable, "model catalog unavailable", err)
	}
	sort.Strings(names)
	models := make([]map[string]any, 0, len(names))
	for _, name := range names {
		model, err := h.loadWorkspaceModel(ctx, name)
		if err != nil {
			return nil, metadataFailure(http.StatusServiceUnavailable, "model catalog unavailable", err)
		}
		models = append(models, model)
	}
	return &ModelCatalogResponse{Data: models}, nil
}
func (h *MetadataHandler) Model(ctx context.Context, id string) (*ModelResponse, error) {
	if err := h.authorizeCatalog(ctx, "model"); err != nil {
		return nil, err
	}
	if h.store == nil {
		return nil, metadataFailure(http.StatusServiceUnavailable, "model catalog unavailable", nil)
	}
	model, err := h.loadWorkspaceModel(ctx, id)
	if err != nil {
		return nil, metadataFailure(http.StatusNotFound, "model not found", err)
	}
	return &ModelResponse{Data: model}, nil
}
func (h *MetadataHandler) SaveModel(ctx context.Context, id string, update map[string]any) (*ModelResponse, error) {
	if err := h.authorizeCatalog(ctx, "model"); err != nil {
		return nil, err
	}
	if h.store == nil {
		return nil, metadataFailure(http.StatusServiceUnavailable, "model catalog unavailable", nil)
	}
	if !validModelID(id) {
		return nil, metadataFailure(http.StatusBadRequest, "invalid model id", nil)
	}
	model, err := h.loadWorkspaceModel(ctx, id)
	if err != nil {
		return nil, metadataFailure(http.StatusNotFound, "model not found", err)
	}
	if update == nil {
		return nil, metadataFailure(http.StatusBadRequest, "invalid model JSON", nil)
	}
	if updateID, ok := update["id"].(string); ok && updateID != id {
		return nil, metadataFailure(http.StatusBadRequest, "model id mismatch", nil)
	}
	mergeModelFields(model, update)
	model["id"] = id
	encoded, err := yaml.Marshal(model)
	if err != nil {
		return nil, metadataFailure(http.StatusBadRequest, "invalid model configuration", err)
	}
	if err = h.store.Save(ctx, ws.KindModel, id, encoded); err != nil {
		return nil, metadataFailure(http.StatusInternalServerError, "save model failed", err)
	}
	return &ModelResponse{Data: model}, nil
}
