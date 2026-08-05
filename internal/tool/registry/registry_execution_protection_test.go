package tool

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	execconfig "github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/genai/llm"
	executionprotection "github.com/viant/agently-core/internal/tool/executionprotection"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	toolprotection "github.com/viant/agently-core/protocol/tool/protection"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type registryClaimRepository struct {
	mu           sync.Mutex
	claims       map[string]struct{}
	finishes     map[string]toolprotection.State
	finishSignal chan struct{}
}

func newRegistryClaimRepository() *registryClaimRepository {
	return &registryClaimRepository{
		claims: map[string]struct{}{}, finishes: map[string]toolprotection.State{}, finishSignal: make(chan struct{}, 32),
	}
}

func (r *registryClaimRepository) Claim(_ context.Context, record executionprotection.ClaimRecord) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.claims[record.ClaimKey]; ok {
		return false, nil
	}
	r.claims[record.ClaimKey] = struct{}{}
	return true, nil
}

func (r *registryClaimRepository) Finish(_ context.Context, key string, state toolprotection.State, _ time.Time) error {
	r.mu.Lock()
	r.finishes[key] = state
	r.mu.Unlock()
	r.finishSignal <- struct{}{}
	return nil
}

func newProtectedVirtualRegistry(t *testing.T, handler Handler) (*Registry, *registryClaimRepository) {
	t.Helper()
	repository := newRegistryClaimRepository()
	guard, err := executionprotection.New(execconfig.ToolExecutionProtectionDefaults{
		Enabled: true,
		Rules: []execconfig.ToolExecutionProtectionRule{{
			ID: "rule-1", Tool: "service/tool", Mode: execconfig.ToolExecutionProtectionModeAtMostOnce,
		}},
	}, repository)
	if err != nil {
		t.Fatal(err)
	}
	return &Registry{
		virtualExec:         map[string]Handler{"service/tool": handler},
		virtualDefs:         map[string]llm.ToolDefinition{},
		virtualTimeout:      map[string]timeoutSupport{},
		cache:               map[string]*toolCacheEntry{},
		internal:            map[string]mcpclient.Interface{},
		recentResults:       map[string]map[string]recentItem{},
		recentTTL:           5 * time.Second,
		executionProtection: guard,
	}, repository
}

func protectedTurnContext(turnID string) context.Context {
	return runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{
		ConversationID: "conv-1", TurnID: turnID,
	})
}

func TestRegistryExecutionProtectionConcurrentDuplicateSuppression(t *testing.T) {
	var providerCalls atomic.Int64
	registry, repository := newProtectedVirtualRegistry(t, func(context.Context, map[string]interface{}) (string, error) {
		providerCalls.Add(1)
		return "sent", nil
	})
	const workers = 16
	start := make(chan struct{})
	results := make(chan string, workers)
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := registry.Execute(protectedTurnContext("turn-1"), "service/tool", map[string]interface{}{"body": "same"})
			results <- result
			errors <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	}
	winners := 0
	duplicates := 0
	for result := range results {
		switch {
		case result == "sent":
			winners++
		case strings.Contains(result, `"status":"duplicate_suppressed"`) && strings.Contains(result, `"providerCalled":false`):
			duplicates++
		default:
			t.Fatalf("unexpected result %q", result)
		}
	}
	if winners != 1 || duplicates != workers-1 || providerCalls.Load() != 1 {
		t.Fatalf("winners=%d duplicates=%d providerCalls=%d", winners, duplicates, providerCalls.Load())
	}
	select {
	case <-repository.finishSignal:
	case <-time.After(time.Second):
		t.Fatal("winner Finish did not complete")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.claims) != 1 || len(repository.finishes) != 1 {
		t.Fatalf("claims=%d finishes=%d", len(repository.claims), len(repository.finishes))
	}
	for _, state := range repository.finishes {
		if state != toolprotection.StateCompleted {
			t.Fatalf("finish state = %q", state)
		}
	}
}

