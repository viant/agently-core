package resource

import (
	"context"
	"encoding/json"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
)

// Result delivers a typed definition alongside the unchanged original provider
// identity, pin and execution provenance. T has no effect on routing or admission.
type Result[T any] struct {
	primitive.GetResult
	Definition T `json:"-"`
}

// Located is provider metadata with an optional typed inline definition. Lists
// that carry metadata only leave Definition at its zero value.
type Located[T any] struct {
	LocatedResource
	Definition T `json:"-"`
}

// List federates the same Gateway and per-actor capability cache for every
// primitive kind. Optional provider pagination remains an ordinary wire detail.
func List[T any](ctx context.Context, g *Gateway, kind, namespace string) ([]Located[T], error) {
	return list[T](ctx, g, kind, namespace, "")
}

// Get reads one exact provider locator through the shared validation pipeline.
// Menus use their separately verified visibility projection as the definition.
type DefinitionDecoder[T any] func(json.RawMessage) (T, error)

func Get[T any](ctx context.Context, g *Gateway, connection Connection, ref identity.ResourceRef, pin *identity.ResolvedResource, decoder ...DefinitionDecoder[T]) (*Result[T], error) {
	uri, err := identity.ParseResourceURI(ref.URI)
	if err != nil {
		poisonReadSequence(ctx, identity.ErrResource)
		return nil, identity.ErrResource
	}
	if uri.Kind == "menu" {
		return get[T](ctx, g, connection, ref, pin, true, true, decoder...)
	}
	return get[T](ctx, g, connection, ref, pin, false, false, decoder...)
}

func decodeDefinition[T any](raw json.RawMessage) (T, error) {
	var result T
	if len(raw) == 0 {
		return result, nil
	}
	// Raw consumers keep exact bytes without an extra JSON parse or copy.
	if target, ok := any(&result).(*json.RawMessage); ok {
		*target = raw
		return result, nil
	}
	err := json.Unmarshal(raw, &result)
	return result, err
}

func untypedLocations[T any](rows []Located[T]) []LocatedResource {
	if rows == nil {
		return nil
	}
	out := make([]LocatedResource, len(rows))
	for i := range rows {
		out[i] = rows[i].LocatedResource
	}
	return out
}
