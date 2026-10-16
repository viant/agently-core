package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	authsvc "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/mcp"
	namespaces "github.com/viant/mcp-protocol/extension/namespaces"
	skillformat "github.com/viant/mcp-protocol/extension/skills"
	"github.com/viant/mcp-protocol/schema"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

const sharedSDKSkillURI = "skill://team/remote-review/SKILL.md"
const sharedSDKSkillDocument = "---\nname: remote-review\ndescription: Existing shared skill\nmetadata:\n  agently-context: inline\n---\nSHARED_INLINE_BODY\n"

type sharedSDKSkillOptions struct{ url string }

func (p sharedSDKSkillOptions) Names(context.Context) ([]string, error) {
	return []string{"shared-fixture"}, nil
}
func (p sharedSDKSkillOptions) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	pass := true
	return &mcpcfg.MCPClient{PrimitiveProviderIdentity: "shared-owner", ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: p.url}}, Auth: &mcp.ClientAuth{PassUserToken: &pass, UseIdToken: true}}}, nil
}

type sharedSDKSkillHTTP struct {
	mu               sync.Mutex
	mode             string
	calls            map[string]int
	allAuthenticated bool
	compiled         *skillformat.Static
}

func (f *sharedSDKSkillHTTP) setMode(mode string) { f.mu.Lock(); defer f.mu.Unlock(); f.mode = mode }
func (f *sharedSDKSkillHTTP) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}
func (f *sharedSDKSkillHTTP) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var q struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&q) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(q.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Mcp-Session-Id", "shared-skills-session")
	w.Header().Set("Content-Type", "application/json")
	var value, rpc any
	wrap := func(v any) any {
		raw, _ := json.Marshal(v)
		return map[string]any{"structuredContent": v, "content": []any{map[string]any{"type": "text", "text": string(raw)}}}
	}
	switch q.Method {
	case "initialize":
		value = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}, "resources": map[string]any{}}, "serverInfo": map[string]any{"name": "shared-only", "version": "1"}}
	case "ping":
		value = map[string]any{}
	case "tools/list":
		tools := []any{}
		for _, name := range []string{namespaces.ListToolName, namespaces.GetToolName, namespaces.ResourceGetToolName} {
			tools = append(tools, map[string]any{"name": name, "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}}})
		}
		value = map[string]any{"tools": tools}
	case "tools/call", "resources/read", "skills/list", "skills/get":
		var p struct {
			Name string         `json:"name"`
			Args map[string]any `json:"arguments"`
			URI  string         `json:"uri"`
		}
		_ = json.Unmarshal(q.Params, &p)
		name := p.Name
		if q.Method != "tools/call" {
			name = q.Method
		}
		authed := r.Header.Get("Authorization") == "Bearer "+remoteSDKCredential
		f.mu.Lock()
		f.calls[name]++
		f.allAuthenticated = f.allAuthenticated && authed
		mode := f.mode
		f.mu.Unlock()
		if !authed || mode == "deny" && name == "resources/read" {
			rpc = map[string]any{"code": 403, "message": "owning source denied"}
			break
		}
		locator, _ := namespaces.URI("opaque skills collection")
		switch name {
		case namespaces.ListToolName:
			value = wrap(namespaces.ListResult{Namespaces: []namespaces.Namespace{{URI: locator, Name: "Owned collection"}}})
		case namespaces.GetToolName:
			if p.Args["uri"] != locator {
				rpc = map[string]any{"code": -32602, "message": "wrong collection"}
				break
			}
			value = wrap(namespaces.GetResult{Resources: []namespaces.Resource{{URI: sharedSDKSkillURI, Kind: "skill", Name: "remote-review", Description: "Existing shared skill", MimeType: "application/json"}}})
		case namespaces.ResourceGetToolName:
			if p.Args["uri"] != sharedSDKSkillURI {
				rpc = map[string]any{"code": -32602, "message": "wrong definition"}
				break
			}
			value = wrap(schema.GetSkillResult{ResultType: "complete", Skill: f.compiled.Metadata()})
		case "resources/read":
			result, err := f.compiled.ReadResource(r.Context(), &schema.ReadResourceRequest{Params: schema.ReadResourceRequestParams{Uri: p.URI}})
			if err != nil {
				rpc = err
				break
			}
			if mode == "drift" {
				result.Contents[0].Text += " altered"
			}
			value = result
		default:
			rpc = map[string]any{"code": -32601, "message": "no native skill methods"}
		}
	default:
		rpc = map[string]any{"code": -32601, "message": "unsupported"}
	}
	response := map[string]any{"jsonrpc": "2.0", "id": q.ID}
	if rpc != nil {
		response["error"] = rpc
	} else {
		response["result"] = value
	}
	_ = json.NewEncoder(w).Encode(response)
}

