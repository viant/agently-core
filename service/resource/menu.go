package resource

import (
	"context"
	"encoding/json"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	producer "github.com/viant/agently-core/service/resource/provider"
)

type MenuActionVisibility = producer.MenuActionVisibility
type MenuVisibilityProjection = producer.MenuVisibilityProjection

var ValidateMenu = producer.ValidateMenu

// GetMenu verifies source provenance and the separate visibility projection.
// The legacy Gateway.Get rejects projections; typed Get verifies and decodes
// their separate visible bytes without treating them as the source definition.
func (g *Gateway) GetMenu(ctx context.Context, connection Connection, ref identity.ResourceRef, pin *identity.ResolvedResource) (*primitive.GetResult, error) {
	uri, err := identity.ParseResourceURI(ref.URI)
	if err != nil || uri.Kind != "menu" {
		return nil, identity.ErrResource
	}
	result, err := Get[json.RawMessage](ctx, g, connection, ref, pin)
	if err != nil {
		return nil, err
	}
	return &result.GetResult, nil
}

func declaresMenuFormat(c capabilities, namespace string) bool {
	for _, ns := range c.namespaces {
		if ns.Namespace != namespace {
			continue
		}
		for _, kind := range ns.Kinds {
			if kind.Kind != "menu" {
				continue
			}
			for _, version := range kind.FormatVersions {
				if version == 1 {
					return true
				}
			}
		}
	}
	return false
}
