package hotswap

import "time"

// Option configures Manager.
type Option func(*Manager)

// WatcherOption configures FSWatcher.
type WatcherOption func(*FSWatcher)

// WithDebounce sets the debounce duration for the FS watcher.
func WithDebounce(d time.Duration) WatcherOption {
	return func(w *FSWatcher) {
		if d > 0 {
			w.debounce = d
		}
	}
}
