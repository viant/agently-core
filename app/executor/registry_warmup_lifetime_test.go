package executor_test

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/protocol/tool"
	"testing"
	"time"
)

type blockedWarmupRegistry struct {
	tool.Registry
	started, canceled, release chan struct{}
}

func (r *blockedWarmupRegistry) Initialize(ctx context.Context) {
	close(r.started)
	<-ctx.Done()
	close(r.canceled)
	<-r.release
}
func TestRuntimeCloseCancelsAndJoinsRegistryWarmup(t *testing.T) {
	registry := &blockedWarmupRegistry{started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	runtime := &executor.Runtime{Registry: registry}
	t.Cleanup(func() {
		select {
		case <-registry.release:
		default:
			close(registry.release)
		}
		require.NoError(t, runtime.Close(context.Background()))
	})
	done := runtime.InitializeRegistryAsync(context.Background(), time.Hour)
	require.Equal(t, done, runtime.InitializeRegistryAsync(context.Background(), time.Hour))
	select {
	case <-registry.started:
	case <-time.After(time.Second):
		t.Fatal("warmup did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- runtime.Close(context.Background()) }()
	select {
	case <-registry.canceled:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel owned warmup")
	}
	select {
	case <-closed:
		t.Fatal("runtime did not join in-flight warmup")
	default:
	}
	close(registry.release)
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("close did not complete after warmup")
	}
	require.Same(t, registry, runtime.Registry)
	select {
	case <-done:
	default:
		t.Fatal("warmup completion not published")
	}
}

type twoContextRegistry struct {
	tool.Registry
	lifetime         context.Context
	started, stopped chan struct{}
	calls            int
}

func (r *twoContextRegistry) InitializeWithRefreshContext(warmup, lifetime context.Context) <-chan struct{} {
	r.calls++
	r.lifetime = lifetime
	close(r.started)
	<-warmup.Done()
	go func() { <-lifetime.Done(); close(r.stopped) }()
	return r.stopped
}
func TestRuntimeRefreshLifetimeOutlivesWarmupAndStopsOnClose(t *testing.T) {
	for _, parentCancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "runtime close", true: "parent cancellation"}[parentCancel], func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			registry := &twoContextRegistry{started: make(chan struct{}), stopped: make(chan struct{})}
			runtime := &executor.Runtime{Registry: registry}
			done := runtime.InitializeRegistryAsync(parent, 20*time.Millisecond)
			require.Equal(t, done, runtime.InitializeRegistryAsync(parent, time.Hour))
			<-done
			require.Equal(t, 1, registry.calls)
			require.NoError(t, registry.lifetime.Err())
			if parentCancel {
				cancel()
				select {
				case <-registry.stopped:
				case <-time.After(time.Second):
					t.Fatal("parent cancellation did not stop refresh")
				}
			}
			require.NoError(t, runtime.Close(context.Background()))
			select {
			case <-registry.stopped:
			default:
				t.Fatal("runtime close did not join refresh")
			}
		})
	}
}
