package finder

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/agent"
	"sync"
	"testing"
)

type configuredLoader struct {
	mu    sync.Mutex
	calls int
	err   error
	items []*agent.Agent
}

func (l *configuredLoader) Load(context.Context, string) (*agent.Agent, error) {
	return nil, errors.New("request-time load not expected")
}
func (l *configuredLoader) LoadAgents(context.Context, string) ([]*agent.Agent, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.items != nil {
		return l.items, l.err
	}
	return []*agent.Agent{{Identity: agent.Identity{ID: "configured"}}}, l.err
}
func TestConfiguredFinderPreloadsOnceAtStartup(t *testing.T) {
	loader := &configuredLoader{}
	f := New(WithLoader(loader))
	require.Empty(t, f.All())
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); require.NoError(t, f.Preload(context.Background())) }()
	}
	wg.Wait()
	require.Equal(t, 1, loader.calls)
	require.Len(t, f.All(), 1)
	require.NoError(t, f.Preload(context.Background()))
	require.Equal(t, 1, loader.calls)
}
func TestConfiguredFinderPreloadFailureIsNotFakeDirectory(t *testing.T) {
	loader := &configuredLoader{err: errors.New("configured asset invalid")}
	f := New(WithLoader(loader))
	require.Error(t, f.Preload(context.Background()))
	require.Empty(t, f.All())
}

func (l *configuredLoader) Add(string, *agent.Agent) {}

func TestConfiguredPreloadPreservesExplicitAgent(t *testing.T) {
	explicit := &agent.Agent{Identity: agent.Identity{ID: "configured", Name: "Explicit"}}
	loader := &configuredLoader{items: []*agent.Agent{{Identity: agent.Identity{ID: "configured", Name: "Disk"}}, {Identity: agent.Identity{ID: "other"}}}}
	f := New(WithInitial(explicit), WithLoader(loader))
	require.NoError(t, f.Preload(context.Background()))
	found, err := f.Find(context.Background(), "configured")
	require.NoError(t, err)
	require.Same(t, explicit, found)
	require.Len(t, f.All(), 2)
}
func TestConfiguredPreloadNormalizesNameOnCopy(t *testing.T) {
	original := &agent.Agent{Identity: agent.Identity{Name: "Named only"}}
	f := New(WithLoader(&configuredLoader{items: []*agent.Agent{original}}))
	require.NoError(t, f.Preload(context.Background()))
	found, err := f.Find(context.Background(), "Named only")
	require.NoError(t, err)
	require.Equal(t, "Named only", found.ID)
	require.Empty(t, original.ID)
	require.NotSame(t, original, found)
}
