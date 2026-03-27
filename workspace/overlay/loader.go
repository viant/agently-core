package overlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/viant/agently-core/workspace"
	wscodec "github.com/viant/agently-core/workspace/codec"
)

var (
	loadOnce sync.Once
	// cached overlays; never mutated after loadOnce executes.
	cached []*Overlay
)

// All returns the list of overlays loaded from the workspace.  The result is
// cached for the lifetime of the process.
func All() []*Overlay {
	loadOnce.Do(func() { cached = load() })
	return cached
}

// load scans "<workspace>/elicitation" and parses every *.yaml|yml|json file
// into an Overlay structure. Invalid files are skipped silently.
func load() []*Overlay {
	root := filepath.Join(workspace.Root(), "elicitation")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var result []*Overlay
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		switch ext {
		case ".yaml", ".yml", ".json":
		default:
			continue
		}

		var ov Overlay
		if ext == ".json" {
			data, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				continue
			}
			if err := json.Unmarshal(data, &ov); err != nil {
				continue
			}
		} else {
			if err := wscodec.DecodeFile(filepath.Join(root, name), &ov); err != nil {
				data, readErr := os.ReadFile(filepath.Join(root, name))
				if readErr != nil || json.Unmarshal(data, &ov) != nil {
					continue
				}
			}
		}
		result = append(result, &ov)
	}
	return result
}
