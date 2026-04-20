package skill

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	skillrepo "github.com/viant/agently-core/workspace/repository/skill"
)

type Watcher struct {
	service  *Service
	watcher  *fsnotify.Watcher
	debounce time.Duration
}

func NewWatcher(service *Service) *Watcher {
	return &Watcher{service: service, debounce: 250 * time.Millisecond}
}

func (w *Watcher) Start(ctx context.Context) error {
	if w == nil || w.service == nil || w.service.loader == nil {
		return nil
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.watcher = fsw
	for _, root := range skillrepo.ResolveRoots(w.service.defaults) {
		_ = addRecursiveWatch(fsw, root)
	}
	go w.loop(ctx)
	return nil
}

func (w *Watcher) loop(ctx context.Context) {
	defer func() {
		if w.watcher != nil {
			_ = w.watcher.Close()
		}
	}()
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	pending := false
	reload := func() {
		_ = w.service.Load(context.Background())
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
				_ = addRecursiveWatch(w.watcher, ev.Name)
			}
			if !pending {
				timer.Reset(w.debounce)
				pending = true
			}
		case <-timer.C:
			pending = false
			reload()
		case _, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func addRecursiveWatch(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = w.Add(path)
		}
		return nil
	})
}
