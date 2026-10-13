package executor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	cfg "github.com/viant/agently-core/protocol/mcp/config"
	identity "github.com/viant/agently-core/protocol/resource"
	proto "github.com/viant/agently-core/protocol/skill"
	resources "github.com/viant/agently-core/service/resource"
	mcp "github.com/viant/mcp"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

const nativeSkillConnection = "internal-skills"
const nativeSkillProvider = "agently:native-skills"

// Internal native definitions preserve exact local document bytes, including
// Agently's existing structured metadata. They are generic primitive JSON;
// ordinary MCP skills retain their separate, unchanged native SEP contract.
type nativeSkillDefinition = resources.NativeSkillDefinition
type primitiveSkillDelivery struct {
	gateway   *resources.Gateway
	source    *resources.NativeSkillSource
	originals map[string]*proto.Skill
	uris      map[string]string
}

func (b *Builder) configurePrimitiveSkills(ctx context.Context, out *Runtime) error {
	if out.Skills == nil || out.PrimitiveProviders == nil {
		return nil
	}
	d, err := newPrimitiveSkillDelivery(ctx, out.PrimitiveProviders, out.Skills.LocalSnapshot())
	if err != nil {
		return err
	}
	out.Skills.SetDelivery(d.source, d)
	return nil
}
func newPrimitiveSkillDelivery(ctx context.Context, gateway *resources.Gateway, items []*proto.Skill) (*primitiveSkillDelivery, error) {
	d := &primitiveSkillDelivery{gateway: gateway, source: &resources.NativeSkillSource{Gateway: gateway, LocalConnection: nativeSkillConnection}, originals: map[string]*proto.Skill{}, uris: map[string]string{}}
	var bindings []resources.LocalResourceBinding
	for _, item := range items {
		uri := "skill://workspace/" + url.PathEscape(item.Frontmatter.Name) + "/SKILL.md"
		root := item.Root
		loader := func(ctx context.Context, reader *resources.ExtensionReader, _ string) (json.RawMessage, error) {
			opened, err := os.OpenRoot(root)
			if err != nil {
				return nil, err
			}
			defer opened.Close()
			definition := nativeSkillDefinition{Files: map[string][]byte{}}
			var count, total int
			err = fs.WalkDir(opened.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if entry.Type()&fs.ModeSymlink != 0 {
					return fmt.Errorf("native skill symlink")
				}
				if entry.IsDir() {
					return nil
				}
				if !entry.Type().IsRegular() {
					return fmt.Errorf("native skill non-file")
				}
				raw, err := reader.ReadFile(ctx, name)
				if err != nil {
					return err
				}
				count++
				total += len(raw)
				if count > 512 || total > 16<<20 {
					return fmt.Errorf("native skill inventory exceeds bounds")
				}
				if name == "SKILL.md" {
					definition.Document = raw
				} else {
					definition.Files[name] = raw
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			if len(definition.Document) == 0 {
				return nil, fmt.Errorf("native skill entrypoint missing")
			}
			return json.Marshal(definition)
		}
		snapshot, err := resources.NewNativeAssetSnapshot(ctx, root, []resources.NativeSnapshotDefinition{{URI: uri, Title: item.Frontmatter.Name, FormatVersion: 1, File: "SKILL.md", Load: loader}}, resources.NativeSnapshotOptions{})
		if err != nil {
			return nil, err
		}
		added, err := snapshot.Bindings(nativeSkillProvider, func(_ context.Context, a identity.VerifiedActor, u identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
			return nativeSkillRevision{actor: a, uri: u.String()}, nil
		})
		if err != nil {
			return nil, err
		}
		added[0].Description = item.Frontmatter.Description
		bindings = append(bindings, added...)
		copied := *item
		d.originals[uri] = &copied
		d.uris[item.Frontmatter.Name] = uri
	}
	if len(bindings) > 0 {
		provider, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: nativeSkillProvider, Actor: gateway.Actor, Verify: gateway.Verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: bindings, Validators: map[string]resources.LocalResourceValidator{"skill": resources.ValidateNativeSkillDefinition}})
		if err != nil {
			return nil, err
		}
		if err = gateway.Manager.RegisterLocal(ctx, nativeSkillConnection, &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: nativeSkillProvider}, func(context.Context) (client.Interface, error) { return resources.NewLocalMCPClient(provider) }); err != nil {
			return nil, err
		}
	}
	return d, nil
}

