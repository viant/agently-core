package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/intent"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	toolservice "github.com/viant/agently-core/protocol/tool/service/llm/tools"
	"github.com/viant/agently-core/runtime/toolselection"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/intake"
	"gopkg.in/yaml.v3"
)

func TestRetainedToolSchemasRemainImmutableOnColdAndConcurrentReads(t *testing.T) {
	for _, path := range []string{"binding", "execution"} {
		t.Run(path, func(t *testing.T) {
			parameters := map[string]interface{}{"type": "object"}
			output := map[string]interface{}{"type": "object"}
			registry := &fakeRegistry{defs: []llm.ToolDefinition{{Name: "catalog-read", Parameters: parameters, OutputSchema: output}}}
			service := &Service{registry: registry, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
				return []*toolbundle.Bundle{{ID: "base", Match: []llm.Tool{{Name: "catalog:read", Tier: llm.ToolTierCritical}}}}, nil
			}}
			var wg sync.WaitGroup
			errors := make(chan error, 8)
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					input := selectionInput(&intake.ToolSelection{Complete: true})
					var definition *llm.ToolDefinition
					if path == "binding" {
						definitions, err := service.buildToolSignatures(context.Background(), input)
						if err != nil {
							errors <- err
							return
						}
						if len(definitions) != 1 {
							errors <- fmt.Errorf("retained definitions: %d", len(definitions))
							return
						}
						definition = definitions[0]
					} else {
						tools, err := service.resolveTools(context.Background(), input)
						if err != nil {
							errors <- err
							return
						}
						if len(tools) != 1 {
							errors <- fmt.Errorf("retained tools: %d", len(tools))
							return
						}
						definition = &tools[0].Definition
						definition.Normalize()
					}
					definition.Parameters["caller"] = true
					definition.OutputSchema["caller"] = true
				}()
			}
			wg.Wait()
			close(errors)
			for err := range errors {
				require.NoError(t, err)
			}
			require.Equal(t, map[string]interface{}{"type": "object"}, parameters, "input schema must remain owned by its source")
			require.Equal(t, map[string]interface{}{"type": "object"}, output, "output schema must remain owned by its source")
			require.False(t, registry.defs[0].AlwaysInclude)
			require.Empty(t, registry.defs[0].Tier)
		})
	}
}

