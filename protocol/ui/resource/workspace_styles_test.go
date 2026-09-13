package resource

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/agently-core/service/ui/style"
)

func TestWorkspaceResourceRevisionIncludesCSS(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	for name, contents := range map[string]string{
		"extension/forge/windows/order.yaml":   "namespace: order\nview:\n  content:\n    id: order\n    items:\n      - id: customer\n        type: text\n        label: Customer\n",
		"extension/forge/styles/manifest.yaml": "version: 1\nfiles: [custom.css]\n",
		"extension/forge/styles/custom.css":    ".agently-workspace {color:red}",
	} {
		target := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	first, err := ReadWorkspaceViewResource(ctx, WorkspaceViewURI("order"), "order")
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	beforeRevision := style.Workspace().Current(ctx).Styles.Revision
	if !strings.Contains(string(before), beforeRevision) {
		t.Fatal("resource lacks CSS revision")
	}
	if err := os.WriteFile(filepath.Join(root, "extension/forge/styles/custom.css"), []byte(".agently-workspace {color:blue}"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := ReadWorkspaceViewResource(ctx, WorkspaceViewURI("order"), "order")
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	afterRevision := style.Workspace().Current(ctx).Styles.Revision
	if afterRevision == beforeRevision || string(before) == string(after) || !strings.Contains(string(after), afterRevision) {
		t.Fatal("CSS-only edit did not invalidate MCP resource content")
	}
}