type nativeSkillRevision struct {
	actor identity.VerifiedActor
	uri   string
}

func (p nativeSkillRevision) SelectRevision(_ context.Context, ref identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if ref.URI != p.uri || len(c) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "native-skill:" + p.actor.IdentityRevision, ValidUntil: p.actor.ValidUntil}, nil
}
func (d *primitiveSkillDelivery) readNative(ctx context.Context, uri string) (*proto.Skill, *nativeSkillDefinition, error) {
	original := d.originals[uri]
	if original == nil || !d.gateway.Manager.IsLocalClient(nativeSkillConnection) {
		return nil, nil, identity.ErrResourceDenied
	}
	got, err := d.gateway.GetForProvider(ctx, nativeSkillProvider, identity.ResourceRef{URI: uri}, nil)
	if err != nil {
		return nil, nil, err
	}
	var definition nativeSkillDefinition
	if err = json.Unmarshal(got.Resource.DefinitionBytes, &definition); err != nil {
		return nil, nil, err
	}
	parsed, diags, err := proto.Parse(original.Path, original.Root, original.Source, string(definition.Document))
	if err != nil {
		return nil, nil, err
	}
	for _, diag := range diags {
		if diag.Level == "error" {
			return nil, nil, fmt.Errorf("invalid local skill: %s", diag.Message)
		}
	}
	if parsed == nil || parsed.Frontmatter.Name != original.Frontmatter.Name {
		return nil, nil, identity.ErrResourceDenied
	}
	parsed.ServerID = nativeSkillConnection
	parsed.RemoteURI = uri
	manifest := schema.Skill{Uri: uri, Frontmatter: parsed.Frontmatter.Raw}
	manifest.Resources.Files = append(manifest.Resources.Files, schema.SkillResource{Uri: uri, Size: int64(len(definition.Document)), Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(definition.Document))})
	for name, raw := range definition.Files {
		manifest.Resources.Files = append(manifest.Resources.Files, schema.SkillResource{Uri: strings.TrimSuffix(uri, "SKILL.md") + name, Size: int64(len(raw)), Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(raw))})
	}
	parsed.Manifest = &manifest
	return parsed, &definition, nil
}
func (d *primitiveSkillDelivery) ReadLocal(ctx context.Context, item *proto.Skill) (*proto.Skill, error) {
	uri := d.uris[item.Frontmatter.Name]
	original := d.originals[uri]
	if original == nil || original.Root != item.Root || original.Path != item.Path || original.Body != item.Body {
		return nil, identity.ErrResourceStale
	}
	parsed, _, err := d.readNative(ctx, uri)
	return parsed, err
}
func (d *primitiveSkillDelivery) ReadSupporting(ctx context.Context, item *proto.Skill, name string) ([]byte, error) {
	uri := d.uris[item.Frontmatter.Name]
	if uri == "" || item.RemoteURI != uri || item.Root != d.originals[uri].Root {
		return nil, identity.ErrResourceDenied
	}
	_, definition, err := d.readNative(ctx, uri)
	if err != nil {
		return nil, err
	}
	raw, ok := definition.Files[name]
	if !ok {
		return nil, identity.ErrResourceDenied
	}
	return append([]byte(nil), raw...), nil
}
func (d *primitiveSkillDelivery) List(ctx context.Context) ([]proto.Metadata, error) {
	var out []proto.Metadata
	query := resources.CatalogQuery{Limit: 100}
	for {
		page, err := resources.ListPage[json.RawMessage](ctx, d.gateway, "skill", query)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Items {
			meta := proto.Metadata{Namespace: row.Resource.Namespace, URI: row.Resource.URI, Name: row.Resource.Name, Description: row.Resource.Description, ServerID: row.Connection.Name, QualifiedName: row.Connection.Name + "/" + row.Resource.Name}
			if row.Connection.Name == nativeSkillConnection && row.Connection.ProviderIdentity == nativeSkillProvider {
				if original := d.originals[row.Resource.URI]; original != nil {
					meta.Name = original.Frontmatter.Name
					meta.LocalSelector = original.Frontmatter.Name
					meta.ExecutionMode = original.Frontmatter.ContextMode()
				}
			}
			out = append(out, meta)
		}
		if page.Complete {
			return out, nil
		}
		query.Cursor = page.NextCursor
	}
}
func (d *primitiveSkillDelivery) Read(ctx context.Context, key string) (*proto.Skill, bool, error) {
	if !strings.HasPrefix(key, "skill://") || strings.Contains(key, "?") {
		return nil, false, nil
	}
	var selected *resources.Located[json.RawMessage]
	query := resources.CatalogQuery{Limit: 100}
	for {
		page, err := resources.ListPage[json.RawMessage](ctx, d.gateway, "skill", query)
		if err != nil {
			return nil, true, err
		}
		for _, row := range page.Items {
			if row.Resource.URI == key {
				if selected != nil {
					return nil, true, resources.ErrCollision
				}
				copy := row
				selected = &copy
			}
		}
		if page.Complete {
			break
		}
		query.Cursor = page.NextCursor
	}
	if selected == nil {
		return nil, false, nil
	}
	if selected.Connection.Name == nativeSkillConnection && selected.Connection.ProviderIdentity == nativeSkillProvider {
		item, _, err := d.readNative(ctx, key)
		return item, true, err
	}
	got, err := resources.Get[json.RawMessage](ctx, d.gateway, selected.Connection, identity.ResourceRef{URI: key, Revision: selected.Resource.Revision}, nil)
	if err != nil {
		return nil, true, err
	}
	var definition struct {
		Frontmatter proto.Frontmatter
		Body        string
	}
	if err = json.Unmarshal(got.Resource.DefinitionBytes, &definition); err != nil {
		return nil, true, err
	}
	if definition.Frontmatter.Name == "" || definition.Body == "" || definition.Frontmatter.PreprocessEnabled() {
		return nil, true, identity.ErrResourceDenied
	}
	return &proto.Skill{Frontmatter: definition.Frontmatter, Body: definition.Body, Source: "mcp", CatalogURI: key, ServerID: selected.Connection.Name, RemoteURI: key}, true, nil
}

func (d *primitiveSkillDelivery) ReadNativeFile(ctx context.Context, target string) ([]byte, bool, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, false, nil
	}
	if parsed.Scheme == "file" {
		if parsed.Host != "" && parsed.Host != "localhost" {
			return nil, false, nil
		}
		target = parsed.Path
	} else if parsed.Scheme != "" {
		return nil, false, nil
	}
	var selected, relative string
	for uri, original := range d.originals {
		rel, err := filepath.Rel(original.Root, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		if selected != "" {
			return nil, true, resources.ErrCollision
		}
		selected, relative = uri, filepath.ToSlash(rel)
	}
	if selected == "" {
		return nil, false, nil
	}
	_, definition, err := d.readNative(ctx, selected)
	if err != nil {
		return nil, true, err
	}
	if relative == "SKILL.md" {
		return append([]byte(nil), definition.Document...), true, nil
	}
	raw, ok := definition.Files[relative]
	if !ok {
		return nil, true, identity.ErrResourceDenied
	}
	return append([]byte(nil), raw...), true, nil
}
