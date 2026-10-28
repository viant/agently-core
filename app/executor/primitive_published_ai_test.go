package executor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	agent "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/agent/finder"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	core "github.com/viant/agently-core/service/core"
	resources "github.com/viant/agently-core/service/resource"
)

// These independent minimal envelopes exercise public contracts, not copied
// application packages. All remote metadata access is an immediate failure.
type publishedAIConsumerSource struct {
	states map[string]primitive.ResourceState
	denied bool
}

func (s *publishedAIConsumerSource) AdmissionKey(context.Context) (string, error) {
	if s.denied {
		return "", identity.ErrResourceDenied
	}
	return "synthetic-session", nil
}
func (s *publishedAIConsumerSource) CurrentHash(ctx context.Context, uri string) (string, error) {
	if _, err := s.AdmissionKey(ctx); err != nil {
		return "", err
	}
	state, ok := s.states[uri]
	if !ok {
		return "", identity.ErrResourceDenied
	}
	return state.ContentFingerprint, nil
}
func (s *publishedAIConsumerSource) Namespaces(ctx context.Context) ([]primitive.NamespaceCapabilities, error) {
	if _, err := s.AdmissionKey(ctx); err != nil {
		return nil, err
	}
	kinds := []primitive.KindSupport{}
	for _, kind := range []string{"agent", "prompt", "playbook", "eval", "toolfeed"} {
		kinds = append(kinds, primitive.KindSupport{Kind: kind, Operations: []string{"list", "get"}})
	}
	return []primitive.NamespaceCapabilities{{Namespace: "example", ProviderIdentity: "publisher", Kinds: kinds}}, nil
}
func (s *publishedAIConsumerSource) List(ctx context.Context, kind, namespace string) ([]primitive.ResourceState, error) {
	if _, err := s.AdmissionKey(ctx); err != nil {
		return nil, err
	}
	var result []primitive.ResourceState
	for _, state := range s.states {
		if state.Kind == kind && (namespace == "" || namespace == state.Namespace) {
			result = append(result, state)
		}
	}
	return result, nil
}
func (s *publishedAIConsumerSource) Get(ctx context.Context, uri string) (*primitive.ResourceState, error) {
	if _, err := s.CurrentHash(ctx, uri); err != nil {
		return nil, err
	}
	value := s.states[uri]
	return &value, nil
}

