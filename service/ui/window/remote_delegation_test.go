package window

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type delegationExecutor struct {
	definition string
	name       string
	args       map[string]interface{}
}

func (e *delegationExecutor) Execute(_ context.Context, name string, args map[string]interface{}) (string, error) {
	if name == "operations:get_ui_window" {
		return e.definition, nil
	}
	e.name, e.args = name, args
	return `{"data":[{"id":1}]}`, nil
}
func TestRemoteProviderDelegationBindsIdentityOutsideInputs(t *testing.T) {
	definition := `{"contractVersion":1,"definitionRevision":"r1","window":{"view":{"content":{"id":"root","type":"container"}}},"dataSources":{"records":{"backend":{"kind":"provider","method":"forgeDatasourceFetch","pinned":{"windowKey":"forged"}}}}}`
	configureRemoteTestProvider(t, "", definition)
	e := &delegationExecutor{definition: definition}
	_, layout := remoteSettings()
	ConfigureRemoteWindowProvider(e, layout)
	if _, err := LoadRemoteWindow(context.Background(), "operations-ui", "overview"); err != nil {
		t.Fatal(err)
	}
	result, err := FetchRemoteDatasource(context.Background(), "operations-ui", "overview", "records", map[string]interface{}{"windowKey": "other", "definitionRevision": "fake"})
	if err != nil || len(result.Rows) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if e.name != "operations:forgeDatasourceFetch" || e.args["windowKey"] != "overview" || e.args["definitionRevision"] != "r1" || e.args["dataSourceId"] != "records" {
		t.Fatalf("delegation not bound: %s %+v", e.name, e.args)
	}
	if e.args["inputs"].(map[string]interface{})["windowKey"] != "other" {
		t.Fatal("inputs not nested")
	}
}
func TestRemoteServerAliasIsHostOwned(t *testing.T) {
	definition := `{"contractVersion":1,"window":{"view":{"content":{"id":"root","type":"container"}}},"dataSources":{"records":{"backend":{"kind":"mcp_tool","service":"portable-source","method":"read"}}}}`
	configureRemoteTestProvider(t, "    serverBindings:\n      portable-source: local-platform\n", definition)
	e := &delegationExecutor{definition: definition}
	_, layout := remoteSettings()
	ConfigureRemoteWindowProvider(e, layout)
	if _, err := FetchRemoteDatasource(context.Background(), "operations-ui", "overview", "records", nil); err != nil {
		t.Fatal(err)
	}
	if e.name != "local-platform:read" {
		t.Fatalf("alias was not mapped: %s", e.name)
	}
}
func TestRemoteDelegationRejectsUnboundContracts(t *testing.T) {
	for _, backend := range []string{`{"kind":"provider","service":"other"}`, `{"kind":"provider","method":"arbitrary"}`} {
		t.Run(backend, func(t *testing.T) {
			definition := `{"contractVersion":1,"definitionRevision":"r1","window":{"view":{"content":{"id":"root","type":"container"}}},"dataSources":{"records":{"backend":` + backend + `}}}`
			configureRemoteTestProvider(t, "", definition)
			if _, err := LoadRemoteWindow(context.Background(), "operations-ui", "overview"); err == nil {
				t.Fatal("invalid delegation allowed")
			}
		})
	}
	definition := `{"contractVersion":1,"window":{"view":{"content":{"id":"root","type":"container"}}},"dataSources":{"records":{"backend":{"kind":"provider"}}}}`
	configureRemoteTestProvider(t, "", definition)
	if _, err := LoadRemoteWindow(context.Background(), "operations-ui", "overview"); err == nil || !strings.Contains(err.Error(), "binding") {
		t.Fatalf("missing revision accepted: %v", err)
	}
}
func TestRemoteProviderConfigurationRoundTrip(t *testing.T) {
	p := RemoteProvider{DatasourceTool: "fetch", ServerBindings: map[string]string{"source": "local"}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var out RemoteProvider
	if err = json.Unmarshal(b, &out); err != nil || out.ServerBindings["source"] != "local" {
		t.Fatalf("round trip: %s %v", b, err)
	}
}

func TestRemoteDatasourceRejectsRenderedRevisionDrift(t *testing.T) {
	definition := `{"contractVersion":1,"definitionRevision":"r2","window":{"view":{"content":{"id":"root","type":"container"}}},"dataSources":{"records":{"backend":{"kind":"provider"}}}}`
	configureRemoteTestProvider(t, "", definition)
	e := &delegationExecutor{definition: definition}
	_, layout := remoteSettings()
	ConfigureRemoteWindowProvider(e, layout)
	_, err := FetchRemoteDatasourceAtRevision(context.Background(), "operations-ui", "overview", "records", "r1", nil)
	if err == nil || e.name != "" {
		t.Fatalf("changed definition dispatched: %s %v", e.name, err)
	}
}

func TestRemoteDatasourceRejectsPrototypeAssignmentPaths(t *testing.T) {
	for _, path := range []string{"__proto__.admin", "request.constructor.prototype.admin", "request[\"__proto__\"].admin"} {
		for _, field := range []string{"parameter", "mcpRequest"} {
			t.Run(field+path, func(t *testing.T) {
				backend := map[string]any{"kind": "provider"}
				source := map[string]any{"backend": backend}
				if field == "parameter" {
					source["parameters"] = []any{map[string]any{"name": path}}
				} else {
					backend["mcpRequest"] = map[string]any{"queryPath": path}
				}
				raw, _ := json.Marshal(map[string]any{"contractVersion": 1, "definitionRevision": "v1", "window": map[string]any{"view": map[string]any{"content": map[string]any{"id": "root", "type": "container"}}}, "dataSources": map[string]any{"records": source}})
				configureRemoteTestProvider(t, "", string(raw))
				if _, err := LoadRemoteWindow(context.Background(), "operations-ui", "overview"); err == nil {
					t.Fatal("prototype path admitted")
				}
			})
		}
	}
}
