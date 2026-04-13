package query

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/genai/llm/provider"
	modelfinder "github.com/viant/agently-core/internal/finder/model"
	agentfinder "github.com/viant/agently-core/protocol/agent/finder"
	agentloader "github.com/viant/agently-core/protocol/agent/loader"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/protocol/tool"
	llmagents "github.com/viant/agently-core/protocol/tool/service/llm/agents"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/sdk"
	agentsvc "github.com/viant/agently-core/service/agent"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	wsfs "github.com/viant/agently-core/workspace/loader/fs"
	modelloader "github.com/viant/agently-core/workspace/loader/model"
	meta "github.com/viant/agently-core/workspace/service/meta"
)

const (
	asyncE2ETimeout    = 15 * time.Second
	asyncE2EMinRuntime = 500 * time.Millisecond
)

// stubMCPProvider satisfies mcpmgr.Provider for tests that don't use MCP servers.
type stubMCPProvider struct{}

func (s *stubMCPProvider) Options(_ context.Context, _ string) (*mcpcfg.MCPClient, error) {
	return nil, fmt.Errorf("no MCP servers configured in test")
}

func skipIfNoAPIKey(t *testing.T) {
	t.Helper()
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set; skipping e2e query test")
	}
}

func skipIfNoExtendedE2E(t *testing.T) {
	t.Helper()
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENTLY_E2E_EXTENDED"))) {
	case "1", "true", "yes", "y", "on":
		return
	}
	t.Skip("AGENTLY_E2E_EXTENDED not set; skipping long-running live LLM e2e")
}

// setupSDK creates an in-memory embedded SDK client backed by the testdata workspace.
func setupSDK(t *testing.T) sdk.Client {
	t.Helper()
	ctx := context.Background()

	// 1. Use a temp dir as the runtime workspace for db/index files.
	//    Agent/model configs are loaded from the embedded testdata via embed.FS.
	tmp := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tmp)
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")

	// 2. Resolve testdata path (go test runs from package dir)
	testdataDir, err := filepath.Abs("testdata")
	require.NoError(t, err, "resolve testdata path")
	prepareWorkspaceForEmbeddedE2E(t, testdataDir, tmp)

	// 3. Agent loader from testdata (loader adds agents/ prefix)
	fs := afs.New()
	wsMeta := meta.New(fs, testdataDir)
	agentLdr := agentloader.New(agentloader.WithMetaService(wsMeta))
	agentFndr := agentfinder.New(agentfinder.WithLoader(agentLdr))

	// 4. Model finder from testdata (loader adds models/ prefix)
	modelMeta := wsMeta
	modelLdr := modelloader.New(wsfs.WithMetaService[provider.Config](modelMeta))
	modelFndr := modelfinder.New(modelfinder.WithConfigLoader(modelLdr))

	// 5. MCP manager (stub) and tool registry
	mcpMgr, err := mcpmgr.New(&stubMCPProvider{})
	require.NoError(t, err, "create MCP manager")
	registry, err := tool.NewDefaultRegistry(mcpMgr)
	require.NoError(t, err, "create tool registry")

	// 6. Build executor runtime — lets builder auto-create DAO (via convsvc.NewDatly),
	//    conversation, and data services, ensuring components are registered exactly once.
	rt, err := executor.NewBuilder().
		WithAgentFinder(agentFndr).
		WithModelFinder(modelFndr).
		WithRegistry(registry).
		WithMCPManager(mcpMgr).
		WithElicitationRouter(elicrouter.New()).
		WithDefaults(&config.Defaults{
			Model:                 "openai_gpt-5.2",
			ElicitationTimeoutSec: 1,
		}).
		Build(ctx)
	require.NoError(t, err, "build runtime")
	tool.AddInternalService(rt.Registry, llmagents.New(rt.Agent, llmagents.WithConversationClient(rt.Conversation)))

	// 7. Endpoint-backed local SDK client
	client, closeClient, err := sdk.NewLocalHTTPFromRuntime(ctx, rt)
	require.NoError(t, err, "create local HTTP SDK client")
	t.Cleanup(closeClient)
	return client
}

