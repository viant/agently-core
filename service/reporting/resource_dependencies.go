package reporting

import (
	"context"
	"encoding/json"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/reporting/registry"
	"github.com/viant/forge/backend/types"
)

// ResourceDependencyResolver is explicitly supplied by the trusted host. An
// existing map requires revalidation of those exact original authority pins.
type ResourceDependencyResolver func(context.Context, identity.ResolvedResource, map[string]primitive.DataSourceReference, map[string]json.RawMessage, map[string]identity.ResolvedResource) (map[string]identity.ResolvedResource, error)

const reportDependencyDomain = "report.dependencies.v1"

func (s *Service) SetResourceDependencies(resolve ResourceDependencyResolver, proof types.WindowTargetProof) {
	if s != nil {
		s.resourceDependencyResolver = resolve
		s.resourceDependencyProof = proof
	}
}
func cloneDependencyPins(input map[string]identity.ResolvedResource) map[string]identity.ResolvedResource {
	if input == nil {
		return nil
	}
	out := make(map[string]identity.ResolvedResource, len(input))
	for id, pin := range input {
		pin.ValidUntil = pin.ValidUntil.UTC()
		out[id] = pin
	}
	return out
}

func (s *Service) bindDependencies(ctx context.Context, parent *identity.ResolvedResource, definition registry.ReportEnvelope, original map[string]identity.ResolvedResource, token string) (map[string]identity.ResolvedResource, string, error) {
	if len(definition.DataSourceResources) == 0 {
		if len(original) > 0 || token != "" {
			return nil, "", identity.ErrResourceDenied
		}
		return nil, "", nil
	}
	if s.resourceDependencyResolver == nil || s.resourceDependencyProof == nil || parent == nil {
		return nil, "", identity.ErrResourceDenied
	}
	if original != nil {
		if token == "" || s.resourceDependencyProof.Verify(ctx, *parent, types.WindowTarget{DependencyPins: original}, reportDependencyDomain, token) != nil {
			return nil, "", identity.ErrResourceDenied
		}
	} else if token != "" {
		return nil, "", identity.ErrResourceDenied
	}
	pins, e := s.resourceDependencyResolver(ctx, *parent, definition.DataSourceResources, definition.DataSources, cloneDependencyPins(original))
	if e != nil {
		return nil, "", e
	}
	if len(pins) != len(definition.DataSourceResources) {
		return nil, "", identity.ErrResourceDenied
	}
	for id, ref := range definition.DataSourceResources {
		pin, ok := pins[id]
		if !ok || ref.Validate(parent.Kind == identity.StampedCandidate) != nil || pin.URI != ref.Resource.URI || pin.Selector() != ref.Resource.Revision || pin.ContentFingerprint != ref.ContentFingerprint || pin.ProviderIdentity != ref.ProviderIdentity || !pin.ResourceCandidate.Valid() || pin.AuthorityBinding == "" || !pin.ValidUntil.After(s.now()) {
			return nil, "", identity.ErrResourceDenied
		}
		if original != nil {
			old, ok := original[id]
			if !ok || old.URI != pin.URI || old.ProviderIdentity != pin.ProviderIdentity || old.ResourceCandidate != pin.ResourceCandidate || old.AuthorityBinding != pin.AuthorityBinding || !old.ValidUntil.After(s.now()) {
				return nil, "", identity.ErrResourceDenied
			}
			if pin.ValidUntil.After(old.ValidUntil) {
				pin.ValidUntil = old.ValidUntil
			}
			pins[id] = pin
		}
		if pin.ValidUntil.Before(parent.ValidUntil) {
			parent.ValidUntil = pin.ValidUntil
		}
	}
	if !parent.ValidUntil.After(s.now()) {
		return nil, "", identity.ErrResourceDenied
	}
	signed, e := s.resourceDependencyProof.Sign(ctx, *parent, types.WindowTarget{DependencyPins: pins}, reportDependencyDomain)
	return cloneDependencyPins(pins), signed, e
}
func (s *Service) verifyDependencies(ctx context.Context, parent identity.ResolvedResource, definition registry.ReportEnvelope, pins map[string]identity.ResolvedResource, token string) error {
	copy := parent
	_, _, e := s.bindDependencies(ctx, &copy, definition, pins, token)
	if e != nil {
		return e
	}
	if !copy.ValidUntil.Equal(parent.ValidUntil) {
		return identity.ErrResourceDenied
	}
	return nil
}
