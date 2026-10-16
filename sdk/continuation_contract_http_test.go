package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/openai"
	iauth "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	resource "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type contractHTTPModels struct{ model llm.Model }

func (f contractHTTPModels) Find(context.Context, string) (llm.Model, error) { return f.model, nil }

type contractHTTPAgents map[string]*agentmdl.Agent

func (f contractHTTPAgents) Find(_ context.Context, id string) (*agentmdl.Agent, error) {
	return f[id], nil
}
func (f contractHTTPAgents) All() []*agentmdl.Agent {
	out := []*agentmdl.Agent{}
	for _, a := range f {
		out = append(out, a)
	}
	return out
}

func TestNativeAGUIAgentChangeDoesNotReuseHostedInstructions(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]any
	states := map[string]string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, request)
		raw, _ := json.Marshal(request["input"])
		state := ""
		if previous, _ := request["previous_response_id"].(string); previous != "" {
			state = states[previous]
		}
		instructions := fmt.Sprint(request["instructions"]) + string(raw)
		if strings.Contains(instructions, "CONTRACT_AGENT_A") {
			state = "A"
		}
		if strings.Contains(instructions, "CONTRACT_AGENT_B") {
			state = "B"
		}
		if state == "" {
			state = "missing"
		}
		id := fmt.Sprintf("resp-%d", len(requests))
		states[id] = state
		text := "answer-" + state
		response := map[string]any{"id": id, "object": "response", "status": "completed", "model": "gpt-4o-mini", "output": []any{map[string]any{"id": "message-" + id, "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text}}}}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1, "total_tokens": 11}}
		body, _ := json.Marshal(response)
		if streaming, _ := request["stream"].(bool); streaming {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":%q,\"delta\":%q}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", "message-"+id, text, body)
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}
	}))
	defer upstream.Close()
	enabled := true
	model := openai.NewClient("fixture-only", "gpt-4o-mini", openai.WithBaseURL(upstream.URL), openai.WithContextContinuation(&enabled))
	ctx := recoveryContext()
	runtime, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Shutdown(ctx)) })
	conv, err := convservice.New(ctx, runtime)
	require.NoError(t, err)
	database := data.NewService(runtime)
	agents := contractHTTPAgents{}
	for _, id := range []string{"A", "B"} {
		a := &agentmdl.Agent{Identity: agentmdl.Identity{ID: id}, ModelSelection: llm.ModelSelection{Model: "contract-openai"}, SystemPrompt: &binding.Prompt{Text: "CONTRACT_AGENT_" + id}, Prompt: &binding.Prompt{Text: "{{.Task.Prompt}}"}}
		a.Init()
		agents[id] = a
	}
	generator := core.New(contractHTTPModels{model}, nil, conv)
	var account atomic.Value
	account.Store("account-a")
	generator.SetContinuationAuthority(func(context.Context) (resource.VerifiedActor, error) {
		return resource.VerifiedActor{Subject: "owner", Issuer: "fixture-issuer", TenantID: "tenant-a", AccountID: account.Load().(string), IdentityRevision: "revision-1", ValidUntil: time.Now().Add(time.Hour)}, nil
	}, func(context.Context, resource.VerifiedActor) error { return nil })
	service := agentsvc.New(generator, agents, nil, nil, &config.Defaults{}, conv, agentsvc.WithDataService(database))
	bus := streaming.NewMemoryBus(128)
	// Native conversation publisher exposes persisted authoritative events.
	service.SetElicitationStreamPublisher(bus)
	conv.SetStreamPublisher(bus)
	backend := &backendClient{agent: service, conv: conv, data: database, goalInvoker: runtime, streaming: bus}
	handler := NewHandler(backend)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "owner"})))
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL)
	require.NoError(t, err)
	queryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first, err := client.QueryAGUI(queryCtx, &agentsvc.QueryInput{AgentID: "A", Query: "first"}, nil)
	require.NoError(t, err)
	require.Equal(t, "answer-A", first.Content)
	second, err := client.QueryAGUI(queryCtx, &agentsvc.QueryInput{ConversationID: first.ConversationID, AgentID: "A", Query: "same agent"}, nil)
	require.NoError(t, err)
	require.Equal(t, "answer-A", second.Content)
	third, err := client.QueryAGUI(queryCtx, &agentsvc.QueryInput{ConversationID: first.ConversationID, AgentID: "B", Query: "changed agent"}, nil)
	require.NoError(t, err)
	require.Equal(t, "answer-B", third.Content)
	account.Store("account-b")
	_, err = client.QueryAGUI(queryCtx, &agentsvc.QueryInput{ConversationID: first.ConversationID, AgentID: "A", Query: "changed verified account"}, nil)
	require.Error(t, err, "same subject must not replay history from a different verified account")
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, requests, 3, "changed account must dispatch zero provider calls")
	require.NotEmpty(t, requests[1]["previous_response_id"], "unchanged trusted contract must retain continuation")
	require.Empty(t, requests[2]["previous_response_id"], "changed agent must send full current instructions")
}