func prepareWorkspaceForEmbeddedE2E(t *testing.T, testdataDir, workspaceDir string) {
	t.Helper()
	dirs := []string{
		filepath.Join(workspaceDir, "agents"),
		filepath.Join(workspaceDir, "mcp"),
		filepath.Join(workspaceDir, "models"),
		filepath.Join(workspaceDir, "tools", "bundles"),
	}
	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(dir, 0o755), "mkdir %s", dir)
	}
	var copyDir func(src, dst string)
	copyDir = func(src, dst string) {
		entries, err := os.ReadDir(src)
		require.NoError(t, err, "read dir %s", src)
		for _, entry := range entries {
			srcPath := filepath.Join(src, entry.Name())
			dstPath := filepath.Join(dst, entry.Name())
			if entry.IsDir() {
				require.NoError(t, os.MkdirAll(dstPath, 0o755), "mkdir %s", dstPath)
				copyDir(srcPath, dstPath)
				continue
			}
			data, err := os.ReadFile(srcPath)
			require.NoError(t, err, "read file %s", srcPath)
			require.NoError(t, os.WriteFile(dstPath, data, 0o644), "write file %s", dstPath)
		}
	}
	copyDir(filepath.Join(testdataDir, "agents"), filepath.Join(workspaceDir, "agents"))
	copyDir(filepath.Join(testdataDir, "mcp"), filepath.Join(workspaceDir, "mcp"))
	copyDir(filepath.Join(testdataDir, "models"), filepath.Join(workspaceDir, "models"))
	copyDir(filepath.Join(testdataDir, "tools", "bundles"), filepath.Join(workspaceDir, "tools", "bundles"))
	configFile := filepath.Join(workspaceDir, "config.yaml")
	configBody := "models: []\nagents: []\n\ninternalMCP:\n  services:\n    - system/exec\n    - system/os\n"
	require.NoError(t, os.WriteFile(configFile, []byte(configBody), 0o644), "write config")
}

func TestQuerySimple(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "simple",
		Query:   "Hi, how are you?",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Content, "expected non-empty response content")
	assert.NotEmpty(t, out.ConversationID, "expected conversation ID")
	fmt.Printf("[simple] content: %s\n", truncate(out.Content, 200))
}

func TestQueryWithLocalKnowledge(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "knowledge_local",
		Query:   "What products does Viant make?",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Content)

	content := strings.ToLower(out.Content)
	hasProduct := strings.Contains(content, "datly") ||
		strings.Contains(content, "endly") ||
		strings.Contains(content, "agently")
	assert.True(t, hasProduct, "response should mention at least one Viant product; got: %s", truncate(out.Content, 300))
	fmt.Printf("[knowledge_local] content: %s\n", truncate(out.Content, 300))
}

func TestQueryWithSystemKnowledge(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "knowledge_system",
		Query:   "What are the Go error handling best practices?",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Content)

	content := strings.ToLower(out.Content)
	hasRule := strings.Contains(content, "wrap") ||
		strings.Contains(content, "sentinel") ||
		strings.Contains(content, "errors.is") ||
		strings.Contains(content, "fmt.errorf")
	assert.True(t, hasRule, "response should reference Go error handling rules; got: %s", truncate(out.Content, 300))
	fmt.Printf("[knowledge_system] content: %s\n", truncate(out.Content, 300))
}

func TestQueryWithForcedToolUsage(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()
	expectedUser := strings.TrimSpace(os.Getenv("USER"))
	require.NotEmpty(t, expectedUser, "USER must be set for forced tool usage e2e")

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "tool_env_seed_user_only",
		Query:   "Please return USER only.",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Content)
	require.NotEmpty(t, out.ConversationID, "expected conversation ID")

	responseText := strings.TrimSpace(out.Content)
	assert.Equal(t, "USER="+expectedUser, responseText, "response should come from actual tool result")

	transcript, err := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: out.ConversationID})
	require.NoError(t, err)
	pages := collectExecutionPages(transcript)
	require.NotEmpty(t, pages, "expected execution pages in transcript")
	toolCallCount := 0
	foundEnvTool := false
	for _, page := range pages {
		if page == nil {
			continue
		}
		for _, toolStep := range page.ToolSteps {
			if toolStep == nil {
				continue
			}
			toolCallCount++
			name := strings.ToLower(strings.TrimSpace(toolStep.ToolName))
			if strings.Contains(name, "system/os:getenv") || strings.Contains(name, "system_os-getenv") || strings.Contains(name, "system/os/getenv") {
				foundEnvTool = true
			}
		}
	}
	assert.Greater(t, toolCallCount, 0, "expected at least one tool call in transcript")
	assert.True(t, foundEnvTool, "expected system/os:getEnv tool call in transcript")
	fmt.Printf("[tool_forced] content: %s\n", truncate(out.Content, 300))
}

