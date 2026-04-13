package hotswap

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManager_AddOrUpdate(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var received []Change
	var mu sync.Mutex

	handler := reloadableFunc(func(ctx context.Context, ch Change) error {
		mu.Lock()
		received = append(received, ch)
		mu.Unlock()
		return nil
	})

	watcher := NewFSWatcher(dir, WithDebounce(50*time.Millisecond))
	mgr := New(watcher)
	mgr.Register("agents", handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()

	// fsnotify watcher startup is external and has no readiness callback.
	time.Sleep(25 * time.Millisecond)

	// Create a YAML file.
	if err := os.WriteFile(filepath.Join(agentsDir, "simple.yaml"), []byte("id: simple"), 0o644); err != nil {
		t.Fatal(err)
	}

	waitUntil(t, time.Second, 10*time.Millisecond, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) > 0
	}, "expected at least one AddOrUpdate change, got none")

	mu.Lock()
	defer mu.Unlock()
	last := received[len(received)-1]
	if last.Name != "simple" {
		t.Errorf("expected name 'simple', got %q", last.Name)
	}
	if last.Action != ActionAddOrUpdate {
		t.Errorf("expected ActionAddOrUpdate, got %d", last.Action)
	}
	if last.Kind != "agents" {
		t.Errorf("expected kind 'agents', got %q", last.Kind)
	}
}

func TestManager_Delete(t *testing.T) {
	dir := t.TempDir()
	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-create the file so we can delete it.
	file := filepath.Join(modelsDir, "gpt4.yaml")
	if err := os.WriteFile(file, []byte("id: gpt4"), 0o644); err != nil {
		t.Fatal(err)
	}

	var received []Change
	var mu sync.Mutex

	handler := reloadableFunc(func(ctx context.Context, ch Change) error {
		mu.Lock()
		received = append(received, ch)
		mu.Unlock()
		return nil
	})

	watcher := NewFSWatcher(dir, WithDebounce(50*time.Millisecond))
	mgr := New(watcher)
	mgr.Register("models", handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()

	// fsnotify watcher startup is external and has no readiness callback.
	time.Sleep(25 * time.Millisecond)

	// Delete the file.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}

	waitUntil(t, time.Second, 10*time.Millisecond, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, ch := range received {
			if ch.Name == "gpt4" && ch.Action == ActionDelete {
				return true
			}
		}
		return false
	}, "expected Delete change for 'gpt4'")

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, ch := range received {
		if ch.Name == "gpt4" && ch.Action == ActionDelete {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Delete change for 'gpt4', received: %+v", received)
	}
}

func TestManager_Debounce(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var count int64

	handler := reloadableFunc(func(ctx context.Context, ch Change) error {
		atomic.AddInt64(&count, 1)
		return nil
	})

	watcher := NewFSWatcher(dir, WithDebounce(200*time.Millisecond))
	mgr := New(watcher)
	mgr.Register("agents", handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()

	// fsnotify watcher startup is external and has no readiness callback.
	time.Sleep(25 * time.Millisecond)

	file := filepath.Join(agentsDir, "rapid.yaml")
	// Rapid writes within the debounce window.
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(file, []byte("v: "+string(rune('0'+i))), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// This test validates debounce behavior, so we must wait slightly longer than
	// the configured debounce window for the timer to fire.
	time.Sleep(300 * time.Millisecond)

	got := atomic.LoadInt64(&count)
	if got > 2 {
		t.Errorf("debounce should coalesce rapid writes; expected <=2 dispatches, got %d", got)
	}
}

func TestManager_IgnoresNonYAML(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var count int64

	handler := reloadableFunc(func(ctx context.Context, ch Change) error {
		atomic.AddInt64(&count, 1)
		return nil
	})

	watcher := NewFSWatcher(dir, WithDebounce(50*time.Millisecond))
	mgr := New(watcher)
	mgr.Register("agents", handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()

	// fsnotify watcher startup is external and has no readiness callback.
	time.Sleep(25 * time.Millisecond)

	// Write a non-YAML file.
	if err := os.WriteFile(filepath.Join(agentsDir, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	assertNever(t, 150*time.Millisecond, 10*time.Millisecond, func() bool {
		return atomic.LoadInt64(&count) != 0
	}, "expected 0 dispatches for .txt file")
}

// reloadableFunc is a test helper that adapts a function to the Reloadable interface.
type reloadableFunc func(ctx context.Context, ch Change) error

func (f reloadableFunc) OnChange(ctx context.Context, ch Change) error { return f(ctx, ch) }
