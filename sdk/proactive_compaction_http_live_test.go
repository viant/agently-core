package sdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider"
	"github.com/viant/agently-core/genai/llm/provider/openai"
	iauth "github.com/viant/agently-core/internal/auth"
	nativeconv "github.com/viant/agently-core/internal/service/conversation"
	registry "github.com/viant/agently-core/internal/tool/registry"
	agentproto "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/mcp/manager"
	messagesvc "github.com/viant/agently-core/protocol/tool/service/message"
	"github.com/viant/agently-core/runtime/recovery"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/core"
)

type compactionHTTPModel struct {
	*openai.Client
	calls  atomic.Int32
	mu     sync.Mutex
	counts []int
	onCall func(string, *llm.GenerateRequest)
}

func (m *compactionHTTPModel) Generate(ctx context.Context, r *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.calls.Add(1)
	if m.onCall != nil {
		m.onCall("generate", r)
	}
	return m.Client.Generate(ctx, r)
}
func (m *compactionHTTPModel) Stream(ctx context.Context, r *llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	m.calls.Add(1)
	if m.onCall != nil {
		m.onCall("stream", r)
	}
	return m.Client.Stream(ctx, r)
}
func (m *compactionHTTPModel) CountInputTokens(ctx context.Context, r *llm.GenerateRequest) (int, error) {
	n, e := m.Client.CountInputTokens(ctx, r)
	if e == nil {
		m.mu.Lock()
		m.counts = append(m.counts, n)
		m.mu.Unlock()
	}
	return n, e
}

type compactionHTTPFinder struct{ model llm.Model }

func (f compactionHTTPFinder) Find(context.Context, string) (llm.Model, error) { return f.model, nil }
func (f compactionHTTPFinder) ConfigByIDOrModel(string) *provider.Config {
	return &provider.Config{Options: provider.Options{Model: "gpt-5-nano", ContextWindow: 400000}}
}

type compactionHTTPAgentFinder struct{ agent *agentproto.Agent }

func (f compactionHTTPAgentFinder) Find(context.Context, string) (*agentproto.Agent, error) {
	return f.agent, nil
}