func TestRegistryExecutionProtectionRequiresTurnAndUsesFinalArgs(t *testing.T) {
	var providerCalls atomic.Int64
	var providerArgs map[string]interface{}
	registry, _ := newProtectedVirtualRegistry(t, func(_ context.Context, args map[string]interface{}) (string, error) {
		providerCalls.Add(1)
		providerArgs = args
		return "ok", nil
	})
	if _, err := registry.Execute(context.Background(), "service/tool", map[string]interface{}{"body": "x"}); err == nil {
		t.Fatal("Execute() without turn error = nil")
	}
	if providerCalls.Load() != 0 {
		t.Fatalf("provider calls = %d, want 0", providerCalls.Load())
	}
	if _, err := registry.Execute(protectedTurnContext("turn-1"), "service/tool", map[string]interface{}{"body": "x", "timeoutMs": 1000}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, ok := providerArgs["timeoutMs"]; ok {
		t.Fatalf("final provider args retain synthetic timeoutMs: %#v", providerArgs)
	}
}

func TestRegistryExecutionProtectionRetryResolverCanonicalAliases(t *testing.T) {
	registry, _ := newProtectedVirtualRegistry(t, func(context.Context, map[string]interface{}) (string, error) { return "ok", nil })
	for _, name := range []string{"service/tool", "service:tool", "service-tool", "service/tool|data.value"} {
		retryable, configured := registry.ToolRetryable(name)
		if retryable || !configured {
			t.Fatalf("ToolRetryable(%q) = %v, %v; want false, true", name, retryable, configured)
		}
	}
	if _, configured := registry.ToolRetryable("service/other"); configured {
		t.Fatal("unprotected tool received retry policy")
	}
}

type protectionTestClient struct {
	mcpclient.Interface
	calls  atomic.Int64
	result string
	err    error
	onCall func(*mcpschema.CallToolRequestParams)
}

func (c *protectionTestClient) CallTool(_ context.Context, params *mcpschema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	c.calls.Add(1)
	if c.onCall != nil {
		c.onCall(params)
	}
	if c.err != nil {
		return nil, c.err
	}
	return &mcpschema.CallToolResult{Content: []mcpschema.CallToolResultContentElem{
		&mcpschema.TextContent{Type: "text", Text: c.result},
	}}, nil
}

type protectionTestManager struct {
	client     mcpclient.Interface
	reconnects atomic.Int64
}

func (m *protectionTestManager) Get(context.Context, string, string) (mcpclient.Interface, error) {
	return m.client, nil
}

func (m *protectionTestManager) Reconnect(context.Context, string, string) (mcpclient.Interface, error) {
	m.reconnects.Add(1)
	return m.client, nil
}

func (m *protectionTestManager) Touch(string, string) {}

func (m *protectionTestManager) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return nil, nil
}

func (m *protectionTestManager) UseIDToken(context.Context, string) bool { return false }

func (m *protectionTestManager) WithAuthTokenContext(ctx context.Context, _ string) context.Context {
	return ctx
}

func newRemoteProtectionRegistry(client mcpclient.Interface, guard toolprotection.Guard) (*Registry, *protectionTestManager) {
	manager := &protectionTestManager{client: client}
	return &Registry{
		mgr:                 manager,
		virtualExec:         map[string]Handler{},
		virtualDefs:         map[string]llm.ToolDefinition{},
		virtualTimeout:      map[string]timeoutSupport{},
		cache:               map[string]*toolCacheEntry{},
		internal:            map[string]mcpclient.Interface{},
		internalMethods:     map[string]map[string]svc.Signature{},
		recentResults:       map[string]map[string]recentItem{},
		recentTTL:           5 * time.Second,
		executionProtection: guard,
	}, manager
}

func newProtectionGuard(t *testing.T, repository executionprotection.Repository) toolprotection.Guard {
	t.Helper()
	guard, err := executionprotection.New(execconfig.ToolExecutionProtectionDefaults{
		Enabled: true,
		Rules: []execconfig.ToolExecutionProtectionRule{{
			ID: "rule-1", Tool: "service/tool", Mode: execconfig.ToolExecutionProtectionModeAtMostOnce,
		}},
	}, repository)
	if err != nil {
		t.Fatal(err)
	}
	return guard
}

