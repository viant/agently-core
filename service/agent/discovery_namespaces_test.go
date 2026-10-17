package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	intent "github.com/viant/agently-core/protocol/intent"
	"github.com/viant/agently-core/protocol/primitive"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/runtime/toolselection"
	intake "github.com/viant/agently-core/service/intake"
	resourcesvc "github.com/viant/agently-core/service/resource"
)

func testNamespace(name, owner, description string, kinds ...string) resourcesvc.DiscoveredNamespace {
	item := resourcesvc.DiscoveredNamespace{Connection: resourcesvc.Connection{ProviderIdentity: owner, Name: owner + "-connection"}, NamespaceCapabilities: primitive.NamespaceCapabilities{ProviderIdentity: owner, Namespace: name, NamespaceMetadata: primitive.NamespaceMetadata{Description: description, UseCases: []string{"Inspect authorized records"}}}}
	for _, kind := range kinds {
		item.Kinds = append(item.Kinds, primitive.KindSupport{Kind: kind})
	}
	return item
}

func namespaceFixture(retained bool) (*Service, *QueryInput, []resourcesvc.DiscoveredNamespace) {
	defs := []llm.ToolDefinition{}
	for i := 0; i < 40; i++ {
		for _, domain := range []struct{ server, namespace, owner string }{{"remote", "analysis", "owner-a"}, {"other", "planning", "owner-b"}} {
			defs = append(defs, llm.ToolDefinition{Name: fmt.Sprintf("%s:query%02d", domain.server, i), Description: strings.Repeat("A detailed authorized operation description. ", 12), SourceResourceURI: "mcp://" + domain.namespace + "/configured-service", SourceProviderIdentity: domain.owner})
		}
	}
	defs = append(defs, llm.ToolDefinition{Name: "unmapped:read", Description: "Ordinary MCP fallback"}, llm.ToolDefinition{Name: "local:read", Description: "Built-in fallback"})
	helpers := []llm.Tool{}
	for _, name := range []string{"llm/tools:list", "llm/tools:activate", "llm/skills:list", "llm/skills:get", "llm/skills:activate"} {
		defs = append(defs, llm.ToolDefinition{Name: name, Description: "Discovery helper"})
		helpers = append(helpers, llm.Tool{Name: name, AlwaysInclude: retained})
	}
	svc := &Service{registry: &fakeRegistry{defs: defs}, toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{{ID: "workflow", Match: helpers}}, nil
	}}
	input := &QueryInput{Agent: &agentmdl.Agent{Tool: agentmdl.Tool{Bundles: []string{"workflow"}}}}
	namespaces := []resourcesvc.DiscoveredNamespace{testNamespace("analysis", "owner-a", "Analyze authorized records", "mcp", "skill"), testNamespace("planning", "owner-b", "Plan authorized changes", "mcp")}
	return svc, input, namespaces
}

func TestNamespaceCompactionReusesSnapshotAndRetainsExactTools(t *testing.T) {
	svc, input, namespaces := namespaceFixture(true)
	flat, allNames, err := svc.intakeCapabilities(context.Background(), input)
	require.NoError(t, err)
	registry := &intakeSnapshotRegistry{fakeRegistry: *svc.registry.(*fakeRegistry)}
	svc.registry = registry
	reads := 0
	svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { reads++; return namespaces, nil }
	compact, names, hints, err := svc.intakeCapabilitySnapshot(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, 1, reads, "one namespace snapshot serves discovery and compaction")
	require.Equal(t, 1, registry.reads, "the existing tool catalog is read only once")
	require.Equal(t, allNames, names, "exact schema enum/backend candidates remain complete")
	require.Equal(t, []string{"analysis", "planning"}, hints)
	var cards []map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(compact), &cards))
	require.Len(t, cards, 7, "two cards, three advertised skill helpers, and two flat fallbacks")
	require.Contains(t, compact, "unmapped-read")
	require.Contains(t, compact, "local-read")
	require.Contains(t, compact, "llm_skills-list")
	require.NotContains(t, compact, "remote-query00")
	require.Less(t, len(compact), len(flat)/5)
	enum, _ := json.Marshal(names)
	t.Logf("metadata bytes flat=%d compact=%d; exact enum names=%d enum_bytes=%d", len(flat), len(compact), len(names), len(enum))
}

