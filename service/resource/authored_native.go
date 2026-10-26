package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/storage"
	"github.com/viant/agently-core/protocol/binding"
	intent "github.com/viant/agently-core/protocol/intent"
	identity "github.com/viant/agently-core/protocol/resource"
	tpl "github.com/viant/agently-core/protocol/template"
	bundle "github.com/viant/agently-core/protocol/templatebundle"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	meta "github.com/viant/agently-core/workspace/service/meta"
	"github.com/viant/forge/backend/authoring/markdown"
	"gopkg.in/yaml.v3"
)

// AuthoredCompilation is an ephemeral static compiler input. Its parsed bundle
// inputs are released after the one NativeAssetSnapshot has captured outputs.
// It has no caller authority, catalog index, runtime cache, or read API.
type AuthoredCompilation struct {
	directory string
	bundles   []*bundle.Bundle
}

func NewAuthoredCompilation(directory string) *AuthoredCompilation {
	return &AuthoredCompilation{directory: directory}
}
func (c *AuthoredCompilation) Release() { c.bundles = nil }
func (c *AuthoredCompilation) Prepare(ctx context.Context, r *ExtensionReader) error {
	c.bundles = nil
	dir := path.Join(c.directory, "bundles")
	if err := r.CheckPath(dir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return fs.WalkDir(r.FS(), dir, func(file string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = r.CheckPath(file); err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		if !strings.HasSuffix(file, ".yaml") && !strings.HasSuffix(file, ".yml") {
			return nil
		}
		raw, err := r.ReadFile(ctx, file)
		if err != nil {
			return err
		}
		var b bundle.Bundle
		if err = decodeNativeAuthored(ctx, r, file, raw, &b); err != nil {
			return err
		}
		if err = b.Validate(); err != nil {
			return err
		}
		c.bundles = append(c.bundles, &b)
		return nil
	})
}
func (c *AuthoredCompilation) TemplateDirectory(namespace string) ExtensionDirectory {
	return ExtensionDirectory{Kind: "template", Namespace: namespace, Directory: c.directory, FormatVersion: 1, Suffixes: []string{".yaml", ".yml"}, ExcludeDirectories: []string{"bundles"}, Load: func(ctx context.Context, r *ExtensionReader, file string) (json.RawMessage, error) {
		raw, err := r.ReadFile(ctx, file)
		if err != nil {
			return nil, err
		}
		var t tpl.Template
		if err = decodeNativeAuthored(ctx, r, file, raw, &t); err != nil {
			return nil, err
		}
		if err = t.Validate(); err != nil {
			return nil, err
		}
		body := TemplateDefinition{Template: t}
		for _, b := range c.bundles {
			for _, ref := range b.Templates {
				if strings.EqualFold(ref, t.ID) || strings.EqualFold(ref, t.Name) {
					body.TemplateBundles = append(body.TemplateBundles, b)
					break
				}
			}
		}
		return json.Marshal(body)
	}}
}
func IntentDirectory(namespace, directory string) ExtensionDirectory {
	// Legacy intents may contain supporting *.user.md prompt fragments. Only
	// their historical YAML/YML entry points are native intent roots.
	return ExtensionDirectory{Kind: "intent", Namespace: namespace, Directory: directory, FormatVersion: 1, Suffixes: []string{".yaml", ".yml"}, Load: NativeIntentLoader}
}
func PlaybookDirectory(namespace, directory string) ExtensionDirectory {
	return ExtensionDirectory{Kind: "playbook", Namespace: namespace, Directory: directory, FormatVersion: 1, Suffixes: []string{".md", ".yaml", ".yml"}, IncludeFile: includeNativePlaybookFile, Load: nativePlaybookLoader}
}

func nativePlaybookLoader(ctx context.Context, reader *ExtensionReader, file string) (json.RawMessage, error) {
	raw, err := reader.ReadFile(ctx, file)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(path.Ext(file), ".md") {
		doc, err := markdown.Parse(raw)
		if err != nil {
			return nil, err
		}
		raw = doc.Metadata
	}
	if err := validateNativeDeclaredKind(raw, "playbook"); err != nil {
		return nil, err
	}
	return NativeIntentLoader(ctx, reader, file)
}

func includeNativePlaybookFile(ctx context.Context, reader *ExtensionReader, file string) (bool, error) {
	if !strings.EqualFold(path.Ext(file), ".md") {
		return true, nil // existing YAML/YML discovery is unchanged
	}
	raw, err := reader.ReadFile(ctx, file)
	if err != nil {
		return false, err
	}
	if !bytes.HasPrefix(raw, []byte("---\n")) && !bytes.HasPrefix(raw, []byte("\xef\xbb\xbf---\n")) {
		return false, nil // README and prompt fragments are not definitions
	}
	doc, err := markdown.Parse(raw)
	if err != nil {
		return false, err
	}
	if len(doc.Metadata) == 0 {
		return false, nil
	}
	var node yaml.Node
	if err := yaml.Unmarshal(doc.Metadata, &node); err != nil {
		return false, err
	}
	if len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
		return false, nil
	}
	var id, kind string
	idDeclared := false
	for i := 0; i+1 < len(node.Content[0].Content); i += 2 {
		key, value := node.Content[0].Content[i], node.Content[0].Content[i+1]
		switch strings.ToLower(strings.TrimSpace(key.Value)) {
		case "id":
			idDeclared = true
			id = strings.TrimSpace(value.Value)
		case "kind":
			kind = strings.TrimSpace(value.Value)
		}
	}
	if idDeclared && id == "" {
		return false, identity.ErrResource
	}
	if id == "" && kind == "" {
		return false, nil
	}
	if kind != "" && !strings.EqualFold(kind, "playbook") {
		return false, identity.ErrResource
	}
	return true, nil
}
func NativeIntentLoader(ctx context.Context, r *ExtensionReader, file string) (json.RawMessage, error) {
	raw, err := r.ReadFile(ctx, file)
	if err != nil {
		return nil, err
	}
	var p intent.Profile
	var authored markdown.Document
	if strings.EqualFold(path.Ext(file), ".md") {
		authored, err = markdown.Parse(raw)
		if err != nil {
			return nil, err
		}
		raw = authored.Metadata
	}
	if len(raw) > 0 {
		if err := validateNativePlaybookKind(raw); err != nil {
			return nil, err
		}
		raw, err = stripNativePlaybookKind(raw)
		if err != nil {
			return nil, err
		}
		if err = decodeNativeAuthored(ctx, r, file, raw, &p); err != nil {
			return nil, err
		}
	}
	if len(authored.Messages) > 0 {
		if len(p.Messages) > 0 {
			return nil, identity.ErrResource
		}
		for _, message := range authored.Messages {
			if message.Field != "" || message.Engine != "" {
				return nil, identity.ErrResource
			}
			text := message.Text
			if strings.Contains(text, "$import(") {
				text, err = binding.ResolveTextImportsWithReader(ctx, func(ctx context.Context, target string) ([]byte, error) {
					target = path.Clean(strings.TrimPrefix(target, "file://"))
					if !ValidExtensionPath(target) {
						return nil, identity.ErrResourceDenied
					}
					return r.ReadFile(ctx, target)
				}, text, file)
				if err != nil {
					return nil, err
				}
			}
			p.Messages = append(p.Messages, intent.Message{Role: message.Role, Text: text, URI: message.URI})
		}
	}
	if p.ID == "" {
		p.ID = strings.TrimSuffix(path.Base(file), path.Ext(file))
	}
	if err = p.Validate(); err != nil {
		return nil, err
	}
	// Supporting static text is compiled by the provider, never read from the
	// consuming client's filesystem. Reader confinement applies to every import.
	for i := range p.Messages {
		if uri := p.Messages[i].URI; uri != "" {
			if !ValidExtensionPath(uri) {
				return nil, identity.ErrResource
			}
			text, err := r.ReadFile(ctx, uri)
			if err != nil {
				return nil, err
			}
			p.Messages[i].Text = strings.TrimSpace(string(text))
			p.Messages[i].URI = ""
		}
	}
	return json.Marshal(p)
}