func TestQueryWithToolUsage(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx := context.Background()
	expectedHome := strings.TrimSpace(os.Getenv("HOME"))
	require.NotEmpty(t, expectedHome, "HOME must be set for tool usage e2e")

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "chatter_system_os",
		Query:   "What is the value of the HOME environment variable? Reply with the exact value only.",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Content)
	require.NotEmpty(t, out.ConversationID, "expected conversation ID")

	responseText := strings.TrimSpace(out.Content)
	assert.Contains(t, responseText, expectedHome, "response should include HOME value; got: %s", truncate(out.Content, 300))

	transcript, err := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: out.ConversationID})
	require.NoError(t, err)
	pages := collectExecutionPages(transcript)
	require.NotEmpty(t, pages, "expected execution pages in transcript")
	toolCallCount := 0
	foundEnvTool := false
	for _, page := range pages {
		if page == nil {
			continue
		}
		for _, toolStep := range page.ToolSteps {
			if toolStep == nil {
				continue
			}
			toolCallCount++
			name := strings.ToLower(strings.TrimSpace(toolStep.ToolName))
			if strings.Contains(name, "system/os:getenv") || strings.Contains(name, "system_os-getenv") || strings.Contains(name, "system/os/getenv") {
				foundEnvTool = true
			}
		}
	}
	assert.Greater(t, toolCallCount, 0, "expected at least one tool call in transcript")
	assert.True(t, foundEnvTool, "expected system/os:getEnv tool call in transcript")
	fmt.Printf("[tool_user] content: %s\n", truncate(out.Content, 300))
}

func TestQueryAsyncExecReporter(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx, cancel := context.WithTimeout(context.Background(), asyncE2ETimeout)
	defer cancel()

	conv, err := client.CreateConversation(ctx, &sdk.CreateConversationInput{
		AgentID: "async_exec_reporter",
		Title:   "async-exec-reporter",
	})
	require.NoError(t, err)
	require.NotNil(t, conv)
	require.NotEmpty(t, conv.Id)
	attachAsyncFailureDebug(t, ctx, client, conv.Id, nil)

	sub, err := client.StreamEvents(ctx, &sdk.StreamEventsInput{ConversationID: conv.Id})
	require.NoError(t, err)
	defer sub.Close()

	var (
		mu     sync.Mutex
		events []*streaming.Event
		done   = make(chan struct{})
	)
	go func() {
		defer close(done)
		for ev := range sub.C() {
			if ev == nil {
				continue
			}
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}
	}()
	attachAsyncFailureDebug(t, ctx, client, conv.Id, func() []*streaming.Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]*streaming.Event(nil), events...)
	})

	started := time.Now()
	out, err := client.Query(ctx, &agentsvc.QueryInput{
		ConversationID: conv.Id,
		AgentID:        "async_exec_reporter",
		UserId:         "e2e-test",
		Query:          "Watch for the report readiness signal and tell me when it is done.",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, "ASYNC_REPORT_DONE found=READY_SIGNAL", strings.TrimSpace(out.Content))
	require.GreaterOrEqual(t, time.Since(started), asyncE2EMinRuntime, "query should stay open while async watcher is running")

	_ = sub.Close()
	<-done

	mu.Lock()
	captured := append([]*streaming.Event(nil), events...)
	mu.Unlock()
	require.NotEmpty(t, captured)

	var sawToolStarted bool
	for _, ev := range captured {
		if ev == nil {
			continue
		}
		switch ev.Type {
		case streaming.EventTypeToolCallStarted:
			if strings.Contains(strings.ToLower(strings.TrimSpace(ev.ToolName)), "system/exec/start") {
				sawToolStarted = true
			}
		}
	}
	require.True(t, sawToolStarted, "expected at least one system/exec:start tool event")

	messages, err := client.GetMessages(ctx, &sdk.GetMessagesInput{ConversationID: conv.Id})
	require.NoError(t, err)
	require.NotNil(t, messages)
	foundAsyncWait := false
	statusToolCalls := 0
	foundReadySignal := false
	for _, row := range messages.Rows {
		if row == nil {
			continue
		}
		if row.Mode != nil && strings.TrimSpace(*row.Mode) == "async_wait" {
			foundAsyncWait = true
		}
		if row.ToolName != nil && strings.Contains(strings.ToLower(strings.TrimSpace(*row.ToolName)), "system/exec/status") {
			statusToolCalls++
			if row.Content != nil && strings.Contains(*row.Content, "FOUND=READY_SIGNAL") {
				foundReadySignal = true
			}
		}
	}
	require.True(t, foundAsyncWait, "expected at least one async reinforcement message")
	require.GreaterOrEqual(t, statusToolCalls, 2, "expected repeated status tool calls after async reinforcement")
	require.True(t, foundReadySignal, "expected final status tool result to contain READY_SIGNAL")
}

