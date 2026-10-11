package resource

import (
	"context"
	"encoding/json"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
)

// ResolveDefinition selects an exact URI or a unique authored alias from the
// same private manager snapshot, then freshly admits the selected definition.
func ResolveDefinition[T any](ctx context.Context, g *Gateway, kind, key string, alias func(T) string, decoder ...DefinitionDecoder[T]) (*Result[T], error) {
	query := CatalogQuery{Limit: 100}
	var selected *Located[json.RawMessage]
	parsed, parseErr := identity.ParseResourceURI(key)
	exactURI := parseErr == nil && parsed.Kind == kind
	for {
		page, err := ListPage[json.RawMessage](ctx, g, kind, query, true)
		if err != nil {
			return nil, err
		}
		for _, candidate := range page.Items {
			matches := candidate.Resource.URI == key
			if !exactURI && alias != nil {
				var projection T
				var decodeErr error
				if len(decoder) > 0 && decoder[0] != nil {
					projection, decodeErr = decoder[0](candidate.Definition)
				} else {
					projection, decodeErr = decodeDefinition[T](candidate.Definition)
				}
				if decodeErr != nil {
					return nil, decodeErr
				}
				matches = alias(projection) == key
			}
			if matches {
				if selected != nil {
					return nil, ErrCollision
				}
				copy := candidate
				selected = &copy
			}
		}
		if page.Complete {
			break
		}
		if page.NextCursor == "" {
			return nil, primitive.ErrCatalogCursor
		}
		query.Cursor = page.NextCursor
	}
	if selected == nil {
		return nil, identity.ErrResourceDenied
	}
	got, err := Get[T](ctx, g, selected.Connection, identity.ResourceRef{URI: selected.Resource.URI, Revision: selected.Resource.Revision}, nil, decoder...)
	if err != nil {
		return nil, err
	}
	if selected.Resource.ContentFingerprint != "" && selected.Resource.ContentFingerprint != got.Resource.ContentFingerprint {
		return nil, identity.ErrResourceStale
	}
	return got, nil
}
