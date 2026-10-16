package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/viant/agently-core/workspace"
	wsfs "github.com/viant/agently-core/workspace/store/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalWorkspaceSDKCannotReadOrMutateMigratedYAML(t *testing.T) {
	root := t.TempDir()
	store := wsfs.New(root)
	ctx := context.Background()
	for kind, name := range map[string]string{"extension/forge/windows": "denied-window", "extension/forge/reporting": "denied-report", "intents": "denied-intent", "skills": "denied-skill", "agents": "allowed-agent", "models": "openai:gpt"} {
		if err := store.Save(ctx, kind, name, []byte("private definition")); err != nil {
			t.Fatal(err)
		}
	}
	client := &backendClient{store: store, rawResourceBoundary: workspace.NewRawResourceBoundary(root)}
	for _, kind := range []string{"window", "windows", "extension/forge/windows", "report", "reports", "extension/forge/reporting", "intent", "intents", "skill", "skills"} {
		if _, err := client.ListResources(ctx, &ListResourcesInput{Kind: kind}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
			t.Fatalf("list %s bypass: %v", kind, err)
		}
		ref := &ResourceRef{Kind: kind, Name: "denied-window"}
		if _, err := client.GetResource(ctx, ref); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
			t.Fatalf("get %s bypass: %v", kind, err)
		}
		if err := client.SaveResource(ctx, &SaveResourceInput{Kind: kind, Name: ref.Name, Data: []byte("forged")}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
			t.Fatalf("save %s bypass: %v", kind, err)
		}
		if err := client.DeleteResource(ctx, ref); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
			t.Fatalf("delete %s bypass: %v", kind, err)
		}
		if _, err := client.ExportResources(ctx, &ExportResourcesInput{Kinds: []string{kind}}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
			t.Fatalf("export %s bypass: %v", kind, err)
		}
	}
	mixed := &ImportResourcesInput{Replace: true, Resources: []Resource{{Kind: "agents", Name: "new-agent", Data: []byte("allowed")}, {Kind: "extension/forge/windows", Name: "denied-window", Data: []byte("forged")}}}
	if _, err := client.ImportResources(ctx, mixed); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
		t.Fatal("import bypass")
	}
	if exists, _ := store.Exists(ctx, "agents", "new-agent"); exists {
		t.Fatal("mixed denied import mutated before preflight")
	}
	if data, _ := store.Load(ctx, "extension/forge/windows", "denied-window"); string(data) != "private definition" {
		t.Fatal("denied draft was overwritten")
	}
	if _, err := client.GetResource(ctx, &ResourceRef{Kind: "agents", Name: "../extension/forge/windows/denied-window"}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
		t.Fatal("traversal bypass")
	}
	if err := os.Symlink(filepath.Join(root, "extension/forge/windows/denied-window.yaml"), filepath.Join(root, "agents", "alias.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetResource(ctx, &ResourceRef{Kind: "agents", Name: "alias"}); !errors.Is(err, workspace.ErrCanonicalResourceAccess) {
		t.Fatal("symlink bypass")
	}
	exported, err := client.ExportResources(ctx, &ExportResourcesInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range exported.Resources {
		if r.Kind != "agents" && r.Kind != "models" {
			t.Fatalf("default export leaked %s", r.Kind)
		}
	}
	if _, err := client.GetResource(ctx, &ResourceRef{Kind: "models", Name: "openai:gpt"}); err != nil {
		t.Fatalf("unrelated model blocked: %v", err)
	}
}
func TestCanonicalWorkspaceResourceHTTPRejectsRawRoutes(t *testing.T) {
	root := t.TempDir()
	client := &backendClient{store: wsfs.New(root), rawResourceBoundary: workspace.NewRawResourceBoundary(root)}
	for _, test := range []struct{ method, path, body string }{{"GET", "/v1/workspace/resources/window/denied-window", ""}, {"PUT", "/v1/workspace/resources/window/denied-window", "forged"}, {"DELETE", "/v1/workspace/resources/report/denied-report", ""}, {"GET", "/v1/workspace/resources?kind=extension%2Fforge%2Fwindows", ""}, {"POST", "/v1/workspace/resources/export", `{"kinds":["extension/forge/reporting"]}`}, {"POST", "/v1/workspace/resources/import", `{"resources":[{"kind":"windows","name":"denied-window","data":"Zm9yZ2Vk"}]}`}} {
		req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		res := httptest.NewRecorder()
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/workspace/resources/{kind}/{name}", handleGetResource(client))
		mux.HandleFunc("PUT /v1/workspace/resources/{kind}/{name}", handleSaveResource(client))
		mux.HandleFunc("DELETE /v1/workspace/resources/{kind}/{name}", handleDeleteResource(client))
		mux.HandleFunc("GET /v1/workspace/resources", handleListResources(client))
		mux.HandleFunc("POST /v1/workspace/resources/export", handleExportResources(client))
		mux.HandleFunc("POST /v1/workspace/resources/import", handleImportResources(client))
		mux.ServeHTTP(res, req)
		if res.Code != 403 {
			t.Fatalf("%s %s status=%d body=%s", test.method, test.path, res.Code, res.Body.String())
		}
	}
	body, _ := json.Marshal(ImportResourcesInput{Resources: []Resource{{Kind: "agents", Name: "allowed", Data: []byte("ok")}}})
	res := httptest.NewRecorder()
	handleImportResources(client)(res, httptest.NewRequest("POST", "/", strings.NewReader(string(body))))
	if res.Code != 200 {
		t.Fatal("unrelated config import blocked")
	}
}
