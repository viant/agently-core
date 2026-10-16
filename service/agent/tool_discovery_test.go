package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	registry "github.com/viant/agently-core/internal/tool/registry"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/intent"
	"github.com/viant/agently-core/protocol/mcp/manager"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	svc "github.com/viant/agently-core/protocol/tool/service"
	toolservice "github.com/viant/agently-core/protocol/tool/service/llm/tools"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/toolselection"
	"github.com/viant/agently-core/service/intake"
	"github.com/viant/agently-core/service/shared/toolexec"
	bundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	uimeta "github.com/viant/mcp-ui/meta"
)

type discoveryFixture struct{ calls int }

func (*discoveryFixture) Name() string { return "operations" }
func (*discoveryFixture) Methods() svc.Signatures {
	var result svc.Signatures
	for _, name := range []string{"configure", "report", "otherReport", "appOnly", "rawSQL", "unbundled"} {
		result = append(result, svc.Signature{Name: name, Description: "Metadata for " + name, Input: reflect.TypeOf(&struct{}{}), Output: reflect.TypeOf(&struct{}{})})
	}
	return result
}
func (s *discoveryFixture) Method(string) (svc.Executable, error) {
	return func(context.Context, interface{}, interface{}) error { s.calls++; return nil }, nil
}
func (*discoveryFixture) MCPUIToolUI(method string) (uimeta.ToolUI, bool) {
	if method == "appOnly" {
		return uimeta.ToolUI{Visibility: []string{"app"}, ResourceUri: "ui://fixture/window"}, true
	}
	return uimeta.ToolUI{}, false
}

func discoveryService(t *testing.T) (*Service, context.Context, *discoveryFixture) {
	t.Helper()
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	fixture := &discoveryFixture{}
	require.NoError(t, reg.AddInternalService(fixture))
	require.NoError(t, reg.AddInternalService(toolservice.New()))
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{TurnID: "turn", ConversationID: "conversation"})
	ctx = toolselection.WithState(toolapprovalqueue.WithState(ctx), nil)
	s := &Service{registry: reg, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{
			{ID: "base", Match: []llm.Tool{{Name: "operations:configure"}}},
			{ID: "reports", Match: []llm.Tool{{Name: "operations:report", Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue}}, {Name: "operations:otherReport"}}},
		}, nil
	}}
	return s, ctx, fixture
}

func TestUnselectedBundleToolDiscoveryAndExactPromotionWithoutProfile(t *testing.T) {
	for _, mode := range []string{"intake", "activation", "emptyBase"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, fixture := discoveryService(t)
			in := selectionInput(nil)
			summary, err := s.intakeCapabilitySummary(ctx, in)
			require.NoError(t, err)
			require.Contains(t, summary, "operations-report")
			require.NotContains(t, summary, "operations-appOnly")
			require.NotContains(t, summary, "parameters")
			in.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection = &intake.ToolSelection{Names: []string{"operations:report"}, Complete: true}
			if mode == "emptyBase" {
				in.Agent.Tool = agentmdl.Tool{}
			}
			if mode == "activation" {
				in.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection.Names = []string{"operations:configure"}
				_, err = s.buildToolSignatures(ctx, in)
				require.NoError(t, err)
				list, _ := toolservice.New().Method("list")
				out := &toolservice.ListOutput{}
				require.NoError(t, list(ctx, &toolservice.ListInput{Query: "operations-report"}, out))
				require.Len(t, out.Tools, 1)
				activate, _ := toolservice.New().Method("activate")
				require.NoError(t, activate(ctx, &toolservice.ActivateInput{Names: []string{"operations-report"}}, &toolservice.ActivateOutput{}))
			}
			defs, err := s.buildToolSignatures(ctx, in)
			require.NoError(t, err)
			require.Contains(t, signatureNames(defs), "operations-report")
			require.NotContains(t, signatureNames(defs), "operations-otherReport")
			require.NotContains(t, signatureNames(defs), "operations-appOnly")
			require.True(t, toolapprovalqueue.RequiresQueue(ctx, "operations-report"), "unselected bundle approval must accompany exact promoted schema")
			store := convmem.New()
			seedConversation(t, store, "conversation")
			_, _, err = toolexec.ExecuteToolStep(ctx, s.registry, toolexec.StepInfo{ID: "report-call", Name: "operations-report", Args: map[string]interface{}{}}, store)
			require.NoError(t, err)
			require.Zero(t, fixture.calls, "discovery/activation must not invoke provider; promoted execution still queues")
			rows, err := store.ListToolApprovalQueues(ctx, &toolapprovalqueuemodel.QueueRowsInput{ConversationId: "conversation", Has: &toolapprovalqueuemodel.QueueRowsInputHas{ConversationId: true}})
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, "pending", rows[0].Status)
			require.Empty(t, in.EffectiveIntentProfileID())
		})
	}
}

