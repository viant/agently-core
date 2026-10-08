package registry

import (
	"context"
	"fmt"

	"github.com/viant/agently-core/runtime/requestctx"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
)

// WindowRequestContext carries only a revalidated server instance pin. A wire
// resource may constrain the request but cannot authorize a different instance.
func (r *Registry) WindowRequestContext(ctx context.Context, namespace, clientID string, window *WindowSnapshot, requested *identity.ResolvedResource, targets ...*types.WindowTarget) (context.Context, error) {
	if !r.canonicalResources() {
		if requested != nil {
			return nil, fmt.Errorf("canonical window resource resolution is not configured")
		}
		return ctx, nil
	}
	if window == nil || window.Resource == nil {
		return nil, identity.ErrResourceDenied
	}
	pin, err := r.bridge.WindowResource(ctx, namespace, clientID, window.WindowID, window.WindowKey)
	if err != nil || pin == nil {
		return nil, identity.ErrResourceDenied
	}
	if window.Resource.URI != pin.URI || window.Resource.ResourceCandidate != pin.ResourceCandidate || window.Resource.AuthorityBinding != pin.AuthorityBinding {
		return nil, identity.ErrResourceDenied
	}
	if requested != nil && (requested.URI != pin.URI || requested.ResourceCandidate != pin.ResourceCandidate || requested.AuthorityBinding != pin.AuthorityBinding) {
		return nil, identity.ErrResourceDenied
	}
	for _, target := range targets {
		if target != nil && !types.SameWindowTarget(target, window.ResourceTarget) {
			return nil, identity.ErrResourceDenied
		}
	}
	return requestctx.WithWindowTarget(requestctx.WithResolvedResource(ctx, *pin), window.ResourceTarget), nil
}
