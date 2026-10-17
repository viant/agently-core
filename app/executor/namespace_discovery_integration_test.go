package executor_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/genai/llm"
	base "github.com/viant/agently-core/genai/llm/provider/base"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	agentsvc "github.com/viant/agently-core/service/agent"
	intake "github.com/viant/agently-core/service/intake"
	resourcesvc "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

// Only model choices are scripted. Catalog discovery, source provenance,
// namespace paging, selection, schema promotion and execution use the runtime.
type namespaceRuntimeModel struct {
	calls   int
	respond func(*llm.GenerateRequest, int) *llm.GenerateResponse
}

func (m *namespaceRuntimeModel) Generate(_ context.Context, req *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.calls++
	return m.respond(req, m.calls), nil
}
func (*namespaceRuntimeModel) Implements(feature string) bool { return feature == base.CanUseTools }

// Both servers intentionally advertise the same literal method. A successful
// result therefore proves dispatch used the discovered configured source.
type namespaceRuntimeHTTP struct {
	domain, field string
	mu            sync.Mutex
	calls         []map[string]interface{}
}

func (h *namespaceRuntimeHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result interface{}
	switch req.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", h.domain+"-session")
		result = map[string]interface{}{"protocolVersion": schema.LatestProtocolVersion, "capabilities": map[string]interface{}{"tools": map[string]interface{}{}}, "serverInfo": map[string]interface{}{"name": "same-handshake-name", "version": "1"}}
	case "server/discover":
		result = map[string]interface{}{"cacheScope": "public", "capabilities": map[string]interface{}{"tools": map[string]interface{}{}}, "resultType": "complete", "supportedVersions": []string{schema.LatestProtocolVersion}, "ttlMs": 600000}
	case "ping":
		result = map[string]interface{}{}
	case "tools/list":
		result = map[string]interface{}{"tools": []interface{}{
			map[string]interface{}{"name": "readRecords", "description": h.domain + " detailed operation metadata", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{h.field: map[string]interface{}{"type": "string"}}, "required": []string{h.field}}},
			map[string]interface{}{"name": "unrelatedMutation", "description": "Unrelated optional operation", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"unrelatedSchemaMarker": map[string]interface{}{"type": "boolean"}}}},
		}}
	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if json.Unmarshal(req.Params, &params) != nil || params.Name != "readRecords" || params.Arguments[h.field] == nil {
			http.Error(w, "wrong discovered source or method", 400)
			return
		}
		h.mu.Lock()
		h.calls = append(h.calls, params.Arguments)
		h.mu.Unlock()
		body, _ := json.Marshal(map[string]interface{}{"source": h.domain, "value": params.Arguments[h.field]})
		result = map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "text", "text": string(body)}}}
	default:
		http.Error(w, "unknown method", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

type namespaceRuntimePolicy struct{ actor identity.VerifiedActor }

func (p namespaceRuntimePolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if len(candidates) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: p.actor.IdentityRevision, ValidUntil: p.actor.ValidUntil}, nil
}

