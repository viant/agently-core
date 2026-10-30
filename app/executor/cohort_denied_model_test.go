package executor_test

import (
	"context"
	"encoding/json"
	"fmt"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	runtimediscovery "github.com/viant/agently-core/runtime/discovery"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/authz"
	"github.com/viant/forge/backend/artifactcohort"
	primitive "github.com/viant/forge/backend/provider/contract"
	identity "github.com/viant/forge/backend/resource"
	resources "github.com/viant/forge/backend/resourcehost"
)

type deniedModelFacts struct{}

func (deniedModelFacts) Resolve(context.Context) (authz.Facts, error) {
	return authz.Facts{}, authz.ErrDenied
}

// Exercise a real query and capture the actual model request. A local cohort
// denial must not remove ordinary tools or leak its namespace through hints.
func TestDeniedCohortDoesNotLeakIntoModelRequest(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		t.Run(fmt.Sprintf("scheduled=%v", scheduled), func(t *testing.T) { testDeniedCohortModelRequest(t, scheduled) })
	}
}
func testDeniedCohortModelRequest(t *testing.T, scheduled bool) {
	root := t.TempDir()
	old, oldRuntime := workspace.Root(), workspace.RuntimeRoot()
	workspace.SetRoot(root)
	workspace.SetRuntimeRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old); workspace.SetRuntimeRoot(oldRuntime) })
	t.Setenv("AGENTLY_WORKSPACE", root)
	t.Setenv("AGENTLY_RUNTIME_ROOT", root)
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	path := filepath.Join(root, "skills", "ordinary", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte("---\nname: ordinary\ndescription: Ordinary available guidance\n---\nUse available evidence."), 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "reader"})
	ctx = runtimediscovery.MergeMode(ctx, runtimediscovery.Mode{Scheduler: scheduled})
	actor := identity.VerifiedActor{Subject: "reader", Issuer: "https://identity.example", TenantID: "tenant", AccountID: "account", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Hour)}
	model := &namespaceRuntimeModel{respond: func(req *llm.GenerateRequest, _ int) *llm.GenerateResponse {
		raw, err := json.Marshal(req)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "restricted-domain")
		require.NotEmpty(t, req.Options.Tools, "unrelated tools must remain usable")
		return &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "Hello."}}}}
	}}
	agent := &agentmdl.Agent{Skills: []string{"ordinary"}, Identity: agentmdl.Identity{ID: "ordinary-agent"}, ModelSelection: llm.ModelSelection{Model: "fixture"}, Prompt: &binding.Prompt{Engine: "go", Text: "{{.Task.Prompt}}"}, Tool: agentmdl.Tool{Items: []*llm.Tool{{Name: "llm/tools:list", AlwaysInclude: true}}}}
	mgr, err := mcpmgr.New(nil)
	require.NoError(t, err)
	var metadataCalls atomic.Int64
	require.NoError(t, mgr.RegisterLocal(ctx, "restricted-domain", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "restricted-domain", ToolsListVisibility: mcpcfg.ToolsListVisibilityPrivate}, func(context.Context) (mcpclient.Interface, error) {
		metadataCalls.Add(1)
		return nil, jsonrpc.NewHTTPStatusError(http.StatusForbidden)
	}))
	rt, err := executor.NewBuilder().WithMCPManager(mgr).WithDefaults(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(root, "skills")}}}).WithPrimitiveAuthority(func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(context.Context, identity.VerifiedActor) error { return nil }, "fixture").WithAgentFinder(&skillTestAgentFinder{agent: agent}).WithModelFinder(&skillTestModelFinder{model: model}).Build(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rt.Close(context.Background())) })
	replica, err := artifactcohort.NewReplica(artifactcohort.Config{Enabled: true, Source: "restricted-domain", Tenant: "tenant", Account: "account", Caller: artifactcohort.NamespaceCaller{Namespace: "restricted-domain"}, Facts: deniedModelFacts{}, SessionKey: func(context.Context) (string, error) { return "", authz.ErrDenied }})
	require.NoError(t, err)
	source, err := artifactcohort.NewMetadataSource(replica, []string{"restricted-domain"})
	require.NoError(t, err)

	require.NoError(t, rt.InstallPublishedMetadata(resources.Connection{Name: "mcp://workspace/restricted-domain", ProviderIdentity: "restricted-domain"}, []string{"restricted-domain"}, source, "restricted-domain"))
	page, err := rt.Skills.ListVisiblePage(ctx, agent, primitive.CatalogQuery{Limit: 100})
	require.NoError(t, err)
	raw, err := json.Marshal(page)
	require.NoError(t, err)
	require.Contains(t, string(raw), "ordinary")
	require.NotContains(t, string(raw), "restricted-domain")
	output := &agentsvc.QueryOutput{}
	require.NoError(t, rt.Agent.Query(ctx, &agentsvc.QueryInput{ConversationID: "denied-cohort-chat", AgentID: agent.ID, Agent: agent, UserId: "reader", Query: "Say hello without reading business data."}, output))
	require.Positive(t, model.calls, "the conversation must reach the model")
	require.Zero(t, metadataCalls.Load(), "a cohort metadata connection is never a user tool-discovery endpoint")
}