func tierFixture(t *testing.T) (*Service, context.Context, *QueryInput) {
	s, ctx, in := templateSelectionFixture(t)
	var bundle toolbundle.Bundle
	require.NoError(t, yaml.Unmarshal([]byte(`id: base
match:
 - {name: 'catalog:read', tier: critical}
 - {name: 'catalog:extra', tier: base}
 - {name: 'catalog:write', tier: onDemand}
 - {name: 'template:get', tier: onDemand, alwaysInclude: true}
 - {name: 'template:list', tier: onDemand}
`), &bundle))
	require.NoError(t, bundle.Validate())
	s.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) { return []*toolbundle.Bundle{&bundle}, nil }
	return s, ctx, in
}
func finalTierNames(t *testing.T, s *Service, ctx context.Context, in *QueryInput) []string {
	t.Helper()
	bound, err := s.BuildBinding(ctx, in)
	require.NoError(t, err)
	generate := &core.GenerateInput{Binding: bound, ModelSelection: llm.ModelSelection{Options: &llm.Options{}}}
	require.NoError(t, generate.Init(ctx))
	var names []string
	for _, tool := range generate.Options.Tools {
		names = append(names, tool.Definition.Name)
	}
	return names
}
func TestToolTiersReachFinalModelWithSelectedTemplateAndEmptyIntake(t *testing.T) {
	s, ctx, in := tierFixture(t)
	for _, mode := range []string{"empty", "clarify", "base-selected", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			tc := intake.FromContext(in.Context)
			tc.Routing.Mode = ""
			tc.Prompting.ToolSelection = &intake.ToolSelection{Complete: true}
			in.Query = "hi"
			if mode == "clarify" {
				tc.Routing.Mode = intake.ModeClarify
			}
			if mode == "base-selected" {
				tc.Prompting.ToolSelection.Names = []string{"catalog:extra"}
			}
			if mode == "legacy" {
				tc.Prompting.ToolSelection = nil
			}
			names := finalTierNames(t, s, ctx, in)
			require.Contains(t, names, "catalog-read", "arbitrary critical tool survives intake")
			require.Contains(t, names, "template-get", "flag overrides onDemand and selected-template suppression")
			require.NotContains(t, names, "catalog-write")
			require.NotContains(t, names, "template-list")
			if mode == "base-selected" || mode == "legacy" {
				require.Contains(t, names, "catalog-extra")
			} else {
				require.NotContains(t, names, "catalog-extra")
			}
		})
	}
}
func TestOnDemandToolRequiresExactSelectionOrActivation(t *testing.T) {
	for _, mode := range []string{"selected", "activated"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, in := tierFixture(t)
			in.ToolBundles = []string{"base"}
			tc := intake.FromContext(in.Context)
			tc.Prompting.ToolSelection = &intake.ToolSelection{Complete: true, Names: []string{"catalog:extra"}}
			require.NotContains(t, finalTierNames(t, s, ctx, in), "catalog-write", "selected owning bundle does not force onDemand tools")
			if mode == "selected" {
				tc.Prompting.ToolSelection.Names = []string{"catalog:write"}
			} else {
				state, _ := toolselection.FromContext(ctx)
				require.NoError(t, state.Activate([]string{"catalog-write"}))
			}
			require.Contains(t, finalTierNames(t, s, ctx, in), "catalog-write")
		})
	}
}
func TestRetainedToolRulesRespectHardGuardsAndAgentConfiguration(t *testing.T) {
	s, ctx, in := tierFixture(t)
	in.ToolsAllowed = []string{"catalog:extra"}
	require.Equal(t, []string{"catalog-extra"}, finalTierNames(t, s, ctx, in))
	in.ToolsAllowed = nil
	disabled := withSelectedPromptProfile(ctx, &intent.Profile{Execution: &intent.Execution{DisableTools: true}})
	require.Empty(t, finalTierNames(t, s, disabled, in))
	s, ctx, fixture := discoveryService(t)
	s.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{{ID: "base", Match: []llm.Tool{{Name: "operations:*", Tier: llm.ToolTierCritical, Exclude: []string{"operations:rawSQL"}}}}}, nil
	}
	in = selectionInput(&intake.ToolSelection{Complete: true})
	defs, err := s.buildToolSignatures(ctx, in)
	require.NoError(t, err)
	require.NotContains(t, signatureNames(defs), "operations-rawSQL")
	require.NotContains(t, signatureNames(defs), "operations-appOnly")
	require.Zero(t, fixture.calls)
	s = selectionService()
	in = selectionInput(&intake.ToolSelection{Complete: true})
	in.Agent.Tool.Items = []*llm.Tool{{Name: "catalog:extra", AlwaysInclude: true, Tier: llm.ToolTierOnDemand}}
	defs, err = s.buildToolSignatures(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, []string{"catalog-extra"}, signatureNames(defs))
}
func TestRetainedToolRuleMetadataSurvivesCacheHitsWithoutPollution(t *testing.T) {
	registry := &scopedCacheRegistry{fakeRegistry: fakeRegistry{defs: []llm.ToolDefinition{{Name: "catalog-read"}}}}
	s := &Service{registry: registry, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{{ID: "base", Match: []llm.Tool{{Name: "catalog:read", Tier: llm.ToolTierCritical, AlwaysInclude: true}}}}, nil
	}}
	in := selectionInput(&intake.ToolSelection{Complete: true})
	firstCalls := 0
	for i := 0; i < 2; i++ {
		defs, err := s.buildToolSignatures(context.Background(), in)
		require.NoError(t, err)
		require.Len(t, defs, 1)
		require.True(t, defs[0].AlwaysInclude)
		require.Equal(t, llm.ToolTierCritical, defs[0].Tier)
		if i == 0 {
			firstCalls = registry.calls
			require.Positive(t, firstCalls)
		} else {
			require.Equal(t, firstCalls, registry.calls, "second binding uses existing catalog cache")
		}
	}
	require.False(t, registry.defs[0].AlwaysInclude, "source catalog must stay unmodified")
	require.Empty(t, registry.defs[0].Tier)
}