func stripNativePlaybookKind(raw []byte) ([]byte, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return nil, err
	}
	if len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
		return raw, nil
	}
	root := node.Content[0]
	for index := 0; index+1 < len(root.Content); index += 2 {
		if strings.EqualFold(root.Content[index].Value, "kind") {
			root.Content = append(root.Content[:index], root.Content[index+2:]...)
			return yaml.Marshal(&node)
		}
	}
	return raw, nil
}

func validateNativePlaybookKind(raw []byte) error {
	return validateNativeDeclaredKind(raw, "playbook", "intent")
}

func validateNativeDeclaredKind(raw []byte, allowed ...string) error {
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return err
	}
	if len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	root := node.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if strings.EqualFold(root.Content[i].Value, "kind") {
			value := strings.ToLower(strings.TrimSpace(root.Content[i+1].Value))
			for _, kind := range allowed {
				if value == kind {
					return nil
				}
			}
			return identity.ErrResource
		}
	}
	return nil
}
func strictAuthoredYAML(raw []byte, target any) error {
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return identity.ErrResource
	}
	return nil
}
func ValidateTemplateDefinition(version int64, raw json.RawMessage) error {
	if version != 1 {
		return identity.ErrResource
	}
	v, err := DecodeTemplateDefinition(raw)
	if err != nil {
		return err
	}
	for _, b := range v.TemplateBundles {
		if b == nil {
			return identity.ErrResource
		}
		if err = b.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func ValidateIntentDefinition(version int64, raw json.RawMessage) error {
	if version != 1 {
		return identity.ErrResource
	}
	_, err := DecodeIntentDefinition(raw)
	return err
}

// IntentCompilation materializes only an intent's declared bundle definitions.
// Parsed static inputs are discarded after the existing snapshot is compiled.
type IntentCompilation struct {
	directory, bundleDirectory string
	bundles                    map[string]*toolbundle.Bundle
}

func NewIntentCompilation(directory, bundleDirectory string) *IntentCompilation {
	return &IntentCompilation{directory: directory, bundleDirectory: bundleDirectory}
}
func (c *IntentCompilation) Release() { c.bundles = nil }
func (c *IntentCompilation) Prepare(ctx context.Context, r *ExtensionReader) error {
	c.bundles = map[string]*toolbundle.Bundle{}
	if err := r.CheckPath(c.bundleDirectory); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return fs.WalkDir(r.FS(), c.bundleDirectory, func(file string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = r.CheckPath(file); err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		if !strings.HasSuffix(file, ".yaml") && !strings.HasSuffix(file, ".yml") {
			return nil
		}
		raw, err := r.ReadFile(ctx, file)
		if err != nil {
			return err
		}
		var b toolbundle.Bundle
		if err = decodeNativeAuthored(ctx, r, file, raw, &b); err != nil {
			return err
		}
		if err = b.Validate(); err != nil {
			return err
		}
		key := strings.ToLower(strings.TrimSpace(b.ID))
		if c.bundles[key] != nil {
			return ErrCollision
		}
		c.bundles[key] = &b
		return nil
	})
}
func (c *IntentCompilation) IntentDirectory(namespace string) ExtensionDirectory {
	return c.withBundles(IntentDirectory(namespace, c.directory))
}
func (c *IntentCompilation) PlaybookDirectory(namespace string) ExtensionDirectory {
	return c.withBundles(PlaybookDirectory(namespace, c.directory))
}
func (c *IntentCompilation) withBundles(d ExtensionDirectory) ExtensionDirectory {
	load := d.Load
	d.Load = func(ctx context.Context, r *ExtensionReader, file string) (json.RawMessage, error) {
		raw, err := load(ctx, r, file)
		if err != nil {
			return nil, err
		}
		p, err := DecodeIntentDefinition(raw)
		if err != nil {
			return nil, err
		}
		for _, id := range p.ToolBundles {
			b := c.bundles[strings.ToLower(strings.TrimSpace(id))]
			if b == nil {
				return nil, identity.ErrResource
			}
			p.ToolBundleDefinitions = append(p.ToolBundleDefinitions, b)
		}
		return json.Marshal(p)
	}
	return d
}

// Imports retain the existing authoring semantics, but every static read belongs
// to the same confined snapshot reader and therefore its generation guard.
type nativeImportReader struct {
	afs.Service
	reader *ExtensionReader
}

func (r *nativeImportReader) DownloadWithURL(ctx context.Context, location string, _ ...storage.Option) ([]byte, error) {
	location = path.Clean(location)
	if !ValidExtensionPath(location) {
		return nil, identity.ErrResourceDenied
	}
	return r.reader.ReadFile(ctx, location)
}
func decodeNativeAuthored(ctx context.Context, r *ExtensionReader, file string, raw []byte, target any) error {
	var node yaml.Node
	if err := strictAuthoredYAML(raw, &node); err != nil {
		return err
	}
	if err := resolveNativeAuthoredImports(ctx, r, file, &node); err != nil {
		return err
	}
	resolved, err := yaml.Marshal(&node)
	if err != nil {
		return err
	}
	return strictAuthoredYAML(resolved, target)
}

func resolveNativeAuthoredImports(ctx context.Context, r *ExtensionReader, file string, node *yaml.Node) error {
	return meta.ResolveImports(ctx, &nativeImportReader{reader: r}, node, path.Dir(file))
}
