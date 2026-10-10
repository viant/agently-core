package resource

import (
	"context"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	producer "github.com/viant/agently-core/service/primitiveproducer"
)

type MenuActionVisibility = producer.MenuActionVisibility

var ValidateMenu = producer.ValidateMenu

// GetMenu verifies source provenance and the separate visibility projection.
// Generic Get intentionally rejects menu projections: they are not source bytes.
func (g *Gateway) GetMenu(ctx context.Context, connection Connection, ref identity.ResourceRef, pin *identity.ResolvedResource) (*primitive.GetResult, error) {
	uri, err := identity.ParseResourceURI(ref.URI)
	if err != nil || uri.Kind != "menu" {
		return nil, identity.ErrResource
	}
	return g.get(ctx, connection, ref, pin, true, true)
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
