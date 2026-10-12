package executor

import (
	"context"
	"crypto/rand"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/policy"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/permittedview"

	auth "github.com/viant/agently-core/service/auth"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/forge/backend/types"
	"strings"
)

// configureDefaultWindowDelivery preserves the original host OPEN policy.
// Target signatures prove exact definition/variant integrity, not identity or
// business permission. Managing MCP still authorizes every data/tool call.
func (b *Builder) configureDefaultWindowDelivery(out *Runtime) error {
	if !out.publicPrimitiveDefault || out.UIBridge == nil || b.primitiveWindowAdmission != nil {
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
	b.primitiveWindowAdmission = func(ctx context.Context, pin identity.ResolvedResource, w *types.Window) error {
		if ctx == nil || ctx.Err() != nil {
			return identity.ErrResourceDenied
		}
		key := pin.URI
		if parsed, err := identity.ParseResourceURI(pin.URI); err == nil && parsed.Namespace == "workspace" {
			key = parsed.Name
		}
		if pin.ProviderIdentity == resources.InternalProviderIdentity && w != nil && strings.TrimSpace(w.WindowKey) != "" {
			key = w.WindowKey
		}
		if out.AuthorizationPolicy != nil && out.AuthorizationPolicy.IsEnabled(policy.OperationWindowView) {
			return out.AuthorizationPolicy.Authorize(ctx, policy.OperationWindowView, "", policy.Candidate{ID: key, Kind: "window"}, nil)
		}
		return nil
	}
	if out.UIBridge != nil {
		out.UIBridge.ConfigureResolvedWindowAuthorizer(func(ctx context.Context, pin identity.ResolvedResource, _ string, _ map[string]any) error {
			// The bridge first checks this current namespace/client/window's held pin.
			// This callback retains original host window admission only.
			return b.primitiveWindowAdmission(ctx, pin, nil)
		})
	}
	return nil
}

func (b *Builder) configureDefaultWindowOpenAdmission(out *Runtime) {
	if !out.publicPrimitiveDefault || out.UIBridge == nil || out.UIBridge.HasWindowOpenAdmission() {
		return
	}
	admission := &permittedview.OpenAdmission{Runtime: permittedview.NewRuntime(out.PermittedResolver), Bootstrap: b.windowOpenBootstrap, MatchSelection: b.windowOpenSelection}
	out.UIBridge.ConfigureWindowOpenAdmission(func(ctx context.Context, pin identity.ResolvedResource, w *types.Window, parameters map[string]any) (*forge.WindowOpenDecision, error) {
		decision, err := admission.ApplyDecision(ctx, pin, w, parameters)
		if err != nil {
			return nil, err
		}
		return &forge.WindowOpenDecision{Window: decision.Window, ValidUntil: decision.ExpiresAt}, nil
	})
}

// configureDefaultUIOwner retains presentation ownership without turning the
// public definition scope into verified identity or business permission.
func (b *Builder) configureDefaultUIOwner(out *Runtime) {
	if !out.publicPrimitiveDefault || out.UIBridge == nil {
		return
	}
	if out.UIBridge.HasNamespaceResolver() {
		return
	}
	publicRead, _ := resources.PublicReadActors()
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
		held, err := publicRead(ctx)
		if err != nil {
			return forge.NamespaceIdentity{}, err
		}
		return forge.NamespaceIdentity{Namespace: namespace, ValidUntil: held.ValidUntil}, nil
	})
}
