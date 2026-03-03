package hotswap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const defaultDebounce = 200 * time.Millisecond

// FSWatcher implements Watcher using fsnotify for filesystem-based workspaces.
type FSWatcher struct {
	root     string
	debounce time.Duration
	watcher  *fsnotify.Watcher
}

// NewFSWatcher creates an FS-based watcher rooted at the given workspace directory.
func NewFSWatcher(root string, opts ...WatcherOption) *FSWatcher {
	w := &FSWatcher{
		root:     root,
		debounce: defaultDebounce,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Watch begins observing the specified workspace kinds (subdirectories) and
// calls onChange for each detected YAML file mutation. It blocks until ctx is
// cancelled.
func (w *FSWatcher) Watch(ctx context.Context, kinds []string, onChange func(Change)) error {
	var err error
	w.watcher, err = fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	// Add each kind subdirectory and its children.
	for _, kind := range kinds {
		dir := filepath.Join(w.root, kind)
		if err := addRecursive(w.watcher, dir); err != nil {
			// Directory might not exist yet; skip silently.
			continue
		}
	}

	db := newDebouncer(w.debounce)
	kindSet := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		kindSet[k] = true
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-w.watcher.Events:
			if !ok {
				return nil
			}
			ch, valid := w.classify(event, kindSet)
			if !valid {
				continue
			}
			// For new directories, start watching them.
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					_ = addRecursive(w.watcher, event.Name)
					continue
				}
			}
			db.submit(event.Name, func() {
				onChange(ch)
			})
		case _, ok := <-w.watcher.Errors:
			if !ok {
				return nil
			}
			// Log and continue; transient errors are expected.
		}
	}
}

// Close releases the underlying fsnotify watcher.
func (w *FSWatcher) Close() error {
	if w.watcher != nil {
		return w.watcher.Close()
	}
	return nil
}

// classify maps an fsnotify event to a Change. Returns valid=false when the
// event should be ignored (non-YAML, not under a known kind).
func (w *FSWatcher) classify(event fsnotify.Event, kinds map[string]bool) (Change, bool) {
	ext := strings.ToLower(filepath.Ext(event.Name))
	if ext != ".yaml" && ext != ".yml" {
		return Change{}, false
	}

	rel, err := filepath.Rel(w.root, event.Name)
	if err != nil {
		return Change{}, false
	}
	parts := strings.SplitN(filepath.ToSlash(rel), "/", 3)
	if len(parts) < 2 {
		return Change{}, false
	}
	kind := parts[0]
	if !kinds[kind] {
		return Change{}, false
	}

	name := strings.TrimSuffix(filepath.Base(event.Name), filepath.Ext(event.Name))
	action := ActionAddOrUpdate
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		action = ActionDelete
	}
	return Change{Kind: kind, Name: name, Action: action}, true
}

// addRecursive walks a directory tree and adds each directory to the watcher.
func addRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if d.IsDir() {
			return w.Add(path)
		}
		return nil
	})
}
