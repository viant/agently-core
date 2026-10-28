package executor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	skillformat "github.com/viant/mcp-protocol/extension/skills"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing/fstest"

	cfg "github.com/viant/agently-core/protocol/mcp/config"
	identity "github.com/viant/agently-core/protocol/resource"
	proto "github.com/viant/agently-core/protocol/skill"
	resources "github.com/viant/agently-core/service/resource"
	skills "github.com/viant/agently-core/service/skill"
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
	compiled  []*skillformat.Static
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
	d := &primitiveSkillDelivery{gateway: gateway, source: &resources.NativeSkillSource{Gateway: gateway, LocalConnection: nativeSkillConnection}, originals: map[string]*proto.Skill{}, uris: map[string]string{}, compiled: []*skillformat.Static{}}
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
			files := fstest.MapFS{"SKILL.md": {Data: definition.Document}}
			for name, data := range definition.Files {
				files[name] = &fstest.MapFile{Data: data}
			}
			compiled, err := (skillformat.Compiler{Source: files}).Compile(ctx, uri)
			if err != nil {
				return nil, err
			}
			d.compiled = append(d.compiled, compiled)
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
	{
		provider, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: nativeSkillProvider, NativeSkills: d.compiled, Actor: gateway.Actor, Verify: gateway.Verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: bindings, Validators: map[string]resources.LocalResourceValidator{"skill": resources.ValidateNativeSkillDefinition}})
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
func (d *primitiveSkillDelivery) nativeFile(ctx context.Context, uri, file string) ([]byte, *schema.Skill, error) {
	connection, err := d.gateway.ConnectionForProvider(ctx, nativeSkillProvider)
	if err != nil {
		return nil, nil, err
	}
	got, err := resources.Get[schema.GetSkillResult](ctx, d.gateway, connection, identity.ResourceRef{URI: uri}, nil)
	if err != nil {
		return nil, nil, err
	}
	manifest := got.Definition.Skill
	target := uri
	if file != "SKILL.md" {
		target = strings.TrimSuffix(uri, "SKILL.md") + file
	}
	var expected *schema.SkillResource
	for i := range manifest.Resources.Files {
		if manifest.Resources.Files[i].Uri == target {
			expected = &manifest.Resources.Files[i]
			break
		}
	}
	if expected == nil {
		return nil, nil, identity.ErrResourceDenied
	}
	response, err := d.source.ReadSkillResource(ctx, nativeSkillConnection, target)
	if err != nil {
		return nil, nil, err
	}
	if response == nil || len(response.Contents) != 1 {
		return nil, nil, identity.ErrResourceDenied
	}
	content := response.Contents[0]
	data := []byte(content.Text)
	if content.Blob != "" {
		if content.Text != "" {
			return nil, nil, identity.ErrResourceDenied
		}
		data, err = base64.StdEncoding.DecodeString(content.Blob)
	}
	if err != nil || content.Uri != target || int64(len(data)) != expected.Size || fmt.Sprintf("sha256:%x", sha256.Sum256(data)) != expected.Digest {
		return nil, nil, identity.ErrResourceDenied
	}
	return data, &manifest, nil
}
func (d *primitiveSkillDelivery) readNative(ctx context.Context, uri string) (*proto.Skill, *nativeSkillDefinition, error) {
	original := d.originals[uri]
	if original == nil || !d.gateway.Manager.IsLocalClient(nativeSkillConnection) {
		return nil, nil, identity.ErrResourceDenied
	}
	document, manifest, err := d.nativeFile(ctx, uri, "SKILL.md")
	if err != nil {
		return nil, nil, err
	}
	parsed, diags, err := proto.Parse(original.Path, original.Root, original.Source, string(document))
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
	parsed.Manifest = manifest
	return parsed, &nativeSkillDefinition{Document: document}, nil
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
	raw, _, err := d.nativeFile(ctx, uri, name)
	return raw, err

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
			if row.Resource.Title != "" {
				meta.Name = row.Resource.Title
				meta.QualifiedName = row.Connection.Name + "/" + meta.Name
			}
			if row.Connection.Name == nativeSkillConnection && row.Connection.ProviderIdentity == nativeSkillProvider {
				if original := d.originals[row.Resource.URI]; original != nil {
					meta.Name = original.Frontmatter.Name
					meta.LocalSelector = original.Frontmatter.Name
					meta.ExecutionMode = original.Frontmatter.ContextMode()
					meta.AlwaysInclude = original.Frontmatter.AlwaysIncluded()
					meta.DiscoveryCacheScope = "public"
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
func (d *primitiveSkillDelivery) OwnsReference(key string) bool {
	return d.source.OwnsPublishedSkill(key)
}

func (d *primitiveSkillDelivery) Read(ctx context.Context, key string) (*proto.Skill, bool, error) {
	if !strings.HasPrefix(key, "skill://") || strings.Contains(key, "?") {
		return nil, false, nil
	}
	selected, err := d.selectSkill(ctx, key)
	if err != nil {
		return nil, true, err
	}
	if selected == nil {
		return nil, true, identity.ErrResourceDenied
	}
	if selected.Connection.Name == nativeSkillConnection && selected.Connection.ProviderIdentity == nativeSkillProvider {
		item, _, err := d.readNative(ctx, key)
		return item, true, err
	}
	manifest, published, err := d.source.PublishedSkill(ctx, selected.Connection.Name, key)
	if err != nil && published {
		return nil, true, err
	}
	if !published {
		got, err := resources.Get[schema.GetSkillResult](ctx, d.gateway, selected.Connection, identity.ResourceRef{URI: key, Revision: selected.Resource.Revision}, nil)
		if err != nil {
			return nil, true, err
		}
		manifest = &got.Definition
	}
	maxBytes := 0
	options, err := d.source.Options(ctx, selected.Connection.Name)
	if err != nil {
		return nil, true, err
	}
	if options != nil && options.SkillDiscovery != nil {
		maxBytes = options.SkillDiscovery.MaxSkillBytes
	}
	item, err := skills.ReadManifestDocument(ctx, manifest, manifest.Skill.Uri, selected.Connection.Name, maxBytes, func(ctx context.Context, uri string) (*schema.ReadResourceResult, error) {
		return d.source.ReadSkillResource(ctx, selected.Connection.Name, uri)
	})
	if err != nil {
		return nil, true, err
	}
	item.CatalogURI = key
	return item, true, nil

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
	raw, _, err := d.nativeFile(ctx, selected, relative)
	return raw, true, err

}

func (d *primitiveSkillDelivery) selectSkill(ctx context.Context, key string) (*resources.Located[json.RawMessage], error) {
	if d.source.OwnsPublishedSkill(key) {
		uri, err := identity.ParseResourceURI(key)
		if err != nil {
			return nil, err
		}
		connection, err := d.gateway.ConnectionForResource(ctx, uri, "")
		if err != nil {
			return nil, err
		}
		selected := &resources.Located[json.RawMessage]{}
		selected.Connection = connection
		selected.Resource.URI = key
		return selected, nil
	}
	var selected *resources.Located[json.RawMessage]
	query := resources.CatalogQuery{Limit: 100}
	for {
		page, err := resources.ListPage[json.RawMessage](ctx, d.gateway, "skill", query)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Items {
			if row.Resource.URI == key {
				if selected != nil {
					return nil, resources.ErrCollision
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
	return selected, nil
}

func (d *primitiveSkillDelivery) ReadRemoteResource(ctx context.Context, item *proto.Skill, uri string) (*schema.ReadResourceResult, bool, error) {
	// Native extension references carry their separate manifest identity. Only
	// the generic canonical reference is owned by this adapter.
	if item == nil || item.CatalogURI == "" || strings.Contains(item.CatalogURI, "?") {
		return nil, false, nil
	}
	selected, err := d.selectSkill(ctx, item.CatalogURI)
	if err != nil {
		return nil, true, err
	}
	if selected == nil {
		return nil, true, identity.ErrResourceDenied
	}
	if selected.Connection.Name == nativeSkillConnection && selected.Connection.ProviderIdentity == nativeSkillProvider {
		return nil, false, nil
	}
	if item.ServerID != selected.Connection.Name {
		return nil, true, identity.ErrResourceDenied
	}
	manifest, published, err := d.source.PublishedSkill(ctx, selected.Connection.Name, selected.Resource.URI)
	if published {
		if err != nil {
			return nil, true, err
		}
		if item.RemoteURI != manifest.Skill.Uri {
			return nil, true, identity.ErrResourceDenied
		}
	} else if item.RemoteURI != selected.Resource.URI {
		return nil, false, nil
	}
	result, err := d.source.ReadSkillResource(ctx, selected.Connection.Name, uri)
	return result, true, err
}

// ExportLocalSkill reuses this provider's compiler-backed captured package. The
// standalone native surface does not reconstruct a second filesystem source.
func (d *primitiveSkillDelivery) ExportLocalSkill(ctx context.Context, item *proto.Skill) (*skillformat.Static, error) {
	uri := d.uris[item.Frontmatter.Name]
	original := d.originals[uri]
	if original == nil || original.Path != item.Path || original.Root != item.Root {
		return nil, identity.ErrResourceDenied
	}
	cli, err := d.source.Get(ctx, "", nativeSkillConnection)
	if err != nil {
		return nil, err
	}
	native, ok := cli.(interface {
		GetSkill(context.Context, string, ...client.RequestOption) (*schema.GetSkillResult, error)
	})
	if !ok {
		return nil, identity.ErrResourceDenied
	}
	got, err := native.GetSkill(ctx, uri)
	if err != nil {
		return nil, err
	}
	for _, compiled := range d.compiled {
		if compiled.Metadata().Uri == uri {
			expected, _ := json.Marshal(compiled.Metadata())
			actual, _ := json.Marshal(got.Skill)
			if string(expected) != string(actual) {
				return nil, identity.ErrResourceStale
			}
			return compiled, nil
		}
	}
	return nil, identity.ErrResourceDenied
}
