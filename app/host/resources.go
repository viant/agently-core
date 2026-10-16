package host

import (
	"context"

	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
)

// BindSources composes a trusted startup inventory with its owning provider and
// optional captured source. Kind-specific loaders and namespace restrictions
// remain in directory; request-time revision authority remains in policy.
// Calling this function publishes no authority or request result cache.
func BindSources[T any](ctx context.Context, root, owner string, sources []T, directory func(T) (resources.ExtensionDirectory, error), policy resources.LocalRevisionPolicyFactory, snapshot identity.ResourceSource) ([]resources.LocalResourceBinding, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	if owner == "" || directory == nil {
		return nil, identity.ErrResourceDenied
	}
	directories := make([]resources.ExtensionDirectory, 0, len(sources))
	for _, source := range sources {
		value, err := directory(source)
		if err != nil {
			return nil, err
		}
		directories = append(directories, value)
	}
	bindings, err := resources.ExtensionBindings(ctx, root, directories, policy)
	if err != nil {
		return nil, err
	}
	for i := range bindings {
		original := bindings[i].Resolver
		bindings[i].Resolver = func(ctx context.Context, actor identity.VerifiedActor, operation string) (*identity.ResourceResolver, error) {
			resolved, err := original(ctx, actor, operation)
			if err != nil || resolved == nil {
				return nil, identity.ErrResourceDenied
			}
			copied := *resolved
			copied.ProviderIdentity = owner
			if snapshot != nil {
				copied.Source = snapshot
			}
			return &copied, nil
		}
	}
	return bindings, nil
}
