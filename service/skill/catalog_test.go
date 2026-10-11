package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/mcp-protocol/schema"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	proto "github.com/viant/agently-core/protocol/skill"
	mcpclient "github.com/viant/mcp/client"
)

type querySkillOptions map[string]*cfg.MCPClient

func (p querySkillOptions) Names(context.Context) ([]string, error) {
	names := []string{}
	for name := range p {
		names = append(names, name)
	}
	return names, nil
}
func (p querySkillOptions) Options(_ context.Context, name string) (*cfg.MCPClient, error) {
	return p[name], nil
}
func TestSkillPagesUseSharedCachedKernelForNativeAndOrdinaryProviders(t *testing.T) {
	native := &queryNativeSkill{skillMCPFixture: skillMCPFixture{body: "---\nname: zulu\ndescription: Sales native skill\n---\nNever fetched to filter."}}
	legacy := &legacySkillFixture{entry: (&skillMCPFixture{body: "---\nname: alpha\ndescription: Sales ordinary skill\n---\nNever fetched to filter."}).entry()}
	legacy.entry = renameQuerySkill(legacy.entry, "alpha")
	options := querySkillOptions{"native": {SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}, "ordinary": {SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}, "unsupported": {}}
	mgr, err := manager.New(options, manager.WithClientFactory(func(_ context.Context, _, name string) (mcpclient.Interface, error) {
		switch name {
		case "native":
			return native, nil
		case "ordinary":
			return &fallbackCapabilityClient{legacy}, nil
		default:
			t.Error("non-skill provider contacted")
			return nil, nil
		}
	}))
	require.NoError(t, err)
	service := &Service{mcpSource: mgr, registry: proto.NewRegistry()}
	actor := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	agent := &agentmdl.Agent{Skills: []string{"*"}}
	first, err := service.ListVisiblePage(actor, agent, primitive.CatalogQuery{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, "alpha", first.Items[0].Name)
	second, err := service.ListVisiblePage(actor, agent, primitive.CatalogQuery{Limit: 1, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Equal(t, "zulu", second.Items[0].Name)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			page, e := service.ListVisiblePage(actor, agent, primitive.CatalogQuery{Namespace: "vendor", DescriptionTerms: []string{"sales", "other"}, Keywords: []string{"alpha"}})
			if e != nil || len(page.Items) != 1 {
				t.Errorf("skill local filter %v %v", page, e)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 1, native.listCalls)
	require.Equal(t, []string{"skills/list"}, legacy.names)
	require.Zero(t, native.readCalls)
	_, err = service.ListVisiblePage(actor, agent, primitive.CatalogQuery{Limit: 1, Cursor: first.NextCursor, Keywords: []string{"sales"}})
	require.ErrorIs(t, err, primitive.ErrCatalogCursor)
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	page, err := service.ListVisiblePage(bob, agent, primitive.CatalogQuery{Namespace: "vendor", NameTerms: []string{"zulu"}})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.Equal(t, 2, native.listCalls)
	first.Items[0].Description = "mutated"
	again, err := service.ListVisiblePage(actor, agent, primitive.CatalogQuery{Namespace: "vendor"})
	require.NoError(t, err)
	require.Equal(t, "Sales ordinary skill", again.Items[0].Description)
	mgr.CloseConversation("skill-catalog")
	_, err = service.ListVisiblePage(actor, agent, primitive.CatalogQuery{Limit: 1, Cursor: first.NextCursor})
	require.ErrorIs(t, err, primitive.ErrCatalogCursor)
}

type queryNativeSkill struct{ skillMCPFixture }

func renameQuerySkill(entry schema.Skill, name string) schema.Skill {
	entry.Uri = strings.Replace(entry.Uri, "/review/", "/"+name+"/", 1)
	for i := range entry.Resources.Files {
		entry.Resources.Files[i].Uri = entry.Uri
	}
	return entry
}
func (f *queryNativeSkill) ListSkills(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*schema.ListSkillsResult, error) {
	result, err := f.skillMCPFixture.ListSkills(ctx, cursor, options...)
	if result != nil {
		for i := range result.Skills {
			result.Skills[i] = renameQuerySkill(result.Skills[i], "zulu")
		}
	}
	return result, err
}

type queryNamespaceNative struct {
	*skillMCPFixture
	entries []schema.Skill
}

func (f *queryNamespaceNative) ListSkills(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*schema.ListSkillsResult, error) {
	result, err := f.skillMCPFixture.ListSkills(ctx, cursor, options...)
	if result != nil && len(result.Skills) > 0 {
		result.Skills = append([]schema.Skill(nil), f.entries...)
	}
	return result, err
}

type queryNamespaceOrdinary struct {
	*fallbackCapabilityClient
	entries []schema.Skill
	lists   int
}

func (f *queryNamespaceOrdinary) CallTool(ctx context.Context, r *schema.CallToolRequestParams, options ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	if r.Name == "skills/list" {
		for key := range r.Arguments {
			if key != "cursor" {
				return nil, fmt.Errorf("unsupported list argument %s", key)
			}
		}
		f.lists++
		return &schema.CallToolResult{StructuredContent: map[string]any{"skills": f.entries}}, nil
	}
	return f.legacySkillFixture.CallTool(ctx, r, options...)
}
func TestSkillCatalogPartitionsOriginalNamespacesWithinOneMCPConnection(t *testing.T) {
	for _, mode := range []string{"native", "ordinary"} {
		t.Run(mode, func(t *testing.T) {
			body := "---\nname: review\ndescription: Review selected records\n---\nbody"
			fixture := &skillMCPFixture{body: body}
			entry := fixture.entry()
			entries := []schema.Skill{entry, fixture.entry()}
			for i, namespace := range []string{"north", "south"} {
				entries[i].Uri = "skill://" + namespace + "/nested/review/SKILL.md"
				for j := range entries[i].Resources.Files {
					entries[i].Resources.Files[j].Uri = entries[i].Uri
				}
			}
			original, _ := json.Marshal(entries)
			native := &queryNamespaceNative{skillMCPFixture: fixture, entries: entries}
			ordinary := &queryNamespaceOrdinary{fallbackCapabilityClient: &fallbackCapabilityClient{legacySkillFixture: &legacySkillFixture{entry: entries[0]}}, entries: entries}
			var client mcpclient.Interface = native
			if mode == "ordinary" {
				client = ordinary
			}
			options := querySkillOptions{"one-connection": {SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}}
			mgr, err := manager.New(options, manager.WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) { return client, nil }))
			require.NoError(t, err)
			defer mgr.CloseConversation("skill-catalog")
			service := &Service{mcpSource: mgr, registry: proto.NewRegistry()}
			actor := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
			agent := &agentmdl.Agent{Skills: []string{"*"}}
			for i, namespace := range []string{"north", "south"} {
				page, err := service.ListVisiblePage(actor, agent, primitive.CatalogQuery{Namespace: namespace, NameTerms: []string{"review"}, Limit: 1})
				require.NoError(t, err)
				require.Len(t, page.Items, 1)
				expected := proto.Ref{ServerID: "one-connection", Name: "review", ResourceURI: entries[i].Uri, Revision: manifestRevision(entries[i])}.URI()
				require.Equal(t, expected, page.Items[0].URI, "partitioning must never rewrite exact catalog identity")
				ref, err := proto.ParseRef(page.Items[0].URI)
				require.NoError(t, err)
				require.Equal(t, entries[i].Uri, ref.ResourceURI)
			}
			transport, err := service.ListVisiblePage(actor, agent, primitive.CatalogQuery{Namespace: "one-connection"})
			require.NoError(t, err)
			require.Empty(t, transport.Items, "a connection name is not an authored namespace")
			after, _ := json.Marshal(entries)
			require.Equal(t, original, after)
			if mode == "native" {
				require.Equal(t, 1, fixture.listCalls)
			} else {
				require.Equal(t, 1, ordinary.lists)
			}
			require.Zero(t, fixture.readCalls)
		})
	}
	legacy := proto.Metadata{URI: proto.Ref{ServerID: "connection", Name: "review", ResourceURI: "https://example.test/review/SKILL.md"}.URI(), ServerID: "connection"}
	require.Equal(t, "connection", skillCatalogNamespace(legacy))
}
