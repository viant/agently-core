package resource

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	skillsvc "github.com/viant/agently-core/service/skill"
	schema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

// Use the existing generic provider fixture, but reject every tool outside its
// actual catalog. A missing-kind call therefore fails the federation test.
type subsetProvider struct {
	*dependencyMCPProvider
	calls             map[string]int
	forbidden         []string
	listOnlyNamespace string
}

func (p *subsetProvider) CallTool(ctx context.Context, request *schema.CallToolRequestParams, options ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	listed, err := p.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	advertised := false
	for _, tool := range listed.Tools {
		if tool.Name == request.Name {
			advertised = true
			break
		}
	}
	if request.Name == "namespaces/get" && request.Arguments["namespace"] == "excluded" {
		p.forbidden = append(p.forbidden, "excluded namespace")
		return nil, fmt.Errorf("configured namespace exclusion was ignored")
	}
	if !advertised {
		p.forbidden = append(p.forbidden, request.Name)
		return nil, fmt.Errorf("unsupported subset tool %s", request.Name)
	}
	p.calls[request.Name]++
	result, err := p.dependencyMCPProvider.CallTool(ctx, request, options...)
	if err == nil && request.Name == "namespaces/get" {
		capabilities := result.StructuredContent.(primitive.NamespaceCapabilities)
		if capabilities.Namespace == p.listOnlyNamespace {
			for i := range capabilities.Kinds {
				capabilities.Kinds[i].Operations = []string{"list"}
				capabilities.Kinds[i].Methods = primitive.Methods(capabilities.Kinds[i].Kind, []string{"list"})
			}
			result.StructuredContent = capabilities
		}
	}
	return result, err
}

// This is a native skills-only provider: no namespace methods or ordinary
// skill tools. Native manifests and resources/read share one owning source.
type ordinarySubsetSkills struct {
	mcpclient.Interface
	entry     schema.Skill
	body      string
	calls     map[string]int
	forbidden []string
}

func (p *ordinarySubsetSkills) Initialize(context.Context, ...mcpclient.RequestOption) (*schema.InitializeResult, error) {
	return &schema.InitializeResult{ProtocolVersion: "2025-11-25", Capabilities: schema.ServerCapabilities{Resources: &schema.ServerCapabilitiesResources{}, Extensions: map[string]map[string]interface{}{schema.SkillsExtension: {}}}}, nil
}
func (p *ordinarySubsetSkills) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	return &schema.ListToolsResult{Tools: []schema.Tool{}}, nil
}
func (p *ordinarySubsetSkills) ListSkills(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListSkillsResult, error) {
	p.calls["skills/list"]++
	return &schema.ListSkillsResult{ResultType: "complete", Skills: []schema.Skill{p.entry}}, nil
}
func (p *ordinarySubsetSkills) GetSkill(_ context.Context, uri string, _ ...mcpclient.RequestOption) (*schema.GetSkillResult, error) {
	p.calls["skills/get"]++
	if uri != p.entry.Uri {
		return nil, fmt.Errorf("unknown skill")
	}
	return &schema.GetSkillResult{ResultType: "complete", Skill: p.entry}, nil
}
func (p *ordinarySubsetSkills) CallTool(_ context.Context, request *schema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	p.forbidden = append(p.forbidden, request.Name)
	return nil, fmt.Errorf("unsupported ordinary tool")
}
func (p *ordinarySubsetSkills) ReadResource(_ context.Context, request *schema.ReadResourceRequestParams, _ ...mcpclient.RequestOption) (*schema.ReadResourceResult, error) {
	p.calls["resources/read"]++
	if request.Uri != p.entry.Uri {
		return nil, fmt.Errorf("unsupported skill resource")
	}
	return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Uri: request.Uri, Text: p.body}}}, nil
}

