package agent

import (
	"context"
	"errors"
	"gopkg.in/yaml.v3"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	intent "github.com/viant/agently-core/protocol/intent"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/mcpname"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/protocol/tool"
	bundle "github.com/viant/agently-core/protocol/tool/bundle"
	control "github.com/viant/agently-core/protocol/tool/service/ui/control"
	metadata "github.com/viant/agently-core/protocol/tool/service/ui/metadata"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	bridge "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/forge/backend/types"
)

type metadataSelectionReader struct {
	gets, lists int
	deny        bool
}

func (*metadataSelectionReader) UsesWindowResourceResolution() bool      { return true }
func (*metadataSelectionReader) MetadataScope() bridge.MetadataReadScope { return nil }
func (r *metadataSelectionReader) WindowDefinitionsList(context.Context, *bridge.WindowDefinitionListInput) (*bridge.WindowDefinitionListOutput, error) {
	r.lists++
	if r.deny {
		return nil, identity.ErrResourceDenied
	}
	return &bridge.WindowDefinitionListOutput{Windows: []bridge.WindowDefinitionSummary{{WindowID: "entry", ResourceURI: "window://team/entry"}}}, nil
}
func (r *metadataSelectionReader) WindowDefinitionGet(context.Context, *bridge.WindowDefinitionGetInput) (*bridge.WindowDefinitionGetOutput, error) {
	r.gets++
	if r.deny {
		return nil, identity.ErrResourceDenied
	}
	return &bridge.WindowDefinitionGetOutput{Definition: &types.Window{WindowKey: "entry", Resource: &identity.ResolvedResource{URI: "window://team/entry"}, View: types.View{Content: &types.Container{ID: "properties", Items: []types.Item{{ID: "name", Label: "Name"}}}}}}, nil
}

func TestMetadataInternalMCPDiscoveryAndModelToolSelectionRespectAuthoredScope(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	reg, err := tool.NewDefaultRegistry(mgr)
	require.NoError(t, err)
	reader := &metadataSelectionReader{}
	require.NoError(t, tool.AddInternalService(reg, metadata.New(reader)))
	// This resolves the actual in-process MCP client's discovered definitions,
	// rather than manufacturing llm.ToolDefinition fixtures or checking Methods.
	discovered := reg.MatchDefinition("ui/metadata:*")
	require.Len(t, discovered, 2)
	policy := &bundle.Bundle{ID: "workspace-metadata", Match: []llm.Tool{{Name: "ui/metadata:list"}, {Name: "ui/metadata:get"}}}
	// An operator can validate the exact configured host bundle without this
	// neutral Core test depending on any product checkout or fixed local path.
	if filename := os.Getenv("AGENTLY_UI_METADATA_TEST_BUNDLE"); filename != "" {
		bytes, readErr := os.ReadFile(filename)
		require.NoError(t, readErr)
		require.NoError(t, yaml.Unmarshal(bytes, policy))
		require.NoError(t, policy.Validate())
		t.Logf("Using actual host policy bundle %s", filename)
	}
	s := &Service{registry: reg, toolBundles: func(context.Context) ([]*bundle.Bundle, error) {
		return []*bundle.Bundle{policy}, nil
	}}
	ctx := requestctx.WithConversationID(context.Background(), "owned-current-conversation")
	selected, err := s.resolveTools(ctx, &QueryInput{ToolBundles: []string{policy.ID}})
	require.NoError(t, err)
	metadataCount := 0
	for _, item := range selected {
		if strings.Contains(item.Definition.Name, "metadata") {
			metadataCount++
			require.Contains(t, item.Definition.Description, "Metadata only")
		}
	}
	require.Equal(t, 2, metadataCount, "configured model tool surface must advertise both metadata operations")
	require.Equal(t, 0, reader.gets)
	require.Equal(t, 0, reader.lists, "tool discovery must not execute metadata or business readers")
	// Disabling the UI bundle does not silently inject registered metadata tools.
	absent, err := s.resolveTools(ctx, &QueryInput{})
	require.NoError(t, err)
	require.Empty(t, absent)
	disabledCtx := withSelectedPromptProfile(ctx, &intent.Profile{ID: "disabled", Execution: &intent.Execution{DisableTools: true}})
	disabled, err := s.resolveTools(disabledCtx, &QueryInput{ToolBundles: []string{policy.ID}})
	require.NoError(t, err)
	require.Empty(t, disabled)
	// Call through the registry's real internal MCP service adapter. A selected
	// metadata definition is useful without a browser or OPEN, but its resolver's
	// current authority still gates every execution.
	output, err := reg.Execute(ctx, "ui/metadata:get", map[string]any{"id": "entry", "nodeId": "properties"})
	require.NoError(t, err)
	require.Contains(t, output, "Name")
	require.Equal(t, 1, reader.gets)
	reader.deny = true
	_, err = reg.Execute(ctx, "ui/metadata:get", map[string]any{"id": "entry"})
	require.Error(t, err)
	// A configured host action guard remains effective before dispatch.
	before := reader.gets
	require.True(t, tool.SetAuthorizationGuard(reg, func(context.Context, string, map[string]any) error { return errors.New("host denied UI metadata") }))
	_, err = reg.Execute(ctx, "ui/metadata:get", map[string]any{"id": "entry"})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "denied"))
	require.Equal(t, before, reader.gets)
}

