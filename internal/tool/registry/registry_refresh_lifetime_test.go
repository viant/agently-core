package tool

import (
	"context"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
	"testing"
	"time"
)

func cachedTool(reg *Registry, name string) bool {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	return reg.cache[name] != nil
}
func TestRefreshSurvivesCanceledWarmupAndJoinsLifetime(t *testing.T) {
	fake := &fakeMCPClient{}
	reg := &Registry{cache: map[string]*toolCacheEntry{}, internal: map[string]client.Interface{"db": fake}, refreshEvery: 10 * time.Millisecond}
	warmup, cancelWarmup := context.WithCancel(context.Background())
	cancelWarmup()
	lifetime, cancelLifetime := context.WithCancel(context.Background())
	defer cancelLifetime()
	done := reg.InitializeWithRefreshContext(warmup, lifetime)
	fake.setUp(true)
	deadline := time.Now().Add(3 * time.Second)
	for !cachedTool(reg, "db/ping") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !cachedTool(reg, "db/ping") {
		t.Fatal("background monitor did not populate cache after canceled warmup")
	}
	cancelLifetime()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh monitors did not join")
	}
}

type lateRefreshClient struct {
	fakeMCPClient
	started, release chan struct{}
}

func (f *lateRefreshClient) ListTools(context.Context, *string, ...client.RequestOption) (*schema.ListToolsResult, error) {
	close(f.started)
	<-f.release
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: "late"}}}, nil
}
func TestCanceledRefreshCannotPublishLateResponse(t *testing.T) {
	f := &lateRefreshClient{started: make(chan struct{}), release: make(chan struct{})}
	reg := &Registry{cache: map[string]*toolCacheEntry{}, internal: map[string]client.Interface{"db": f}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reg.refreshServerTools(ctx, "db") }()
	<-f.started
	cancel()
	close(f.release)
	<-done
	if cachedTool(reg, "db/late") {
		t.Fatal("late response published after cancellation")
	}
}
func TestLegacyInitializeUsesCallerLifetime(t *testing.T) {
	fake := &fakeMCPClient{}
	fake.setUp(true)
	reg := &Registry{cache: map[string]*toolCacheEntry{}, internal: map[string]client.Interface{"db": fake}, refreshEvery: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	reg.Initialize(ctx)
	cancel()
	// The eager path still publishes while the caller is live.
	if !cachedTool(reg, "db/ping") {
		t.Fatal("legacy eager initialization changed")
	}
}
