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
	intent "github.com/viant/agently-core/protocol/intent"
	identity "github.com/viant/agently-core/protocol/resource"
	tpl "github.com/viant/agently-core/protocol/template"
	bundle "github.com/viant/agently-core/protocol/templatebundle"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	meta "github.com/viant/agently-core/workspace/service/meta"
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
	if err := r.check(dir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return fs.WalkDir(r.root.FS(), dir, func(file string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = r.check(file); err != nil {
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
	return ExtensionDirectory{Kind: "intent", Namespace: namespace, Directory: directory, FormatVersion: 1, Suffixes: []string{".yaml", ".yml"}, Load: NativeIntentLoader}
}
func NativeIntentLoader(ctx context.Context, r *ExtensionReader, file string) (json.RawMessage, error) {
	raw, err := r.ReadFile(ctx, file)
	if err != nil {
		return nil, err
	}
	var p intent.Profile
	if err = decodeNativeAuthored(ctx, r, file, raw, &p); err != nil {
		return nil, err
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
			if !extensionPath(uri) {
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
	if err := r.check(c.bundleDirectory); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return fs.WalkDir(r.root.FS(), c.bundleDirectory, func(file string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = r.check(file); err != nil {
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
	d := IntentDirectory(namespace, c.directory)
	d.Load = func(ctx context.Context, r *ExtensionReader, file string) (json.RawMessage, error) {
		raw, err := NativeIntentLoader(ctx, r, file)
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
	if !extensionPath(location) {
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