// This gate exercises the production HTTP handler and complete agent Query loop.
// It is opt-in because it sends requests to OpenAI; all runtime data is private.
func TestProactiveCompactionHTTPLiveNano(t *testing.T) {
	if os.Getenv("AGENTLY_PROACTIVE_HTTP_LIVE") != "1" {
		t.Skip("explicit AGENTLY_PROACTIVE_HTTP_LIVE=1 required")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY unavailable")
	}
	root := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	t.Setenv("AGENTLY_DB_DRIVER", "sqlite")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", filepath.Join(root, "compaction.db"))
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	nativeBackend, err := native.New(ctx, native.Options{WorkspaceRoot: root})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nativeBackend.Shutdown(context.Background()) })
	store, err := nativeconv.New(ctx, nativeBackend)
	require.NoError(t, err)
	mgr, err := manager.New(manager.NewRepoProvider())
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(messagesvc.New(store)))
	model := &compactionHTTPModel{Client: openai.NewClient(key, "gpt-5-nano", openai.WithHTTPClient(&http.Client{Timeout: 90 * time.Second}), openai.WithTimeout(90), openai.WithMaxTokens(8192))}
	model.onCall = func(kind string, request *llm.GenerateRequest) {
		t.Logf("provider %s start elapsed=%s calls=%d messages=%d", kind, time.Since(started), model.calls.Load(), len(request.Messages))
	}
	percent := 1.0
	agent := &agentproto.Agent{Identity: agentproto.Identity{ID: "private-http-nano"}, ModelSelection: llm.ModelSelection{Model: "private-nano", Options: &llm.Options{MaxTokens: 8192, Reasoning: &llm.Reasoning{Effort: "low"}}}, ContextCompactionPercent: &percent, Prompt: &binding.Prompt{Text: "{{.Task.Prompt}}", Engine: "go"}, SystemPrompt: &binding.Prompt{Text: "Preserve exact facts and completed operation IDs. Never execute an already completed operation. Quoting its ID is allowed and required when asked."}, Tool: agentproto.Tool{Items: []*llm.Tool{{Name: "message:remove"}}}}
	defaults := &config.Defaults{Agent: agent.ID, Model: "private-nano"}
	llmService := core.New(compactionHTTPFinder{model: model}, reg, store)
	service := agentsvc.New(llmService, compactionHTTPAgentFinder{agent: agent}, nil, reg, defaults, store, agentsvc.WithDataService(data.NewService(nativeBackend)))
	bus := streaming.NewMemoryBus(256)
	runtime, err := executor.NewBuilder().WithNativeRuntime(nativeBackend).WithConversation(store).WithData(data.NewService(nativeBackend)).WithRegistry(reg).WithCore(llmService).WithAgentService(service).WithAgentFinder(compactionHTTPAgentFinder{agent: agent}).WithModelFinder(compactionHTTPFinder{model: model}).WithMCPManager(mgr).WithDefaults(defaults).WithStreamingBus(bus).WithSkipRegistryInitialize(true).Build(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	backend, err := NewBackendFromRuntime(runtime)
	require.NoError(t, err)
	handler := NewHandler(backend)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "private-owner"})))
	}))
	defer server.Close()
	conv := apiconv.NewConversation()
	conv.SetId("private-compaction-http")
	conv.SetStatus("active")
	conv.SetCreatedByUserID("private-owner")
	conv.SetAgentId(agent.ID)
	require.NoError(t, store.PatchConversations(ctx, conv))
	meta := requestctx.TurnMeta{ConversationID: conv.Id, TurnID: "seed-history"}
	turn := apiconv.NewTurn()
	turn.SetId(meta.TurnID)
	turn.SetConversationID(meta.ConversationID)
	turn.SetStatus("succeeded")
	require.NoError(t, store.PatchTurn(ctx, turn))
	seedUser, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithStatus("completed"), apiconv.WithContent("Project codename SILVER-OTTER-718 must survive compaction. "+strings.Repeat("Historical disposable observation: the old report was checked and its result was confirmed. ", 700)))
	require.NoError(t, err)
	toolMessage, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("tool"), apiconv.WithType("tool_op"), apiconv.WithStatus("completed"), apiconv.WithContent(`{"operation":"lookup-done-718","status":"completed","result":"account 7180287 verified"}`))
	require.NoError(t, err)
	tc := apiconv.NewToolCall()
	tc.SetMessageID(toolMessage.Id)
	tc.SetTurnID(meta.TurnID)
	tc.SetOpID("lookup-done-718")
	tc.SetToolName("account-lookup")
	tc.SetToolKind("function")
	tc.SetStatus("succeeded")
	require.NoError(t, store.PatchToolCall(ctx, tc))
	post := func(body []byte) string {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/ag-ui/run", strings.NewReader(string(body)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := server.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		wire, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode, string(wire))
		return string(wire)
	}
	request := func(id string) []byte {
		body, err := json.Marshal(map[string]any{"threadId": conv.Id, "runId": "run-" + id, "messages": []any{map[string]any{"id": id, "role": "user", "content": "Quote the exact project codename and already completed lookup operation ID. Quoting an operation ID is not executing it. Answer those two strings only; do not invoke tools."}}})
		require.NoError(t, err)
		return body
	}
	firstBody := request("first-user")
	first := post(firstBody)
	t.Logf("first HTTP complete elapsed=%s calls=%d", time.Since(started), model.calls.Load())
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(first)), "RUN_FINISHED")
	assertCompactionHTTPWire(t, first, true)
	calls := model.calls.Load()
	require.Equal(t, decodeAGUISSE(t, strings.NewReader(first)), decodeAGUISSE(t, strings.NewReader(post(firstBody))))
	require.Equal(t, calls, model.calls.Load(), "durable replay must not call provider")
	t.Log("first durable replay complete")
	fresh, err := store.GetConversation(ctx, conv.Id, apiconv.WithIncludeToolCall(true), apiconv.WithIncludeModelCall(true))
	require.NoError(t, err)
	archived, summaries := 0, 0
	archivedSeedUser, archivedSeedTool, latestUserPreserved := false, false, false
	var handoff strings.Builder
	for _, turn := range fresh.GetTranscript() {
		for _, m := range turn.Message {

			if m.Archived != nil && *m.Archived == 1 {
				archived++
				if m.Id == seedUser.Id {
					archivedSeedUser = true
				}
				if m.Id == toolMessage.Id {
					archivedSeedTool = true
				}
			}
			if m.Status != nil && *m.Status == "summary" {
				summaries++
				handoff.WriteString(*m.Content)
			}
			if m.Role == "user" && m.Content != nil && strings.HasPrefix(*m.Content, "Quote the exact project codename") && (m.Archived == nil || *m.Archived == 0) {
				latestUserPreserved = true
			}
		}
	}
	require.True(t, archivedSeedUser)
	require.True(t, archivedSeedTool)
	require.True(t, latestUserPreserved)
	require.Contains(t, handoff.String(), "SILVER-OTTER-718")
	require.Contains(t, handoff.String(), "lookup-done-718")
	require.Greater(t, archived, 0)
	require.Greater(t, summaries, 0)
	require.False(t, recovery.NeedsFullHistory(fresh))
	model.mu.Lock()
	counts := append([]int(nil), model.counts...)
	model.mu.Unlock()
	require.GreaterOrEqual(t, len(counts), 2)
	require.GreaterOrEqual(t, counts[0], 4000)
	require.Less(t, counts[len(counts)-1], counts[0])
	deadline, _ := ctx.Deadline()
	t.Logf("second starting elapsed=%s remaining=%s counts=%v calls=%d", time.Since(started), time.Until(deadline), counts, model.calls.Load())
	secondBody := request("second-user")
	second := post(secondBody)
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(second)), "RUN_FINISHED")
	assertCompactionHTTPWire(t, second, false)
	calls = model.calls.Load()
	require.Equal(t, decodeAGUISSE(t, strings.NewReader(second)), decodeAGUISSE(t, strings.NewReader(post(secondBody))))
	require.Equal(t, calls, model.calls.Load())
	agent.ContextCompactionPercent = nil
	model.mu.Lock()
	beforeOff := len(model.counts)
	model.mu.Unlock()
	third := post(request("disabled-user"))
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(third)), "RUN_FINISHED")
	model.mu.Lock()
	require.Equal(t, beforeOff, len(model.counts))
	model.mu.Unlock()
	t.Logf("real HTTP/native Datly gate: tokenCounts=%v archived=%d summaries=%d providerCalls=%d replayExtraCalls=0 nilThresholdExtraCounts=0", counts, archived, summaries, model.calls.Load())
}