func TestQueryAsyncExecCanceler(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx, cancel := context.WithTimeout(context.Background(), asyncE2ETimeout)
	defer cancel()

	conv, err := client.CreateConversation(ctx, &sdk.CreateConversationInput{
		AgentID: "async_exec_canceler",
		Title:   "async-exec-canceler",
	})
	require.NoError(t, err)
	require.NotNil(t, conv)
	require.NotEmpty(t, conv.Id)
	attachAsyncFailureDebug(t, ctx, client, conv.Id, nil)

	sub, err := client.StreamEvents(ctx, &sdk.StreamEventsInput{ConversationID: conv.Id})
	require.NoError(t, err)
	defer sub.Close()

	var (
		mu     sync.Mutex
		events []*streaming.Event
		done   = make(chan struct{})
	)
	go func() {
		defer close(done)
		for ev := range sub.C() {
			if ev == nil {
				continue
			}
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}
	}()
	attachAsyncFailureDebug(t, ctx, client, conv.Id, func() []*streaming.Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]*streaming.Event(nil), events...)
	})

	started := time.Now()
	out, err := client.Query(ctx, &agentsvc.QueryInput{
		ConversationID: conv.Id,
		AgentID:        "async_exec_canceler",
		UserId:         "e2e-test",
		Query:          "Start the shell heartbeat watcher, then cancel it after the async update arrives.",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, "ASYNC_CANCEL_DONE status=canceled", strings.TrimSpace(out.Content))
	require.GreaterOrEqual(t, time.Since(started), asyncE2EMinRuntime, "query should stay open while async heartbeat is running")

	_ = sub.Close()
	<-done

	mu.Lock()
	captured := append([]*streaming.Event(nil), events...)
	mu.Unlock()
	require.NotEmpty(t, captured)

	var sawStart, sawCancel bool
	for _, ev := range captured {
		if ev == nil {
			continue
		}
		switch ev.Type {
		case streaming.EventTypeToolCallStarted:
			toolName := strings.ToLower(strings.TrimSpace(ev.ToolName))
			if matchesExecTool(toolName, "start") {
				sawStart = true
			}
			if matchesExecTool(toolName, "cancel") {
				sawCancel = true
			}
		}
	}
	require.True(t, sawStart, "expected at least one system/exec:start tool event")
	require.True(t, sawCancel, "expected at least one system/exec:cancel tool event")

	messages, err := client.GetMessages(ctx, &sdk.GetMessagesInput{ConversationID: conv.Id})
	require.NoError(t, err)
	require.NotNil(t, messages)

	foundAsyncWait := false
	cancelToolCalls := 0
	statusToolCalls := 0
	foundRunningHeartbeat := false
	foundCanceledStatus := false
	for _, row := range messages.Rows {
		if row == nil {
			continue
		}
		if row.Mode != nil && strings.TrimSpace(*row.Mode) == "async_wait" {
			foundAsyncWait = true
		}
		if row.ToolName != nil {
			toolName := strings.ToLower(strings.TrimSpace(*row.ToolName))
			switch {
			case strings.Contains(toolName, "system/exec/cancel"):
				cancelToolCalls++
			case strings.Contains(toolName, "system/exec/status"):
				statusToolCalls++
				if row.Content != nil && strings.Contains(*row.Content, "HEARTBEAT") {
					foundRunningHeartbeat = true
				}
				if row.Content != nil && strings.Contains(strings.ToLower(*row.Content), "canceled") {
					foundCanceledStatus = true
				}
			}
		}
	}
	require.True(t, foundAsyncWait, "expected at least one async reinforcement message")
	require.GreaterOrEqual(t, cancelToolCalls, 1, "expected at least one cancel tool call")
	require.GreaterOrEqual(t, statusToolCalls, 2, "expected status before and after cancel")
	require.True(t, foundRunningHeartbeat, "expected running status output to contain HEARTBEAT")
	require.True(t, foundCanceledStatus, "expected terminal status tool result to contain canceled")
}

