package skill

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/primitive"
	proto "github.com/viant/agently-core/protocol/skill"
)

func TestSkillRetentionMetadataAndSelectorsDoNotReadOrExpandVisibility(t *testing.T) {
	fixture := &skillMCPFixture{body: "---\nname: review\ndescription: Synthetic review procedure\nmetadata:\n  agently-always-include: \"true\"\n---\nPrivate synthetic body."}
	service := &Service{mcpSource: &skillSourceFixture{f: fixture}, registry: proto.NewRegistry()}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	metadata, err := service.ListVisible(ctx, &agentmdl.Agent{Skills: []string{"review"}})
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	require.True(t, metadata[0].AlwaysInclude)
	require.Zero(t, fixture.readCalls)
	metadata, err = service.ListVisible(ctx, &agentmdl.Agent{Skills: []string{"other"}, SkillsAlwaysInclude: []string{"*"}})
	require.NoError(t, err)
	require.Empty(t, metadata, "retention selectors must not authorize excluded skills")
	fixture.body = "---\nname: review\ndescription: Synthetic review procedure\n---\nPrivate synthetic body."
	metadata, err = service.ListVisible(ctx, &agentmdl.Agent{Skills: []string{"review"}, SkillsAlwaysInclude: []string{"review"}})
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	require.True(t, metadata[0].AlwaysInclude)
	require.Zero(t, fixture.readCalls)
}

func TestDefaultSkillNamespaceFilterPreservesLegacyCatalog(t *testing.T) {
	registry := proto.NewRegistry()
	registry.Add(&proto.Skill{Frontmatter: proto.Frontmatter{Name: "local", Description: "Synthetic local procedure"}}, nil)
	service := &Service{registry: registry}
	page, err := service.ListVisiblePage(context.Background(), &agentmdl.Agent{Skills: []string{"*"}}, primitive.CatalogQuery{Namespace: "default"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "local", page.Items[0].Name)
}
