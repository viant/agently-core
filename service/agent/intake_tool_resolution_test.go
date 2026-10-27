package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/intent"
	"github.com/viant/agently-core/protocol/mcpname"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/service/intake"
)

type selectedSchemaRegistry struct {
	fakeRegistry
	broken   error
	patterns []string
}

func (r *selectedSchemaRegistry) MatchDefinitionWithContextResult(_ context.Context, pattern string) ([]*llm.ToolDefinition, error) {
	r.patterns = append(r.patterns, pattern)
	if strings.Contains(pattern, "remote") {
		return nil, r.broken
	}
	definitions := r.MatchDefinition(pattern)
	for _, definition := range definitions {
		if mcpname.Canonical(definition.Name) == "local-run" {
			definition.Parameters = map[string]any{"version": "fresh"}
		}
	}
	return definitions, nil
}
func selectedSchemaService() (*Service, *selectedSchemaRegistry) {
	registry := &selectedSchemaRegistry{fakeRegistry: fakeRegistry{defs: []llm.ToolDefinition{{Name: "local:run", Parameters: map[string]any{"version": "advisory"}}, {Name: "local:keep"}, {Name: "remote:query"}}}, broken: errors.New("synthetic remote discovery failure")}
	service := &Service{registry: registry, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{
			{ID: "base", Match: []llm.Tool{{Name: "local:*"}, {Name: "remote:*"}, {Name: "local:keep", AlwaysInclude: true}}},
			{ID: "required", Match: []llm.Tool{{Name: "remote:*"}}},
		}, nil
	}}
	return service, registry
}
func TestCompleteIntakeResolvesSelectedSchemasBeforeAutomaticWildcards(t *testing.T) {
	for _, execution := range []bool{false, true} {
		t.Run(map[bool]string{false: "binding", true: "execution"}[execution], func(t *testing.T) {
			service, registry := selectedSchemaService()
			input := selectionInput(&intake.ToolSelection{Names: []string{"local:run"}, Complete: true})
			var definitions []llm.ToolDefinition
			if execution {
				tools, err := service.resolveTools(context.Background(), input)
				require.NoError(t, err)
				definitions = toolsToDefs(tools)
			} else {
				pointers, err := service.buildToolSignatures(context.Background(), input)
				require.NoError(t, err)
				for _, definition := range pointers {
					definitions = append(definitions, *definition)
				}
			}
			require.Len(t, definitions, 2)
			for _, definition := range definitions {
				if definition.Name == "local-run" {
					require.Equal(t, "fresh", definition.Parameters["version"])
				}
			}
			for _, pattern := range registry.patterns {
				require.NotContains(t, pattern, "remote", "automatic broad rules must use the prepared catalog")
			}
			candidates, err := service.discoverableToolDefinitions(context.Background(), input)
			require.NoError(t, err)
			require.Len(t, candidates, 3, "selection does not narrow discovery availability")
		})
	}
}
func TestSelectedAndExplicitRequiredDiscoveryFailuresStillPropagate(t *testing.T) {
	for _, mode := range []string{"selected", "required"} {
		t.Run(mode, func(t *testing.T) {
			service, registry := selectedSchemaService()
			input := selectionInput(&intake.ToolSelection{Names: []string{"local:run"}, Complete: true})
			if mode == "selected" {
				input.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection.Names = []string{"remote:query"}
			}
			if mode == "required" {
				input.ToolBundles = []string{"required"}
			}
			if mode == "incomplete" {
				input.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection.Complete = false
			}
			_, err := service.buildToolSignatures(context.Background(), input)
			require.ErrorIs(t, err, registry.broken)
		})
	}
}

func TestCompleteIntakeIgnoresUnavailableAutomaticPatternWithRequiredHealthyBundle(t *testing.T) {
	service, registry := selectedSchemaService()
	registry.defs = registry.defs[:2] // The failed backend advertised no candidate schemas.
	original := service.toolBundles
	service.toolBundles = func(ctx context.Context) ([]*toolbundle.Bundle, error) {
		bundles, err := original(ctx)
		return append(bundles, &toolbundle.Bundle{ID: "healthy", Match: []llm.Tool{{Name: "local:run"}}}), err
	}
	input := selectionInput(&intake.ToolSelection{Names: []string{"local:run"}, Complete: true})
	input.ToolBundles = []string{"healthy"}
	input.Agent.Tool.Items = []*llm.Tool{{Name: "remote:*"}}
	definitions, err := service.buildToolSignatures(context.Background(), input)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"local-run", "local-keep"}, signatureNames(definitions))
	for _, pattern := range registry.patterns {
		require.NotContains(t, pattern, "remote")
	}
}

func TestAutomaticSchemaDiscoveryUsesHealthyCatalogWithoutCompleteIntake(t *testing.T) {
	for _, mode := range []string{"absent", "incomplete", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			service, registry := selectedSchemaService()
			registry.defs = registry.defs[:2]
			var selection *intake.ToolSelection
			if mode == "incomplete" {
				selection = &intake.ToolSelection{Names: []string{"local:run"}}
			}
			if mode == "invalid" {
				selection = &intake.ToolSelection{Names: []string{"missing:tool"}, Complete: true}
			}
			input := selectionInput(selection)
			input.Agent.Tool.Items = []*llm.Tool{{Name: "remote:*"}}
			pointers, err := service.buildToolSignatures(context.Background(), input)
			require.NoError(t, err)
			require.ElementsMatch(t, []string{"local-run", "local-keep"}, signatureNames(pointers))
			tools, err := service.resolveTools(context.Background(), input)
			require.NoError(t, err)
			require.Len(t, tools, 2)
			for _, pattern := range registry.patterns {
				require.NotContains(t, pattern, "remote")
			}
		})
	}
}

func TestAbsentIntakeStillRequiresExplicitBundleAndProfileSchemas(t *testing.T) {
	for _, mode := range []string{"bundle", "profile"} {
		t.Run(mode, func(t *testing.T) {
			service, registry := selectedSchemaService()
			input := selectionInput(nil)
			ctx := context.Background()
			if mode == "bundle" {
				input.ToolBundles = []string{"required"}
			} else {
				ctx = withSelectedPromptProfile(ctx, &intent.Profile{ID: "required-profile", PreferredTools: []string{"remote:query"}})
			}
			_, err := service.buildToolSignatures(ctx, input)
			require.ErrorIs(t, err, registry.broken)
			_, err = service.resolveTools(ctx, input)
			require.ErrorIs(t, err, registry.broken)
		})
	}
}
