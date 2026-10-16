package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	toolservice "github.com/viant/agently-core/protocol/tool/service/llm/tools"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/toolselection"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/intake"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func templateSelectionFixture(t *testing.T) (*Service, context.Context, *QueryInput) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "templates"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "templates", "analytics_dashboard.yaml"), []byte("id: analytics_dashboard\nname: analytics_dashboard\ninstructions: Render the dashboard.\n"), 0644))
	store := convmem.New()
	seedConversation(t, store, "conversation")
	msg := apiconv.NewMessage()
	msg.SetId("question")
	msg.SetConversationID("conversation")
	msg.SetTurnID("turn")
	msg.SetRole("user")
	msg.SetType("text")
	msg.SetContent("render dashboard")
	require.NoError(t, store.PatchMessage(context.Background(), msg))
	s := selectionService()
	s.defaults = &config.Defaults{}
	s.conversation = store
	s.templateRepo = tplrepo.NewWithStore(fsstore.New(root))
	s.registry.(*fakeRegistry).defs = append(s.registry.(*fakeRegistry).defs, llm.ToolDefinition{Name: "template:get", Parameters: map[string]interface{}{"name": map[string]interface{}{"type": "string"}}}, llm.ToolDefinition{Name: "template:list"})
	in := selectionInput(&intake.ToolSelection{Names: []string{"catalog:read"}, Complete: true})
	in.ConversationID = "conversation"
	in.MessageID = "turn"
	in.TemplateId = "analytics_dashboard"
	in.Agent.Tool.Items = []*llm.Tool{{Name: "template:*", Tier: llm.ToolTierOnDemand}}
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "conversation", TurnID: "turn"})
	ctx = toolselection.WithState(ctx, nil)
	return s, ctx, in
}

func TestExplicitTemplateActivationReachesFinalModelSurface(t *testing.T) {
	s, ctx, in := templateSelectionFixture(t)
	before, err := s.BuildBinding(ctx, in)
	require.NoError(t, err)
	require.NotContains(t, signatureNames(before.Tools.Signatures), "template-get")
	activate, _ := toolservice.New().Method("activate")
	require.NoError(t, activate(ctx, &toolservice.ActivateInput{Names: []string{"template-get"}}, &toolservice.ActivateOutput{}))
	after, err := s.BuildBinding(ctx, in)
	require.NoError(t, err)
	generate := &core.GenerateInput{Binding: after, ModelSelection: llm.ModelSelection{Options: &llm.Options{}}}
	require.NoError(t, generate.Init(ctx))
	names := []string{}
	for _, tool := range generate.Options.Tools {
		names = append(names, tool.Definition.Name)
	}
	require.Contains(t, names, "template-get", "successful explicit activation exposes the on-demand template")
	require.NotContains(t, names, "template-list", "unselected on-demand template helpers remain omitted")
	require.Equal(t, "analytics_dashboard", in.TemplateId)
	var injected bool
	for _, doc := range after.SystemDocuments.Items {
		if doc.SourceURI == "template://analytics_dashboard" {
			injected = true
		}
	}
	require.True(t, injected, "explicit activation must preserve normal selected-template injection")
	// Revocation still wins over a prior activation: the exception retains
	// existing visible definitions; it cannot reintroduce a removed candidate.
	reg := s.registry.(*fakeRegistry)
	var remaining []llm.ToolDefinition
	for _, def := range reg.defs {
		if def.Name != "template:get" {
			remaining = append(remaining, def)
		}
	}
	reg.defs = remaining
	revoked, err := s.BuildBinding(ctx, in)
	require.NoError(t, err)
	require.NotContains(t, signatureNames(revoked.Tools.Signatures), "template-get")
	state, _ := toolselection.FromContext(ctx)
	require.NotContains(t, state.SelectedNames(), "template-get")
}

func TestIntakeSelectedTemplateReachesFinalModelSurface(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selection *intake.ToolSelection
		want      bool
	}{
		{"complete", &intake.ToolSelection{Names: []string{"template-get"}, Complete: true}, true},
		{"incomplete", &intake.ToolSelection{Names: []string{"template-get"}}, false},
		{"invalidBatch", &intake.ToolSelection{Names: []string{"template-get", "foreign-tool"}, Complete: true}, false},
		{"wildcard", &intake.ToolSelection{Names: []string{"template-*"}, Complete: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx, in := templateSelectionFixture(t)
			in.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection = tc.selection
			bound, err := s.BuildBinding(ctx, in)
			require.NoError(t, err)
			generate := &core.GenerateInput{Binding: bound, ModelSelection: llm.ModelSelection{Options: &llm.Options{}}}
			require.NoError(t, generate.Init(ctx))
			var names []string
			for _, tool := range generate.Options.Tools {
				names = append(names, tool.Definition.Name)
			}
			if tc.want {
				require.Contains(t, names, "template-get")
			} else {
				require.NotContains(t, names, "template-get")
			}
			require.NotContains(t, names, "template-list")
		})
	}
}