func TestNamespaceCompactionRequiresActuallyRetainedHelpers(t *testing.T) {
	for _, retained := range []bool{false, true} {
		svc, input, namespaces := namespaceFixture(retained)
		svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { return namespaces, nil }
		summary, _, hints, err := svc.intakeCapabilitySnapshot(context.Background(), input)
		require.NoError(t, err)
		if retained {
			require.Len(t, hints, 2)
		} else {
			require.Empty(t, hints)
			require.Contains(t, summary, "remote-query00")
		}
	}
}

func TestDeclaredNamespaceOwnershipNeverGuessesFromServer(t *testing.T) {
	namespaces := []resourcesvc.DiscoveredNamespace{testNamespace("first", "owner", "First domain", "mcp", "skill"), testNamespace("second", "owner", "Second domain", "mcp", "skill"), testNamespace("workspace", "owner", "", "mcp")}
	require.Equal(t, "second", describedToolNamespace(llm.ToolDefinition{Name: "first:query", SourceResourceURI: "mcp://second/server", SourceProviderIdentity: "owner"}, namespaces))
	for _, def := range []llm.ToolDefinition{
		{Name: "first:query"},
		{Name: "first:query", SourceResourceURI: "mcp://first/server"},
		{Name: "first:query", SourceResourceURI: "mcp://first/server", SourceProviderIdentity: "wrong-owner"},
		{Name: "first:query", SourceResourceURI: "mcp://workspace/server", SourceProviderIdentity: "owner"},
		{Name: "first:query", SourceResourceURI: "skill://first/server", SourceProviderIdentity: "owner"},
	} {
		require.Empty(t, describedToolNamespace(def, namespaces))
	}
	require.Equal(t, "first", describedSkillNamespace("skill://first/review/SKILL.md", "owner-connection", namespaces))
	require.Empty(t, describedSkillNamespace("ordinary-server/review", "owner-connection", namespaces))
	ambiguous := append(namespaces, testNamespace("first", "another-owner", "Different owner", "skill"))
	require.Empty(t, describedSkillNamespace("skill://first/review/SKILL.md", "owner-connection", ambiguous))
	require.Empty(t, describedToolNamespace(llm.ToolDefinition{Name: "server:query", SourceResourceURI: "mcp://first/server", SourceProviderIdentity: "owner"}, ambiguous), "ambiguous namespace cards cannot describe a different owner")
}

func TestNamespaceHintsDoNotScopeCandidateDiscoveryOrCallerPolicies(t *testing.T) {
	svc, input, namespaces := namespaceFixture(true)
	svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { return namespaces, nil }
	input.Context = map[string]interface{}{intake.ContextKey: &intake.Context{Prompting: intake.PromptingContext{DiscoveryNamespaces: []string{"analysis", "stale"}, ToolSelection: &intake.ToolSelection{Names: []string{}, Complete: true}}}}
	ctx := toolselection.WithState(context.Background(), nil)
	defs, err := svc.buildToolSignatures(ctx, input)
	require.NoError(t, err)
	require.Len(t, defs, 5, "empty initial selection begins with retained discovery helpers")
	state, ok := toolselection.FromContext(ctx)
	require.True(t, ok)
	page, err := state.ListPage(ctx, primitive.CatalogQuery{Namespace: "planning", Limit: 2}, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 2, "outside-hint domain remains discoverable")
	require.NoError(t, state.Activate([]string{page.Items[0].Name}))
	legacy, err := state.ListPage(ctx, primitive.CatalogQuery{Namespace: "unmapped", Limit: 2}, 0)
	require.NoError(t, err)
	require.Len(t, legacy.Items, 1)
	b := &binding.Binding{}
	appendDiscoveryNamespaceHints(input, b)
	require.Len(t, b.SystemDocuments.Items, 1)
	require.Contains(t, b.SystemDocuments.Items[0].PageContent, "Broaden discovery outside")
	input.ToolsAllowed = []string{"remote:query00"}
	scoped, err := svc.discoverableToolDefinitions(ctx, input)
	require.NoError(t, err)
	require.Len(t, scoped, 1)
	require.Equal(t, "remote-query00", scoped[0].Name)
}

