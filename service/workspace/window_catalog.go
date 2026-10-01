package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/viant/agently-core/service/policy"
	ws "github.com/viant/agently-core/workspace"
	"gopkg.in/yaml.v3"
)

// ToolDefinition is the existing SDK tool definition projected for the
// built-in Forge Tools window. Its endpoint wraps rows as {data:[...]}.
type ToolDefinition struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	Parameters   map[string]interface{} `json:"parameters,omitempty"`
	Required     []string               `json:"required,omitempty"`
	OutputSchema map[string]interface{} `json:"output_schema,omitempty"`
	Cacheable    bool                   `json:"cacheable,omitempty"`
}

func (h *MetadataHandler) SetToolDefinitionsLoader(load func(context.Context) ([]ToolDefinition, error)) {
	h.toolDefinitions = load
}

func (h *MetadataHandler) authorizeBuiltInWindow(r *http.Request, key string) error {
	runtime := h.authorizationPolicy
	if runtime == nil || !runtime.IsEnabled(policy.OperationWindowView) {
		return nil
	}
	return runtime.Authorize(r.Context(), policy.OperationWindowView, "", policy.Candidate{ID: key, Kind: "window"}, nil)
}

func catalogAuthorizationError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, policy.ErrDenied) {
		http.Error(w, "window not found", http.StatusNotFound)
	} else {
		http.Error(w, "window authorization unavailable", http.StatusServiceUnavailable)
	}
	return true
}

func (h *MetadataHandler) handleWorkspaceTools() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if catalogAuthorizationError(w, h.authorizeBuiltInWindow(r, "tool")) {
			return
		}
		if h.toolDefinitions == nil {
			http.Error(w, "tool catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		definitions, err := h.toolDefinitions(r.Context())
		if err != nil {
			http.Error(w, "tool catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		pattern := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("pattern")))
		filtered := make([]ToolDefinition, 0, len(definitions))
		for _, definition := range definitions {
			if pattern == "" || strings.Contains(strings.ToLower(definition.Name+" "+definition.Description), pattern) {
				filtered = append(filtered, definition)
			}
		}
		sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })
		writeCatalogJSON(w, http.StatusOK, map[string]any{"data": filtered})
	}
}

func (h *MetadataHandler) handleWorkspaceModels() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if catalogAuthorizationError(w, h.authorizeBuiltInWindow(r, "model")) {
			return
		}
		if h.store == nil {
			http.Error(w, "model catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		names, err := h.store.List(r.Context(), ws.KindModel)
		if err != nil {
			http.Error(w, "model catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		sort.Strings(names)
		models := make([]map[string]any, 0, len(names))
		for _, name := range names {
			model, loadErr := h.loadWorkspaceModel(r.Context(), name)
			if loadErr != nil {
				http.Error(w, "model catalog unavailable", http.StatusServiceUnavailable)
				return
			}
			models = append(models, model)
		}
		writeCatalogJSON(w, http.StatusOK, map[string]any{"data": models})
	}
}

func validModelID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for _, char := range id {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func (h *MetadataHandler) loadWorkspaceModel(ctx context.Context, name string) (map[string]any, error) {
	if !validModelID(name) {
		return nil, fmt.Errorf("invalid model id")
	}
	raw, err := h.store.Load(ctx, ws.KindModel, name)
	if err != nil {
		return nil, err
	}
	var model map[string]any
	if err = yaml.Unmarshal(raw, &model); err != nil {
		return nil, err
	}
	if model == nil {
		model = map[string]any{}
	}
	if strings.TrimSpace(fmt.Sprint(model["id"])) == "" || model["id"] == nil {
		model["id"] = name
	}
	return model, nil
}

func (h *MetadataHandler) handleGetWorkspaceModel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if catalogAuthorizationError(w, h.authorizeBuiltInWindow(r, "model")) {
			return
		}
		if h.store == nil {
			http.Error(w, "model catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		model, err := h.loadWorkspaceModel(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, "model not found", http.StatusNotFound)
			return
		}
		writeCatalogJSON(w, http.StatusOK, map[string]any{"data": model})
	}
}

func mergeModelFields(target, update map[string]any) {
	for key, value := range update {
		if nested, ok := value.(map[string]any); ok {
			current, _ := target[key].(map[string]any)
			if current == nil {
				current = map[string]any{}
			}
			mergeModelFields(current, nested)
			target[key] = current
		} else {
			target[key] = value
		}
	}
}

func (h *MetadataHandler) handleSaveWorkspaceModel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if catalogAuthorizationError(w, h.authorizeBuiltInWindow(r, "model")) {
			return
		}
		if h.store == nil {
			http.Error(w, "model catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		id := r.PathValue("id")
		if !validModelID(id) {
			http.Error(w, "invalid model id", http.StatusBadRequest)
			return
		}
		model, err := h.loadWorkspaceModel(r.Context(), id)
		if err != nil {
			http.Error(w, "model not found", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "invalid model body", http.StatusBadRequest)
			return
		}
		var update map[string]any
		if err = json.Unmarshal(body, &update); err != nil || update == nil {
			http.Error(w, "invalid model JSON", http.StatusBadRequest)
			return
		}
		if updateID, ok := update["id"].(string); ok && updateID != id {
			http.Error(w, "model id mismatch", http.StatusBadRequest)
			return
		}
		mergeModelFields(model, update)
		model["id"] = id
		encoded, err := yaml.Marshal(model)
		if err != nil {
			http.Error(w, "invalid model configuration", http.StatusBadRequest)
			return
		}
		if err = h.store.Save(r.Context(), ws.KindModel, id, encoded); err != nil {
			http.Error(w, "save model failed", http.StatusInternalServerError)
			return
		}
		writeCatalogJSON(w, http.StatusOK, map[string]any{"data": model})
	}
}

func writeCatalogJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