func assertCompactionHTTPWire(t *testing.T, wire string, expectRemoval bool) {
	t.Helper()
	starts, ends := map[string]int{}, map[string]int{}
	toolStarts, toolEnds, toolResults := map[string]int{}, map[string]int{}, map[string]int{}
	var answer strings.Builder
	removal := 0
	for _, event := range decodeAGUISSE(t, strings.NewReader(wire)) {
		switch event.Type {
		case "TEXT_MESSAGE_START":
			require.NotEmpty(t, event.MessageID)
			starts[event.MessageID]++
		case "TEXT_MESSAGE_CONTENT":
			require.Greater(t, starts[event.MessageID], 0)
			answer.WriteString(event.Delta)
		case "TEXT_MESSAGE_END":
			ends[event.MessageID]++
		case "TOOL_CALL_START":
			require.NotEmpty(t, event.ToolCallID)
			toolStarts[event.ToolCallID]++
			if strings.Contains(event.ToolCallName, "remove") {
				removal++
			}
		case "TOOL_CALL_ARGS":
			require.Greater(t, toolStarts[event.ToolCallID], 0)
		case "TOOL_CALL_END":
			toolEnds[event.ToolCallID]++
		case "TOOL_CALL_RESULT":
			toolResults[event.ToolCallID]++
		}
	}
	require.Equal(t, starts, ends, "SSE text messages must be paired")
	require.Equal(t, toolStarts, toolEnds, "SSE tool IDs must be paired")
	for id := range toolResults {
		require.Greater(t, toolStarts[id], 0, "tool result must reference a started ID")
	}
	require.Contains(t, answer.String(), "SILVER-OTTER-718")
	require.Contains(t, answer.String(), "lookup-done-718")
	if expectRemoval {
		require.Greater(t, removal, 0, "real compaction must expose its message/remove call")
	} else {
		require.Empty(t, toolStarts, "quoting completed operation IDs must not invoke tools")
	}
}
