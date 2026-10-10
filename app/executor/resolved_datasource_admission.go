package executor

import (
	"context"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/policy"
	resourcesvc "github.com/viant/agently-core/service/resource"
)

// AuthorizeResolvedDatasource intersects a provider-verified descriptor with
// the host's existing datasource.fetch mapping. The descriptor ID comes from
// the selected window bundle; the resulting resource must match its held pin.
func (p *PreparedAuthorization) AuthorizeResolvedDatasource(ctx context.Context, pin identity.ResolvedResource, id string, inputs map[string]interface{}) error {
	var contextErr error
	ctx, contextErr = resourcesvc.ReadContext(ctx)
	if contextErr != nil {
		return contextErr
	}
	if p == nil || p.backend == nil || ctx == nil || ctx.Err() != nil || id == "" || pin.ProviderIdentity == "" || !pin.ResourceCandidate.Valid() || pin.AuthorityBinding == "" || !pin.ValidUntil.After(time.Now()) {
		return policy.ErrDenied
	}
	ctx = requestctx.WithResolvedResource(ctx, pin)
	resource, action, selected, permission, err := p.backend(ctx, "datasource.fetch", id, inputs)
	if err != nil {
		return err
	}
	uri, err := identity.ParseResourceURI(pin.URI)
	if err != nil || (uri.Kind != "window" && uri.Kind != "datasource") || resource.Kind != uri.Kind || resource.ID != pin.URI || resource.Version != pin.Selector() {
		return policy.ErrDenied
	}
	if err := p.checker.AuthorizeMany(ctx, resource, action, selected, permission); err != nil {
		return err
	}
	if ctx.Err() != nil || !pin.ValidUntil.After(time.Now()) {
		return policy.ErrDenied
	}
	return nil
}
