package tablepreference

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceAdapterConfiguration(t *testing.T) {
	root := t.TempDir()
	c, err := LoadConfig(root)
	if err != nil || c.TablePreferences.Adapter != "browser" {
		t.Fatalf("default: %#v %v", c, err)
	}
	path := filepath.Join(root, ConfigPath)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	valid := `version: 1
tablePreferences:
  adapter: mcp
  mcp:
    serverRef: personal-preferences
    tools: {get: table_get, set: table_set, reset: table_reset}
`
	if err = os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = LoadConfig(root)
	if err != nil || c.TablePreferences.MCP.Tools.Reset != "table_reset" {
		t.Fatalf("external: %#v %v", c, err)
	}
	for _, invalid := range []string{
		"tablePreferences: {adapter: mcp}\n",
		"tablePreferences: {adapter: remote}\n",
		"tablePreferences: {adapter: browser, credentials: secret}\n",
		valid + "---\ntablePreferences: {adapter: browser}\n",
	} {
		if err = os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = LoadConfig(root); err == nil {
			t.Fatal("invalid explicit config silently accepted")
		}
	}
}