func TestMixedMCPProvidersSupportIndependentPrimitiveSubsets(t *testing.T) {
	now := time.Now()
	item := func(title string) *dependencyResource {
		raw, _ := json.Marshal(map[string]string{"title": title})
		return &dependencyResource{raw: raw, formatVersion: 1, revision: identity.WorkingCandidate, authorityBinding: "subset-authority", validUntil: now.Add(time.Minute)}
	}
	windows := &subsetProvider{dependencyMCPProvider: &dependencyMCPProvider{owner: "windows-and-data", items: map[string]*dependencyResource{
		"window://team/sales": item("Sales"), "datasource://team/orders": item("Orders"),
		"window://read-only/summary": item("Summary"), "window://excluded/hidden": item("Hidden"),
	}}, calls: map[string]int{}, listOnlyNamespace: "read-only"}
	intents := &subsetProvider{dependencyMCPProvider: &dependencyMCPProvider{owner: "intents-only", items: map[string]*dependencyResource{"intent://team/review": item("Review")}}, calls: map[string]int{}}
	body := "---\nname: review\ndescription: Review data\n---\nReview the selected report."
	uri := "skill://vendor/review/SKILL.md"
	skills := &ordinarySubsetSkills{body: body, calls: map[string]int{}, entry: schema.Skill{Uri: uri, Frontmatter: map[string]interface{}{"name": "review", "description": "Review data"}, Resources: schema.SkillResources{Files: []schema.SkillResource{{Uri: uri, Size: int64(len(body)), Digest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body)))}}}}}
	options := fixtureOptions{
		"skills-only":  {SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}},
		"windows-data": {PrimitiveNamespaces: []string{"team", "read-only"}},
		"intents-only": {},
	}
	clients := map[string]mcpclient.Interface{"skills-only": skills, "windows-data": windows, "intents-only": intents}
	mgr, err := manager.New(options, manager.WithClientFactory(func(_ context.Context, _, name string) (mcpclient.Interface, error) { return clients[name], nil }))
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation(""); mgr.CloseConversation("skill-catalog") })
	actor := identity.VerifiedActor{Subject: "alice", Issuer: "https://idp.example", TenantID: "team", AccountID: "account", IdentityRevision: "1", ValidUntil: now.Add(time.Minute)}
	gateway := NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(context.Context, identity.VerifiedActor) error { return nil }, "host")
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	type definition struct {
		Title string `json:"title"`
	}
	for _, test := range []struct{ kind, title, owner string }{{"window", "Sales", "windows-and-data"}, {"datasource", "Orders", "windows-and-data"}, {"intent", "Review", "intents-only"}} {
		t.Run(test.kind, func(t *testing.T) {
			rows, err := List[definition](ctx, gateway, test.kind, "team")
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, test.owner, rows[0].Connection.ProviderIdentity)
			got, err := Get[definition](ctx, gateway, rows[0].Connection, identity.ResourceRef{URI: rows[0].Resource.URI}, nil)
			require.NoError(t, err)
			require.Equal(t, test.title, got.Definition.Title)
		})
	}
	readonly, err := List[definition](ctx, gateway, "window", "read-only")
	require.NoError(t, err)
	require.Len(t, readonly, 1)
	before := windows.calls["windows/get"]
	_, err = Get[definition](ctx, gateway, readonly[0].Connection, identity.ResourceRef{URI: readonly[0].Resource.URI}, nil)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Equal(t, before, windows.calls["windows/get"], "a namespace without get must never dispatch it")
	excluded, err := List[definition](ctx, gateway, "window", "excluded")
	require.NoError(t, err)
	require.Empty(t, excluded)
	unsupported, err := List[definition](ctx, gateway, "menu", "team")
	require.NoError(t, err)
	require.Empty(t, unsupported)
	// The same manager continues to serve valid providers after an unsupported
	// kind and after a denied operation on a valid namespace.
	again, err := List[definition](ctx, gateway, "intent", "team")
	require.NoError(t, err)
	require.Len(t, again, 1)
	skillService := skillsvc.New(nil, nil, nil)
	skillService.SetMCPSource(mgr)
	agent := &agentmdl.Agent{Skills: []string{"*"}}
	listed, err := skillService.ListVisible(ctx, agent)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "skills-only", listed[0].ServerID)
	gotSkill, err := skillService.GetVisible(ctx, agent, listed[0].URI)
	require.NoError(t, err)
	require.NotNil(t, gotSkill)
	require.Positive(t, skills.calls["skills/list"])
	require.Positive(t, skills.calls["skills/get"])
	require.Positive(t, skills.calls["resources/read"])
	require.Empty(t, skills.forbidden)
	require.Empty(t, windows.forbidden)
	require.Empty(t, intents.forbidden)
	require.Zero(t, intents.calls["windows/list"])
	require.Zero(t, intents.calls["datasources/list"])
	require.Zero(t, windows.calls["intents/list"])
	require.Zero(t, windows.calls["skills/list"])
}
