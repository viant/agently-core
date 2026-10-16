package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/runtime/toolselection"
)

func TestDiscoveryAndActivationExposeMetadataOnly(t *testing.T) {
	ctx := toolselection.WithState(context.Background(), []llm.Tool{{Definition: llm.ToolDefinition{Name: "arbitrary/server:read", Description: strings.Repeat("description ", 1000), Parameters: map[string]interface{}{"privateSchemaMarker": true}, OutputSchema: map[string]interface{}{"privateOutputMarker": true}}}})
	svc := New()
	list, _ := svc.Method("list")
	activate, _ := svc.Method("activate")
	out := &ListOutput{}
	if err := list(ctx, &ListInput{}, out); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), "Schema") || strings.Contains(string(encoded), "OutputMarker") || len(encoded) > 800 {
		t.Fatalf("unbounded/schema output: %s", encoded)
	}
	if err := activate(ctx, &ActivateInput{Names: []string{"arbitrary/server:read"}}, &ActivateOutput{}); err != nil {
		t.Fatal(err)
	}
	state, _ := toolselection.FromContext(ctx)
	if names := state.SelectedNames(); len(names) != 1 || names[0] != "arbitrary/server:read" {
		t.Fatalf("activation: %v", names)
	}
	if err := activate(ctx, &ActivateInput{Names: []string{"forbidden:read"}}, &ActivateOutput{}); err == nil {
		t.Fatal("unknown name accepted")
	}
	if err := list(context.Background(), &ListInput{}, &ListOutput{}); err == nil {
		t.Fatal("discovery without owned state accepted")
	}
	if err := activate(context.Background(), &ActivateInput{Names: []string{"arbitrary/server:read"}}, &ActivateOutput{}); err == nil {
		t.Fatal("activation without owned state accepted")
	}
}
