package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	toolservice "github.com/viant/agently-core/protocol/tool/service/llm/tools"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/toolselection"
	intake "github.com/viant/agently-core/service/intake"
	intakerepo "github.com/viant/agently-core/workspace/repository/intake"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func selectionInput(selection *intake.ToolSelection) *QueryInput {
	return &QueryInput{Agent: &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"base"}}}, Context: map[string]any{intake.ContextKey: &intake.Context{Prompting: intake.PromptingContext{ToolSelection: selection}}}}
}
func selectionService() *Service {
	return &Service{registry: &fakeRegistry{defs: []llm.ToolDefinition{
		{Name: "catalog:read", Parameters: map[string]any{"original": "schema"}}, {Name: "catalog:write"}, {Name: "catalog:extra"},
		{Name: "llm/tools:list"}, {Name: "llm/tools:activate"},
	}}, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{
			{ID: "base", Match: []llm.Tool{{Name: "catalog:*"}}},
			{ID: "workflow", Match: []llm.Tool{{Name: "catalog:write"}}},
		}, nil
	}}
}
func signatureNames(defs []*llm.ToolDefinition) []string {
	out := []string{}
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

func TestIntakeSelectionNarrowsSignaturesWithoutChangingConfiguredBundles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selection *intake.ToolSelection
		want      int
	}{
		{"absent", nil, 3}, {"incomplete", &intake.ToolSelection{Names: []string{"catalog:read"}}, 3},
		{"selected", &intake.ToolSelection{Names: []string{"catalog:read"}, Complete: true}, 1},
		{"empty", &intake.ToolSelection{Names: []string{}, Complete: true}, 0},
		{"unknown", &intake.ToolSelection{Names: []string{"foreign:tool"}, Complete: true}, 3},
		{"wildcard", &intake.ToolSelection{Names: []string{"catalog:*"}, Complete: true}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := selectionService()
			in := selectionInput(tc.selection)
			defs, err := s.buildToolSignatures(context.Background(), in)
			require.NoError(t, err)
			require.Len(t, defs, tc.want)
			tools, err := s.resolveTools(context.Background(), in)
			require.NoError(t, err)
			require.Len(t, tools, tc.want)
			require.Equal(t, []string{"base"}, in.Agent.Tool.Bundles)
			if tc.name == "selected" {
				require.Equal(t, "schema", defs[0].Parameters["original"])
			}
		})
	}
}

func TestIntakeSelectionRetainsProfileAndTurnRequiredTools(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "prompts"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "prompts", "chosen.yaml"), []byte("id: chosen\ntoolBundles: [workflow]\n"), 0644))
	s := selectionService()
	s.promptRepo = intakerepo.NewWithStore(fsstore.New(root))
	for _, profile := range []bool{true, false} {
		in := selectionInput(&intake.ToolSelection{Names: []string{"catalog:read"}, Complete: true})
		if profile {
			in.PromptProfileId = "chosen"
			inject := false
			in.Agent.Prompts.InjectSelectedProfile = &inject
		} else {
			in.ToolBundles = []string{"workflow"}
		}
		defs, err := s.buildToolSignatures(context.Background(), in)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{mcpname.Canonical("catalog:read"), mcpname.Canonical("catalog:write")}, signatureNames(defs))
	}
}

func TestIntakeSelectionAdditionalToolActivationReachesNextBinding(t *testing.T) {
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "conversation", TurnID: "turn"})
	ctx = toolselection.WithState(ctx, nil)
	s := selectionService()
	enableFixtureDiscoveryRules(s)
	in := selectionInput(&intake.ToolSelection{Names: []string{"catalog:read"}, Complete: true})
	defs, err := s.buildToolSignatures(ctx, in)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{mcpname.Canonical("catalog:read"), mcpname.Canonical("llm/tools:list"), mcpname.Canonical("llm/tools:activate")}, signatureNames(defs))
	activate, err := toolservice.New().Method("activate")
	require.NoError(t, err)
	require.NoError(t, activate(ctx, &toolservice.ActivateInput{Names: []string{mcpname.Canonical("catalog:extra")}}, &toolservice.ActivateOutput{}))
	defs, err = s.buildToolSignatures(ctx, in)
	require.NoError(t, err)
	require.Contains(t, signatureNames(defs), mcpname.Canonical("catalog:extra"))
	require.NotContains(t, signatureNames(defs), mcpname.Canonical("catalog:write"))
	require.Error(t, activate(ctx, &toolservice.ActivateInput{Names: []string{"foreign:tool"}}, &toolservice.ActivateOutput{}))
	next := requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "conversation", TurnID: "next"})
	require.Error(t, activate(next, &toolservice.ActivateInput{Names: []string{mcpname.Canonical("catalog:write")}}, &toolservice.ActivateOutput{}))
}

func TestIntakeClarificationHasNoBusinessSignatures(t *testing.T) {
	in := selectionInput(nil)
	intake.FromContext(in.Context).Routing.Mode = intake.ModeClarify
	defs, err := selectionService().buildToolSignatures(context.Background(), in)
	require.NoError(t, err)
	require.Empty(t, defs)
}

