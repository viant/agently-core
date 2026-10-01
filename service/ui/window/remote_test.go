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
