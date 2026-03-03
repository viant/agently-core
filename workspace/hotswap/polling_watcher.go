package hotswap

import (
	"context"
	"time"

	"github.com/viant/agently-core/workspace"
)

const defaultPollInterval = 5 * time.Second

// PollingWatcher implements Watcher for non-FS stores by periodically polling
// Store.Entries and comparing snapshots to detect changes.
type PollingWatcher struct {
	store    workspace.Store
	interval time.Duration
}

// PollingOption configures a PollingWatcher.
type PollingOption func(*PollingWatcher)

// WithPollInterval sets the polling interval.
func WithPollInterval(d time.Duration) PollingOption {
	return func(w *PollingWatcher) {
		if d > 0 {
			w.interval = d
		}
	}
}

// NewPollingWatcher creates a watcher that polls the given store.
func NewPollingWatcher(store workspace.Store, opts ...PollingOption) *PollingWatcher {
	w := &PollingWatcher{
		store:    store,
		interval: defaultPollInterval,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}
	return w
}

type entryKey struct {
	kind string
	name string
}

// Watch polls the store at the configured interval and calls onChange when
// resources are added, updated, or deleted. It blocks until ctx is cancelled.
func (w *PollingWatcher) Watch(ctx context.Context, kinds []string, onChange func(Change)) error {
	// Build initial snapshot.
	prev := make(map[entryKey]time.Time)
	for _, kind := range kinds {
		entries, err := w.store.Entries(ctx, kind)
		if err != nil {
			continue
		}
		for _, e := range entries {
			prev[entryKey{kind: e.Kind, name: e.Name}] = e.UpdatedAt
		}
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			curr := make(map[entryKey]time.Time)
			for _, kind := range kinds {
				entries, err := w.store.Entries(ctx, kind)
				if err != nil {
					continue
				}
				for _, e := range entries {
					curr[entryKey{kind: e.Kind, name: e.Name}] = e.UpdatedAt
				}
			}

			// Detect additions and updates.
			for key, ts := range curr {
				oldTs, existed := prev[key]
				if !existed || !ts.Equal(oldTs) {
					onChange(Change{Kind: key.kind, Name: key.name, Action: ActionAddOrUpdate})
				}
			}

			// Detect deletions.
			for key := range prev {
				if _, exists := curr[key]; !exists {
					onChange(Change{Kind: key.kind, Name: key.name, Action: ActionDelete})
				}
			}

			prev = curr
		}
	}
}

// Close is a no-op for the polling watcher.
func (w *PollingWatcher) Close() error { return nil }
