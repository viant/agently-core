package meta

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/viant/afs"
	"gopkg.in/yaml.v3"
)

func TestResolveImports_ResolvesNestedTopLevelScalarImports(t *testing.T) {
	root := t.TempDir()
	mustWriteImportFile(t, filepath.Join(root, "main.yaml"), "$import(shared/main.yaml)\n")
	mustWriteImportFile(t, filepath.Join(root, "shared", "main.yaml"), "$import(web/main.yaml)\n")
	mustWriteImportFile(t, filepath.Join(root, "shared", "web", "main.yaml"), "id: order\nwindowKey: order\n")

	data, err := os.ReadFile(filepath.Join(root, "main.yaml"))
	if err != nil {
		t.Fatalf("read root yaml: %v", err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		t.Fatalf("unmarshal root yaml: %v", err)
	}
	if err := ResolveImports(context.Background(), afs.New(), &node, root); err != nil {
		t.Fatalf("resolve imports: %v", err)
	}
	var actual struct {
		ID        string `yaml:"id"`
		WindowKey string `yaml:"windowKey"`
	}
	if err := node.Decode(&actual); err != nil {
		t.Fatalf("decode resolved yaml: %v", err)
	}
	if actual.ID != "order" || actual.WindowKey != "order" {
		t.Fatalf("unexpected resolved content: %#v", actual)
	}
}

func mustWriteImportFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
