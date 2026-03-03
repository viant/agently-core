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

	// Allow watcher to initialize.
	time.Sleep(100 * time.Millisecond)

	// Create a YAML file.
	if err := os.WriteFile(filepath.Join(agentsDir, "simple.yaml"), []byte("id: simple"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce + dispatch.
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("expected at least one AddOrUpdate change, got none")
	}
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

	time.Sleep(100 * time.Millisecond)

	// Delete the file.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)

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

	time.Sleep(100 * time.Millisecond)

	file := filepath.Join(agentsDir, "rapid.yaml")
	// Rapid writes within the debounce window.
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(file, []byte("v: "+string(rune('0'+i))), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for debounce to fire.
	time.Sleep(500 * time.Millisecond)

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

	time.Sleep(100 * time.Millisecond)

	// Write a non-YAML file.
	if err := os.WriteFile(filepath.Join(agentsDir, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)

	if got := atomic.LoadInt64(&count); got != 0 {
		t.Errorf("expected 0 dispatches for .txt file, got %d", got)
	}
}

// reloadableFunc is a test helper that adapts a function to the Reloadable interface.
type reloadableFunc func(ctx context.Context, ch Change) error

func (f reloadableFunc) OnChange(ctx context.Context, ch Change) error { return f(ctx, ch) }
