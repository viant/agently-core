package browsermcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/viant/agently-core/protocol/agui"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
)

func TestBrowserMCPCatalogOwnerThreadSchemaAndGeneration(t *testing.T) {
	descriptor := cfg.BrowserDescriptor{Name: "device", ExecutionLocation: "browser", Transport: cfg.BrowserTransport{Type: "chrome-extension", ExtensionID: strings.Repeat("a", 32), PortName: "generic-v1"}, AllowedTools: []string{"safe_*"}}
	registry := New(func(context.Context) ([]cfg.BrowserDescriptor, error) {
		return []cfg.BrowserDescriptor{descriptor}, nil
	})
	input := Registration{ConversationID: "owned", ThreadID: "wire-thread", Server: "device", ConnectionID: "connection1", Tools: []Tool{{Name: "safe_read", Description: "Fixture tool", InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)}}}
	catalog, err := registry.Register(context.Background(), "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Tools[0].Name != "device-safe_read" {
		t.Fatal("canonical server namespace changed")
	}
	if defs, err := registry.Validate(context.Background(), "owner", "wire-thread", catalog.Tools); err != nil || len(defs) != 1 {
		t.Fatal(err)
	}
	for _, scope := range [][2]string{{"other", "wire-thread"}, {"owner", "other-thread"}} {
		if _, err = registry.Validate(context.Background(), scope[0], scope[1], catalog.Tools); err == nil {
			t.Fatal("cross-owner/thread catalog admitted")
		}
	}
	for _, change := range []func(*agui.Tool){func(v *agui.Tool) { v.Parameters = json.RawMessage(`{"type":"object","additionalProperties":true}`) }, func(v *agui.Tool) { v.Name = "system_exec-run" }, func(v *agui.Tool) { v.Metadata = nil }, func(v *agui.Tool) { v.Description = "changed" }} {
		tool := catalog.Tools[0]
		change(&tool)
		if _, err = registry.Validate(context.Background(), "owner", "wire-thread", []agui.Tool{tool}); err == nil {
			t.Fatal("catalog substitution admitted")
		}
	}
	input.ConnectionID = "connection2"
	next, err := registry.Register(context.Background(), "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.Validate(context.Background(), "owner", "wire-thread", catalog.Tools); err == nil {
		t.Fatal("old connection retained authority")
	}
	descriptor.Transport.ExtensionID = strings.Repeat("b", 32)
	if _, err = registry.Current(context.Background(), "owner", next.ID); err == nil {
		t.Fatal("changed transport retained catalog")
	}
	input.Tools[0].Name = "forbidden"
	if _, err = registry.Register(context.Background(), "owner", input); err == nil {
		t.Fatal("name outside configured policy admitted")
	}
}
func TestBrowserMCPResultMetadataCannotSubstituteConnection(t *testing.T) {
	expected := json.RawMessage(`{"browserMCP":{"catalogId":"one","connectionId":"originating","server":"device","tool":"read"}}`)
	if VerifyResultMetadata("device-read", expected, expected) != nil {
		t.Fatal("exact metadata rejected")
	}
	if VerifyResultMetadata("device-read", expected, json.RawMessage(`{"browserMCP":{"catalogId":"one","connectionId":"other"}}`)) == nil {
		t.Fatal("substituted result connection accepted")
	}
}
