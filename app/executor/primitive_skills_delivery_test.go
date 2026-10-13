package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agent "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	proto "github.com/viant/agently-core/protocol/skill"
	resources "github.com/viant/agently-core/service/resource"
	skills "github.com/viant/agently-core/service/skill"
	"github.com/viant/mcp"
	format "github.com/viant/mcp-protocol/extension/skills"
	protocol "github.com/viant/mcp-protocol/server"
	client "github.com/viant/mcp/client"
	server "github.com/viant/mcp/server"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

func TestNativeSkillGatewayCapturesFilesAndFreshlyChecksCaller(t *testing.T) {
	root := t.TempDir()
	body := "---\nname: review\ndescription: Review synthetic content\nallowed-tools: system/exec:execute\n---\nOriginal local body."
	require.NoError(t, os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(body), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "refs"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "refs/note.txt"), []byte("original supporting bytes"), 0600))
	local, _, err := proto.Parse(filepath.Join(root, "SKILL.md"), root, "workspace", body)
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	actor, verify := resources.PublicReadActors()
	var denied atomic.Bool
	checked := func(ctx context.Context) (identity.VerifiedActor, error) {
		if denied.Load() || authctx.EffectiveUserID(ctx) != "alice" {
			return identity.VerifiedActor{}, identity.ErrResourceDenied
		}
		return actor(ctx)
	}
	gateway := resources.NewGateway(mgr, checked, verify, "host")
	t.Cleanup(gateway.Close)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	d, err := newPrimitiveSkillDelivery(ctx, gateway, []*proto.Skill{local})
	require.NoError(t, err)
	got, err := d.ReadLocal(ctx, local)
	require.NoError(t, err)
	require.Equal(t, local.Body, got.Body)
	require.Equal(t, local.Root, got.Root)
	require.Equal(t, local.Frontmatter.AllowedTools, got.Frontmatter.AllowedTools)
	support, err := d.ReadSupporting(ctx, got, "refs/note.txt")
	require.NoError(t, err)
	require.Equal(t, "original supporting bytes", string(support))
	raw, owned, err := d.ReadNativeFile(ctx, "file://"+filepath.Join(root, "SKILL.md"))
	require.NoError(t, err)
	require.True(t, owned)
	require.Equal(t, body, string(raw))
	raw, owned, err = d.ReadNativeFile(ctx, "file://"+filepath.Join(root, "refs/note.txt"))
	require.NoError(t, err)
	require.True(t, owned)
	require.Equal(t, string(support), string(raw))
	service := skills.New(nil, nil, nil)
	service.SetDelivery(d.source, d)
	page, err := service.ListVisiblePage(ctx, &agent.Agent{Skills: []string{"*"}}, primitive.CatalogQuery{Namespace: "workspace"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	got, err = service.GetVisible(ctx, &agent.Agent{Skills: []string{"*"}}, page.Items[0].URI)
	require.NoError(t, err)
	require.Equal(t, root, got.Root)
	denied.Store(true)
	_, err = service.GetVisible(ctx, &agent.Agent{Skills: []string{"*"}}, page.Items[0].URI)
	require.Error(t, err)
	denied.Store(false)
	foreign := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	_, err = d.ReadLocal(foreign, local)
	require.Error(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "refs/note.txt"), []byte("changed supporting bytes"), 0600))
	retained, err := d.ReadLocal(ctx, local)
	require.NoError(t, err)
	require.Equal(t, local.Body, retained.Body)
	unchanged, err := d.ReadSupporting(ctx, retained, "refs/note.txt")
	require.NoError(t, err)
	require.Equal(t, "original supporting bytes", string(unchanged), "file edits cannot replace captured support bytes")

}