func TestRuntimeQuery_NamespaceHintDiscoversExecutesAndBroadensThroughRealMCP(t *testing.T) {
	root := t.TempDir()
	previousRoot, previousRuntime := workspace.Root(), workspace.RuntimeRoot()
	workspace.SetRoot(root)
	workspace.SetRuntimeRoot(root)

	t.Cleanup(func() { workspace.SetRoot(previousRoot); workspace.SetRuntimeRoot(previousRuntime) })
	t.Setenv("AGENTLY_WORKSPACE", root)
	t.Setenv("AGENTLY_RUNTIME_ROOT", root)
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", filepath.Join(root, "trace.jsonl"))
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "namespace-user"})
	actor := identity.VerifiedActor{Subject: "namespace-user", Issuer: "fixture", TenantID: "tenant", AccountID: "account", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Hour)}
	actorSource := func(context.Context) (identity.VerifiedActor, error) { return actor, nil }
	verify := func(context.Context, identity.VerifiedActor) error { return nil }
	const owner = "namespace-fixture-owner"
	analysis := &namespaceRuntimeHTTP{domain: "analysis", field: "analysisToken"}
	planning := &namespaceRuntimeHTTP{domain: "planning", field: "planningToken"}
	a, b := httptest.NewServer(analysis), httptest.NewServer(planning)
	defer a.Close()
	defer b.Close()
	options := map[string]*mcpcfg.MCPClient{}
	for _, entry := range []struct{ server, domain, url string }{{"transport_a", "analysis", a.URL}, {"transport_b", "planning", b.URL}} {
		options[entry.server] = &mcpcfg.MCPClient{ToolsListVisibility: mcpcfg.ToolsListVisibilityPublic, ClientOptions: &mcp.ClientOptions{Name: entry.server, ProtocolVersion: schema.LatestProtocolVersion, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: entry.url + "/mcp"}}}, ResolvedIdentity: &mcpcfg.ResolvedServiceIdentity{ServiceName: entry.server, ResourceURI: "mcp://" + entry.domain + "/" + entry.server, ProviderIdentity: owner}}
	}
	mgr, err := manager.New(reportIntegrationProvider{options: options})
	require.NoError(t, err)
	var bindings []resourcesvc.LocalResourceBinding
	for _, name := range []string{"transport_a", "transport_b"} {
		option := options[name]
		uri, err := identity.ParseResourceURI(option.ResolvedIdentity.ResourceURI)
		require.NoError(t, err)
		body, err := json.Marshal(option)
		require.NoError(t, err)
		bindings = append(bindings, resourcesvc.LocalResourceBinding{URI: uri.String(), FormatVersion: 1, Resolver: func(context.Context, identity.VerifiedActor, string) (*identity.ResourceResolver, error) {
			return &identity.ResourceResolver{ProviderIdentity: owner, Policy: namespaceRuntimePolicy{actor}, Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return body, nil }}}, nil
		}})
	}
	catalog, err := resourcesvc.NewLocalProvider(resourcesvc.LocalConfig{ProviderIdentity: owner, Actor: actorSource, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: bindings, Validators: map[string]resourcesvc.LocalResourceValidator{"mcp": resourcesvc.ValidateMCPDefinition}, NamespaceMetadata: map[string]primitive.NamespaceMetadata{
		"analysis": {Description: "Inspect authorized analysis records", UseCases: []string{"Read current evidence"}},
		"planning": {Description: "Inspect authorized planning records", UseCases: []string{"Read planning evidence"}},
	}})
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(ctx, "namespace-catalog", &mcpcfg.MCPClient{PrimitiveProviderIdentity: owner, ClientOptions: &mcp.ClientOptions{}}, func(context.Context) (mcpclient.Interface, error) { return resourcesvc.NewLocalMCPClient(catalog) }))
	canonicalA, canonicalB := mcpname.Canonical("transport_a/readRecords"), mcpname.Canonical("transport_b/readRecords")
	helper := mcpname.Canonical("llm/tools:list")
	reply := func(content string, calls ...llm.ToolCall) *llm.GenerateResponse {
		return &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: content, ToolCalls: calls}}}}
	}
	toolsIn := func(req *llm.GenerateRequest) map[string]llm.ToolDefinition {
		out := map[string]llm.ToolDefinition{}
		for _, tool := range req.Options.Tools {
			out[tool.Definition.Name] = tool.Definition
		}
		return out
	}
	intakeModel := &namespaceRuntimeModel{respond: func(req *llm.GenerateRequest, call int) *llm.GenerateResponse {
		require.Equal(t, 1, call)

		text := ""
		for _, message := range req.Messages {
			text += message.Content
		}
		require.True(t, strings.Contains(text, `"kind":"namespace"`), "intake must receive namespace cards")
		require.Contains(t, text, `"name":"analysis"`)
		require.False(t, strings.Contains(text, "analysis detailed operation metadata"), "covered descriptions must be replaced by namespace metadata")
		prompting := req.Options.OutputSchema["properties"].(map[string]interface{})["prompting"].(map[string]interface{})["properties"].(map[string]interface{})
		require.Contains(t, prompting, "discoveryNamespaces")
		names := prompting["toolSelection"].(map[string]interface{})["properties"].(map[string]interface{})["names"].(map[string]interface{})["items"].(map[string]interface{})["enum"]
		require.Contains(t, names, canonicalA)
		require.Contains(t, names, canonicalB)
		return reply(`{"prompting":{"appendToolBundles":[],"toolSelection":{"names":[],"complete":true},"discoveryNamespaces":["analysis"]}}`)
	}}
	mainModel := &namespaceRuntimeModel{respond: func(req *llm.GenerateRequest, call int) *llm.GenerateResponse {
		tools := toolsIn(req)
		switch call {
		case 1:
			require.Len(t, tools, 1, "first main request must expose retained discovery helpers only")
			require.Contains(t, tools, helper)
			messages := ""
			for _, message := range req.Messages {
				messages += message.Content
			}
			require.Contains(t, messages, `["analysis"]`)
			require.Contains(t, messages, "optional hints")
			return reply("", llm.ToolCall{ID: "discover-a", Name: "llm/tools:list", Arguments: map[string]interface{}{"namespace": "analysis", "nameTerms": []string{"readRecords"}, "limit": 1}})
		case 2:
			require.Len(t, tools, 2)
			require.Contains(t, tools, canonicalA)
			require.NotContains(t, tools, canonicalB)
			require.Contains(t, tools[canonicalA].Parameters["properties"], "analysisToken")
			return reply("", llm.ToolCall{ID: "execute-a", Name: canonicalA, Arguments: map[string]interface{}{"analysisToken": "scope-a"}})
		case 3:
			require.Len(t, tools, 2)
			require.NotContains(t, tools, canonicalB)
			return reply("", llm.ToolCall{ID: "discover-outside-hint", Name: "llm/tools:list", Arguments: map[string]interface{}{"namespace": "planning", "nameTerms": []string{"readRecords"}, "limit": 1}})
		case 4:
			require.Len(t, tools, 3)
			require.Contains(t, tools, canonicalA)
			require.Contains(t, tools, canonicalB)
			require.Contains(t, tools[canonicalB].Parameters["properties"], "planningToken")
			return reply("", llm.ToolCall{ID: "execute-b", Name: canonicalB, Arguments: map[string]interface{}{"planningToken": "scope-b"}})
		case 5:
			require.Len(t, tools, 3)
			for _, definition := range tools {
				require.NotContains(t, definition.Parameters["properties"], "unrelatedSchemaMarker")
			}
			return reply("Both sources verified.")
		default:
			t.Fatalf("unexpected extra main-model call %d", call)
			return reply("unexpected")
		}
	}}
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "namespace-agent", Name: "Namespace Agent"}, ModelSelection: llm.ModelSelection{Model: "main-model"}, Prompt: &binding.Prompt{Engine: "go", Text: "{{.Task.Prompt}}"}, Tool: agentmdl.Tool{Items: []*llm.Tool{{Name: "llm/tools:list", AlwaysInclude: true}}}, Intake: agentmdl.Intake{Enabled: true, Model: "intake-model", Scope: []string{"tools"}}}
	runtime, err := executor.NewBuilder().WithMCPManager(mgr).WithPrimitiveAuthority(actorSource, verify, "fixture-host").WithAgentFinder(&skillTestAgentFinder{agent: agent}).WithModelFinder(&skillTestModelFinder{model: mainModel, byID: map[string]llm.Model{"main-model": mainModel, "intake-model": intakeModel}}).Build(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, runtime.Close(closeCtx))
	})
	require.NotNil(t, runtime.PrimitiveProviders)

	var scans atomic.Int32
	agentsvc.WithNamespaceProvider(func(ctx context.Context) ([]resourcesvc.DiscoveredNamespace, error) {
		scans.Add(1)
		page, err := runtime.PrimitiveProviders.NamespacePage(ctx, resourcesvc.CatalogQuery{Limit: 1000})
		if err != nil {
			return nil, err
		}
		if !page.Complete {
			return nil, fmt.Errorf("unexpected fixture namespace pagination")
		}
		return page.Items, nil
	})(runtime.Agent)
	output := &agentsvc.QueryOutput{}
	input := &agentsvc.QueryInput{ConversationID: "namespace-runtime-conversation", AgentID: agent.ID, Agent: agent, UserId: "namespace-user", Query: "Inspect both authorized domains", DisplayQuery: "Inspect both authorized domains"}
	require.NoError(t, runtime.Agent.Query(ctx, input, output))
	require.Equal(t, 1, intakeModel.calls)
	require.Equal(t, 5, mainModel.calls)
	require.Equal(t, int32(1), scans.Load(), "intake and all iterations share one namespace page scan")
	require.Equal(t, []string{"analysis"}, intake.FromContext(input.Context).Prompting.DiscoveryNamespaces)
	require.Contains(t, output.Content, "Both sources verified.")
	for _, source := range []*namespaceRuntimeHTTP{analysis, planning} {
		source.mu.Lock()
		calls := append([]map[string]interface{}(nil), source.calls...)
		source.mu.Unlock()
		require.Len(t, calls, 1, "exactly one execution must reach each configured HTTP MCP source")
		require.Equal(t, "scope-"+map[string]string{"analysis": "a", "planning": "b"}[source.domain], calls[0][source.field])
	}
	require.NotEqual(t, options["transport_a"].ResolvedIdentity.ProviderIdentity, "transport_a", "ownership comes from trusted resource identity, never server naming")
	require.False(t, strings.Contains(canonicalA, "analysis"), "the declared domain is independent of transport/tool naming")
}