func TestQueryAsyncExecFailure(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx, cancel := context.WithTimeout(context.Background(), asyncE2ETimeout)
	defer cancel()

	conv, err := client.CreateConversation(ctx, &sdk.CreateConversationInput{
		AgentID: "async_exec_failer",
		Title:   "async-exec-failer",
	})
	require.NoError(t, err)
	require.NotNil(t, conv)
	require.NotEmpty(t, conv.Id)
	attachAsyncFailureDebug(t, ctx, client, conv.Id, nil)

	sub, err := client.StreamEvents(ctx, &sdk.StreamEventsInput{ConversationID: conv.Id})
	require.NoError(t, err)
	defer sub.Close()

	var (
		mu     sync.Mutex
		events []*streaming.Event
		done   = make(chan struct{})
	)
	go func() {
		defer close(done)
		for ev := range sub.C() {
			if ev == nil {
				continue
			}
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}
	}()
	attachAsyncFailureDebug(t, ctx, client, conv.Id, func() []*streaming.Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]*streaming.Event(nil), events...)
	})

	started := time.Now()
	out, err := client.Query(ctx, &agentsvc.QueryInput{
		ConversationID: conv.Id,
		AgentID:        "async_exec_failer",
		UserId:         "e2e-test",
		Query:          "Start the async shell job that should eventually fail.",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, "ASYNC_FAIL_DONE status=failed", strings.TrimSpace(out.Content))
	require.GreaterOrEqual(t, time.Since(started), asyncE2EMinRuntime, "query should stay open while async failing job is running")

	_ = sub.Close()
	<-done

	mu.Lock()
	captured := append([]*streaming.Event(nil), events...)
	mu.Unlock()
	require.NotEmpty(t, captured)

	var sawStart bool
	for _, ev := range captured {
		if ev == nil {
			continue
		}
		toolName := strings.ToLower(strings.TrimSpace(ev.ToolName))
		switch ev.Type {
		case streaming.EventTypeToolCallStarted:
			if matchesExecTool(toolName, "start") {
				sawStart = true
			}
		}
	}
	require.True(t, sawStart, "expected at least one system/exec:start tool event")

	messages, err := client.GetMessages(ctx, &sdk.GetMessagesInput{ConversationID: conv.Id})
	require.NoError(t, err)
	require.NotNil(t, messages)

	foundAsyncWait := false
	statusToolCalls := 0
	foundWaitingOutput := false
	foundTerminalFailure := false
	for _, row := range messages.Rows {
		if row == nil {
			continue
		}
		if row.Mode != nil && strings.TrimSpace(*row.Mode) == "async_wait" {
			foundAsyncWait = true
		}
		if row.ToolName != nil && strings.Contains(strings.ToLower(strings.TrimSpace(*row.ToolName)), "system/exec/status") {
			statusToolCalls++
			if row.Content != nil && strings.Contains(*row.Content, "WAITING_FOR_FAILURE") {
				foundWaitingOutput = true
			}
			if row.Content != nil && strings.Contains(*row.Content, "TERMINAL_FAILURE") {
				foundTerminalFailure = true
			}
		}
	}
	require.True(t, foundAsyncWait, "expected at least one async reinforcement message")
	require.GreaterOrEqual(t, statusToolCalls, 2, "expected repeated status tool calls across async failure")
	require.True(t, foundWaitingOutput, "expected non-terminal status output to contain WAITING_FOR_FAILURE")
	require.True(t, foundTerminalFailure, "expected terminal failed status output to contain TERMINAL_FAILURE")
}