func TestUndescribedNamespacePreservesLegacyServiceFilter(t *testing.T) {
	svc, input, _ := namespaceFixture(true)
	svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) {
		return []resourcesvc.DiscoveredNamespace{testNamespace("analysis", "owner-a", "", "mcp")}, nil
	}
	defs, err := svc.discoverableToolDefinitions(context.Background(), input)
	require.NoError(t, err)
	for _, def := range defs {
		require.Empty(t, def.DiscoveryNamespace)
	}
	stateCtx := toolselection.WithState(context.Background(), defsToTools(defs))
	state, _ := toolselection.FromContext(stateCtx)
	page, err := state.ListPage(stateCtx, primitive.CatalogQuery{Namespace: "remote", Limit: 2}, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
}

func TestNamespaceCriticalToolRemainsIndividual(t *testing.T) {
	svc, input, namespaces := namespaceFixture(true)
	previous := svc.toolBundles
	svc.toolBundles = func(ctx context.Context) ([]*toolbundle.Bundle, error) {
		bundles, err := previous(ctx)
		bundles[0].Match = append(bundles[0].Match, llm.Tool{Name: "remote:query00", AlwaysInclude: true})
		return bundles, err
	}
	svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { return namespaces, nil }
	summary, _, _, err := svc.intakeCapabilitySnapshot(context.Background(), input)
	require.NoError(t, err)
	require.Contains(t, summary, "remote-query00")
	require.NotContains(t, summary, "remote-query01")
}

func TestSkillNamespaceRequiresActualConnectionAlias(t *testing.T) {
	namespace := testNamespace("analysis", "provider-owner", "Analysis skills", "skill")
	namespace.Connection.Name = "native-connection-alias"
	require.Equal(t, "analysis", describedSkillNamespace("skill://analysis/review/SKILL.md", "native-connection-alias", []resourcesvc.DiscoveredNamespace{namespace}))
	require.Empty(t, describedSkillNamespace("skill://analysis/review/SKILL.md", "rogue-connection", []resourcesvc.DiscoveredNamespace{namespace}), "same URI from another connection must remain individually visible")
	require.Empty(t, describedSkillNamespace("skill://analysis/review/SKILL.md", "provider-owner", []resourcesvc.DiscoveredNamespace{namespace}), "connection alias is not provider identity")
	require.Empty(t, describedSkillNamespace("skill://analysis/review/SKILL.md", "", []resourcesvc.DiscoveredNamespace{namespace}))
}

func TestNamespaceSnapshotReusedAcrossIntakeAndBindingIterations(t *testing.T) {
	svc, input, namespaces := namespaceFixture(true)
	reads := 0
	svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { reads++; return namespaces, nil }
	ctx := svc.withNamespaceSnapshot(context.Background())
	_, _, _, err := svc.intakeCapabilitySnapshot(ctx, input)
	require.NoError(t, err)
	for iteration := 0; iteration < 4; iteration++ {
		require.Same(t, ctx, svc.ensureNamespaceSnapshot(ctx))
		definitions, err := svc.discoverableToolDefinitions(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "analysis", definitions[0].DiscoveryNamespace)
	}
	require.Equal(t, 1, reads, "same request/turn reuses one gateway metadata page scan and namespace index")
	changed := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "another-actor"})
	changed = svc.ensureNamespaceSnapshot(changed)
	require.Equal(t, 1, reads, "a new scope is lazy until used")
	_, err = svc.discoverableToolDefinitions(changed, input)
	require.NoError(t, err)
	require.Equal(t, 2, reads, "another actor cannot inherit the old metadata snapshot")
	next := svc.withNamespaceSnapshot(ctx)
	require.Equal(t, 2, reads, "a new Query masks rather than eagerly loading its parent's snapshot")
	_, err = svc.discoverableToolDefinitions(next, input)
	require.NoError(t, err)
	require.Equal(t, 3, reads, "a new Query gets its own snapshot even with an inherited context")
}

