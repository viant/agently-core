package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ws "github.com/viant/agently-core/workspace"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func TestBuiltInWindowCatalogDatasourceRoutes(t *testing.T) {
	store := fsstore.New(t.TempDir())
	if err := store.Save(context.Background(), ws.KindModel, "sample", []byte("id: sample\nname: Sample\noptions:\n  provider: openai\n  model: example\n")); err != nil {
		t.Fatal(err)
	}
	h := NewMetadataHandler(nil, store, "test")
	h.SetToolDefinitionsLoader(func(context.Context) ([]ToolDefinition, error) {
		return []ToolDefinition{{Name: "system:status", Description: "Current status"}}, nil
	})
	mux := http.NewServeMux()
	h.Register(mux)

	for _, route := range []string{"/v1/workspace/tool?pattern=status", "/v1/workspace/models", "/v1/workspace/models/sample"} {
		writer := httptest.NewRecorder()
		mux.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, route, nil))
		if writer.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", route, writer.Code, writer.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(writer.Body.Bytes(), &response); err != nil || response["data"] == nil {
			t.Fatalf("%s: response=%v err=%v", route, response, err)
		}
	}

	writer := httptest.NewRecorder()
	mux.ServeHTTP(writer, httptest.NewRequest(http.MethodPut, "/v1/workspace/models/sample", strings.NewReader(`{"id":"sample","name":"Updated"}`)))
	if writer.Code != http.StatusOK {
		t.Fatalf("PUT model: status=%d body=%s", writer.Code, writer.Body.String())
	}
	updated, err := h.loadWorkspaceModel(context.Background(), "sample")
	if err != nil || updated["name"] != "Updated" {
		t.Fatalf("saved model=%v err=%v", updated, err)
	}
	options, _ := updated["options"].(map[string]any)
	if options["provider"] != "openai" {
		t.Fatalf("save lost existing options: %v", options)
	}
}