func TestSDKHTTPSharedNamespaceSkillManifestActivationAndSupportingFiles(t *testing.T) {
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	root := t.TempDir()
	previousRoot, previousRuntimeRoot := workspace.Root(), workspace.RuntimeRoot()
	workspace.SetRoot(root)
	workspace.SetRuntimeRoot(root)
	t.Cleanup(func() { workspace.SetRoot(previousRoot); workspace.SetRuntimeRoot(previousRuntimeRoot) })
	compiled, err := (skillformat.Compiler{Source: fstest.MapFS{"SKILL.md": {Data: []byte(sharedSDKSkillDocument)}, "refs/note.txt": {Data: []byte("Original shared supporting bytes")}}}).Compile(context.Background(), sharedSDKSkillURI)
	require.NoError(t, err)
	fixture := &sharedSDKSkillHTTP{compiled: compiled, calls: map[string]int{}, allAuthenticated: true}
	upstream := httptest.NewServer(http.HandlerFunc(fixture.serve))
	defer upstream.Close()
	mgr, err := manager.New(sharedSDKSkillOptions{upstream.URL}, manager.WithUserIDExtractor(authsvc.EffectiveUserID))
	require.NoError(t, err)
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "owned", Name: "Owned"}, Skills: []string{sharedSDKSkillURI}}
	model := &sharedSkillNoModel{}
	rt, err := executor.NewBuilder().WithAgentFinder(&streamTestAgentFinder{agent: agent}).WithModelFinder(&streamTestModelFinder{model: model}).WithMCPManager(mgr).Build(context.Background())
	require.NoError(t, err)
	defer rt.Close(context.Background())
	backend, err := newBackendFromRuntime(rt)
	require.NoError(t, err)
	handler := NewHandler(backend)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token != "" {
			owner := "owned-user"
			if token != remoteSDKCredential {
				owner = "foreign-user"
			}
			ctx = authsvc.InjectTokens(authsvc.InjectUser(ctx, owner), &scyauth.Token{Token: oauth2.Token{AccessToken: token}, IDToken: token})
		}
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL, WithTokenProvider(func(ctx context.Context) (string, error) { return authsvc.MCPAuthToken(ctx, true), nil }))
	require.NoError(t, err)
	ctx := authsvc.InjectTokens(context.Background(), &scyauth.Token{Token: oauth2.Token{AccessToken: remoteSDKCredential}, IDToken: remoteSDKCredential})
	list, err := client.ListSkills(ctx, &ListSkillsInput{AgentID: "owned", CatalogQuery: primitive.CatalogQuery{Limit: 10}})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.Equal(t, sharedSDKSkillURI, list.Items[0].URI)
	require.Equal(t, "Existing shared skill", list.Items[0].Description)
	require.Zero(t, fixture.count("resource/get"))
	require.Zero(t, fixture.count("resources/read"))
	_, err = client.ListSkills(ctx, &ListSkillsInput{AgentID: "owned", CatalogQuery: primitive.CatalogQuery{Limit: 10, NameTerms: []string{"remote-review"}}})
	require.NoError(t, err)
	require.Zero(t, fixture.count("resource/get"))
	conv, err := client.CreateConversation(ctx, &CreateConversationInput{AgentID: "owned", Title: "Shared skill"})
	require.NoError(t, err)
	active, err := client.ActivateSkill(ctx, &ActivateSkillInput{ConversationID: conv.Id, Name: sharedSDKSkillURI})
	require.NoError(t, err)
	require.Contains(t, active.Body, "SHARED_INLINE_BODY")
	require.Equal(t, 1, fixture.count("resource/get"))
	// Exercise the existing SDK tool route with its supported conversation selector.
	args, _ := json.Marshal(map[string]any{"uri": sharedSDKSkillURI, "path": "refs/note.txt"})
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/tools/"+url.PathEscape("llm/skills:get")+"/execute?conversationId="+url.QueryEscape(conv.Id), strings.NewReader(string(args)))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+remoteSDKCredential)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	raw, err := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, string(raw))
	require.Contains(t, string(raw), "Original shared supporting bytes")
	require.Equal(t, 2, fixture.count("resource/get"), "a separate supporting-file operation refreshes its working manifest")
	before := fixture.count("resources/read")
	fixture.setMode("deny")
	denied, err := client.ActivateSkill(ctx, &ActivateSkillInput{ConversationID: conv.Id, Name: sharedSDKSkillURI})
	require.Error(t, err)
	require.Nil(t, denied)
	require.Equal(t, before+1, fixture.count("resources/read"), "managing denial is terminal without replay")
	fixture.setMode("drift")
	_, err = client.ActivateSkill(ctx, &ActivateSkillInput{ConversationID: conv.Id, Name: sharedSDKSkillURI})
	require.Error(t, err)
	fixture.setMode("")
	fixture.mu.Lock()
	require.True(t, fixture.allAuthenticated)
	fixture.mu.Unlock()
	foreign := authsvc.InjectTokens(context.Background(), &scyauth.Token{Token: oauth2.Token{AccessToken: "foreign-credential"}, IDToken: "foreign-credential"})
	_, err = client.ListSkills(foreign, &ListSkillsInput{AgentID: "owned", CatalogQuery: primitive.CatalogQuery{Limit: 10}})
	require.Error(t, err)
	require.Zero(t, model.calls.Load(), "inline activation must not invoke a model")
	require.Zero(t, fixture.count("skills/list"))
	require.Zero(t, fixture.count("skills/get"))
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	require.False(t, fixture.allAuthenticated, "foreign owner attempt reached owning MCP and was rejected")
}

// Any accidental model query makes this model-free integration fail.
type sharedSkillNoModel struct {
	streamTestModel
	calls atomic.Int32
}

func (m *sharedSkillNoModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.calls.Add(1)
	return nil, errors.New("unexpected model request")
}