func TestDiscoveryPreservesAllowlistAndBaselineFallback(t *testing.T) {
	s, ctx, _ := discoveryService(t)
	in := selectionInput(nil)
	defs, err := s.buildToolSignatures(ctx, in)
	require.NoError(t, err)
	require.Equal(t, []string{"operations-configure"}, signatureNames(defs))
	in.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection = &intake.ToolSelection{Names: []string{"foreign:tool"}, Complete: true}
	defs, err = s.buildToolSignatures(ctx, in)
	require.NoError(t, err)
	require.Equal(t, []string{"operations-configure"}, signatureNames(defs))
	in.ToolsAllowed = []string{"operations:configure"}
	in.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection = &intake.ToolSelection{Names: []string{"operations:configure"}, Complete: true}
	summary, err := s.intakeCapabilitySummary(ctx, in)
	require.NoError(t, err)
	require.NotContains(t, summary, "operations-report")
	defs, err = s.buildToolSignatures(ctx, in)
	require.NoError(t, err)
	require.Equal(t, []string{"operations-configure"}, signatureNames(defs))
	state, _ := toolselection.FromContext(ctx)
	require.Error(t, state.Activate([]string{"operations-report"}))
	require.Error(t, state.Activate([]string{"operations-appOnly"}))
}

func TestDiscoveryHonorsProfileDisableTools(t *testing.T) {
	s, ctx, _ := discoveryService(t)
	ctx = withSelectedPromptProfile(ctx, &intent.Profile{Execution: &intent.Execution{DisableTools: true}})
	in := selectionInput(&intake.ToolSelection{Names: []string{"operations:report"}, Complete: true})
	summary, err := s.intakeCapabilitySummary(ctx, in)
	require.NoError(t, err)
	require.Empty(t, summary)
	defs, err := s.buildToolSignatures(ctx, in)
	require.NoError(t, err)
	require.Empty(t, defs)
	state, _ := toolselection.FromContext(ctx)
	require.Error(t, state.Activate([]string{"operations-report"}))
}

func TestDiscoveryUsesTrustedRuleUnionAndPreservesExclusions(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "excluded", true: "another-trusted-rule-allows"}[allow], func(t *testing.T) {
			s, ctx, fixture := discoveryService(t)
			s.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) {
				bundles := []*toolbundle.Bundle{
					{ID: "base", Match: []llm.Tool{{Name: "operations:configure"}}},
					{ID: "reports", Match: []llm.Tool{{Name: "operations:*", Exclude: []string{"operations:configure", "operations:rawSQL", "operations:unbundled"}}}},
				}
				if allow {
					bundles = append(bundles, &toolbundle.Bundle{ID: "approved-query", Match: []llm.Tool{{Name: "operations:rawSQL", Approval: &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue}}}})
				}
				return bundles, nil
			}
			in := selectionInput(&intake.ToolSelection{Names: []string{"operations:report"}, Complete: true})
			summary, err := s.intakeCapabilitySummary(ctx, in)
			require.NoError(t, err)
			require.Contains(t, summary, "operations-report", "unselected workflow remains discoverable")
			require.NotContains(t, summary, "operations-unbundled")
			if allow {
				require.Contains(t, summary, "operations-rawSQL")
			} else {
				require.NotContains(t, summary, "operations-rawSQL")
			}
			defs, err := s.buildToolSignatures(ctx, in)
			require.NoError(t, err)
			require.Contains(t, signatureNames(defs), "operations-report")
			require.NotContains(t, signatureNames(defs), "operations-otherReport", "only the chosen exact operation is promoted")
			state, _ := toolselection.FromContext(ctx)
			require.Error(t, state.Activate([]string{"operations-unbundled"}))
			err = state.Activate([]string{"operations-rawSQL"})
			if allow {
				require.NoError(t, err)
				defs, err = s.buildToolSignatures(ctx, in)
				require.NoError(t, err)
				require.Contains(t, signatureNames(defs), "operations-rawSQL")
				require.True(t, toolapprovalqueue.RequiresQueue(ctx, "operations-rawSQL"))
				store := convmem.New()
				seedConversation(t, store, "conversation")
				_, _, err = toolexec.ExecuteToolStep(ctx, s.registry, toolexec.StepInfo{ID: "query-call", Name: "operations-rawSQL", Args: map[string]interface{}{}}, store)
				require.NoError(t, err)
				require.Zero(t, fixture.calls)
			} else {
				require.Error(t, err)
				in.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection.Names = []string{"operations:rawSQL"}
				defs, err = s.buildToolSignatures(ctx, in)
				require.NoError(t, err)
				require.Equal(t, []string{"operations-configure"}, signatureNames(defs), "intake cannot bypass the same exclusion")
			}
		})
	}
}

