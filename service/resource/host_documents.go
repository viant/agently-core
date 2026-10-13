package resource

import (
	"context"
	"encoding/json"
	"fmt"
	bundle "github.com/viant/agently-core/protocol/tool/bundle"
	"gopkg.in/yaml.v3"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HostDocuments is a typed consumer of explicit startup inventory. It stores
// locators only; captured bytes stay in the owning native snapshot/provider.
type HostDocuments struct {
	Gateway                    *Gateway
	Connection, Provider, Root string
	Files                      map[string]string
}

func CaptureHostDocuments(ctx context.Context, root string) (*NativeAssetSnapshot, map[string]string, error) {
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, err
	}
	defer opened.Close()
	files := map[string]string{}
	var definitions []NativeSnapshotDefinition
	directories := []string{"tools/bundles", "tools/instructions", "intake", "agents"}
	for _, directory := range directories {
		if _, err := opened.Stat(directory); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, nil, err
		}
		err = fs.WalkDir(opened.FS(), directory, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("host document symlink")
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("host document non-file")
			}
			base := filepath.Base(name)
			ext := filepath.Ext(name)
			include := directory == "intake" || directory == "tools/instructions" || directory == "tools/bundles" && (ext == ".yaml" || ext == ".yml" || ext == ".json") || directory == "agents" && (ext == ".md" || ext == ".txt" || ext == ".tmpl" || base == "planner.schema.json" || base == "schema.json" || base == "planner.validation.json" || base == "validation.json")
			if !include {
				return nil
			}
			if len(files) >= 4096 {
				return fmt.Errorf("host document inventory exceeds bounds")
			}
			files[name] = NativeDocumentURI(name)
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	// Original webdriver playbook fallback is the top-level tools directory.
	entries, err := fs.ReadDir(opened.FS(), "tools")
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".md" {
			name := "tools/" + entry.Name()
			files[name] = NativeDocumentURI(name)
		}
	}
	if len(files) == 0 {
		return nil, files, nil
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		file := name
		definitions = append(definitions, NativeSnapshotDefinition{URI: files[file], File: file, RawDocument: true, Load: func(ctx context.Context, reader *ExtensionReader, _ string) (json.RawMessage, error) {
			raw, err := reader.ReadFile(ctx, file)
			return json.RawMessage(raw), err
		}})
	}
	snapshot, err := NewNativeAssetSnapshot(ctx, root, definitions, NativeSnapshotOptions{})
	return snapshot, files, err
}
func (d *HostDocuments) Names(prefix string) []string {
	var out []string
	for name := range d.Files {
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
func (d *HostDocuments) Read(ctx context.Context, file string) ([]byte, error) {
	if d == nil || d.Gateway == nil {
		return nil, fmt.Errorf("host documents unavailable")
	}
	file = filepath.ToSlash(filepath.Clean(file))
	uri := d.Files[file]
	if uri == "" {
		return nil, fmt.Errorf("host document is not registered")
	}
	return d.Gateway.ReadNativeResource(ctx, d.Connection, d.Provider, uri)
}
func (d *HostDocuments) ReadURI(ctx context.Context, uri string) ([]byte, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, fmt.Errorf("host document is not registered")
	}
	name := uri
	if parsed.Scheme == "file" {
		if parsed.Host != "" && parsed.Host != "localhost" {
			return nil, fmt.Errorf("host document is not registered")
		}
		name = parsed.Path
	} else if parsed.Scheme != "" {
		return nil, fmt.Errorf("host document is not registered")
	}
	if filepath.IsAbs(name) {
		name, err = filepath.Rel(d.Root, name)
		if err != nil {
			return nil, err
		}
	}
	return d.Read(ctx, name)
}

func (d *HostDocuments) ContainsURI(uri string) bool {
	parsed, err := url.Parse(uri)
	if err != nil {
		return false
	}
	name := uri
	if parsed.Scheme == "file" {
		name = parsed.Path
	} else if parsed.Scheme != "" {
		return false
	}
	if filepath.IsAbs(name) {
		name, err = filepath.Rel(d.Root, name)
		if err != nil {
			return false
		}
	}
	return d.Files[filepath.ToSlash(filepath.Clean(name))] != ""
}
func (d *HostDocuments) RootURI() string { return "file://" + filepath.ToSlash(d.Root) }
func (d *HostDocuments) LoadAll(ctx context.Context) ([]*bundle.Bundle, error) {
	var out []*bundle.Bundle
	seen := map[string]bool{}
	for _, name := range d.Names("tools/bundles/") {
		raw, err := d.Read(ctx, name)
		if err != nil {
			return nil, err
		}
		var item bundle.Bundle
		if err = yaml.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		if err = item.Validate(); err != nil {
			return nil, err
		}
		if seen[item.ID] {
			return nil, fmt.Errorf("duplicate tool bundle id")
		}
		seen[item.ID] = true
		out = append(out, &item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