func TestRegistryExecutionProtectionAppliesSameScopeAcrossTurnKinds(t *testing.T) {
	const (
		protectedTool   = "delivery/send"
		unprotectedTool = "delivery/validate"
		ruleID          = "delivery-send-at-most-once"
	)
	repository := newRegistryClaimRepository()
	guard, err := executionprotection.New(execconfig.ToolExecutionProtectionDefaults{
		Enabled: true,
		Rules: []execconfig.ToolExecutionProtectionRule{{
			ID: ruleID, Tool: protectedTool, Mode: execconfig.ToolExecutionProtectionModeAtMostOnce,
		}},
	}, repository)
	if err != nil {
		t.Fatal(err)
	}
	client := &protectionTestClient{
		result: "sent",
		onCall: func(params *mcpschema.CallToolRequestParams) {
			if params.Name != "send" {
				t.Errorf("protected fake MCP tool name = %q, want send", params.Name)
			}
		},
	}
	registry, _ := newRemoteProtectionRegistry(client, guard)
	args := map[string]interface{}{
		"recipient": "principal-1",
		"subject":   "T3-D rollout",
		"body":      "same report",
		"artifact":  map[string]interface{}{"name": "report.pdf", "sourceURL": "artifact://report.pdf"},
	}

	runPair := func(turnID, toolName string) []string {
		t.Helper()
		start := make(chan struct{})
		results := make(chan string, 2)
		errs := make(chan error, 2)
		var wait sync.WaitGroup
		for i := 0; i < 2; i++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				result, executeErr := registry.Execute(protectedTurnContext(turnID), toolName, args)
				results <- result
				errs <- executeErr
			}()
		}
		close(start)
		wait.Wait()
		close(results)
		close(errs)
		for executeErr := range errs {
			if executeErr != nil {
				t.Fatalf("Execute(%q, %q) error = %v", turnID, toolName, executeErr)
			}
		}
		var collected []string
		for result := range results {
			collected = append(collected, result)
		}
		return collected
	}

	assertProtectedPair := func(turnID string, wantCalls int64) {
		t.Helper()
		results := runPair(turnID, protectedTool)
		var sent, suppressed int
		for _, result := range results {
			switch {
			case result == "sent":
				sent++
			case strings.Contains(result, `"status":"duplicate_suppressed"`) &&
				strings.Contains(result, `"providerCalled":false`) &&
				strings.Contains(result, `"ruleId":"`+ruleID+`"`):
				suppressed++
			default:
				t.Fatalf("unexpected protected result %q", result)
			}
		}
		if sent != 1 || suppressed != 1 || client.calls.Load() != wantCalls {
			t.Fatalf("turn %q: sent=%d suppressed=%d downstream calls=%d, want 1,1,%d", turnID, sent, suppressed, client.calls.Load(), wantCalls)
		}
	}

	assertProtectedPair("msg_interactive_t3d", 1)
	assertProtectedPair("scheduler_run_01K3T3D0000000000000000000", 2)
	if result, executeErr := registry.Execute(protectedTurnContext("msg_interactive_t3d_next"), protectedTool, args); executeErr != nil || result != "sent" {
		t.Fatalf("new turn Execute() = %q, %v", result, executeErr)
	}
	if client.calls.Load() != 3 {
		t.Fatalf("new turn downstream calls = %d, want 3", client.calls.Load())
	}

	client.onCall = func(params *mcpschema.CallToolRequestParams) {
		if params.Name != "validate" {
			t.Errorf("fake MCP tool name = %q, want validate", params.Name)
		}
	}
	for _, result := range runPair("msg_interactive_t3d_unprotected", unprotectedTool) {
		if result != "sent" {
			t.Fatalf("unprotected result = %q, want sent", result)
		}
	}
	if client.calls.Load() != 4 {
		t.Fatalf("unprotected downstream calls = %d, want total 4", client.calls.Load())
	}
	repository.mu.Lock()
	claimCount := len(repository.claims)
	repository.mu.Unlock()
	if claimCount != 3 {
		t.Fatalf("protected claims = %d, want 3", claimCount)
	}
}

func TestRegistryExecutionProtectionLimitsReconnectAttempts(t *testing.T) {
	protectedClient := &protectionTestClient{err: io.EOF}
	protectedRegistry, protectedManager := newRemoteProtectionRegistry(
		protectedClient,
		newProtectionGuard(t, newRegistryClaimRepository()),
	)
	if _, err := protectedRegistry.Execute(protectedTurnContext("turn-protected"), "service/tool", map[string]interface{}{"body": "x"}); err == nil {
		t.Fatal("protected Execute() error = nil")
	}
	if protectedClient.calls.Load() != 1 || protectedManager.reconnects.Load() != 0 {
		t.Fatalf("protected calls=%d reconnects=%d, want 1,0", protectedClient.calls.Load(), protectedManager.reconnects.Load())
	}

	unprotectedClient := &protectionTestClient{err: io.EOF}
	unprotectedRegistry, unprotectedManager := newRemoteProtectionRegistry(unprotectedClient, nil)
	if _, err := unprotectedRegistry.Execute(protectedTurnContext("turn-unprotected"), "service/tool", map[string]interface{}{"body": "x"}); err == nil {
		t.Fatal("unprotected Execute() error = nil")
	}
	if unprotectedClient.calls.Load() != 3 || unprotectedManager.reconnects.Load() != 2 {
		t.Fatalf("unprotected calls=%d reconnects=%d, want 3,2", unprotectedClient.calls.Load(), unprotectedManager.reconnects.Load())
	}
}