func TestToolsOnlyCompactionNeedsOnlyRetainedAutoActivatingList(t *testing.T) {
	svc, input, namespaces := namespaceFixture(false)
	previous := svc.toolBundles
	svc.toolBundles = func(ctx context.Context) ([]*toolbundle.Bundle, error) {
		bundles, err := previous(ctx)
		for i := range bundles[0].Match {
			bundles[0].Match[i].AlwaysInclude = bundles[0].Match[i].Name == "llm/tools:list"
		}
		return bundles, err
	}
	namespaces[0].Kinds = append(namespaces[0].Kinds, primitive.KindSupport{Kind: "window"}, primitive.KindSupport{Kind: "intent"}, primitive.KindSupport{Kind: "report"})
	svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { return namespaces, nil }
	retained := svc.retainedNamespaceDiscovery(context.Background(), input)
	require.True(t, retained["mcp"])
	require.False(t, retained["skill"])
	summary, _, hints, err := svc.intakeCapabilitySnapshot(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, hints, 2)
	require.NotContains(t, summary, "remote-query00")
	var cards []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(summary), &cards))
	for _, card := range cards {
		var name, kind string
		_ = json.Unmarshal(card["name"], &name)
		_ = json.Unmarshal(card["kind"], &kind)
		if kind == "namespace" && name == "analysis" {
			var supported []primitive.KindSupport
			require.NoError(t, json.Unmarshal(card["supportedKinds"], &supported))
			require.Equal(t, namespaces[0].Kinds, supported, "producer supportedKinds remains complete, not relabeled as compaction coverage")
			return
		}
	}
	t.Fatal("analysis namespace card missing")
}

func TestNamespaceCardsUseTheValidatedDescribedOwnerInEitherOrder(t *testing.T) {
	for _, invalidFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(invalidFirst), func(t *testing.T) {
			svc, input, namespaces := namespaceFixture(true)
			undescribed := testNamespace("analysis", "unrelated-owner", "", "skill")
			expected := namespaces[0]
			if invalidFirst {
				namespaces = append([]resourcesvc.DiscoveredNamespace{undescribed}, namespaces...)
			} else {
				namespaces = append(namespaces, undescribed)
			}
			svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { return namespaces, nil }
			summary, _, hints, err := svc.intakeCapabilitySnapshot(context.Background(), input)
			require.NoError(t, err)
			require.Equal(t, []string{"analysis", "planning"}, hints)
			require.NotContains(t, summary, "remote-query00")
			var cards []namespaceCapability
			require.NoError(t, json.Unmarshal([]byte(summary), &cards))
			for _, card := range cards {
				if card.Kind == "namespace" && card.Name == "analysis" {
					require.Equal(t, expected.Description, card.Description)
					require.Equal(t, expected.UseCases, card.UseCases)
					require.Equal(t, expected.Kinds, card.SupportedKinds)
					return
				}
			}
			t.Fatal("validated analysis owner card missing")
		})
	}
}

func TestConflictingSameOwnerNamespaceDescriptorsLeaveResourcesFlat(t *testing.T) {
	for _, conflict := range []string{"description", "kinds"} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%v", conflict, reverse), func(t *testing.T) {
				svc, input, namespaces := namespaceFixture(true)
				duplicate := namespaces[0]
				if conflict == "description" {
					duplicate.Description = "A conflicting purpose"
				} else {
					duplicate.Kinds = []primitive.KindSupport{{Kind: "skill"}}
				}
				if reverse {
					namespaces = append([]resourcesvc.DiscoveredNamespace{duplicate}, namespaces...)
				} else {
					namespaces = append(namespaces, duplicate)
				}
				svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { return namespaces, nil }
				summary, _, hints, err := svc.intakeCapabilitySnapshot(context.Background(), input)
				require.NoError(t, err)
				require.Equal(t, []string{"planning"}, hints)
				require.Contains(t, summary, "remote-query00", "ambiguous card metadata cannot justify hiding any of its individual resources")
				var cards []namespaceCapability
				require.NoError(t, json.Unmarshal([]byte(summary), &cards))
				for _, card := range cards {
					if card.Kind == "namespace" {
						require.NotEqual(t, "analysis", card.Name)
					}
				}
			})
		}
	}
}

