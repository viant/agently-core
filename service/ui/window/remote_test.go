package window

import (
	"context"
	"strings"
	"testing"

	"github.com/viant/agently-core/workspace"
)

type remoteStubExecutor struct{ calls []string }

func (s *remoteStubExecutor) Execute(_ context.Context, name string, _ map[string]interface{}) (string, error) {
	s.calls = append(s.calls, name)
	switch name {
	case "operations:list_ui_windows":
		return `{"contractVersion":1,"catalogRevision":"v1","groups":[{"id":"management","title":"Management"}],"windows":[{"key":"overview","title":"Overview","groupId":"management"}]}`, nil
	case "operations:get_ui_window":
		return `{"contractVersion":1,"window":{"view":{"content":{"id":"root","type":"container"}}},"dataSources":{"records":{"backend":{"kind":"mcp_tool","service":"operations","method":"list_records"}}}}`, nil
	case "operations:list_records":
		return `{"data":[{"id":1}]}`, nil
	default:
		return "", nil
	}
}

type remoteDefinitionExecutor struct{ definition string }

func (s remoteDefinitionExecutor) Execute(_ context.Context, name string, _ map[string]interface{}) (string, error) {
	if name == "operations:get_ui_window" {
		return s.definition, nil
	}
	return "", nil
}

func configureRemoteTestProvider(t *testing.T, trustLine, definition string) {
	t.Helper()
	previousRoot := workspace.Root()
	workspace.SetRoot(t.TempDir())
	ConfigureRemoteWindowAuthorization(nil)
	ConfigureRemoteWindowProvider(remoteDefinitionExecutor{definition: definition}, []byte(`windowProviders:
  - id: operations-ui
    type: mcp
    serverRef: operations
    catalogTool: list_ui_windows
    windowTool: get_ui_window
`+trustLine))
	t.Cleanup(func() {
		workspace.SetRoot(previousRoot)
		ConfigureRemoteWindowProvider(nil, nil)
		ConfigureRemoteWindowAuthorization(nil)
	})
}

func TestTrustedRemoteWindowPreservesDefinition(t *testing.T) {
	definition := `{"contractVersion":1,"trusted":false,"window":{"view":{"content":{"id":"root","type":"container"}},"actions":{"code":"({ run: () => 1 })"},"actionRefs":["shared-actions"],"dataSource":{"local":{"service":{"endpoint":"agentlyAPI","uri":"/v1/records","method":"POST"}}}},"dataSources":{"records":{"backend":{"kind":"mcp_tool","service":"operations","method":"list_records"}}}}`
	configureRemoteTestProvider(t, "    trusted: true\n", definition)

	window, err := LoadRemoteWindow(context.Background(), "operations-ui", "overview")
	if err != nil {
		t.Fatal(err)
	}
	if window.Actions == nil || window.Actions.Code != "({ run: () => 1 })" {
		t.Fatalf("actions were not preserved: %+v", window.Actions)
	}
	if len(window.ActionRefs) != 1 || window.ActionRefs[0] != "shared-actions" {
		t.Fatalf("action refs were not preserved: %v", window.ActionRefs)
	}
	if source := window.DataSource["local"].Service; source == nil || source.URI != "/v1/records" || source.Method != "POST" {
		t.Fatalf("service-backed datasource was not preserved: %+v", source)
	}
	if source := window.DataSource["records"].Service; source == nil || !strings.Contains(source.URI, "/operations-ui/windows/overview/datasources/records/fetch") {
		t.Fatalf("MCP datasource route was not generated: %+v", source)
	}
}

func TestUntrustedRemoteWindowRejectsExecutableFields(t *testing.T) {
	tests := []struct {
		name       string
		trustLine  string
		definition string
		wantError  string
	}{
		{"omitted trust rejects code", "", `{"contractVersion":1,"trusted":true,"window":{"view":{"content":{"id":"root","type":"container"}},"actions":{"code":"({ run: () => 1 })"}}}`, "executable actions are unsupported"},
		{"explicit false rejects action refs", "    trusted: false\n", `{"contractVersion":1,"window":{"view":{"content":{"id":"root","type":"container"}},"actionRefs":["shared-actions"]}}`, "action references are unsupported"},
		{"omitted trust rejects service datasource", "", `{"contractVersion":1,"window":{"view":{"content":{"id":"root","type":"container"}},"dataSource":{"local":{"service":{"endpoint":"agentlyAPI","uri":"/v1/records","method":"POST"}}}}}`, "must use an inline MCP backend"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configureRemoteTestProvider(t, test.trustLine, test.definition)
			_, err := LoadRemoteWindow(context.Background(), "operations-ui", "overview")
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error=%v, want %q", err, test.wantError)
			}
		})
	}
}

func TestTrustedRemoteWindowStillValidatesMCPDatasource(t *testing.T) {
	definition := `{"contractVersion":1,"window":{"view":{"content":{"id":"root","type":"container"}}},"dataSources":{"records":{"backend":{"kind":"mcp_tool","service":"unapproved","method":"list_records"}}}}`
	configureRemoteTestProvider(t, "    trusted: true\n", definition)
	_, err := LoadRemoteWindow(context.Background(), "operations-ui", "overview")
	if err == nil || !strings.Contains(err.Error(), "unapproved MCP service") {
		t.Fatalf("error=%v, want unapproved MCP service", err)
	}
}

func TestRemoteWindowCatalogAndInlineDatasource(t *testing.T) {
	previousRoot := workspace.Root()
	workspace.SetRoot(t.TempDir())
	ConfigureRemoteWindowAuthorization(nil)
	t.Cleanup(func() {
		workspace.SetRoot(previousRoot)
		ConfigureRemoteWindowProvider(nil, nil)
		ConfigureRemoteWindowAuthorization(nil)
	})
	executor := &remoteStubExecutor{}
	ConfigureRemoteWindowProvider(executor, []byte(`windowProviders:
  - id: operations-ui
    type: mcp
    serverRef: operations
    catalogTool: list_ui_windows
    windowTool: get_ui_window
`))
	catalog, err := ListRemoteWindows(context.Background(), "operations-ui", "operations", "")
	if err != nil || len(catalog.Windows) != 1 || len(catalog.Groups) != 1 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	win, err := LoadRemoteWindow(context.Background(), "operations-ui", "overview")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(win.DataSource["records"].Service.URI, "/operations-ui/windows/overview/datasources/records/fetch") {
		t.Fatalf("unexpected datasource route: %+v", win.DataSource["records"].Service)
	}
	result, err := FetchRemoteDatasource(context.Background(), "operations-ui", "overview", "records", nil)
	if err != nil || len(result.Rows) != 1 {
		t.Fatalf("fetch=%+v err=%v", result, err)
	}
	if len(executor.calls) != 4 {
		t.Fatalf("expected catalog, window, window, data calls; got %v", executor.calls)
	}
}