func TestCriticalDiscoveryControlsWorkOnClarificationAndEmptyTurns(t *testing.T) {
	for _, clarify := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "clarify"}[clarify], func(t *testing.T) {
			s, ctx, _ := discoveryService(t)
			enableFixtureDiscoveryRules(s)
			in := selectionInput(&intake.ToolSelection{Complete: true})
			in.Query = "hi"
			if clarify {
				intake.FromContext(in.Context).Routing.Mode = intake.ModeClarify
			}
			definitions, err := s.buildToolSignatures(ctx, in)
			require.NoError(t, err)
			require.ElementsMatch(t, []string{"llm_tools-list", "llm_tools-activate"}, signatureNames(definitions))
			list, _ := toolservice.New().Method("list")
			listed := &toolservice.ListOutput{}
			require.NoError(t, list(ctx, &toolservice.ListInput{Query: "operations-report"}, listed))
			require.Len(t, listed.Tools, 1)
			activate, _ := toolservice.New().Method("activate")
			require.NoError(t, activate(ctx, &toolservice.ActivateInput{Names: []string{"operations-report"}}, &toolservice.ActivateOutput{}))
			definitions, err = s.buildToolSignatures(ctx, in)
			require.NoError(t, err)
			require.Contains(t, signatureNames(definitions), "operations-report")
			require.NotContains(t, signatureNames(definitions), "operations-otherReport")
		})
	}
}

func TestCallerScopeIsExactNamesUnionExplicitRuntimeBundleOnly(t *testing.T) {
	for _, hint := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "intake"}[hint], func(t *testing.T) {
			s, ctx, in := tierFixture(t)
			s.registry.(*fakeRegistry).defs = []llm.ToolDefinition{{Name: "system/exec:execute"}, {Name: "system/os:getEnv"}, {Name: "system/os:remove"}, {Name: "unrelated:critical"}}
			s.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) {
				return []*toolbundle.Bundle{
					{ID: "os", Match: []llm.Tool{{Name: "system/os:*", Exclude: []string{"system/os:remove"}}}},
					{ID: "global", Match: []llm.Tool{{Name: "unrelated:critical", Tier: llm.ToolTierCritical, AlwaysInclude: true}}},
				}, nil
			}
			in.ToolsAllowed = []string{"system/exec:execute"}
			in.ToolBundles = []string{"os"}
			in.Agent.Tool.Bundles = nil
			in.Agent.Tool.Items = []*llm.Tool{{Name: "*"}}
			in.TemplateId = ""
			if hint {
				intake.FromContext(in.Context).Prompting.ToolSelection = &intake.ToolSelection{Complete: true}
			} else {
				intake.FromContext(in.Context).Prompting.ToolSelection = nil
			}
			require.ElementsMatch(t, []string{"system_exec-execute", "system_os-getEnv"}, finalTierNames(t, s, ctx, in))
			tools, err := s.resolveTools(ctx, in)
			require.NoError(t, err)
			var names []string
			for _, tool := range tools {
				names = append(names, tool.Definition.Name)
			}
			require.ElementsMatch(t, []string{"system_exec-execute", "system_os-getEnv"}, names)
			state, _ := toolselection.FromContext(ctx)
			require.Error(t, state.Activate([]string{"unrelated-critical"}))
			require.Error(t, state.Activate([]string{"system_os-remove"}))
		})
	}
}

func TestExactCallerAndProfileChoicesSelectOnDemandWithoutIntake(t *testing.T) {
	for _, source := range []string{"caller", "profile", "profile-wildcard"} {
		t.Run(source, func(t *testing.T) {
			s, ctx, in := tierFixture(t)
			intake.FromContext(in.Context).Prompting.ToolSelection = nil
			// No owning bundle or profile message injection may be needed for
			// an authoritative exact preferred operation.
			if source == "profile" {
				ctx = context.Background()
				in.Agent.Tool.Bundles = nil
				in.Agent.Tool.Items = nil
				inject := false
				in.Agent.Prompts.InjectSelectedProfile = &inject
			}
			if source == "caller" {
				in.ToolsAllowed = []string{"catalog:write"}
			} else {
				preferred := "catalog:write"
				if source == "profile-wildcard" {
					preferred = "catalog:*"
				}
				ctx = withSelectedPromptProfile(ctx, &intent.Profile{PreferredTools: []string{preferred}})
			}
			names := finalTierNames(t, s, ctx, in)
			if source == "profile-wildcard" {
				require.NotContains(t, names, "catalog-write")
			} else {
				require.Contains(t, names, "catalog-write")
			}
		})
	}
}
