package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/viant/agently-core/app/executor"
	primitiveprovider "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	"github.com/viant/authz/oauth"
)

// Providers binds trusted authority interfaces. The standard HTTP
// adapter is configured by the operator; embedding hosts may override it.
type Providers struct {
	// NativeWindowIdentity verifies identity only for configured static YAML reads.
	// It carries no role, feature or entity authority. Protected operations use Identity.
	NativeWindowIdentity func(context.Context) (gating.Principal, error)
	Entitlements         map[string]gating.EntitlementProvider
	AccountProjection    permittedview.AccountProjection
	DecisionScope        executor.AuthorizationDecisionScope
	MetadataScope        primitiveprovider.MetadataReadScope
	Identity             oauth.IdentityAuthority
	AllowsTenant         func(string) bool
	EntityPermissions    gating.EntityPermissionProvider
	PermissionWithLease  func(context.Context, authz.Facts, authz.Entity, string) (bool, time.Time, error)
}

// ProviderFactory consumes only operator-owned configuration.
type ProviderFactory func(context.Context, json.RawMessage, json.RawMessage) (Providers, error)

// HTTPProviders makes the normal executable usable
// without compiling an application-specific host or importing private IdP code.
func HTTPProviders(ctx context.Context, identityRaw, evaluationRaw json.RawMessage) (Providers, error) {
	var identityConfig oauth.HTTPIdentityConfig
	if err := decodeHTTPAuthorityConfig(identityRaw, &identityConfig); err != nil {
		return Providers{}, fmt.Errorf("identity adapter: %w", err)
	}
	var evaluationConfig *oauth.HTTPEvaluationConfig
	if len(bytes.TrimSpace(evaluationRaw)) != 0 && !bytes.Equal(bytes.TrimSpace(evaluationRaw), []byte("null")) {
		evaluationConfig = &oauth.HTTPEvaluationConfig{}
		if err := decodeHTTPAuthorityConfig(evaluationRaw, evaluationConfig); err != nil {
			return Providers{}, fmt.Errorf("evaluation adapter: %w", err)
		}
	}
	providers, err := oauth.NewHTTPProviders(ctx, identityConfig, evaluationConfig)
	if err != nil {
		return Providers{}, err
	}
	return Providers{
		NativeWindowIdentity: providers.NativeWindowIdentity,
		Entitlements:         providers.Entitlements,
		AccountProjection:    providers.AccountProjection,
		DecisionScope:        providers.DecisionScope,
		MetadataScope:        providers.MetadataScope,
		Identity:             providers.Identity,
		AllowsTenant:         providers.AllowsTenant,
		EntityPermissions:    providers.EntityPermissions,
		PermissionWithLease:  providers.PermissionWithLease,
	}, nil
}

func decodeHTTPAuthorityConfig(raw json.RawMessage, target any) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("explicit adapter configuration is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("adapter configuration must contain one JSON object")
	}
	return nil
}