func TestRegistryExecutionProtectionBypassesRecentResults(t *testing.T) {
	protectedClient := &protectionTestClient{result: "provider-result"}
	protectedRegistry, _ := newRemoteProtectionRegistry(
		protectedClient,
		newProtectionGuard(t, newRegistryClaimRepository()),
	)
	protectedRegistry.recentResults["conv-1"] = map[string]recentItem{
		"|service/tool||{\"body\":\"same\"}": {when: time.Now(), out: "cached-result"},
	}
	result, err := protectedRegistry.Execute(protectedTurnContext("turn-protected"), "service/tool", map[string]interface{}{"body": "same"})
	if err != nil || result != "provider-result" || protectedClient.calls.Load() != 1 {
		t.Fatalf("protected Execute() = %q, %v calls=%d", result, err, protectedClient.calls.Load())
	}

	unprotectedClient := &protectionTestClient{result: "provider-result"}
	unprotectedRegistry, _ := newRemoteProtectionRegistry(unprotectedClient, nil)
	unprotectedRegistry.recentResults["conv-1"] = map[string]recentItem{
		"|service/tool||{\"body\":\"same\"}": {when: time.Now(), out: "cached-result"},
	}
	result, err = unprotectedRegistry.Execute(protectedTurnContext("turn-unprotected"), "service/tool", map[string]interface{}{"body": "same"})
	if err != nil || result != "cached-result" || unprotectedClient.calls.Load() != 0 {
		t.Fatalf("unprotected Execute() = %q, %v calls=%d", result, err, unprotectedClient.calls.Load())
	}
}

func TestRegistryRecentResultsCoalescesConcurrentUnprotectedCalls(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	client := &protectionTestClient{
		result: "provider-result",
		onCall: func(params *mcpschema.CallToolRequestParams) {
			if params.Name != "tool" {
				t.Errorf("fake MCP tool name = %q, want tool", params.Name)
			}
			started <- struct{}{}
			<-release
		},
	}
	registry, _ := newRemoteProtectionRegistry(client, nil)
	start := make(chan struct{})
	results := make(chan string, 2)
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := registry.Execute(protectedTurnContext("turn-unprotected"), "service/tool", map[string]interface{}{"body": "same"})
			results <- result
			errs <- err
		}()
	}
	close(start)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider call did not start")
	}
	close(release)
	wait.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	}
	for result := range results {
		if result != "provider-result" {
			t.Fatalf("Execute() result = %q, want provider-result", result)
		}
	}
	if client.calls.Load() != 1 {
		t.Fatalf("downstream calls = %d, want 1", client.calls.Load())
	}
}

type rawResultProtectionClient struct {
	mcpclient.Interface
	calls atomic.Int64
}

func (c *rawResultProtectionClient) CallTool(_ context.Context, params *mcpschema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	c.calls.Add(1)
	if params.Name != "tool" {
		return nil, errors.New("unexpected tool name")
	}
	return &mcpschema.CallToolResult{Content: []mcpschema.CallToolResultContentElem{
		map[string]interface{}{"ok": true},
	}}, nil
}

func TestRegistryRecentResultsCachesSequentialRawUnprotectedCalls(t *testing.T) {
	client := &rawResultProtectionClient{}
	registry, _ := newRemoteProtectionRegistry(client, nil)
	args := map[string]interface{}{"body": "same"}
	first, err := registry.Execute(protectedTurnContext("turn-unprotected"), "service/tool", args)
	if err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	second, err := registry.Execute(protectedTurnContext("turn-unprotected"), "service/tool", args)
	if err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if first != second {
		t.Fatalf("sequential raw results differ: first=%q second=%q", first, second)
	}
	if client.calls.Load() != 1 {
		t.Fatalf("downstream calls = %d, want 1", client.calls.Load())
	}
}

