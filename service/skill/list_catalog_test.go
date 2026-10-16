package skill

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	conv "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	proto "github.com/viant/agently-core/protocol/skill"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	mcpclient "github.com/viant/mcp/client"
)

func TestSkillListToolUsesFilteredCatalogWithoutLoadingBodies(t *testing.T) {
	providers := map[string]*queryNativeSkill{
		"first":  {skillMCPFixture: skillMCPFixture{body: "---\nname: alpha\ndescription: Sales planning\n---\nSECRET BODY"}, name: "alpha"},
		"second": {skillMCPFixture: skillMCPFixture{body: "---\nname: zulu\ndescription: Sales reporting\n---\nSECRET BODY"}, name: "zulu"},
	}
	options := querySkillOptions{"first": {SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}, "second": {SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}}
	mgr, err := manager.New(options, manager.WithClientFactory(func(_ context.Context, _, name string) (mcpclient.Interface, error) { return providers[name], nil }))
	require.NoError(t, err)
	id := "agent"
	service := &Service{mcpSource: mgr, registry: proto.NewRegistry(), agentFinder: &testFinder{agent: &agentmdl.Agent{Skills: []string{"*"}}}, conv: &testConversationClient{conv: &conv.Conversation{Id: "conversation", AgentId: &id}}}
	actor := requestctx.WithConversationID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "conversation")
	call, err := service.Method("list")
	require.NoError(t, err)
	input := &ListInput{}
	require.NoError(t, json.Unmarshal([]byte(`{"namespace":"vendor","keywords":["sales"],"limit":1}`), input))
	first := &ListOutput{}
	require.NoError(t, call(actor, input, first))
	require.Equal(t, 2, first.Total)
	require.False(t, first.Complete)
	require.Len(t, first.Items, 1)
	require.Equal(t, "alpha", first.Items[0].Name)
	input.Cursor = first.NextCursor
	second := &ListOutput{}
	require.NoError(t, call(actor, input, second))
	require.True(t, second.Complete)
	require.Equal(t, "zulu", second.Items[0].Name)
	input.NameTerms = []string{"alpha"}
	require.Error(t, call(actor, input, &ListOutput{}), "cursor cannot be reused with different filters")
	input.Cursor = ""
	filtered := &ListOutput{}
	require.NoError(t, call(actor, input, filtered))
	require.Equal(t, 1, filtered.Total)
	for _, provider := range providers {
		require.Equal(t, 1, provider.listCalls, "paging must reuse discovery")
		require.Zero(t, provider.readCalls, "search must not load skill instructions")
	}
	raw, err := json.Marshal(filtered)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "SECRET BODY")
	legacy := &ListOutput{}
	require.NoError(t, call(actor, &ListInput{}, legacy))
	require.Len(t, legacy.Items, 2)
	require.True(t, legacy.Complete)
	bob := requestctx.WithConversationID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"}), "conversation")
	denied := &ListOutput{}
	require.NoError(t, call(bob, &ListInput{Limit: 1}, denied))
	require.Empty(t, denied.Items)
}
