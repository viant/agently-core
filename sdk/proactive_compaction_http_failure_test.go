package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	iauth "github.com/viant/agently-core/internal/auth"
	nativeconv "github.com/viant/agently-core/internal/service/conversation"
	registry "github.com/viant/agently-core/internal/tool/registry"
	agentproto "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/mcp/manager"
	messagesvc "github.com/viant/agently-core/protocol/tool/service/message"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/core"
)

type failingRemovalHTTPModel struct {
	calls atomic.Int32
	oldID string
}

func (m *failingRemovalHTTPModel) Implements(string) bool { return false }
func (m *failingRemovalHTTPModel) CountInputTokens(context.Context, *llm.GenerateRequest) (int, error) {
	return 10000, nil
}
func (m *failingRemovalHTTPModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.calls.Add(1)
	call := llm.NewToolCall("failed-removal", "message-remove", map[string]interface{}{"tuples": []interface{}{map[string]interface{}{"messageIds": []interface{}{m.oldID}, "summary": ""}}}, "")
	return &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, FinishReason: "tool_calls"}}}, nil
}

// The full HTTP Query path must surface real removal validation errors before
// it starts a fresh assistant generation. Durable replay must not retry them.
func TestProactiveCompactionHTTPFailedRemovalStopsContinuation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	t.Setenv("AGENTLY_DB_DRIVER", "sqlite")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", filepath.Join(root, "private.db"))
	ctx := context.Background()
	nativeBackend, err := native.New(ctx, native.Options{WorkspaceRoot: root})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nativeBackend.Shutdown(ctx) })
	store, err := nativeconv.New(ctx, nativeBackend)
	require.NoError(t, err)
	mgr, err := manager.New(manager.NewRepoProvider())
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(messagesvc.New(store)))
	percent := 1.0
	agent := &agentproto.Agent{Identity: agentproto.Identity{ID: "private-failure"}, ModelSelection: llm.ModelSelection{Model: "fixture"}, ContextCompactionPercent: &percent, Prompt: &binding.Prompt{Text: "{{.Task.Prompt}}", Engine: "go"}, Tool: agentproto.Tool{Items: []*llm.Tool{{Name: "message:remove"}}}}
	conv := apiconv.NewConversation()
	conv.SetId("private-failure-thread")
	conv.SetCreatedByUserID("owner")
	conv.SetAgentId(agent.ID)
	require.NoError(t, store.PatchConversations(ctx, conv))
	meta := requestctx.TurnMeta{ConversationID: conv.Id, TurnID: "history"}
	turn := apiconv.NewTurn()
	turn.SetId(meta.TurnID)
	turn.SetConversationID(meta.ConversationID)
	turn.SetStatus("succeeded")
	require.NoError(t, store.PatchTurn(ctx, turn))
	old, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithStatus("completed"), apiconv.WithContent("Preserve this completed history."))
	require.NoError(t, err)
	model := &failingRemovalHTTPModel{oldID: old.Id}
	finder := compactionHTTPFinder{model: model}
	defaults := &config.Defaults{Agent: agent.ID, Model: "fixture"}
	llmService := core.New(finder, reg, store)
	service := agentsvc.New(llmService, compactionHTTPAgentFinder{agent: agent}, nil, reg, defaults, store, agentsvc.WithDataService(data.NewService(nativeBackend)))
	runtime, err := executor.NewBuilder().WithNativeRuntime(nativeBackend).WithConversation(store).WithData(data.NewService(nativeBackend)).WithRegistry(reg).WithCore(llmService).WithAgentService(service).WithAgentFinder(compactionHTTPAgentFinder{agent: agent}).WithModelFinder(finder).WithMCPManager(mgr).WithDefaults(defaults).WithStreamingBus(streaming.NewMemoryBus(128)).WithSkipRegistryInitialize(true).Build(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	backend, err := NewBackendFromRuntime(runtime)
	require.NoError(t, err)
	handler := NewHandler(backend)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "owner"})))
	}))
	defer server.Close()
	body := `{"threadId":"private-failure-thread","runId":"failed-run","messages":[{"id":"new-user","role":"user","content":"Continue"}]}`
	server.URL += "/v1/ag-ui/run"
	status, wire := durablePost(t, server, body, nil)
	require.Equal(t, 200, status, wire)
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(wire)), "RUN_ERROR")
	require.Contains(t, wire, "proactive history compaction failed")
	require.Contains(t, wire, "handoff summary")
	require.EqualValues(t, 1, model.calls.Load())
	status, replay := durablePost(t, server, body, nil)
	require.Equal(t, 200, status, replay)
	require.Equal(t, decodeAGUISSE(t, strings.NewReader(wire)), decodeAGUISSE(t, strings.NewReader(replay)))
	require.EqualValues(t, 1, model.calls.Load())
	fresh, err := store.GetConversation(ctx, conv.Id, apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	failedTool := false
	for _, turn := range fresh.GetTranscript() {
		for _, message := range turn.Message {
			if message.Id == old.Id {
				require.True(t, message.Archived == nil || *message.Archived == 0)
			}
			require.True(t, message.Status == nil || *message.Status != "summary")
			if message.Role == "tool" && message.Content != nil && strings.Contains(*message.Content, "handoff summary") {
				failedTool = true
				require.Equal(t, "failed", *message.Status)
			}
		}
	}
	require.True(t, failedTool)
	// The failed run's durable event payload remains valid JSON.
	for _, event := range decodeAGUISSE(t, strings.NewReader(wire)) {
		require.True(t, json.Valid(event.Standard))
	}
}