func TestNamespaceSnapshotDoesNotLoadOnDisabledPaths(t *testing.T) {
	svc, input, namespaces := namespaceFixture(true)
	reads := 0
	svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { reads++; return namespaces, nil }
	ctx := svc.withNamespaceSnapshot(context.Background())
	ctx = svc.ensureNamespaceSnapshot(ctx)
	require.Zero(t, reads, "request/turn setup is lazy")
	input.Agent.Intake.Enabled = false
	svc.maybeRunIntakeSidecar(ctx, input)
	require.Zero(t, reads, "disabled intake does not load namespace inventory")
	ctx = withSelectedPromptProfile(ctx, &intent.Profile{ID: "disabled", Execution: &intent.Execution{DisableTools: true}})
	definitions, err := svc.discoverableToolDefinitions(ctx, input)
	require.NoError(t, err)
	require.Empty(t, definitions)
	require.Empty(t, svc.namespaceDefinitions(ctx, nil))
	require.Zero(t, reads, "disabled tools and empty metadata paths never load the gateway namespace catalog")
}

func TestNamespaceProjectionOwnsDefinitionsBeforeSettingHostMetadata(t *testing.T) {
	svc, _, namespaces := namespaceFixture(true)
	svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { return namespaces, nil }
	original := []llm.ToolDefinition{{Name: "transport:read", SourceResourceURI: "mcp://analysis/config", SourceProviderIdentity: "owner-a", DiscoveryNamespace: "original-host-metadata", Parameters: map[string]interface{}{"type": "object"}}}
	result := svc.namespaceDefinitions(svc.withNamespaceSnapshot(context.Background()), original)
	require.Equal(t, "analysis", result[0].DiscoveryNamespace)
	require.Equal(t, "original-host-metadata", original[0].DiscoveryNamespace, "caller/registry slice cannot be mutated by namespace projection")
	require.Equal(t, original[0].Parameters, result[0].Parameters)
}

func TestNamespacePostDiscoveryValidationUsesLiveParentContext(t *testing.T) {
	svc, input, namespaces := namespaceFixture(true)
	original := svc.toolBundles
	calls := 0
	svc.toolBundles = func(ctx context.Context) ([]*toolbundle.Bundle, error) {
		calls++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return original(ctx)
	}
	svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { return namespaces, nil }
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, hints, err := svc.intakeCapabilitySnapshot(parent, input)
	require.NoError(t, err)
	require.NoError(t, parent.Err())
	require.GreaterOrEqual(t, calls, 2, "bundle reader participates in discovery and post-Wait retained-helper validation")
	require.Equal(t, []string{"analysis", "planning"}, hints, "a canceled worker context must not silently disable compaction")
}

// Measures one intake metadata build (no model/network latency). Each operation
// gets a fresh request snapshot; gateway warm-cache behavior is covered by the
// runtime integration rather than replaced by a second benchmark cache.
func BenchmarkNamespaceIntakeMetadata(b *testing.B) {
	for _, compact := range []bool{false, true} {
		name := "flat"
		if compact {
			name = "namespace_cards"
		}
		b.Run(name, func(b *testing.B) {
			svc, input, namespaces := namespaceFixture(true)
			if compact {
				svc.namespaceProvider = func(context.Context) ([]resourcesvc.DiscoveredNamespace, error) { return namespaces, nil }
			}
			var summary string
			var names, hints []string
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var err error
				summary, names, hints, err = svc.intakeCapabilitySnapshot(context.Background(), input)
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			encoded, _ := json.Marshal(names)
			b.ReportMetric(float64(len(summary)), "metadata_bytes")
			b.ReportMetric(float64(len(encoded)), "enum_bytes")
			b.ReportMetric(float64(len(names)), "exact_names")
			b.ReportMetric(float64(len(hints)), "namespace_hints")
		})
	}
}
