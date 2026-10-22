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
	if err := b.configureMCPDefinitionSource(out); err != nil {
		return fmt.Errorf("MCP definition source: %w", err)
	}
	if err := b.configurePublicPrimitiveProviders(out); err != nil {
		return fmt.Errorf("public primitive providers: %w", err)
	}
	if err := b.configureNativeHostDocuments(out); err != nil {
		return fmt.Errorf("native host documents: %w", err)
	}
	if out.LocalPrimitiveProvider != nil && !out.localPrimitiveRegistered {
		if out.MCPManager == nil || out.LocalPrimitiveProvider.ProviderIdentity() == "" {
			return fmt.Errorf("internal primitives require a manager and declared provider identity")
		}
		provider := out.LocalPrimitiveProvider
		var resourceScopes map[string]string
		if out.publicPrimitiveDefault {
			resourceScopes = map[string]string{"workspace": mcpcfg.ResourceCachePublic, "system": mcpcfg.ResourceCachePublic, "workspace/navigation": mcpcfg.ResourceCacheUser, "workspace/mcp": mcpcfg.ResourceCacheUser, "system/navigation": mcpcfg.ResourceCacheUser}
			// Window availability can be projected through original application
			// visibility rules. Such metadata belongs to the current user even
			// though its authored bytes are immutable and grant no data access.
			if b.presentationPrincipalResolver != nil || out.PermittedResolver != nil || out.AuthorizationTool != "" || out.AuthorizationPolicy != nil {
				resourceScopes["workspace/window"] = mcpcfg.ResourceCacheUser
			}
			if b.presentationPrincipalResolver != nil {
				resourceScopes["system/window"] = mcpcfg.ResourceCacheUser
			}
		}
		if err := out.MCPManager.RegisterLocal(context.Background(), "internal", &mcpcfg.MCPClient{
			ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: provider.ProviderIdentity(),
			ResourceCacheScopes: resourceScopes,
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
	if err := b.configureDefaultWindowDelivery(out); err != nil {
		return err
	}
	if out.NativeHostDocuments != nil {
		out.NativeHostDocuments.Gateway = out.PrimitiveProviders
	}
	// A raw-document-only fallback is not an admission source for business
	// datasource/report/window adapters. Preserve their configured boundaries.
	if out.nativeDocumentOnlyGateway {
		if b.primitiveTargetProof != nil {
			return fmt.Errorf("delegated windows require verified primitive authority and a host UI bridge")
		}
		return nil
	}
	if out.PrimitiveProviders != nil {
		out.DatasourceReadSequence = func(ctx context.Context) (context.Context, func() error, error) {
			return out.PrimitiveProviders.BeginReadSequence(ctx, out.DatasourceReadFactsScope)
		}
	}
	reportProof := b.primitiveReportDependencyProof
	if reportProof == nil {
		reportProof = b.primitiveTargetProof
	}
	if out.Reporting != nil && out.PrimitiveProviders != nil && reportProof != nil {
		out.Reporting.SetResourceDependencies(func(ctx context.Context, pin identity.ResolvedResource, refs map[string]primitive.DataSourceReference, descriptors map[string]json.RawMessage, original map[string]identity.ResolvedResource) (map[string]identity.ResolvedResource, error) {
			parent, e := out.PrimitiveProviders.ConnectionForProvider(ctx, pin.ProviderIdentity)
			if e != nil {
				return nil, e
			}
			return out.PrimitiveProviders.ResolveDependencyPins(ctx, parent, pin, refs, descriptors, original)
		}, reportProof)
	}
	if err := b.configurePrimitiveReports(out); err != nil {
		return fmt.Errorf("primitive reports: %w", err)
	}
	if b.primitiveTargetProof == nil || out.PrimitiveWindows != nil {
		b.configureStandaloneDatasourceProviders(out)
		return nil
	}
	if out.PrimitiveProviders == nil || out.UIBridge == nil {
		return fmt.Errorf("delegated windows require verified primitive authority and a host UI bridge")
	}
	defaultNamespace := ""
	if out.publicPrimitiveDefault {
		defaultNamespace = "workspace"
	}
	remote := &resourcesvc.WindowCatalog{DefaultNamespace: defaultNamespace, Gateway: out.PrimitiveProviders, TargetProof: b.primitiveTargetProof}
	if out.publicPrimitiveDefault {
		remote.DefaultProviderIdentity = resourcesvc.InternalProviderIdentity
		remote.Aliases = b.nativeWindowAliases
		if err := remote.RegisterNativeHTTPWindows(resourcesvc.Connection{Name: "internal", ProviderIdentity: resourcesvc.InternalProviderIdentity}, b.nativeHTTPWindowURIs); err != nil {
			return err
		}
	}
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
		return registerDatasourceReadIntegrity(ctx, *pin, target, descriptor, inputs)
	}
	b.configureStandaloneDatasourceProviders(out)
	return nil
}
