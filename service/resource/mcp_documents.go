package resource

import (
	"context"
	"fmt"

	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	producer "github.com/viant/agently-core/service/resource/provider"
	"github.com/viant/mcp-protocol/schema"
)

// MCPResourceDocuments exposes the same configured MCP definitions over the
// existing standard resources/read handler. Get owns actor/source validation;
// conversion adds no credential resolution or client initialization.
func MCPResourceDocuments(p *LocalProvider) ([]producer.NativeResource, error) {
	if p == nil {
		return nil, nil
	}
	current, err := p.neutral()
	if err != nil {
		return nil, err
	}
	var documents []producer.NativeResource
	for _, binding := range current.ConfigurationSnapshot().Bindings {
		uri, err := identity.ParseResourceURI(binding.URI)
		if err != nil {
			return nil, err
		}
		if uri.Kind != "mcp" {
			continue
		}
		source := binding
		mime := "application/yaml"
		documents = append(documents, producer.NativeResource{Metadata: schema.Resource{Uri: source.URI, Name: uri.Name, MimeType: &mime}, Read: func(ctx context.Context) ([]byte, error) {
			result, err := p.Get(ctx, "mcp", primitive.GetRequest{URI: source.URI})
			if err != nil {
				return nil, err
			}
			if result == nil || result.Resource == nil {
				return nil, fmt.Errorf("MCP definition unavailable")
			}
			return MCPDefinitionYAML(result.Resource.FormatVersion, result.Resource.Definition)
		}})
	}
	return documents, nil
}
