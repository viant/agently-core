package skill

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"time"
)

type Watcher struct {
	service  *Service
	interval time.Duration
}

func NewWatcher(service *Service) *Watcher {
	return &Watcher{service: service, interval: 30 * time.Second}
}

type manifestStamp struct {
	modified time.Time
	size     int64
}

// Only immediate child manifests are indexed by Loader.LoadAll. Supporting
// documents are read when requested. Do not descend into their directories.
func manifestSnapshot(roots []string) (map[string]manifestStamp, error) {
	result := make(map[string]manifestStamp)
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(root, entry.Name(), "SKILL.md")
			info, err := os.Stat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			result[path] = manifestStamp{info.ModTime(), info.Size()}
		}
	}
	return result, nil
}

func (w *Watcher) Start(ctx context.Context) error {
	if w == nil || w.service == nil || w.service.loader == nil {
		return nil
	}
	roots := w.service.loader.Roots()
	previous, _ := manifestSnapshot(roots)
	go func() {
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				next, err := manifestSnapshot(roots)
				if err == nil && !maps.Equal(previous, next) && w.service.Load(ctx) == nil {
					previous = next
				}
			}
		}
	}()
	return nil
}