func TestControlReadInternalMCPDiscoveryAndModelSelectionRespectExplicitUIPolicy(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	reg, err := tool.NewDefaultRegistry(mgr)
	require.NoError(t, err)
	require.NoError(t, tool.AddInternalService(reg, control.New(nil)))
	for _, name := range []string{"ui/control:get", "ui/control:list", "ui/control:focus"} {
		definitions := reg.MatchDefinition(name)
		require.Len(t, definitions, 1, "public Read/Focus method must be wired before policy exposes it")
	}
	policy := &bundle.Bundle{ID: "workspace-control-read", Match: []llm.Tool{{Name: "ui/control:get"}, {Name: "ui/control:list"}, {Name: "ui/control:focus"}}}
	if filename := os.Getenv("AGENTLY_UI_METADATA_TEST_BUNDLE"); filename != "" {
		bytes, readErr := os.ReadFile(filename)
		require.NoError(t, readErr)
		require.NoError(t, yaml.Unmarshal(bytes, policy))
		require.NoError(t, policy.Validate())
		t.Logf("Using actual host policy bundle %s", filename)
	}
	s := &Service{registry: reg, toolBundles: func(context.Context) ([]*bundle.Bundle, error) { return []*bundle.Bundle{policy}, nil }}
	ctx := requestctx.WithConversationID(context.Background(), "owned-control-read-conversation")
	selected, err := s.resolveTools(ctx, &QueryInput{ToolBundles: []string{policy.ID}})
	require.NoError(t, err)
	names := map[string]bool{}
	for _, item := range selected {
		names[mcpname.Canonical(item.Definition.Name)] = true
	}
	for _, name := range []string{"ui/control:get", "ui/control:list", "ui/control:focus"} {
		require.True(t, names[mcpname.Canonical(name)], "enabled host bundle must advertise %s to the model", name)
	}
	absent, err := s.resolveTools(ctx, &QueryInput{})
	require.NoError(t, err)
	require.Empty(t, absent, "registered Read methods must not bypass an absent UI bundle")
	disabledCtx := withSelectedPromptProfile(ctx, &intent.Profile{ID: "disabled-control-read", Execution: &intent.Execution{DisableTools: true}})
	disabled, err := s.resolveTools(disabledCtx, &QueryInput{ToolBundles: []string{policy.ID}})
	require.NoError(t, err)
	require.Empty(t, disabled)
	// Advertised Read/Focus still requires a live authorized admitted window. Discovery
	// cannot turn a missing bridge/current-window context into a successful read.
	for _, name := range []string{"ui/control:get", "ui/control:list", "ui/control:focus"} {
		_, err = reg.Execute(ctx, name, map[string]any{"windowId": "unadmitted", "controlId": "name"})
		require.Error(t, err)
	}
	require.True(t, tool.SetAuthorizationGuard(reg, func(context.Context, string, map[string]any) error {
		return errors.New("host denied current control Read/Focus")
	}))
	for _, name := range []string{"ui/control:get", "ui/control:list", "ui/control:focus"} {
		_, err = reg.Execute(ctx, name, map[string]any{"windowId": "unadmitted", "controlId": "name"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "host denied current control Read/Focus")
	}
}