func TestRegistryExecutionProtectionUnavailableRepositoryPreventsDispatch(t *testing.T) {
	var providerCalls atomic.Int64
	registry := &Registry{
		virtualExec: map[string]Handler{"service/tool": func(context.Context, map[string]interface{}) (string, error) {
			providerCalls.Add(1)
			return "sent", nil
		}},
		virtualTimeout:      map[string]timeoutSupport{},
		cache:               map[string]*toolCacheEntry{},
		executionProtection: newProtectionGuard(t, executionprotection.NewDAORepository(nil)),
	}
	if _, err := registry.Execute(protectedTurnContext("turn"), "service/tool", map[string]interface{}{"body": "x"}); err == nil {
		t.Fatal("Execute() with unavailable repository error = nil")
	}
	if providerCalls.Load() != 0 {
		t.Fatalf("provider calls = %d, want 0", providerCalls.Load())
	}
}

func TestRegistryExecutionProtectionRecordsFailureStates(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		state toolprotection.State
	}{
		{name: "explicit error", err: errors.New("provider rejected request"), state: toolprotection.StateFailed},
		{name: "timeout", err: context.DeadlineExceeded, state: toolprotection.StateUnknown},
		{name: "cancel", err: context.Canceled, state: toolprotection.StateUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := newRegistryClaimRepository()
			guard := newProtectionGuard(t, repository)
			registry := &Registry{
				virtualExec: map[string]Handler{"service/tool": func(context.Context, map[string]interface{}) (string, error) {
					return "", test.err
				}},
				virtualTimeout:      map[string]timeoutSupport{},
				cache:               map[string]*toolCacheEntry{},
				executionProtection: guard,
			}
			if _, err := registry.Execute(protectedTurnContext("turn-"+test.name), "service/tool", map[string]interface{}{"body": "x"}); !errors.Is(err, test.err) {
				t.Fatalf("Execute() error = %v, want %v", err, test.err)
			}
			select {
			case <-repository.finishSignal:
			case <-time.After(time.Second):
				t.Fatal("Finish did not complete")
			}
			repository.mu.Lock()
			defer repository.mu.Unlock()
			if len(repository.finishes) != 1 {
				t.Fatalf("finish count = %d", len(repository.finishes))
			}
			for _, state := range repository.finishes {
				if state != test.state {
					t.Fatalf("finish state = %q, want %q", state, test.state)
				}
			}
		})
	}
}

type blockingFinishGuard struct {
	started  chan bool
	release  chan struct{}
	finished chan struct{}
}

func (g *blockingFinishGuard) IsProtected(string) bool { return true }

func (g *blockingFinishGuard) Claim(context.Context, string, map[string]interface{}) (toolprotection.Claim, error) {
	return toolprotection.Claim{Protected: true, Acquired: true, RuleID: "rule", ClaimKey: strings.Repeat("a", 64)}, nil
}

func (g *blockingFinishGuard) Finish(ctx context.Context, _ toolprotection.Claim, _ toolprotection.State) {
	_, hasDeadline := ctx.Deadline()
	g.started <- hasDeadline
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	close(g.finished)
}

func TestRegistryExecutionProtectionFinishIsDetachedAndBounded(t *testing.T) {
	guard := &blockingFinishGuard{
		started: make(chan bool, 1), release: make(chan struct{}), finished: make(chan struct{}),
	}
	registry := &Registry{
		virtualExec: map[string]Handler{"service/tool": func(context.Context, map[string]interface{}) (string, error) {
			return "provider-result", nil
		}},
		virtualTimeout:      map[string]timeoutSupport{},
		cache:               map[string]*toolCacheEntry{},
		executionProtection: guard,
	}
	type executionResult struct {
		value string
		err   error
	}
	executed := make(chan executionResult, 1)
	go func() {
		value, err := registry.Execute(protectedTurnContext("turn-detached"), "service/tool", map[string]interface{}{"body": "x"})
		executed <- executionResult{value: value, err: err}
	}()
	select {
	case result := <-executed:
		if result.err != nil || result.value != "provider-result" {
			t.Fatalf("Execute() = %q, %v", result.value, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Execute() blocked on Finish")
	}
	select {
	case hasDeadline := <-guard.started:
		if !hasDeadline {
			t.Fatal("detached Finish context has no deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("Finish did not start")
	}
	close(guard.release)
	select {
	case <-guard.finished:
	case <-time.After(time.Second):
		t.Fatal("Finish goroutine did not terminate")
	}
}