func TestPublishedAIConsumersResolveActualTypedDefinitionsLocally(t *testing.T) {
	ctx := context.Background()
	trap := &publishedSkillTransportTrap{}
	mgr, err := manager.New(trap)
	require.NoError(t, err)
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(gateway.Close)
	source := &publishedAIConsumerSource{states: map[string]primitive.ResourceState{}}
	definitions := map[string]any{
		"agent://example/review":    map[string]any{"schemaVersion": 1, "document": "---\nkind: agent\nid: review\nname: Reviewer\nmodel: synthetic-model\nskills: [\"skill://example/review\"]\nprompt:\n  uri: prompt://example/question\n---\nClosed instruction body."},
		"prompt://example/question": map[string]any{"schemaVersion": 1, "text": "Exact prompt text.", "engine": "go", "arguments": []map[string]any{{"name": "topic", "required": true}}},
		"playbook://example/review": map[string]any{"schemaVersion": 1, "profile": map[string]any{"id": "review", "name": "Review", "instructions": "Closed playbook instructions.", "preferredTools": []string{"example/read"}}},
		"eval://example/review":     map[string]any{"schemaVersion": 1, "id": "review", "title": "Review routing", "user_prompt": "Review a synthetic record.", "entry_agent": "agent://example/review", "expected_routing": map[string]any{"agent": "agent://example/review", "profile": "review"}, "expected_output": map[string]any{"template": "summary"}, "dependencies": []any{}},
	}
	for uri, value := range definitions {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		parsed, err := identity.ParseResourceURI(uri)
		require.NoError(t, err)
		source.states[uri] = primitive.ResourceState{URI: uri, Kind: parsed.Kind, Namespace: parsed.Namespace, Name: parsed.Name, Title: parsed.Name, FormatVersion: 1, DefinitionBytes: raw, ContentFingerprint: identity.ContentFingerprint(raw)}
	}
	require.NoError(t, gateway.RegisterPublishedMetadataSource(resources.Connection{Name: "catalog", ProviderIdentity: "publisher"}, []string{"example"}, source))
	runtime := &Runtime{PrimitiveProviders: gateway}
	fallback := finder.New(finder.WithInitial(&agent.Agent{Identity: agent.Identity{ID: "local", Name: "Local"}}))
	selected := &gatewayAgentFinder{runtime: runtime, fallback: fallback}
	loaded, err := selected.Find(ctx, "agent://example/review")
	require.NoError(t, err)
	require.Equal(t, "review", loaded.ID)
	require.Equal(t, "synthetic-model", loaded.Model)
	require.Contains(t, loaded.InstructionPrompt.Text, "Closed instruction body.")
	require.Equal(t, "Exact prompt text.", loaded.Prompt.Text)
	require.Equal(t, "go", loaded.Prompt.Engine)
	require.Empty(t, loaded.Prompt.URI, "materialized prompt must not trigger an AFS fallback")
	_, err = selected.Find(ctx, "local")
	require.NoError(t, err)
	directory, err := publishedAgentDirectory(runtime, localAgentDirectory(fallback, nil))(ctx)
	require.NoError(t, err)
	require.Len(t, directory, 2)
	prompt, err := resources.NewPromptReader(gateway).Definition(ctx, "prompt://example/question")
	require.NoError(t, err)
	require.True(t, prompt.Arguments[0].Required)
	// The direct core template consumer shares the same canonical prompt path.
	coreService := core.New(nil, nil, nil)
	coreService.SetPromptReader(&runtimePromptReader{runtime: runtime})
	expanded := &core.ExpandUserPromptOutput{}
	require.NoError(t, coreService.ExpandUserPrompt(ctx, &core.ExpandUserPromptInput{Prompt: &binding.Prompt{URI: "prompt://example/question"}}, expanded))
	require.Equal(t, "Exact prompt text.", expanded.ExpandedUserPrompt)

	playbooks, err := resources.NewPlaybookReader(gateway).LoadAll(ctx)
	require.NoError(t, err)
	require.Len(t, playbooks, 1)
	profile, err := resources.NewPlaybookReader(gateway).Load(ctx, "playbook://example/review")
	require.NoError(t, err)
	require.Equal(t, "Closed playbook instructions.", profile.Instructions)
	cases, err := resources.NewEvalReader(gateway).LoadAll(ctx)
	require.NoError(t, err)
	require.Len(t, cases, 1)
	caseDoc, err := resources.NewEvalReader(gateway).Load(ctx, "eval://example/review")
	require.NoError(t, err)
	require.Equal(t, "agent://example/review", caseDoc.EntryAgent)
	require.Zero(t, trap.calls)
	// Materialized prompt text belongs to the closed owning agent; retaining
	// its old locator must not require a separate child root/grant.
	sealed, err := json.Marshal(map[string]any{"schemaVersion": 1, "document": "id: review\nname: Reviewer\nprompt:\n  text: Embedded closed prompt.\n  uri: prompt://example/not-a-root\n  engine: go\n"})
	require.NoError(t, err)
	state := source.states["agent://example/review"]
	state.DefinitionBytes = sealed
	state.ContentFingerprint = identity.ContentFingerprint(sealed)
	source.states[state.URI] = state
	embedded, err := selected.Find(ctx, state.URI)
	require.NoError(t, err)
	require.Equal(t, "Embedded closed prompt.", embedded.Prompt.Text)
	require.Empty(t, embedded.Prompt.URI)
	source.denied = true
	_, err = selected.Find(ctx, "agent://example/review")
	require.Error(t, err)
	_, err = resources.NewPromptReader(gateway).Load(ctx, "prompt://example/question")
	require.Error(t, err)
	_, err = resources.NewEvalReader(gateway).Load(ctx, "eval://example/review")
	require.Error(t, err)
	_, err = resources.NewPlaybookReader(gateway).Load(ctx, "playbook://example/review")
	require.Error(t, err)
	require.Zero(t, trap.calls)
}
