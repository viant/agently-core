package intake

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/file"
	"github.com/viant/agently-core/protocol/binding"
	intent "github.com/viant/agently-core/protocol/intent"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/repository/base"
	meta "github.com/viant/agently-core/workspace/service/meta"
	"github.com/viant/forge/backend/authoring/markdown"
	"gopkg.in/yaml.v3"
)

type Reader interface {
	LoadAll(context.Context) ([]*intent.Profile, error)
	Load(context.Context, string) (*intent.Profile, error)
}

type Repository struct {
	reader Reader
	*base.Repository[intent.Profile]
	legacyIntent *base.Repository[intent.Profile]
	legacyPrompt *base.Repository[intent.Profile]
	fs           afs.Service
	store        workspace.Store
}

// NewWithReader binds definition consumption to a host-owned reader.
func NewWithReader(reader Reader) *Repository { return &Repository{reader: reader} }

func New(fs afs.Service) *Repository {
	return &Repository{
		Repository:   base.New[intent.Profile](fs, workspace.KindPlaybook),
		legacyIntent: base.New[intent.Profile](fs, workspace.KindIntent),
		legacyPrompt: base.New[intent.Profile](fs, workspace.KindPrompt),
		fs:           fs,
	}
}

func NewWithStore(store workspace.Store) *Repository {
	return &Repository{
		Repository:   base.NewWithStore[intent.Profile](store, workspace.KindPlaybook),
		legacyIntent: base.NewWithStore[intent.Profile](store, workspace.KindIntent),
		legacyPrompt: base.NewWithStore[intent.Profile](store, workspace.KindPrompt),
		store:        store,
	}
}

// source selects one exact local identity. Parallel format/legacy roots may
// remain readable, but must never silently shadow the same logical ID.
func (r *Repository) source(ctx context.Context, name string) (*base.Repository[intent.Profile], string, error) {
	if r == nil || r.Repository == nil {
		return nil, "", fmt.Errorf("playbook repository not configured")
	}
	var selected *base.Repository[intent.Profile]
	var selectedKind string
	for _, candidate := range []struct {
		kind string
		repo *base.Repository[intent.Profile]
	}{
		{workspace.KindPlaybook, r.Repository},
		{workspace.KindIntent, r.legacyIntent},
		{workspace.KindPrompt, r.legacyPrompt},
	} {
		if candidate.repo == nil {
			continue
		}
		exists, err := r.sourceExists(ctx, candidate.kind, candidate.repo, name)
		if err != nil {
			return nil, "", err
		}
		if exists {
			if selected != nil {
				return nil, "", fmt.Errorf("duplicate playbook identity %q in %s and %s", name, selectedKind, candidate.kind)
			}
			selected, selectedKind = candidate.repo, candidate.kind
		}
	}
	if selected != nil {
		return selected, selectedKind, nil
	}
	return r.Repository, workspace.KindPlaybook, nil
}

func (r *Repository) sourceExists(ctx context.Context, kind string, repo *base.Repository[intent.Profile], name string) (bool, error) {
	if r.store != nil {
		return r.store.Exists(ctx, kind, name)
	}
	_, err := r.localPath(ctx, kind, name)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func (r *Repository) Load(ctx context.Context, name string) (*intent.Profile, error) {
	if r != nil && r.reader != nil {
		return r.reader.Load(ctx, name)
	}
	source, kind, err := r.source(ctx, name)
	if err != nil {
		return nil, err
	}
	filename, pathErr := r.localPath(ctx, kind, name)
	if pathErr != nil && !os.IsNotExist(pathErr) {
		return nil, pathErr
	}
	if pathErr != nil {
		return source.Load(ctx, name)
	}
	raw, err := r.readRaw(ctx, kind, name, filename)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(filename), ".md") {
		return r.decodeMarkdown(ctx, filename, raw, name)
	}
	return r.decodeYAML(ctx, filename, raw)
}

func (r *Repository) GetRaw(ctx context.Context, name string) ([]byte, error) {
	source, kind, err := r.source(ctx, name)
	if err != nil {
		return nil, err
	}
	filename, pathErr := r.localPath(ctx, kind, name)
	if pathErr != nil && !os.IsNotExist(pathErr) {
		return nil, pathErr
	}
	if pathErr == nil {
		return r.readRaw(ctx, kind, name, filename)
	}
	return source.GetRaw(ctx, name)
}

