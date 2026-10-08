package resource

import (
	"context"
	"encoding/json"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
	"strings"
)

func (g *Gateway) dependencyConnection(ctx context.Context, parent Connection, owner string) (Connection, error) {
	if owner == parent.ProviderIdentity {
		return parent, nil
	}
	discovered, e := g.Discover(ctx)
	if e != nil {
		return Connection{}, e
	}
	var found Connection
	for _, ns := range discovered {
		if ns.Connection.ProviderIdentity == owner {
			if found.Name != "" && found != ns.Connection {
				return Connection{}, ErrCollision
			}
			found = ns.Connection
		}
	}
	if found.Name == "" {
		return found, identity.ErrResourceDenied
	}
	return found, nil
}

// ResolveDependencyPins authorizes each explicit child candidate and compares
// the server materialized attachment with the exact child descriptor. Existing
// pins are mandatory on execution/re-read; omitted pins only start a new open.
func (g *Gateway) ResolveDependencyPins(ctx context.Context, parent Connection, parentPin identity.ResolvedResource, refs map[string]primitive.DataSourceReference, descriptors map[string]json.RawMessage, existing map[string]identity.ResolvedResource) (map[string]identity.ResolvedResource, error) {
	if len(refs) > 128 || existing != nil && len(existing) != len(refs) {
		return nil, identity.ErrResourceDenied
	}
	result := make(map[string]identity.ResolvedResource, len(refs))
	for id, ref := range refs {
		if ref.Validate(parentPin.Kind == identity.StampedCandidate) != nil || descriptors[id] == nil {
			return nil, identity.ErrResourceDenied
		}
		connection, e := g.dependencyConnection(ctx, parent, ref.ProviderIdentity)
		if e != nil {
			return nil, e
		}
		var original *identity.ResolvedResource
		if existing != nil {
			pin, ok := existing[id]
			if !ok || pin.URI != ref.Resource.URI || pin.Selector() != ref.Resource.Revision || pin.ContentFingerprint != ref.ContentFingerprint || pin.ProviderIdentity != ref.ProviderIdentity {
				return nil, identity.ErrResourceDenied
			}
			original = &pin
		}
		child, e := g.Get(ctx, connection, ref.Resource, original)
		if e != nil {
			return nil, e
		}
		if child.ResolvedResource.ContentFingerprint != ref.ContentFingerprint || child.ResolvedResource.ProviderIdentity != ref.ProviderIdentity {
			return nil, identity.ErrResourceDenied
		}
		var envelope struct {
			SchemaVersion int64           `json:"schemaVersion"`
			DataSource    json.RawMessage `json:"dataSource"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(child.Resource.Definition)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&envelope) != nil || envelope.SchemaVersion != 1 || !json.Valid(envelope.DataSource) {
			return nil, identity.ErrResourceDenied
		}
		canonical, e := types.CanonicalWindowDescriptor(envelope.DataSource)
		if e != nil {
			return nil, e
		}
		var descriptor map[string]json.RawMessage
		if json.Unmarshal(canonical, &descriptor) != nil || descriptor == nil {
			return nil, identity.ErrResourceDenied
		}
		var sourceID string
		if json.Unmarshal(descriptor["id"], &sourceID) != nil || sourceID == "" {
			return nil, identity.ErrResourceDenied
		}
		encodedID, _ := json.Marshal(id)
		descriptor["id"] = encodedID
		derived, e := json.Marshal(descriptor)
		if e != nil {
			return nil, e
		}
		expected, e := types.WindowDescriptorFingerprint(descriptors[id])
		if e != nil {
			return nil, e
		}
		actual, e := types.WindowDescriptorFingerprint(derived)
		if e != nil || actual != expected {
			return nil, identity.ErrResourceDenied
		}
		result[id] = *child.ResolvedResource
	}
	return result, nil
}