func TestDiscoveryBundleOmissionIsNotDenialButExplicitExclusionIs(t *testing.T) {
	for _, excluded := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted-only", true: "explicit-exclusions"}[excluded], func(t *testing.T) {
			s := selectionService()
			s.toolBundles = nil
			s.registry.(*fakeRegistry).defs = []llm.ToolDefinition{{Name: "system/patch:apply"}, {Name: "system/patch:commit"}, {Name: "system/patch:rollback"}}
			if excluded {
				s.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) {
					return []*toolbundle.Bundle{{ID: "system/patch", Match: []llm.Tool{{Name: "system/patch:*", Exclude: []string{"system/patch:commit", "system/patch:rollback"}}}}}, nil
				}
			}
			in := selectionInput(nil)
			in.Agent.Tool = agentmdl.Tool{Bundles: []string{"system/patch"}}
			definitions, err := s.discoverableToolDefinitions(context.Background(), in)
			require.NoError(t, err)
			if excluded {
				require.Equal(t, []string{"system_patch-apply"}, agentDefinitionNames(definitions))
			} else {
				require.ElementsMatch(t, []string{"system_patch-apply", "system_patch-commit", "system_patch-rollback"}, agentDefinitionNames(definitions))
			}
		})
	}
}

func agentDefinitionNames(definitions []llm.ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, d := range definitions {
		names = append(names, d.Name)
	}
	return names
}

func TestDiscoveryOptionalBundleDirectoryDoesNotHideInvalidAuthoredBundles(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-uses-defaults", true: "malformed-remains-error"}[present], func(t *testing.T) {
			root := t.TempDir()
			if present {
				directory := filepath.Join(root, "tools/bundles")
				require.NoError(t, os.MkdirAll(directory, 0700))
				require.NoError(t, os.WriteFile(filepath.Join(directory, "broken.yaml"), []byte("id: [invalid"), 0600))
			}
			s := selectionService()
			s.toolBundles = nil
			s.toolBundleRepo = bundlerepo.NewWithStore(fsstore.New(root))
			definitions, err := s.discoverableToolDefinitions(context.Background(), selectionInput(nil))
			if present {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.ElementsMatch(t, []string{"catalog-read", "catalog-write", "catalog-extra"}, agentDefinitionNames(definitions))
			}
		})
	}
}

func TestDiscoveryUsesSelectedProfileRuleAttachments(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "profile-excludes", true: "explicit-profile-tool-allows"}[explicit], func(t *testing.T) {
			s, ctx, _ := discoveryService(t)
			// The admitted profile's own attachment overrides the ambient bundle with
			// the same ID; merely knowing the ID must not recover that ambient wildcard.
			s.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) {
				return []*toolbundle.Bundle{{ID: "base", Match: []llm.Tool{{Name: "operations:configure"}}}, {ID: "reports", Match: []llm.Tool{{Name: "operations:*"}}}}, nil
			}
			profile := &intent.Profile{ID: "chosen", ResourceURI: "intent://workspace/chosen", ToolBundles: []string{"reports"}, ToolBundleDefinitions: []*toolbundle.Bundle{{ID: "reports", Match: []llm.Tool{{Name: "operations:*", Exclude: []string{"operations:rawSQL", "operations:unbundled"}}}}}}
			if explicit {
				profile.PreferredTools = []string{"operations:rawSQL"}
			}
			ctx = withSelectedPromptProfile(ctx, profile)
			definitions, err := s.discoverableToolDefinitions(ctx, selectionInput(nil))
			require.NoError(t, err)
			names := agentDefinitionNames(definitions)
			require.Contains(t, names, "operations-report")
			require.NotContains(t, names, "operations-unbundled")
			if explicit {
				require.Contains(t, names, "operations-rawSQL")
			} else {
				require.NotContains(t, names, "operations-rawSQL")
			}
		})
	}
}

func TestDiscoveryDoesNotHideInjectedBundleProviderFailure(t *testing.T) {
	s := selectionService()
	sentinel := errors.New("injected bundle provider failed")
	s.toolBundles = func(context.Context) ([]*toolbundle.Bundle, error) { return nil, sentinel }
	s.toolBundlesConfigured = true
	s.toolBundleRepo = bundlerepo.NewWithStore(fsstore.New(t.TempDir()))
	_, err := s.discoverableToolDefinitions(context.Background(), selectionInput(nil))
	require.ErrorIs(t, err, sentinel)
}

func TestUnbundledConnectedToolCanBeSelectedWithoutOwningBundle(t *testing.T) {
	s, ctx, _ := discoveryService(t)
	in := selectionInput(&intake.ToolSelection{Names: []string{"operations:unbundled"}, Complete: true})
	summary, err := s.intakeCapabilitySummary(ctx, in)
	require.NoError(t, err)
	require.Contains(t, summary, "operations-unbundled")
	definitions, err := s.buildToolSignatures(ctx, in)
	require.NoError(t, err)
	require.Equal(t, []string{"operations-unbundled"}, signatureNames(definitions))
	require.NotContains(t, signatureNames(definitions), "operations-appOnly")
}