func matchesExecTool(toolName, action string) bool {
	toolName = strings.ToLower(strings.TrimSpace(toolName))
	action = strings.ToLower(strings.TrimSpace(action))
	if toolName == "" || action == "" {
		return false
	}
	return strings.Contains(toolName, "system/exec/"+action) || strings.Contains(toolName, "system/exec:"+action)
}

func TestQueryMultiTurn(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx := context.Background()

	// Turn 1: start a conversation
	out1, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "simple",
		Query:   "My name is Alice. Please remember that.",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out1)
	assert.NotEmpty(t, out1.Content, "turn 1: expected non-empty response")
	assert.NotEmpty(t, out1.ConversationID, "turn 1: expected conversation ID")
	fmt.Printf("[multi_turn] turn 1 content: %s\n", truncate(out1.Content, 200))

	// Turn 2: continue the same conversation, reference prior context
	out2, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID:        "simple",
		ConversationID: out1.ConversationID,
		Query:          "What is my name?",
		UserId:         "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out2)
	assert.NotEmpty(t, out2.Content, "turn 2: expected non-empty response")
	assert.Equal(t, out1.ConversationID, out2.ConversationID, "should use same conversation")

	content := strings.ToLower(out2.Content)
	assert.True(t, strings.Contains(content, "alice"),
		"turn 2 should remember the name Alice; got: %s", truncate(out2.Content, 300))
	fmt.Printf("[multi_turn] turn 2 content: %s\n", truncate(out2.Content, 200))
}

func TestQueryLLMSourcedElicitationFavoriteColor(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "elicitation_favorite_color",
		Query:   "describe my favourite color in 3 sentences",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotEmpty(t, out.ConversationID, "expected conversation ID")
	require.NotNil(t, out.Plan, "expected plan to be present")
	require.NotNil(t, out.Plan.Elicitation, "expected model to return elicitation plan")

	elic := out.Plan.Elicitation
	assert.Contains(t, compactText(elic.Message), "favoritecolor", "elicitation message should request favorite color")
	assert.Contains(t, elic.RequestedSchema.Required, "favoriteColor", "required schema should include favoriteColor")
	_, hasFavoriteColor := elic.RequestedSchema.Properties["favoriteColor"]
	assert.True(t, hasFavoriteColor, "requested schema should define favoriteColor property")

	msgs, err := client.GetMessages(ctx, &sdk.GetMessagesInput{
		ConversationID: out.ConversationID,
		Roles:          []string{"assistant"},
		Types:          []string{"text"},
	})
	require.NoError(t, err)
	require.NotNil(t, msgs)

	foundElicitationMessage := false
	for _, row := range msgs.Rows {
		if row == nil || row.ElicitationId == nil || strings.TrimSpace(*row.ElicitationId) == "" {
			continue
		}
		if row.ElicitationPayloadId != nil && strings.TrimSpace(*row.ElicitationPayloadId) != "" {
			foundElicitationMessage = true
			break
		}
		if row.Content != nil && strings.Contains(strings.ToLower(*row.Content), "favorite color") {
			foundElicitationMessage = true
			break
		}
	}
	assert.True(t, foundElicitationMessage, "expected persisted assistant elicitation message with payload linkage")
}

func TestQueryOpenAIResponsesImageAttachment(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx := context.Background()

	imageData := mustCreatePNG(t, color.RGBA{R: 255, A: 255})
	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID:       "simple",
		ModelOverride: "openai_gpt-5.2_responses",
		Query:         "The attached image is a single solid-color square. What color is it? Answer with one word.",
		UserId:        "e2e-image",
		Attachments: []*prompt.Attachment{
			{Name: "red-square.png", Mime: "image/png", Data: imageData},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Contains(t, strings.ToLower(out.Content), "red")

	transcript, err := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: out.ConversationID})
	require.NoError(t, err)
	require.NotNil(t, transcript)
	require.NotNil(t, transcript.Conversation)
	require.NotEmpty(t, transcript.Conversation.Turns)
	msgs, err := client.GetMessages(ctx, &sdk.GetMessagesInput{ConversationID: out.ConversationID, Roles: []string{"user"}})
	require.NoError(t, err)
	require.NotNil(t, msgs)
	foundAttachmentPayload := false
	for _, row := range msgs.Rows {
		if row == nil || row.AttachmentPayloadId == nil || strings.TrimSpace(*row.AttachmentPayloadId) == "" {
			continue
		}
		foundAttachmentPayload = true
		break
	}
	assert.True(t, foundAttachmentPayload, "expected persisted attachment payload on user message rows")
}

