package executor

import (
	"context"
	"encoding/json"
	"fmt"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	resourcesvc "github.com/viant/agently-core/service/resource"
	"github.com/viant/forge/backend/types"
	mcp "github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

func (b *Builder) configurePrimitiveProviders(out *Runtime) error {
	if out.LocalPrimitiveProvider != nil && !out.localPrimitiveRegistered {
		if out.MCPManager == nil || out.LocalPrimitiveProvider.ProviderIdentity() == "" {
			return fmt.Errorf("internal primitives require a manager and declared provider identity")
		}
		provider := out.LocalPrimitiveProvider
		if err := out.MCPManager.RegisterLocal(context.Background(), "internal", &mcpcfg.MCPClient{
			ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: provider.ProviderIdentity(),
		}, func(context.Context) (mcpclient.Interface, error) {
			return resourcesvc.NewLocalMCPClient(provider)
		}); err != nil {
			return err
		}
		out.localPrimitiveRegistered = true
	}
	if out.PrimitiveProviders == nil {
		actor, verify := b.primitiveActor, b.primitiveVerifier
		if actor == nil && verify == nil && out.ComponentAuthority != nil {
			actor, verify = resourcesvc.AuthorityActors(out.ComponentAuthority)
		}
		if (actor == nil) != (verify == nil) {
			return fmt.Errorf("primitive discovery requires both verified actor and verifier")
		}
		if actor != nil {
			out.PrimitiveProviders = resourcesvc.NewGateway(out.MCPManager, actor, verify, b.primitiveGatewayIdentity)
		}
	}
	if (b.primitiveWindowAdmission == nil) != (b.primitiveTargetProof == nil) {
		return fmt.Errorf("delegated windows require host admission and target proof")
	}
	if out.PrimitiveProviders != nil {
		out.DatasourceReadSequence = func(ctx context.Context) (context.Context, func() error, error) {
			return out.PrimitiveProviders.BeginReadSequence(ctx, out.DatasourceReadFactsScope)
		}
	}
	if out.Reporting != nil && out.PrimitiveProviders != nil && b.primitiveTargetProof != nil {
		out.Reporting.SetResourceDependencies(func(ctx context.Context, pin identity.ResolvedResource, refs map[string]primitive.DataSourceReference, descriptors map[string]json.RawMessage, original map[string]identity.ResolvedResource) (map[string]identity.ResolvedResource, error) {
			parent, e := out.PrimitiveProviders.ConnectionForProvider(ctx, pin.ProviderIdentity)
			if e != nil {
				return nil, e
			}
			return out.PrimitiveProviders.ResolveDependencyPins(ctx, parent, pin, refs, descriptors, original)
		}, b.primitiveTargetProof)
	}
	if b.primitiveWindowAdmission == nil || out.PrimitiveWindows != nil {
		b.configureStandaloneDatasourceProviders(out)
		return nil
	}
	if out.PrimitiveProviders == nil || out.UIBridge == nil {
		return fmt.Errorf("delegated windows require verified primitive authority and a host UI bridge")
	}
	remote := &resourcesvc.WindowCatalog{Gateway: out.PrimitiveProviders, Admission: b.primitiveWindowAdmission, TargetProof: b.primitiveTargetProof}
	out.UIBridge.ConfigureWindowCatalog(func(local forge.WindowDefinitionCatalog) forge.WindowDefinitionCatalog {
		out.PrimitiveWindows = &resourcesvc.CompositeWindowCatalog{Local: local, LocalProviderIdentity: b.primitiveLocalIdentity, Remote: remote}
		return out.PrimitiveWindows
	})
	localResolve, localRevalidate, localAuthorize := out.DatasourceDefinitionResolver, out.DatasourceResourceRevalidator, out.DatasourceDefinitionAuthorizer
	out.DatasourceProviderExecutor = func(ctx context.Context, descriptor *dsproto.DataSource, inputs map[string]interface{}) (json.RawMessage, error) {
		pin, ok := requestctx.ResolvedResourceFromContext(ctx)
		if !ok {
			return nil, identity.ErrResourceDenied
		}
		target, ok := requestctx.WindowTargetFromContext(ctx)
		if !ok {
			return nil, identity.ErrResourceDenied
		}
		return remote.FetchProviderDatasource(ctx, *pin, target, descriptor, inputs)
	}
	out.DatasourceDefinitionResolver = func(ctx context.Context, pin identity.ResolvedResource, target *types.WindowTarget, id string) (*dsproto.DataSource, error) {
		var contextErr error
		ctx, contextErr = resourcesvc.ReadContext(ctx)
		if contextErr != nil {
			return nil, contextErr
		}
		if pin.ProviderIdentity == b.primitiveLocalIdentity {
			if localResolve == nil {
				return nil, identity.ErrResourceDenied
			}
			return localResolve(ctx, pin, target, id)
		}
		return remote.ResolveDatasource(ctx, pin, target, id)
	}
	out.DatasourceResourceRevalidator = func(ctx context.Context, pin identity.ResolvedResource) (*identity.ResolvedResource, error) {
		var contextErr error
		ctx, contextErr = resourcesvc.ReadContext(ctx)
		if contextErr != nil {
			return nil, contextErr
		}
		if pin.ProviderIdentity == b.primitiveLocalIdentity {
			if localRevalidate == nil {
				return nil, identity.ErrResourceDenied
			}
			return localRevalidate(ctx, pin)
		}
		result, e := out.PrimitiveProviders.GetForProvider(ctx, pin.ProviderIdentity, identity.ResourceRef{URI: pin.URI, Revision: pin.Selector()}, &pin)
		if e != nil {
			return nil, e
		}
		return result.ResolvedResource, nil
	}
	out.DatasourceDefinitionAuthorizer = func(ctx context.Context, descriptor *dsproto.DataSource, inputs map[string]interface{}) error {
		var contextErr error
		ctx, contextErr = resourcesvc.ReadContext(ctx)
		if contextErr != nil {
			return contextErr
		}
		pin, ok := requestctx.ResolvedResourceFromContext(ctx)
		if !ok {
			return identity.ErrResourceDenied
		}
		if pin.ProviderIdentity == b.primitiveLocalIdentity {
			if localAuthorize == nil {
				return identity.ErrResourceDenied
			}
			return localAuthorize(ctx, descriptor, inputs)
		}
		target, _ := requestctx.WindowTargetFromContext(ctx)
		if _, e := remote.ResolveDatasource(ctx, *pin, target, descriptor.ID); e != nil {
			return e
		}
		if b.primitiveDatasourceAdmission != nil {
			if err := b.primitiveDatasourceAdmission(ctx, *pin, descriptor, inputs); err != nil {
				return err
			}
		}
		return registerDatasourceReadAdmission(ctx, *pin, target, descriptor, inputs, b.primitiveDatasourceAdmission)
	}
	b.configureStandaloneDatasourceProviders(out)
	return nil
}
