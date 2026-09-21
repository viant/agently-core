package skill

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	proto "github.com/viant/agently-core/protocol/skill"
	format "github.com/viant/mcp-protocol/extension/skills"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

type skillMCPFixture struct {
	client.Interface
	listCalls, readCalls int
	seenURI              string
	body                 string
	repeat               bool
	malformed            bool
	hide                 bool
	noExtension          bool
}

func (f *skillMCPFixture) Initialize(context.Context, ...client.RequestOption) (*schema.InitializeResult, error) {
	return &schema.InitializeResult{}, nil
}
func (f *skillMCPFixture) ListTools(context.Context, *string, ...client.RequestOption) (*schema.ListToolsResult, error) {
	return &schema.ListToolsResult{}, nil
}
func (f *skillMCPFixture) Discover(context.Context, ...client.RequestOption) (*schema.DiscoverResult, error) {
	if f.noExtension {
		return &schema.DiscoverResult{}, nil
	}
	return &schema.DiscoverResult{Capabilities: schema.ServerCapabilities{Resources: &schema.ServerCapabilitiesResources{}, Extensions: map[string]map[string]interface{}{schema.SkillsExtension: {}}}}, nil
}
func (f *skillMCPFixture) entry() schema.Skill {
	front, _ := format.Frontmatter([]byte(f.body))
	uri := "skill://vendor/review/SKILL.md"
	return schema.Skill{Uri: uri, Frontmatter: front, Resources: schema.SkillResources{Files: []schema.SkillResource{{Uri: uri, Size: int64(len(f.body)), Digest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(f.body)))}}}}
}
func (f *skillMCPFixture) GetSkill(ctx context.Context, uri string, _ ...client.RequestOption) (*schema.GetSkillResult, error) {
	if authctx.EffectiveUserID(ctx) != "alice" {
		return nil, fmt.Errorf("not found")
	}
	ttl := 0
	return &schema.GetSkillResult{ResultType: "complete", Skill: f.entry(), TtlMs: &ttl, CacheScope: "private"}, nil
}
func (f *skillMCPFixture) ListSkills(ctx context.Context, _ *string, _ ...client.RequestOption) (*schema.ListSkillsResult, error) {
	f.listCalls++
	ttl := 0
	result := &schema.ListSkillsResult{ResultType: "complete", TtlMs: &ttl, CacheScope: "private"}
	if authctx.EffectiveUserID(ctx) == "alice" && !f.hide {
		result.Skills = []schema.Skill{f.entry()}
	}
	if f.repeat {
		c := "same"
		result.NextCursor = &c
	}
	return result, nil
}

func TestMCPDirectGetAndPinnedManifest(t *testing.T) {
	f := &skillMCPFixture{hide: true, body: "---\nname: review\ndescription: x\n---\nbody"}
	s := &Service{mcpSource: &skillSourceFixture{f: f}, registry: proto.NewRegistry()}
	a := &agentmdl.Agent{Skills: []string{"*"}}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	ref, err := normalizeSkillInput("", "skill://vendor/review/SKILL.md", "github")
	require.NoError(t, err)
	item, err := s.GetVisible(ctx, a, ref)
	require.NoError(t, err)
	require.Zero(t, f.listCalls)
	f.body += " changed"
	_, err = s.GetVisible(ctx, a, item.Identity())
	require.ErrorContains(t, err, "changed")
	f.noExtension = true
	_, err = s.GetVisible(ctx, a, ref)
	require.ErrorContains(t, err, "extension not declared")
}
func (f *skillMCPFixture) ReadResource(ctx context.Context, in *schema.ReadResourceRequestParams, _ ...client.RequestOption) (*schema.ReadResourceResult, error) {
	f.readCalls++
	f.seenURI = in.Uri
	uri := in.Uri
	if f.malformed {
		uri = "skill://other/review/SKILL.md"
	}
	return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Uri: uri, Text: f.body}}}, nil
}

type skillSourceFixture struct {
	f    *skillMCPFixture
	gets int
}

func (s *skillSourceFixture) Names(context.Context) ([]string, error) { return []string{"github"}, nil }
func (s *skillSourceFixture) Options(context.Context, string) (*cfg.MCPClient, error) {
	return &cfg.MCPClient{SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}, nil
}
func (s *skillSourceFixture) Get(context.Context, string, string) (client.Interface, error) {
	s.gets++
	return s.f, nil
}
func (s *skillSourceFixture) WithAuthTokenContext(ctx context.Context, _ string) context.Context {
	return ctx
}
func (s *skillSourceFixture) UseIDToken(context.Context, string) bool { return false }

func TestMCPFederationUserBoundaryAndExactURI(t *testing.T) {
	f := &skillMCPFixture{body: "---\nname: review\ndescription: Review changes\n---\nRead carefully."}
	source := &skillSourceFixture{f: f}
	s := &Service{mcpSource: source, registry: proto.NewRegistry()}
	a := &agentmdl.Agent{Skills: []string{"*"}}
	anon, err := s.ListVisible(context.Background(), a)
	require.NoError(t, err)
	require.Empty(t, anon)
	require.Zero(t, source.gets)
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	list, err := s.ListVisible(alice, a)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Contains(t, list[0].URI, "skill://github/review~")
	require.Zero(t, f.readCalls)
	list, err = s.ListVisible(bob, a)
	require.NoError(t, err)
	require.Empty(t, list)
	_, err = s.GetVisible(bob, a, "skill://github/review")
	require.Error(t, err)
	require.Zero(t, f.readCalls)
	item, err := s.GetVisible(alice, a, "skill://github/review")
	require.NoError(t, err)
	require.Equal(t, "review", item.Frontmatter.Name)
	require.Contains(t, item.Identity(), "skill://github/review~")
	require.Equal(t, "skill://vendor/review/SKILL.md", f.seenURI)
	_, err = s.GetVisible(alice, &agentmdl.Agent{Skills: []string{"*", "!github/*"}}, "skill://github/review")
	require.Error(t, err)
}

func TestMCPRejectsRepeatedCursorAndUnsafeDocuments(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	a := &agentmdl.Agent{Skills: []string{"*"}}
	f := &skillMCPFixture{repeat: true, body: "---\nname: review\ndescription: x\n---\nbody"}
	s := &Service{mcpSource: &skillSourceFixture{f: f}, registry: proto.NewRegistry()}
	_, err := s.ListVisible(ctx, a)
	require.Error(t, err)
	require.LessOrEqual(t, f.listCalls, 2)
	require.Zero(t, f.readCalls)
	f.repeat = false
	for _, body := range []string{
		"---\nname: review\n---\nbody",
		"---\nname: other\ndescription: x\n---\nbody",
		"---\nname: review\ndescription: x\nmetadata:\n  agently-preprocess: true\n---\nbody",
	} {
		f.body = body
		_, err = s.GetVisible(ctx, a, "github/review")
		require.Error(t, err)
	}
	f.body = "---\nname: review\ndescription: x\n---\nbody"
	f.malformed = true
	_, err = s.GetVisible(ctx, a, "github/review")
	require.Error(t, err)
}
