package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	intent "github.com/viant/agently-core/protocol/intent"
	tpl "github.com/viant/agently-core/protocol/template"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
)

type owningTemplateReader struct{ called bool }

func (r *owningTemplateReader) LoadAll(context.Context) ([]*tpl.Template, error) {
	return nil, errors.New("unscoped catalog forbidden")
}
func (r *owningTemplateReader) Load(context.Context, string) (*tpl.Template, error) {
	return nil, errors.New("unscoped selected read forbidden")
}
func (r *owningTemplateReader) LoadFrom(_ context.Context, key, parent, provider string) (*tpl.Template, error) {
	if key != "same-template" || parent != "intent://team/original" || provider != "owning-provider" {
		return nil, errors.New("foreign owner")
	}
	r.called = true
	return &tpl.Template{ID: "same-template", Name: "Original", Instructions: "Owning template body"}, nil
}
func TestMainWorkerUsesAdmittedOwnerAndBundleAttachments(t *testing.T) {
	reader := &owningTemplateReader{}
	svc := &Service{templateRepo: tplrepo.NewWithReader(reader), registry: &fakeRegistry{defs: []llm.ToolDefinition{{Name: "owner-read"}, {Name: "foreign-read"}}}, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
		return nil, errors.New("filesystem fallback forbidden")
	}}
	own := &intent.Profile{ID: "original", ResourceURI: "intent://team/original", ResourceProviderIdentity: "owning-provider", ResourceFingerprint: "owning-fingerprint", ToolBundles: []string{"same-bundle"}, ToolBundleDefinitions: []*toolbundle.Bundle{{ID: "same-bundle", Match: []llm.Tool{{Name: "owner-read"}}}}}
	ctx := withSelectedPromptProfile(context.Background(), own)
	b := &binding.Binding{}
	require.NoError(t, svc.applySelectedTemplate(ctx, &QueryInput{TemplateId: "same-template"}, b))
	require.True(t, reader.called)
	require.Len(t, b.SystemDocuments.Items, 1)
	require.Contains(t, b.SystemDocuments.Items[0].PageContent, "Owning template body")
	tools, err := svc.resolveTools(ctx, &QueryInput{ToolBundles: []string{"same-bundle"}})
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Equal(t, "owner-read", tools[0].Definition.Name)
	foreign := *own
	foreign.ResourceURI = "intent://other/foreign"
	foreign.ResourceProviderIdentity = "foreign-provider"
	foreign.ResourceFingerprint = "foreign-fingerprint"
	foreign.ToolBundleDefinitions = []*toolbundle.Bundle{{ID: "same-bundle", Match: []llm.Tool{{Name: "foreign-read"}}}}
	tools, err = svc.resolveTools(withSelectedPromptProfile(context.Background(), &foreign), &QueryInput{ToolBundles: []string{"same-bundle"}})
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Equal(t, "foreign-read", tools[0].Definition.Name, "same bundle ID cannot reuse another admitted profile's cached tool surface")
	foreign.ToolBundleDefinitions = nil
	_, err = svc.resolveTools(withSelectedPromptProfile(context.Background(), &foreign), &QueryInput{ToolBundles: []string{"same-bundle"}})
	require.Error(t, err, "missing declared attachment never falls back to host/filesystem names")
	own.ToolBundleDefinitions[0].Match = []llm.Tool{{Name: "not-registered"}}
	own.ResourceFingerprint = "unavailable-tool"
	_, err = svc.resolveTools(ctx, &QueryInput{ToolBundles: []string{"same-bundle"}})
	require.Error(t, err, "authored patterns grant no unregistered tool")
}

func TestAdmittedIntentCanUseAdditionalRuntimeToolfeedBundle(t *testing.T) {
	svc := &Service{
		registry: &fakeRegistry{defs: []llm.ToolDefinition{{Name: "owner-read"}, {Name: "remote-report"}, {Name: "foreign-read"}}},
		toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
			return []*toolbundle.Bundle{
				{ID: "advanced-reporting-tools", Match: []llm.Tool{{Name: "remote-report"}}},
				{ID: "workspace-ui", Match: []llm.Tool{{Name: "foreign-read"}}},
			}, nil
		},
	}
	profile := &intent.Profile{
		ResourceURI:           "intent://workspace/workspace_ui",
		ToolBundles:           []string{"workspace-ui"},
		ToolBundleDefinitions: []*toolbundle.Bundle{{ID: "workspace-ui", Match: []llm.Tool{{Name: "owner-read"}}}},
	}
	ctx := withSelectedPromptProfile(context.Background(), profile)
	tools, err := svc.resolveTools(ctx, &QueryInput{ToolBundles: []string{"workspace-ui", "advanced-reporting-tools"}})
	require.NoError(t, err)
	require.Len(t, tools, 2)
	names := []string{tools[0].Definition.Name, tools[1].Definition.Name}
	require.ElementsMatch(t, []string{"owner-read", "remote-report"}, names)
}
