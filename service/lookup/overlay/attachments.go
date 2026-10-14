package overlay

import (
	"context"
	"encoding/json"

	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	resources "github.com/viant/agently-core/service/resource"
)

type presentationAttachments struct {
	Overlays  []*loproto.Overlay         `json:"lookupOverlays"`
	Libraries []loproto.LibraryReference `json:"lookupLibraries"`
}

// ReadAttachments reads only a declared owning primitive and its explicit
// template libraries. Every read uses the same gateway and current actor. The
// returned service belongs to this operation, never to a process-wide store.
func ReadAttachments(ctx context.Context, gateway *resources.Gateway, owner identity.ResolvedResource) (*Service, error) {
	if gateway == nil {
		return nil, identity.ErrResourceDenied
	}
	parsed, err := identity.ParseResourceURI(owner.URI)
	if err != nil || (parsed.Kind != "window" && parsed.Kind != "template" && parsed.Kind != "intent" && parsed.Kind != "lookup") {
		return nil, identity.ErrResourceDenied
	}
	visited := map[string]bool{}
	var overlays []*loproto.Overlay
	var read func(identity.ResourceRef, string, *identity.ResolvedResource, string, int) error
	read = func(ref identity.ResourceRef, provider string, pin *identity.ResolvedResource, fingerprint string, depth int) error {
		key := provider + "\x00" + ref.URI + "\x00" + ref.Revision
		if depth > 8 || len(visited) >= 128 || visited[key] {
			return identity.ErrResourceDenied
		}
		visited[key] = true
		defer delete(visited, key)
		uri, err := identity.ParseResourceURI(ref.URI)
		if err != nil {
			return identity.ErrResourceDenied
		}
		connection, err := gateway.ConnectionForResource(ctx, uri, provider)
		if err != nil {
			return err
		}
		got, err := resources.Get[json.RawMessage](ctx, gateway, connection, ref, pin)
		if err != nil {
			return err
		}
		if got.ResolvedResource.ProviderIdentity != provider || (fingerprint != "" && got.ResolvedResource.ContentFingerprint != fingerprint) {
			return identity.ErrResourceStale
		}
		raw := got.Definition
		// Canonical window envelopes retain the authored window as a nested object.
		if uri.Kind == "window" {
			var envelope struct {
				Window json.RawMessage `json:"window"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				return err
			}
			if len(envelope.Window) > 0 {
				raw = envelope.Window
			}
		}
		var attached presentationAttachments
		if err := json.Unmarshal(raw, &attached); err != nil {
			return err
		}
		if uri.Kind == "lookup" {
			definition, err := resources.DecodeLookupDefinition(1, raw)
			if err != nil {
				return err
			}
			definition.Resource = got.ResolvedResource
			for i := range definition.Bindings {
				projected, e := definition.Bindings[i].Lookup.WithResourceScope(got.ResolvedResource)
				if e != nil {
					return e
				}
				definition.Bindings[i].Lookup = projected
			}
			overlays = append(overlays, definition)
		} else {
			overlays = append(overlays, attached.Overlays...)
		}
		immutable := got.ResolvedResource.Kind == identity.StampedCandidate
		if err := loproto.ValidateLibraryReferences(attached.Libraries, immutable); err != nil {
			return err
		}
		for _, library := range attached.Libraries {
			// Existing signed dependency context, when present, must agree with the
			// exact authored reference. It never grants access to the library.
			var child *identity.ResolvedResource
			if candidate, ok := requestctx.ResourceDependencyFromContext(ctx, library.Resource.URI); ok {
				if candidate.ProviderIdentity != library.ProviderIdentity || candidate.ContentFingerprint != library.ContentFingerprint {
					return identity.ErrResourceStale
				}
				child = candidate
			}
			if err := read(library.Resource, library.ProviderIdentity, child, library.ContentFingerprint, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	revision := owner.Revision
	if owner.Kind == identity.WorkingCandidate {
		revision = identity.WorkingCandidate
	}
	if err := read(identity.ResourceRef{URI: owner.URI, Revision: revision}, owner.ProviderIdentity, &owner, owner.ContentFingerprint, 0); err != nil {
		return nil, err
	}
	store := NewMemoryStore()
	store.Replace(overlays)
	return New(store), nil
}
