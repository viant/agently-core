package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	skills "github.com/viant/agently-core/service/skill"
	"github.com/viant/mcp"
)

type optionalSkillTransport struct {
	*manager.Manager
	config *cfg.MCPClient
}

func (s *optionalSkillTransport) Names(context.Context) ([]string, error) {
	return []string{"optional"}, nil
}
func (s *optionalSkillTransport) Options(context.Context, string) (*cfg.MCPClient, error) {
	return s.config, nil
}

func TestSkillControlsDoNotInitializeUnavailableProducer(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	source := &optionalSkillTransport{config: &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL}}}, SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}}
	mgr, err := manager.New(source)
	require.NoError(t, err)
	source.Manager = mgr
	t.Cleanup(func() { mgr.CloseConversation("") })
	svc := skills.New(nil, nil, nil)
	svc.SetMCPSource(source)
	s := &Service{skillSvc: svc}
	input := &QueryInput{Query: "hello", Agent: &agentmdl.Agent{Skills: []string{"*"}}}
	input.Agent.Tool.Items = []*llm.Tool{{Name: "healthy:read"}}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "example-user"})
	for i := 0; i < 2; i++ {
		control, err := s.resolveToolControl(ctx, input)
		require.NoError(t, err)
		require.Contains(t, control.Tools, "healthy-read")
		require.NotEmpty(t, control.Tools)
	}
	store := convmem.New()
	conv := apiconv.NewConversation()
	conv.SetId("example-conversation")
	require.NoError(t, store.PatchConversations(ctx, conv))
	s.conversation = store
	s.registry = &fakeRegistry{defs: []llm.ToolDefinition{{Name: "healthy:read"}, {Name: "llm/skills:list"}, {Name: "llm/skills:get"}, {Name: "llm/skills:activate"}}}
	input.ConversationID = "example-conversation"
	bound, err := s.BuildBinding(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, bound)
	require.Contains(t, bindingToolNames(bound.Tools.Signatures), "healthy-read")
	require.Zero(t, calls.Load(), "advertising discovery controls must not initialize an optional producer")
	// An explicit catalog read still crosses the actual transport and preserves
	// the producer failure; it must not return a successful empty catalog.
	_, err = svc.ListVisible(ctx, input.Agent)
	require.Error(t, err)
	require.Contains(t, err.Error(), "503")
	require.Positive(t, calls.Load())
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.resolveToolControl(canceled, input)
	require.ErrorIs(t, err, context.Canceled)
}