func TestProtectedHTTPNativeSkillsUseGatewayFreshDelivery(t *testing.T) {
	files := fstest.MapFS{"SKILL.md": {Data: []byte("---\nname: review\ndescription: Protected synthetic review\nallowed-tools: lookup\n---\nRemote synthetic body.")}, "refs/note.txt": {Data: []byte("remote supporting")}}
	compiled, err := (format.Compiler{Source: files}).Compile(context.Background(), "skill://team/review/SKILL.md")
	require.NoError(t, err)
	srv, err := server.New(server.WithNewHandler(protocol.WithDefaultHandler(context.Background(), func(h *protocol.DefaultHandler) error { return h.RegisterStaticSkill(compiled) })))
	require.NoError(t, err)
	srv.UseStreamableHTTP(true)
	handler := srv.HTTP(context.Background(), "").Handler
	var denied atomic.Bool
	var lists, gets, reads, namespaces atomic.Int64
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-alice" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var req struct{ Method string }
		_ = json.Unmarshal(raw, &req)
		switch req.Method {
		case "skills/list":
			lists.Add(1)
		case "skills/get":
			gets.Add(1)
		case "resources/read":
			reads.Add(1)
		case "tools/call":
			namespaces.Add(1)
		}
		if denied.Load() && (req.Method == "skills/get" || req.Method == "resources/read") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	options := skillOptions{"only-skills": {ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: httpServer.URL + "/mcp"}}}, SkillDiscovery: &cfg.SkillDiscovery{Enabled: true}}}
	mgr, err := manager.New(options)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("skill-catalog"); mgr.CloseConversation("") })
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(gateway.Close)
	d, err := newPrimitiveSkillDelivery(context.Background(), gateway, nil)
	require.NoError(t, err)
	service := skills.New(nil, nil, nil)
	service.SetDelivery(d.source, d)
	a := &agent.Agent{Skills: []string{"*"}}
	ctx := authctx.WithUserInfo(authctx.WithBearer(context.Background(), "synthetic-alice"), &authctx.UserInfo{Subject: "alice"})
	page, err := service.ListVisiblePage(ctx, a, primitive.CatalogQuery{Namespace: "team"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	before := lists.Load()
	_, err = service.ListVisiblePage(ctx, a, primitive.CatalogQuery{NameTerms: []string{"review"}})
	require.NoError(t, err)
	require.Equal(t, before, lists.Load(), "filter/page must reuse manager snapshot")
	require.Zero(t, gets.Load(), "filtering must not fetch manifests")
	require.Zero(t, reads.Load(), "filtering must not fetch skill document/support bytes")
	got, err := service.GetVisible(ctx, a, page.Items[0].URI)
	require.NoError(t, err)
	require.Equal(t, "", got.Root)
	require.Equal(t, "mcp", got.Source)
	require.Equal(t, "", got.Frontmatter.AllowedTools)
	require.Equal(t, "only-skills:lookup", got.RequestedTools)
	require.Greater(t, gets.Load(), int64(0))
	require.Greater(t, reads.Load(), int64(0))
	require.Zero(t, namespaces.Load(), "ordinary native skill server must never receive namespace methods")
	denied.Store(true)
	got, err = service.GetVisible(ctx, a, page.Items[0].URI)
	require.Error(t, err)
	require.Nil(t, got, "warm metadata cannot grant delivery")
	denied.Store(false)
	foreign := authctx.WithUserInfo(authctx.WithBearer(context.Background(), "synthetic-bob"), &authctx.UserInfo{Subject: "bob"})
	_, err = service.GetVisible(foreign, a, page.Items[0].URI)
	require.Error(t, err)
}

type skillOptions map[string]*cfg.MCPClient

func (p skillOptions) Names(context.Context) ([]string, error) {
	var names []string
	for name := range p {
		names = append(names, name)
	}
	return names, nil
}
func (p skillOptions) Options(_ context.Context, name string) (*cfg.MCPClient, error) {
	return p[name], nil
}

type skillRevision struct {
	uri   string
	actor identity.VerifiedActor
}

func (p skillRevision) SelectRevision(_ context.Context, ref identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if ref.URI != p.uri || len(c) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "synthetic-skills", ValidUntil: p.actor.ValidUntil}, nil
}
func registerNamespaceSkill(t *testing.T, mgr *manager.Manager, uri string, denied *atomic.Bool) {
	t.Helper()
	parsed, err := identity.ParseResourceURI(uri)
	require.NoError(t, err)
	actor, verify := resources.PublicReadActors()
	raw := json.RawMessage(`{"Frontmatter":{"Name":"review","Description":"Namespace skill","AllowedTools":"system/exec:execute lookup"},"Body":"Original remote instruction","Root":"/forged/local","Source":"workspace"}`)
	provider, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: "synthetic-namespace-owner", Actor: actor, Verify: verify, Authorize: func(ctx context.Context, a identity.VerifiedActor, u identity.ResourceURI, action string) error {
		if denied.Load() {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: []resources.LocalResourceBinding{{URI: uri, Description: "Namespace skill", FormatVersion: 1, Resolver: func(ctx context.Context, a identity.VerifiedActor, action string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{ProviderIdentity: "synthetic-namespace-owner", Source: &identity.LocalResource{URI: parsed, Load: func(context.Context) (json.RawMessage, error) { return append(json.RawMessage(nil), raw...), nil }}, Policy: skillRevision{uri: uri, actor: a}}, nil
	}}}, Validators: map[string]resources.LocalResourceValidator{"skill": func(int64, json.RawMessage) error { return nil }}})
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(context.Background(), "namespace-skills", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "synthetic-namespace-owner"}, func(context.Context) (client.Interface, error) { return resources.NewLocalMCPClient(provider) }))
}
func TestNamespaceAndNativeSkillsShareGatewayAndRejectCollision(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation(""); mgr.CloseConversation("skill-catalog") })
	var denied atomic.Bool
	uri := "skill://studio/nested/review"
	registerNamespaceSkill(t, mgr, uri, &denied)
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(gateway.Close)
	d, err := newPrimitiveSkillDelivery(context.Background(), gateway, nil)
	require.NoError(t, err)
	service := skills.New(nil, nil, nil)
	service.SetDelivery(d.source, d)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	a := &agent.Agent{Skills: []string{"*"}}
	page, err := service.ListVisiblePage(ctx, a, primitive.CatalogQuery{Namespace: "studio", DescriptionTerms: []string{"namespace"}})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, uri, page.Items[0].URI)
	got, err := service.GetVisible(ctx, a, uri)
	require.NoError(t, err)
	require.Equal(t, "mcp", got.Source)
	require.Empty(t, got.Root)
	require.Empty(t, got.Frontmatter.AllowedTools)
	require.Contains(t, got.RequestedTools, "system/exec:execute")
	require.Error(t, skills.ValidateExecution(skills.WithConstraints(ctx, skills.BuildConstraints([]*proto.Skill{got})), "system/exec:execute", nil), "remote requested tools never grant local execution")
	denied.Store(true)
	_, err = service.GetVisible(ctx, a, uri)
	require.Error(t, err, "warm catalog does not grant selected GET")

	collisionMgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { collisionMgr.CloseConversation(""); collisionMgr.CloseConversation("skill-catalog") })
	denied.Store(false)
	registerNamespaceSkill(t, collisionMgr, "skill://workspace/review/SKILL.md", &denied)
	collisionGateway := resources.NewGateway(collisionMgr, actor, verify, "host")
	t.Cleanup(collisionGateway.Close)
	root := t.TempDir()
	body := "---\nname: review\ndescription: Native collision\n---\nNative body."
	require.NoError(t, os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(body), 0600))
	local, _, err := proto.Parse(filepath.Join(root, "SKILL.md"), root, "workspace", body)
	require.NoError(t, err)
	collision, err := newPrimitiveSkillDelivery(ctx, collisionGateway, []*proto.Skill{local})
	require.NoError(t, err)
	_, handled, err := collision.Read(ctx, "skill://workspace/review/SKILL.md")
	require.True(t, handled)
	require.ErrorIs(t, err, resources.ErrCollision)
}