// Save preserves an existing YAML/YML definition and writes a new playbook as
// Markdown. The parsed profile remains the same runtime model in either form.
func (r *Repository) Save(ctx context.Context, name string, profile *intent.Profile) error {
	if r == nil || profile == nil {
		return fmt.Errorf("playbook repository or profile is nil")
	}
	logical := name
	if extension := strings.ToLower(filepath.Ext(name)); extension == ".md" || extension == ".yaml" || extension == ".yml" {
		logical = strings.TrimSuffix(name, filepath.Ext(name))
	}
	source, kind, err := r.source(ctx, logical)
	if err != nil {
		return err
	}
	filename, pathErr := r.localPath(ctx, kind, logical)
	if pathErr != nil && !os.IsNotExist(pathErr) {
		return pathErr
	}
	if pathErr == nil && !strings.EqualFold(filepath.Ext(filename), ".md") {
		encoded, err := yaml.Marshal(profile)
		if err != nil {
			return err
		}
		return r.writeRaw(ctx, kind, logical, filename, encoded)
	}
	if pathErr == nil && (strings.EqualFold(filepath.Ext(name), ".yaml") || strings.EqualFold(filepath.Ext(name), ".yml")) {
		return fmt.Errorf("cannot change existing Markdown playbook %q to YAML by name", logical)
	}
	if pathErr != nil && kind != workspace.KindPlaybook {
		return source.Save(ctx, logical, profile)
	}
	if pathErr != nil && (strings.EqualFold(filepath.Ext(name), ".yaml") || strings.EqualFold(filepath.Ext(name), ".yml")) {
		encoded, err := yaml.Marshal(profile)
		if err != nil {
			return err
		}
		filename = filepath.Join(r.root(), workspace.KindPlaybook, name)
		return r.writeRaw(ctx, workspace.KindPlaybook, logical, filename, encoded)
	}
	metadata := *profile
	metadata.Messages = nil
	frontmatter, err := yaml.Marshal(&metadata)
	if err != nil {
		return err
	}
	messages := make([]markdown.Message, 0, len(profile.Messages))
	for _, message := range profile.Messages {
		messages = append(messages, markdown.Message{Role: message.Role, Text: message.Text, URI: message.URI})
	}
	encoded, err := markdown.Marshal(frontmatter, messages)
	if err != nil {
		return err
	}
	if pathErr != nil {
		filename = filepath.Join(r.root(), workspace.KindPlaybook, logical+".md")
	}
	return r.writeRaw(ctx, workspace.KindPlaybook, logical, filename, encoded)
}

func (r *Repository) root() string {
	if r.store != nil {
		return r.store.Root()
	}
	return workspace.Root()
}

