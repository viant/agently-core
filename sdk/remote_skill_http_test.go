package sdk

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	authsvc "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/mcp"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

const remoteSDKSkillDocument = "---\nname: remote-review\ndescription: Owned remote review\nmetadata:\n  agently-context: inline\n---\nREMOTE_INLINE_STATIC_BODY\n"
const remoteSDKSkillURI = "skill://fixture/remote-review/SKILL.md"
const remoteSDKCredential = "owned-test-credential"

type remoteSDKSkillOptions struct{ url string }

func (p remoteSDKSkillOptions) Names(context.Context) ([]string, error) {
	return []string{"remote-fixture"}, nil
}
func (p remoteSDKSkillOptions) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	pass := true
	return &mcpcfg.MCPClient{SkillDiscovery: &mcpcfg.SkillDiscovery{Enabled: true, CatalogID: "remote-fixture"}, ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: p.url}}, Auth: &mcp.ClientAuth{PassUserToken: &pass, UseIdToken: true}}}, nil
}

type remoteSDKSkillHTTP struct {
	mu               sync.Mutex
	mode             string
	calls            map[string]int
	allAuthenticated bool
}

func (f *remoteSDKSkillHTTP) setMode(mode string) { f.mu.Lock(); f.mode = mode; f.mu.Unlock() }
func (f *remoteSDKSkillHTTP) serve(w http.ResponseWriter, r *http.Request) {
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
	w.Header().Set("Mcp-Session-Id", "skill-fixture-session")
	w.Header().Set("Content-Type", "application/json")
	var value any
	var rpc any
	sum := sha256.Sum256([]byte(remoteSDKSkillDocument))
	entry := map[string]any{"uri": remoteSDKSkillURI, "frontmatter": map[string]any{"name": "remote-review", "description": "Owned remote review", "metadata": map[string]any{"agently-context": "inline"}}, "resources": []any{map[string]any{"uri": remoteSDKSkillURI, "size": len(remoteSDKSkillDocument), "digest": fmt.Sprintf("sha256:%x", sum[:])}}}
	wrap := func(v any) any {
		body, _ := json.Marshal(v)
		return map[string]any{"structuredContent": v, "content": []any{map[string]any{"type": "text", "text": string(body)}}}
	}
	switch q.Method {
	case "initialize":
		value = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}, "resources": map[string]any{}}, "serverInfo": map[string]any{"name": "owned-skills", "version": "1"}}
	case "ping":
		value = map[string]any{}
	case "tools/list":
		value = map[string]any{"tools": []any{map[string]any{"name": "skills/list", "inputSchema": map[string]any{"type": "object"}}, map[string]any{"name": "skills/get", "inputSchema": map[string]any{"type": "object"}}}}
	case "tools/call", "resources/read":
		var p struct {
			Name string         `json:"name"`
			Args map[string]any `json:"arguments"`
			URI  string         `json:"uri"`
		}
		_ = json.Unmarshal(q.Params, &p)
		name := p.Name
		if q.Method == "resources/read" {
			name = q.Method
		}
		authed := r.Header.Get("Authorization") == "Bearer "+remoteSDKCredential
		f.mu.Lock()
		f.calls[name]++
		f.allAuthenticated = f.allAuthenticated && authed
		mode := f.mode
		f.mu.Unlock()
		if !authed || mode == "deny" {
			if q.Method == "resources/read" {
				rpc = map[string]any{"code": -32001, "message": "unavailable"}
			} else {
				value = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "unavailable"}}}
			}
			break
		}
		if q.Method == "resources/read" {
			body := remoteSDKSkillDocument
			if mode == "drift" {
				body += "changed"
			}
			value = map[string]any{"contents": []any{map[string]any{"uri": remoteSDKSkillURI, "mimeType": "text/markdown", "text": body}}}
		} else if p.Name == "skills/list" {
			value = wrap(map[string]any{"skills": []any{entry}})
		} else if p.Name == "skills/get" && p.Args["uri"] == remoteSDKSkillURI {
			value = wrap(map[string]any{"skill": entry})
		} else {
			rpc = map[string]any{"code": -32602, "message": "invalid reference"}
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

func TestSDKHTTPRemoteSkillListAndInlineActivationPreservePinnedIdentity(t *testing.T) {
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	root := t.TempDir()
	previousRoot, previousRuntimeRoot := workspace.Root(), workspace.RuntimeRoot()
	workspace.SetRoot(root)
	workspace.SetRuntimeRoot(root)
	t.Cleanup(func() { workspace.SetRoot(previousRoot); workspace.SetRuntimeRoot(previousRuntimeRoot) })
	require.NoError(t, os.MkdirAll(filepath.Join(root, "skills/local-review"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "skills/local-review/SKILL.md"), []byte("---\nname: local-review\ndescription: Local review\n---\nLOCAL_INLINE_BODY\n"), 0600))
	fixture := &remoteSDKSkillHTTP{calls: map[string]int{}, allAuthenticated: true}
	upstream := httptest.NewServer(http.HandlerFunc(fixture.serve))
	defer upstream.Close()
	mgr, err := manager.New(remoteSDKSkillOptions{upstream.URL})
	require.NoError(t, err)
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "owned", Name: "Owned"}, Skills: []string{"local-review", "remote-fixture/remote-review"}}
	rt, err := executor.NewBuilder().WithAgentFinder(&streamTestAgentFinder{agent: agent}).WithModelFinder(&streamTestModelFinder{model: &streamTestModel{}}).WithMCPManager(mgr).Build(context.Background())
	require.NoError(t, err)
	defer rt.Close(context.Background())
	backend, err := newBackendFromRuntime(rt)
	require.NoError(t, err)
	handler := NewHandler(backend)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if r.Header.Get("Authorization") == "Bearer "+remoteSDKCredential {
			ctx = authsvc.InjectTokens(authsvc.InjectUser(ctx, "owned-user"), &scyauth.Token{Token: oauth2.Token{AccessToken: remoteSDKCredential}, IDToken: remoteSDKCredential})
		}
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL, WithTokenProvider(func(ctx context.Context) (string, error) { return authsvc.MCPAuthToken(ctx, true), nil }))
	require.NoError(t, err)
	ctx := authsvc.InjectTokens(context.Background(), &scyauth.Token{Token: oauth2.Token{AccessToken: remoteSDKCredential}, IDToken: remoteSDKCredential})
	listed, err := client.ListSkills(ctx, &ListSkillsInput{AgentID: "owned"})
	require.NoError(t, err)
	require.Len(t, listed.Items, 2)
	remoteName := ""
	localFound := false
	for _, item := range listed.Items {
		if item.Name == "local-review" {
			localFound = true
		}
		if strings.HasPrefix(item.Name, "skill://remote-fixture/remote-review~") {
			remoteName = item.Name
		}
	}
	require.True(t, localFound, "local public names must remain unchanged")
	require.NotEmpty(t, remoteName, "SDK must expose original pinned remote identity, not a bare name")
	conv, err := client.CreateConversation(ctx, &CreateConversationInput{AgentID: "owned", Title: "Owned remote skill"})
	require.NoError(t, err)
	byConversation, err := client.ListSkills(ctx, &ListSkillsInput{ConversationID: conv.Id})
	require.NoError(t, err)
	require.Equal(t, listed.Items, byConversation.Items)
	active, err := client.ActivateSkill(ctx, &ActivateSkillInput{ConversationID: conv.Id, Name: remoteName})
	require.NoError(t, err)
	require.Equal(t, remoteName, active.Name)
	require.Contains(t, active.Body, "REMOTE_INLINE_STATIC_BODY")
	require.Contains(t, active.Body, `configured server "remote-fixture"`)
	anon, err := client.ListSkills(context.Background(), &ListSkillsInput{AgentID: "owned"})
	require.NoError(t, err)
	for _, item := range anon.Items {
		require.NotEqual(t, remoteName, item.Name)
	}
	t.Run("managing denial propagates", func(t *testing.T) {
		fixture.setMode("deny")
		defer fixture.setMode("")
		_, err := client.ActivateSkill(ctx, &ActivateSkillInput{ConversationID: conv.Id, Name: remoteName})
		require.Error(t, err)
		_, err = client.ListSkills(ctx, &ListSkillsInput{AgentID: "owned"})
		require.Error(t, err)
	})
	t.Run("changed manifest document fails", func(t *testing.T) {
		fixture.setMode("drift")
		defer fixture.setMode("")
		_, err := client.ActivateSkill(ctx, &ActivateSkillInput{ConversationID: conv.Id, Name: remoteName})
		require.Error(t, err)
	})
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	require.True(t, fixture.allAuthenticated)
	for _, name := range []string{"skills/list", "skills/get", "resources/read"} {
		require.Positive(t, fixture.calls[name])
	}
}