func TestQueryOpenAIResponsesPDFInlineAttachment(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx := context.Background()

	pdfData := mustCreatePDF(t, "PDF_TEST_TOKEN_4729")
	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "pdf_inline",
		Query:   "What exact token appears in the attached PDF? Answer only with the token.",
		UserId:  "e2e-pdf-inline",
		Attachments: []*prompt.Attachment{
			{Name: "token.pdf", Mime: "application/pdf", Data: pdfData},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Contains(t, out.Content, "PDF_TEST_TOKEN_4729")
}

func TestQueryOpenAIResponsesPDFRefAttachment(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx := context.Background()

	pdfData := mustCreatePDF(t, "PDF_TEST_TOKEN_4729")
	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "pdf_ref",
		Query:   "What exact token appears in the attached PDF? Answer only with the token.",
		UserId:  "e2e-pdf-ref",
		Attachments: []*prompt.Attachment{
			{Name: "token.pdf", Mime: "application/pdf", Data: pdfData},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Contains(t, out.Content, "PDF_TEST_TOKEN_4729")
}

func TestQueryOpenAIResponsesGeneratedImageOutput(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "image_generator",
		Query:   "Generate a tiny red square PNG image and reply with only the filename.",
		UserId:  "e2e-file",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	files, err := client.ListFiles(ctx, &sdk.ListFilesInput{ConversationID: out.ConversationID})
	require.NoError(t, err)
	require.NotNil(t, files)
	require.NotEmpty(t, files.Files)
	assert.Equal(t, "generated-image.png", files.Files[0].Name)

	fileData, err := client.DownloadFile(ctx, &sdk.DownloadFileInput{
		ConversationID: out.ConversationID,
		FileID:         files.Files[0].ID,
	})
	require.NoError(t, err)
	require.NotNil(t, fileData)
	assert.True(t,
		strings.Contains(strings.ToLower(fileData.ContentType), "image/png") ||
			bytes.HasPrefix(fileData.Data, []byte{0x89, 0x50, 0x4e, 0x47}),
		"expected generated image payload; contentType=%q len=%d", fileData.ContentType, len(fileData.Data),
	)
}

func TestQueryLinkedConversationCriticReview(t *testing.T) {
	skipIfNoAPIKey(t)
	skipIfNoExtendedE2E(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "linked_story_chatter",
		Query:   "Write a story about a dog.",
		UserId:  "e2e-linked",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, compactText("A dog named Comet found a blue ball in the park and carried it home proudly."), compactText(out.Content))

	transcript, err := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: out.ConversationID})
	require.NoError(t, err)
	parentPages := collectExecutionPages(transcript)
	require.NotEmpty(t, parentPages, "expected execution pages in parent transcript")
	require.NotEmpty(t, parentPages[0].ModelSteps)
	assert.NotEmpty(t, parentPages[0].AssistantMessageID)
	assert.True(t, len(parentPages[0].ToolSteps) > 0 || len(parentPages) > 1, "expected model-driven execution flow")
	linkedConversationID := firstLinkedConversationID(transcript)
	require.NotEmpty(t, linkedConversationID, "expected linked child conversation in transcript")

	linkedPage, err := client.ListLinkedConversations(ctx, &sdk.ListLinkedConversationsInput{
		ParentConversationID: out.ConversationID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, linkedPage.Rows)
	assert.Equal(t, linkedConversationID, linkedPage.Rows[0].ConversationID)
	assert.NotEmpty(t, linkedPage.Rows[0].Status)
	assert.Contains(t, compactText(linkedPage.Rows[0].Response), compactText("A dog named Comet found a blue ball in the park and carried it home proudly."))

	childTranscript, err := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: linkedConversationID})
	require.NoError(t, err)
	childPages := collectExecutionPages(childTranscript)
	require.NotEmpty(t, childPages, "expected execution pages in child transcript")
	assert.True(t, childPages[len(childPages)-1].FinalResponse, "expected child transcript to end with final response page")
	assert.Contains(t, compactText(childPages[len(childPages)-1].Content), compactText("A dog named Comet found a blue ball in the park and carried it home proudly."))
	childText := collectTranscriptText(childTranscript)
	assert.Contains(t, compactText(childText), compactText("A dog named Comet found a blue ball in the park and carried it home proudly."))
}

