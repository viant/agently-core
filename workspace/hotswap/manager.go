package hotswap

import (
	"context"
	"log"
	"sync"
	"time"
)

// Action describes what happened to a workspace resource.
type Action int

const (
	// ActionAddOrUpdate indicates a resource was created or modified.
	ActionAddOrUpdate Action = iota
	// ActionDelete indicates a resource was removed.
	ActionDelete
)

// Change represents a single workspace resource mutation.
type Change struct {
	Kind   string // workspace kind (e.g. "agents", "models", "embedders")
	Name   string // bare resource name without extension
	Action Action
}

// Reloadable handles workspace resource changes for a specific kind.
type Reloadable interface {
	OnChange(ctx context.Context, change Change) error
}

// Watcher is an abstraction over the mechanism that detects workspace changes.
// An FS implementation uses fsnotify; a DB implementation might poll or use
// change streams.
type Watcher interface {
	// Watch begins observing the given workspace kinds and delivers changes to
	// the provided callback. It blocks until ctx is cancelled or an
	// unrecoverable error occurs.
	Watch(ctx context.Context, kinds []string, onChange func(Change)) error

	// Close releases watcher resources.
	Close() error
}

// Manager coordinates watchers and dispatches changes to registered Reloadables.
type Manager struct {
	watcher  Watcher
	handlers map[string][]Reloadable // kind → handlers
	mu       sync.RWMutex
	cancel   context.CancelFunc
	done     chan struct{}
}

// New creates a Manager that uses the given Watcher.
func New(watcher Watcher, opts ...Option) *Manager {
	m := &Manager{
		watcher:  watcher,
		handlers: make(map[string][]Reloadable),
		done:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Register adds a Reloadable handler for the specified workspace kind.
func (m *Manager) Register(kind string, r Reloadable) {
	m.mu.Lock()
	m.handlers[kind] = append(m.handlers[kind], r)
	m.mu.Unlock()
}

// Start begins watching and dispatching. It spawns a goroutine and returns
// immediately. Call Stop to shut down.
func (m *Manager) Start(ctx context.Context) error {
	ctx, m.cancel = context.WithCancel(ctx)

	// Collect registered kinds.
	m.mu.RLock()
	kinds := make([]string, 0, len(m.handlers))
	for k := range m.handlers {
		kinds = append(kinds, k)
	}
	m.mu.RUnlock()

	if len(kinds) == 0 {
		close(m.done)
		return nil
	}

	go func() {
		defer close(m.done)
		err := m.watcher.Watch(ctx, kinds, func(ch Change) {
			m.dispatch(ctx, ch)
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("[hotswap] watcher error: %v", err)
		}
	}()
	return nil
}

// Stop cancels the watcher and waits for the dispatch loop to exit.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	<-m.done
	_ = m.watcher.Close()
}

func (m *Manager) dispatch(ctx context.Context, ch Change) {
	m.mu.RLock()
	handlers := m.handlers[ch.Kind]
	m.mu.RUnlock()

	for _, h := range handlers {
		if err := h.OnChange(ctx, ch); err != nil {
			log.Printf("[hotswap] %s/%s handler error: %v", ch.Kind, ch.Name, err)
		}
	}
}

// debouncer accumulates events keyed by path and fires after a quiet period.
type debouncer struct {
	mu       sync.Mutex
	timers   map[string]*time.Timer
	duration time.Duration
}

func newDebouncer(d time.Duration) *debouncer {
	return &debouncer{
		timers:   make(map[string]*time.Timer),
		duration: d,
	}
}

func (d *debouncer) submit(key string, fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.timers[key]; ok {
		t.Stop()
	}
	d.timers[key] = time.AfterFunc(d.duration, func() {
		d.mu.Lock()
		delete(d.timers, key)
		d.mu.Unlock()
		fn()
	})
}
