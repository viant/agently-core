package executor

import (
	"context"
	"crypto/rand"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"log"

	forge "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/permittedview"

	auth "github.com/viant/agently-core/service/auth"
	"github.com/viant/forge/backend/types"
)

// configureDefaultWindowDelivery signs exact target/definition selections.
// Target signatures prove exact definition/variant integrity, not identity or
// business permission. Managing MCP still authorizes every data/tool call.
func (b *Builder) configureDefaultWindowDelivery(out *Runtime) error {
	if !out.publicPrimitiveDefault || out.UIBridge == nil || b.primitiveTargetProof != nil || out.UIBridge.UsesWindowResourceResolution() {
		return nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	proof, err := types.NewWindowTargetHMAC(secret)
	if err != nil {
		return err
	}
	b.primitiveTargetProof = proof
	return nil
}

func (b *Builder) configureWindowUIProjection(out *Runtime) {
	if out.UIBridge == nil || out.UIBridge.HasWindowUIProjection() {
		return
	}
	if b.presentationPrincipalResolver != nil {
		out.UIBridge.ConfigureWindowUIProjection(principalPresentationProjection(b.presentationPrincipalResolver))
		return
	}
	resolver := out.PermittedResolver

	if resolver == nil {
		return
	}
	runtime := permittedview.NewRuntime(resolver)
	out.UIBridge.ConfigureWindowUIProjection(func(ctx context.Context, input *permittedview.BoundView) (*types.Window, error) {
		result, err := runtime.ProjectPrincipal(ctx, input.Window)
		if err != nil {
			return nil, err
		}
		for _, diagnostic := range result.Diagnostics {
			log.Printf("window presentation: %s", diagnostic.Code)
		}
		return result.Window, nil
	})
}

// configureDefaultUIOwner retains presentation ownership without turning the
// public definition scope into verified identity or business permission.
func (b *Builder) configureDefaultUIOwner(out *Runtime) {
	if out.UIBridge != nil && out.AuthConfig != nil && out.AuthConfig.Enabled {
		out.UIBridge.ConfigureLocalStateOwner(auth.LocalStateOwner)
	}
	if !out.publicPrimitiveDefault || out.UIBridge == nil {
		return
	}
	if out.UIBridge.HasNamespaceResolver() {
		return
	}
	out.UIBridge.ConfigureNamespaceResolver(func(ctx context.Context) (forge.NamespaceIdentity, error) {
		if ctx == nil || ctx.Err() != nil {
			return forge.NamespaceIdentity{}, identity.ErrResourceDenied
		}
		namespace := "default"
		if out.AuthConfig != nil && out.AuthConfig.Enabled {
			namespace = auth.EffectiveUserID(ctx)
			if namespace == "" {
				return forge.NamespaceIdentity{}, identity.ErrResourceDenied
			}
		}
		return forge.NamespaceIdentity{Namespace: namespace, SessionBound: true}, nil
	})
}

// Default workspace profile IDs retain their authored namespace. Explicit URIs
// still resolve external profiles through the same gateway.
func defaultIntentReader(out *Runtime) *resources.IntentReader {
	reader := resources.NewPlaybookReader(out.PrimitiveProviders)
	if out.publicPrimitiveDefault {
		reader.Scope = resources.DefinitionScope{Namespace: "workspace", ProviderIdentity: resources.InternalProviderIdentity}
	}
	return reader
}