func mustCreatePNG(t *testing.T, fill color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	require.NoError(t, err)
	return buf.Bytes()
}

func mustCreatePDF(t *testing.T, text string) []byte {
	t.Helper()
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(false)
	pdf.AddPage()
	pdf.SetFont("Helvetica", "", 16)
	pdf.Text(20, 30, text)
	var buf bytes.Buffer
	err := pdf.Output(&buf)
	require.NoError(t, err)
	return buf.Bytes()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func attachAsyncFailureDebug(t *testing.T, ctx context.Context, client sdk.Client, conversationID string, eventSnapshot func() []*streaming.Event) {
	t.Helper()
	if strings.TrimSpace(conversationID) == "" {
		return
	}
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		t.Logf("async e2e debug: conversation=%s", conversationID)
		if eventSnapshot != nil {
			events := eventSnapshot()
			t.Logf("async e2e debug: captured_events=%d", len(events))
			for i, ev := range events {
				if ev == nil {
					continue
				}
				t.Logf("event[%d] type=%s tool=%q status=%q turn=%q msg=%q op=%q content=%q", i, ev.Type, ev.ToolName, ev.Status, ev.TurnID, ev.MessageID, ev.OperationID, truncate(strings.TrimSpace(ev.Content), 160))
			}
		}
		msgs, err := client.GetMessages(ctx, &sdk.GetMessagesInput{ConversationID: conversationID})
		if err != nil {
			t.Logf("async e2e debug: get messages error: %v", err)
			return
		}
		if msgs == nil {
			t.Logf("async e2e debug: no messages")
			return
		}
		t.Logf("async e2e debug: message_rows=%d", len(msgs.Rows))
		for i, row := range msgs.Rows {
			if row == nil {
				continue
			}
			role := strings.TrimSpace(row.Role)
			mode := ""
			if row.Mode != nil {
				mode = strings.TrimSpace(*row.Mode)
			}
			toolName := ""
			if row.ToolName != nil {
				toolName = strings.TrimSpace(*row.ToolName)
			}
			content := ""
			if row.Content != nil {
				content = truncate(strings.TrimSpace(*row.Content), 160)
			}
			status := ""
			if row.Status != nil {
				status = strings.TrimSpace(*row.Status)
			}
			t.Logf("row[%d] role=%q mode=%q tool=%q status=%q content=%q", i, role, mode, toolName, status, content)
		}
	})
}

func compactText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), ""))
}

func firstLinkedConversationID(resp *sdk.ConversationStateResponse) string {
	if resp == nil || resp.Conversation == nil {
		return ""
	}
	for _, turn := range resp.Conversation.Turns {
		if turn == nil {
			continue
		}
		for _, lc := range turn.LinkedConversations {
			if lc != nil && strings.TrimSpace(lc.ConversationID) != "" {
				return strings.TrimSpace(lc.ConversationID)
			}
		}
	}
	return ""
}

func collectTranscriptText(resp *sdk.ConversationStateResponse) string {
	var parts []string
	if resp == nil || resp.Conversation == nil {
		return ""
	}
	for _, turn := range resp.Conversation.Turns {
		if turn == nil {
			continue
		}
		if turn.Assistant != nil {
			if turn.Assistant.Preamble != nil && strings.TrimSpace(turn.Assistant.Preamble.Content) != "" {
				parts = append(parts, strings.TrimSpace(turn.Assistant.Preamble.Content))
			}
			if turn.Assistant.Final != nil && strings.TrimSpace(turn.Assistant.Final.Content) != "" {
				parts = append(parts, strings.TrimSpace(turn.Assistant.Final.Content))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func collectExecutionPages(resp *sdk.ConversationStateResponse) []*sdk.ExecutionPageState {
	var pages []*sdk.ExecutionPageState
	if resp == nil || resp.Conversation == nil {
		return nil
	}
	for _, turn := range resp.Conversation.Turns {
		if turn == nil || turn.Execution == nil {
			continue
		}
		pages = append(pages, turn.Execution.Pages...)
	}
	return pages
}
