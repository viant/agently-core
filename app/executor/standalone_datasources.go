package executor

import (
	"context"
	"encoding/json"
	"strings"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/forge/backend/types"
)

// Standalone definitions stay on their owning resource connection. Existing
// inline, MCP and window callbacks remain the fallback for other resource kinds.
func (b *Builder) configureStandaloneDatasourceProviders(out *Runtime) {
	if out.PrimitiveProviders == nil || out.standaloneDSConfigured {
		return
	}
	out.standaloneDSConfigured = true
	previousExecute := out.DatasourceProviderExecutor
	previousResolve := out.DatasourceDefinitionResolver
	previousRevalidate := out.DatasourceResourceRevalidator
	previousAuthorize := out.DatasourceDefinitionAuthorizer
	gateway := out.PrimitiveProviders
	out.DatasourceProviderExecutor = func(ctx context.Context, descriptor *dsproto.DataSource, inputs map[string]interface{}) (json.RawMessage, error) {
		pin, ok := requestctx.ResolvedResourceFromContext(ctx)
		if ok && strings.HasPrefix(pin.URI, "datasource://") {
			proof, ok := requestctx.ResourceExecutionProofFromContext(ctx)
			if !ok {
				return nil, identity.ErrResourceDenied
			}
			return gateway.FetchDatasource(ctx, *pin, proof, descriptor, inputs)
		}
		if previousExecute == nil {
			return nil, identity.ErrResourceDenied
		}
		return previousExecute(ctx, descriptor, inputs)
	}
	out.DatasourceDefinitionResolver = func(ctx context.Context, pin identity.ResolvedResource, target *types.WindowTarget, id string) (*dsproto.DataSource, error) {
		if strings.HasPrefix(pin.URI, "datasource://") {
			descriptor, _, err := gateway.ResolveDatasource(ctx, pin)
			if err != nil {
				return nil, err
			}
			if descriptor.ID != id {
				return nil, identity.ErrResourceDenied
			}
			return descriptor, nil
		}
		if previousResolve == nil {
			return nil, identity.ErrResourceDenied
		}
		return previousResolve(ctx, pin, target, id)
	}
	out.DatasourceResourceRevalidator = func(ctx context.Context, pin identity.ResolvedResource) (*identity.ResolvedResource, error) {
		if strings.HasPrefix(pin.URI, "datasource://") {
			result, err := gateway.GetForProvider(ctx, pin.ProviderIdentity, identity.ResourceRef{URI: pin.URI, Revision: pin.Selector()}, &pin)
			if err != nil {
				return nil, err
			}
			return result.ResolvedResource, nil
		}
		if previousRevalidate == nil {
			return nil, identity.ErrResourceDenied
		}
		return previousRevalidate(ctx, pin)
	}
	out.DatasourceDefinitionAuthorizer = func(ctx context.Context, descriptor *dsproto.DataSource, inputs map[string]interface{}) error {
		pin, ok := requestctx.ResolvedResourceFromContext(ctx)
		if ok && strings.HasPrefix(pin.URI, "datasource://") {
			actual, _, err := gateway.ResolveDatasource(ctx, *pin)
			if err != nil {
				return err
			}
			a, err := json.Marshal(actual)
			if err != nil {
				return err
			}
			e, err := json.Marshal(descriptor)
			if err != nil {
				return err
			}
			aFP, err := types.WindowDescriptorFingerprint(a)
			if err != nil {
				return err
			}
			eFP, err := types.WindowDescriptorFingerprint(e)
			if err != nil || aFP != eFP {
				return identity.ErrResourceDenied
			}
			if b.primitiveDatasourceAdmission != nil {
				return b.primitiveDatasourceAdmission(ctx, *pin, descriptor, inputs)
			}
			return nil
		}
		if previousAuthorize == nil {
			return identity.ErrResourceDenied
		}
		return previousAuthorize(ctx, descriptor, inputs)
	}
}
