package intake

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/mcp/manager"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	resources "github.com/viant/agently-core/service/resource"
	primitive "github.com/viant/forge/backend/provider/contract"
	identity "github.com/viant/forge/backend/resource"
	host "github.com/viant/forge/backend/resourcehost"
)

const routingGuideURI = "routing://example/records"

type routingGuideSource struct {
	payload []byte
	reads   int
	denied  bool
}

func (s *routingGuideSource) AdmissionKey(context.Context) (string, error) {
	if s.denied {
		return "", identity.ErrResourceDenied
	}
	return "synthetic-session", nil
}
func (s *routingGuideSource) CurrentHash(context.Context, string) (string, error) {
	return identity.ContentFingerprint(s.payload), nil
}
func (*routingGuideSource) Namespaces(context.Context) ([]primitive.NamespaceCapabilities, error) {
	return []primitive.NamespaceCapabilities{{ProviderIdentity: "producer", Namespace: "example"}}, nil
}
func (*routingGuideSource) List(context.Context, string, string) ([]primitive.ResourceState, error) {
	return nil, nil
}
func (s *routingGuideSource) Get(_ context.Context, ref string) (*primitive.ResourceState, error) {
	s.reads++
	return &primitive.ResourceState{
		URI: ref, Kind: primitive.RoutingKind, Namespace: "example", Name: "records",
		DefinitionBytes: append([]byte(nil), s.payload...), ContentFingerprint: identity.ContentFingerprint(s.payload),
	}, nil
}

type routingIntakeModel struct{ requests []*llm.GenerateRequest }

func (m *routingIntakeModel) Find(context.Context, string) (llm.Model, error) { return m, nil }
func (*routingIntakeModel) Implements(string) bool                            { return false }
func (m *routingIntakeModel) Generate(_ context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.requests = append(m.requests, request)
	return &llm.GenerateResponse{Choices: []llm.Choice{{
		Message: llm.NewAssistantMessage(`{"classification":{"intent":"review","confidence":0.95}}`),
	}}}, nil
}

func TestRoutingGuideFollowsCurrentCohortInOneIntakeCall(t *testing.T) {
	ctx := t.Context()
	manager, err := manager.New(nil)
	require.NoError(t, err)
	actor, verify := host.PublicReadActors()
	gateway := host.NewGateway(manager, actor, verify, "host")
	t.Cleanup(gateway.Close)
	source := &routingGuideSource{}
	setBody := func(body string) {
		source.payload, err = json.Marshal(primitive.RoutingGuideEnvelope{SchemaVersion: 1,
			Document: "---\nname: Record review\ndescription: Select a reader\n---\n" + body + "\n"})
		require.NoError(t, err)
	}
	setBody("Use the first reader for summaries.")
	require.NoError(t, gateway.RegisterPublishedMetadataSource(host.Connection{Name: "producer", ProviderIdentity: "producer"}, []string{"example"}, source))
	model := &routingIntakeModel{}
	service := New(core.New(model, nil, nil), WithRoutingGuideReader(func(ctx context.Context, ref string) (string, error) {
		return resources.ReadRoutingGuide(ctx, gateway, ref)
	}))
	cfg := &agentmdl.Intake{Enabled: true, Model: "synthetic", Scope: []string{agentmdl.IntakeScopeIntent}, RoutingGuides: []string{routingGuideURI},
		Prompt: agentmdl.IntakePrompt{Prompt: binding.Prompt{Text: "Existing local guidance."}}}
	classify := func() string {
		t.Helper()
		before := len(model.requests)
		result := service.Run(ctx, "Review records", cfg, "reader")
		require.NotNil(t, result)
		require.Equal(t, "review", result.Classification.Intent)
		require.Len(t, model.requests, before+1)
		return model.requests[before].Messages[0].Content
	}
	require.Contains(t, classify(), "Use the first reader for summaries.")
	require.Contains(t, classify(), "Use the first reader for summaries.")
	require.Equal(t, 1, source.reads, "unchanged selection should reuse its compiled body")

	setBody("Use the second reader for summaries.")
	current := classify()
	require.Contains(t, current, "Use the second reader for summaries.")
	require.NotContains(t, current, "Use the first reader")
	require.Equal(t, 2, source.reads)

	source.denied = true
	denied := classify()
	require.Contains(t, denied, "Existing local guidance.")
	require.NotContains(t, denied, "Published routing guidance")
	require.Equal(t, 2, source.reads, "denial must never hydrate the body")

	cfg.RoutingGuides = []string{"routing://unregistered/records"}
	require.NotContains(t, classify(), "Published routing guidance")
	require.Equal(t, 2, source.reads, "unregistered namespaces must never fall back to MCP discovery")
}

func TestRoutingGuidePreviewRunDoesNotCallToolsOrWriteConversation(t *testing.T) {
	model := &routingIntakeModel{}
	conversation := &recordingIntakeConversationClient{}
	service := New(core.New(model, nil, nil), WithConversationClient(conversation), WithRoutingGuideReader(func(_ context.Context, uri string) (string, error) {
		require.Equal(t, routingGuideURI, uri)
		return "Review only records matching the supplied scope.", nil
	}))
	ctx := runtimerequestctx.WithTurnMeta(t.Context(), runtimerequestctx.TurnMeta{ConversationID: "example-conversation", TurnID: "example-turn"})
	ctx = runtimerequestctx.WithModelMessageID(ctx, "example-model-message")
	ctx = runtimerequestctx.WithoutConversationTracking(ctx)
	cfg := &agentmdl.Intake{Enabled: true, Model: "synthetic", Scope: []string{agentmdl.IntakeScopeIntent}, RoutingGuides: []string{routingGuideURI}}
	result := service.Run(ctx, "Review records", cfg, "reader", WithLiveContext("Selected collection: Example"))
	require.NotNil(t, result)
	require.Equal(t, "review", result.Classification.Intent)
	require.Len(t, model.requests, 1)
	request := model.requests[0]
	require.Contains(t, request.Messages[0].Content, "Review only records matching the supplied scope.")
	require.Contains(t, request.Messages[1].Content, "Selected collection: Example")
	require.Empty(t, request.Options.Tools)
	require.Equal(t, llm.NewNoneToolChoice(), request.Options.ToolChoice)
	require.Nil(t, conversation.message)
}
