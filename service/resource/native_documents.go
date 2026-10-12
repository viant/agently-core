package resource

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	identity "github.com/viant/agently-core/protocol/resource"
	producer "github.com/viant/agently-core/service/primitiveproducer"
	"github.com/viant/mcp-protocol/schema"
	"path/filepath"
)

// ConfigureNativeResources is pre-serving composition. It extends the same
// provider's standard MCP handler and leaves its authority/callbacks untouched.
func (p *LocalProvider) ConfigureNativeResources(resources []producer.NativeResource) error {
	if p == nil || len(p.nativeResources) > 0 {
		return fmt.Errorf("native resources already configured")
	}
	if _, err := p.neutral(); err != nil {
		return err
	}
	// Validate using the same producer constructor, retaining original bindings.
	prior := p.nativeResources
	p.nativeResources = append([]producer.NativeResource(nil), resources...)
	if _, err := p.neutral(); err != nil {
		p.nativeResources = prior
		return err
	}
	return nil
}
func NativeDocumentURI(file string) string {
	return fmt.Sprintf("agently-runtime://workspace/%x", sha256.Sum256([]byte(filepath.ToSlash(file))))
}
func (s *NativeAssetSnapshot) NativeResourceDocuments() []producer.NativeResource {
	var out []producer.NativeResource
	for _, definition := range s.definitions {
		if !definition.RawDocument {
			continue
		}
		def := definition
		mime := "application/octet-stream"
		out = append(out, producer.NativeResource{Metadata: schema.Resource{Uri: def.URI, Name: filepath.Base(def.File), MimeType: &mime}, Read: func(ctx context.Context) ([]byte, error) {
			uri, err := identity.ParseResourceURI(def.URI)
			if err != nil {
				return nil, err
			}
			value, err := s.value(ctx, uri)
			if err != nil {
				return nil, err
			}
			return append([]byte(nil), value.raw...), nil
		}})
	}
	return out
}

// ReadNativeResource uses standard resources/read on the configured owning
// internal provider. URIs are opaque inventory keys, never filesystem locators.
func (g *Gateway) ReadNativeResource(ctx context.Context, connection, owner, uri string) ([]byte, error) {
	actor, err := g.actor(ctx)
	if err != nil {
		return nil, err
	}
	options, err := g.Manager.Options(ctx, connection)
	if err != nil {
		return nil, err
	}
	if !g.Manager.IsLocalClient(connection) || options == nil || options.PrimitiveProviderIdentity != owner {
		return nil, identity.ErrResourceDenied
	}
	client, err := g.client(ctx, connection)
	if err != nil {
		return nil, err
	}
	result, err := client.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: uri})
	if err != nil {
		return nil, err
	}
	if result == nil || len(result.Contents) != 1 || result.Contents[0].Uri != uri {
		return nil, identity.ErrResourceDenied
	}
	content := result.Contents[0]
	raw := []byte(content.Text)
	if content.Blob != "" {
		if content.Text != "" {
			return nil, identity.ErrResourceDenied
		}
		raw, err = base64.StdEncoding.DecodeString(content.Blob)
		if err != nil {
			return nil, err
		}
	}
	if err = g.final(ctx, actor); err != nil {
		return nil, err
	}
	return append([]byte(nil), raw...), nil
}