func (r *Repository) writeRaw(ctx context.Context, kind, name, filename string, encoded []byte) error {
	if r.store != nil {
		return r.store.Save(ctx, kind, name+filepath.Ext(filename), encoded)
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	return r.fs.Upload(ctx, filename, file.DefaultFileOsMode, strings.NewReader(string(encoded)))
}

// List merges all catalogs and rejects duplicate logical IDs across roots.
func (r *Repository) List(ctx context.Context) ([]string, error) {
	if r == nil || r.Repository == nil {
		return nil, fmt.Errorf("intake profile repository not configured")
	}
	seen := map[string]string{}
	var names []string
	for _, candidate := range []struct {
		kind string
		repo *base.Repository[intent.Profile]
	}{{workspace.KindPlaybook, r.Repository}, {workspace.KindIntent, r.legacyIntent}, {workspace.KindPrompt, r.legacyPrompt}} {
		repo := candidate.repo
		if repo == nil {
			continue
		}
		if r.store == nil {
			exists, err := repo.DirExists(ctx)
			if err != nil {
				return nil, err
			}
			if !exists {
				continue
			}
		}
		items, err := r.listKind(ctx, candidate.kind, repo)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, name := range items {
			key := strings.ToLower(name)
			if first := seen[key]; first != "" {
				return nil, fmt.Errorf("duplicate playbook identity %q in %s and %s", name, first, candidate.kind)
			}
			seen[key] = candidate.kind
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (r *Repository) localPath(ctx context.Context, kind, name string) (string, error) {
	root := workspace.Root()
	if r.store != nil {
		root = r.store.Root()
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if ext := strings.ToLower(filepath.Ext(name)); ext != ".md" && ext != ".yaml" && ext != ".yml" {
		base = name
	}
	var found string
	for _, extension := range []string{".md", ".yaml", ".yml"} {
		for _, candidate := range []string{
			filepath.Join(root, kind, base+extension),
			filepath.Join(root, kind, base, filepath.Base(base)+extension),
		} {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if _, err := os.Stat(candidate); err == nil {
				if found != "" && found != candidate {
					return "", fmt.Errorf("duplicate playbook format %s and %s", found, candidate)
				}
				found = candidate
			} else if !os.IsNotExist(err) {
				return "", err
			}
		}
	}
	if found == "" {
		return "", os.ErrNotExist
	}
	return found, nil
}

func (r *Repository) listKind(ctx context.Context, kind string, repo *base.Repository[intent.Profile]) ([]string, error) {
	if r.store != nil {
		return repo.List(ctx)
	}
	dir := filepath.Join(workspace.Root(), kind)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	seen := map[string]bool{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if entry.IsDir() {
			if _, err := r.localPath(ctx, kind, name); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return nil, err
			}
		} else if extension := strings.ToLower(filepath.Ext(name)); extension == ".md" || extension == ".yaml" || extension == ".yml" {
			name = strings.TrimSuffix(name, filepath.Ext(name))
		} else {
			continue
		}
		if seen[strings.ToLower(name)] {
			return nil, fmt.Errorf("duplicate playbook identity %q in %s", name, kind)
		}
		seen[strings.ToLower(name)] = true
		names = append(names, name)
	}
	return names, nil
}

func (r *Repository) readRaw(ctx context.Context, kind, name, filename string) ([]byte, error) {
	if r.store != nil {
		return r.store.Load(ctx, kind, name)
	}
	return r.fs.DownloadWithURL(ctx, filename)
}

func (r *Repository) decodeMarkdown(ctx context.Context, filename string, raw []byte, name string) (*intent.Profile, error) {
	doc, err := markdown.Parse(raw)
	if err != nil {
		return nil, err
	}
	var profile intent.Profile
	if len(doc.Metadata) > 0 {
		var node yaml.Node
		if err := yaml.Unmarshal(doc.Metadata, &node); err != nil {
			return nil, err
		}
		if err := validatePlaybookKind(&node); err != nil {
			return nil, err
		}
		if err := meta.ResolveImports(ctx, afs.New(), &node, filepath.Dir(filename)); err != nil {
			return nil, err
		}
		if err := node.Decode(&profile); err != nil {
			return nil, err
		}
	}
	if len(doc.Messages) > 0 {
		if len(profile.Messages) > 0 {
			return nil, fmt.Errorf("playbook Markdown body conflicts with frontmatter messages")
		}
		for _, message := range doc.Messages {
			if message.Field != "" {
				return nil, fmt.Errorf("playbook message cannot target agent field %q", message.Field)
			}
			if message.Engine != "" {
				return nil, fmt.Errorf("playbook message cannot declare agent prompt engine %q", message.Engine)
			}
			text := message.Text
			if strings.Contains(text, "$import(") {
				text, err = binding.ResolveTextImports(ctx, afs.New(), text, filename)
				if err != nil {
					return nil, err
				}
			}
			profile.Messages = append(profile.Messages, intent.Message{Role: message.Role, Text: text, URI: message.URI})
		}
	}
	if profile.ID == "" {
		profile.ID = name
	}
	return &profile, profile.Validate()
}

func (r *Repository) decodeYAML(ctx context.Context, filename string, raw []byte) (*intent.Profile, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return nil, err
	}
	if err := validatePlaybookKind(&node); err != nil {
		return nil, err
	}
	if err := meta.ResolveImports(ctx, afs.New(), &node, filepath.Dir(filename)); err != nil {
		return nil, err
	}
	var profile intent.Profile
	if err := node.Decode(&profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func validatePlaybookKind(node *yaml.Node) error {
	if node == nil || len(node.Content) == 0 {
		return nil
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if strings.EqualFold(root.Content[i].Value, "kind") {
			value := strings.TrimSpace(root.Content[i+1].Value)
			if !strings.EqualFold(value, "playbook") && !strings.EqualFold(value, "intent") {
				return fmt.Errorf("expected playbook kind, got %q", root.Content[i+1].Value)
			}
			return nil
		}
	}
	return nil
}

func (r *Repository) LoadAll(ctx context.Context) ([]*intent.Profile, error) {
	if r != nil && r.reader != nil {
		return r.reader.LoadAll(ctx)
	}
	if r == nil || r.Repository == nil {
		return nil, fmt.Errorf("prompt repository not configured")
	}
	names, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)
	out := make([]*intent.Profile, 0, len(names))
	seenIDs := map[string]string{}
	for _, name := range names {
		profile, err := r.Load(ctx, name)
		if err != nil {
			return nil, err
		}
		if profile == nil {
			continue
		}
		if id := strings.ToLower(strings.TrimSpace(profile.ID)); id != "" {
			if first := seenIDs[id]; first != "" && first != name {
				return nil, fmt.Errorf("duplicate declared playbook id %q in %s and %s", profile.ID, first, name)
			}
			seenIDs[id] = name
		}
		out = append(out, profile)
	}
	return out, nil
}

func FilterAllowedProfiles(profiles []*intent.Profile, allow []string) []*intent.Profile {
	if len(profiles) == 0 {
		return nil
	}
	if len(allow) == 0 {
		return profiles
	}
	allowed := map[string]struct{}{}
	for _, id := range allow {
		if trimmed := strings.ToLower(strings.TrimSpace(id)); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	filtered := make([]*intent.Profile, 0, len(profiles))
	for _, profile := range profiles {
		if profile == nil {
			continue
		}
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(profile.ID))]; ok {
			filtered = append(filtered, profile)
		}
	}
	return filtered
}

// AdmitSelected freshly reads delegated definitions; explicit filesystem repositories retain their original behavior.
func (r *Repository) AdmitSelected(ctx context.Context, key string, selected *intent.Profile) (*intent.Profile, error) {
	if r != nil && r.reader != nil {
		return r.reader.Load(ctx, key)
	}
	return selected, nil
}
