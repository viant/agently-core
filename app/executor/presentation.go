package executor

import (
	"context"
	"encoding/json"

	"github.com/viant/agently-core/service/policy"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/layoutgate"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	"github.com/viant/forge/backend/types"
)

// WithPresentationPrincipalResolver binds a trusted startup identity source only
// to UI facts. It installs no resource/tool/data authorizer, capability mapping,
// backend preflight, or datasource cache policy. The source owns verification,
// account binding and lease/cache semantics; browser metadata supplies none.
func (b *Builder) WithPresentationPrincipalResolver(source gating.PrincipalResolver) *Builder {
	b.presentationPrincipalResolver = source
	return b
}

type presentationPrincipalProvider struct{ gating.PrincipalResolver }

func (p presentationPrincipalProvider) Resolve(ctx context.Context) (authz.Facts, error) {
	principal, err := p.ResolvePrincipal(ctx)
	return principal.Facts, err
}

type presentationSnapshot struct{ snapshot *permittedview.Snapshot }

func (p presentationSnapshot) Resolve(_ context.Context, request *permittedview.Request) (*permittedview.Snapshot, error) {
	if request != nil && request.SchemaVersion == 2 {
		copy := *p.snapshot
		copy.SchemaVersion = 2
		return &copy, nil
	}
	return p.snapshot, nil
}

func principalPresentationResolver(source gating.PrincipalResolver) permittedview.Resolver {
	return &permittedview.AuthzResolver{Service: &authz.Service{Provider: presentationPrincipalProvider{source}}}
}

// Each menu projection resolves its principal once. Authored resource/capability
// declarations consume only that snapshot; they cannot request entity facts.
// Multiple projections in one resource read may still resolve the principal
// again; the trusted identity source owns any request/profile-cache reuse.
func principalMenuProjection(source gating.PrincipalResolver, admission *policy.Runtime) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	resolver := principalPresentationResolver(source)
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		snapshot, err := resolver.Resolve(ctx, &permittedview.Request{IncludePrincipal: true})
		if err != nil {
			return nil, err
		}
		return layoutgate.MenuProjection(permittedview.NewRuntime(presentationSnapshot{snapshot}), admission, snapshot)(ctx, raw)
	}
}

func principalPresentationProjection(source gating.PrincipalResolver) forge.WindowUIProjection {
	resolver := principalPresentationResolver(source)
	return func(ctx context.Context, input *permittedview.BoundView) (*types.Window, error) {
		// The managing producer's presentation snapshot is authoritative. Do not
		// replace it or fetch a second host identity representation.
		if input.Window.AuthorizationSnapshot != nil {
			return input.Window, nil
		}
		// An explicit configured identity failure must surface. The optional legacy
		// projection adapter intentionally has different fallback semantics.
		snapshot, err := resolver.Resolve(ctx, &permittedview.Request{IncludePrincipal: true})
		if err != nil {
			return nil, err
		}
		// Eager facts belong to the explicit presentation binding: unguarded
		// windows may consume account/principal data in JS or data bindings.
		// Resolve once, then let the shared compiler consume that same snapshot.
		projected := *input.Window
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &projected.AuthorizationSnapshot); err != nil {
			return nil, err
		}
		runtime := permittedview.NewRuntime(presentationSnapshot{snapshot})
		result, err := runtime.Project(ctx, &projected)
		if err != nil {
			return nil, err
		}
		return result.Window, nil
	}
}