func TestIntakeCompleteEmptySelectionDoesNotAddDiscoveryTools(t *testing.T) {
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "chat", TurnID: "greeting"})
	ctx = toolselection.WithState(ctx, nil)
	s := selectionService()
	bundleReads := 0
	s.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) {
		bundleReads++
		return []*toolbundle.Bundle{{ID: "base", Match: []llm.Tool{{Name: "catalog:*"}}}}, nil
	}
	defs, err := s.buildToolSignatures(ctx, selectionInput(&intake.ToolSelection{Names: []string{}, Complete: true}))
	require.NoError(t, err)
	require.Empty(t, defs)
	require.Equal(t, 1, bundleReads, "only trusted rule metadata is needed on an empty turn")
}

func TestIntakeSelectionKeepsBundleApproval(t *testing.T) {
	s := selectionService()
	s.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{{ID: "base", Match: []llm.Tool{
			{Name: "catalog:read"}, {Name: "catalog:write", Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue}},
		}}}, nil
	}
	ctx := approvalqueue.WithState(context.Background())
	defs, err := s.buildToolSignatures(ctx, selectionInput(&intake.ToolSelection{Names: []string{"catalog:write"}, Complete: true}))
	require.NoError(t, err)
	require.Equal(t, []string{mcpname.Canonical("catalog:write")}, signatureNames(defs))
	require.True(t, approvalqueue.RequiresQueue(ctx, "catalog:write"))
}

func TestIntakeSelectionPreservesResourceInputTools(t *testing.T) {
	for _, names := range [][]string{{}, {"catalog:read"}} {
		s := selectionService()
		reg := s.registry.(*fakeRegistry)
		resources := []string{"resources:inspect", "resources:read", "resources:readImage", "resources:export", "resources:list"}
		for _, name := range resources {
			reg.defs = append(reg.defs, llm.ToolDefinition{Name: name})
		}
		in := selectionInput(&intake.ToolSelection{Names: names, Complete: true})
		in.ResourceURIs = []string{"upload://owned/workbook"}
		defs, err := s.buildToolSignatures(context.Background(), in)
		require.NoError(t, err)
		for _, name := range resources {
			require.Contains(t, signatureNames(defs), mcpname.Canonical(name))
		}
		require.NotContains(t, signatureNames(defs), mcpname.Canonical("catalog:write"))
		require.Len(t, defs, len(resources)+len(names))
	}
}

func TestIntakeUncertainSelectionFallsBackToChosenWorkflow(t *testing.T) {
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "chat", TurnID: "workflow"})
	ctx = toolselection.WithState(ctx, nil)
	s := selectionService()
	enableFixtureDiscoveryRules(s)
	in := selectionInput(&intake.ToolSelection{Names: []string{}, Complete: false})
	in.ToolBundles = []string{"workflow"}
	defs, err := s.buildToolSignatures(ctx, in)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{mcpname.Canonical("catalog:write"), mcpname.Canonical("llm/tools:list"), mcpname.Canonical("llm/tools:activate")}, signatureNames(defs))
	state, ok := toolselection.FromContext(ctx)
	require.True(t, ok)
	require.NoError(t, state.Activate([]string{mcpname.Canonical("catalog:read")}))
	defs, err = s.buildToolSignatures(ctx, in)
	require.NoError(t, err)
	require.Contains(t, signatureNames(defs), mcpname.Canonical("catalog:read"))
	require.NotContains(t, signatureNames(defs), mcpname.Canonical("catalog:extra"))
}

func TestIntakeUncertainWithoutWorkflowRetainsDiscoveryEscalation(t *testing.T) {
	s, ctx, _ := discoveryService(t)
	enableFixtureDiscoveryRules(s)
	in := selectionInput(&intake.ToolSelection{Complete: false})
	// The agent baseline does not include the independently discoverable reports.
	require.Empty(t, in.ToolBundles)
	defs, err := s.buildToolSignatures(ctx, in)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"operations-configure", "llm_tools-list", "llm_tools-activate"}, signatureNames(defs))
	tools, err := s.resolveTools(ctx, in)
	require.NoError(t, err)
	require.Len(t, tools, 3)
	list, _ := toolservice.New().Method("list")
	listed := &toolservice.ListOutput{}
	require.NoError(t, list(ctx, &toolservice.ListInput{Query: "operations-report"}, listed))
	require.Len(t, listed.Tools, 1)
	activate, _ := toolservice.New().Method("activate")
	require.NoError(t, activate(ctx, &toolservice.ActivateInput{Names: []string{"operations-report"}}, &toolservice.ActivateOutput{}))
	defs, err = s.buildToolSignatures(ctx, in)
	require.NoError(t, err)
	require.Contains(t, signatureNames(defs), "operations-report")
	require.NotContains(t, signatureNames(defs), "operations-otherReport")
}

func enableFixtureDiscoveryRules(s *Service) {
	previous := s.toolBundles
	s.toolBundles = func(ctx context.Context) ([]*toolbundle.Bundle, error) {
		bundles, err := previous(ctx)
		if err != nil {
			return nil, err
		}
		return append(bundles, &toolbundle.Bundle{ID: "discovery", Match: []llm.Tool{{Name: "llm/tools:list", AlwaysInclude: true}, {Name: "llm/tools:activate", AlwaysInclude: true}}}), nil
	}
}
