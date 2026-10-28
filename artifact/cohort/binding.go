package cohort

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"time"

	"github.com/viant/agently-core/app/executor"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	"github.com/viant/forge/backend/artifactcohort"
	artifactcache "github.com/viant/forge/backend/artifactcohort/cache"
)

// Registration is an operator-owned bridge from one configured
// MCP catalog to its dedicated, separately authenticated host-service client.
// Browser requests cannot select this client, source scope, account, or cohort.
type Registration struct {
	ConnectionName, ProviderIdentity, Tenant, Account, Cohort string
	Namespace                                                 string
	Client                                                    artifactcohort.ToolClient
	Notifications                                             *artifactcohort.NotificationHandler
}

type verifiedCohortFacts struct {
	source          gating.PrincipalResolver
	tenant, account string
}

func (p verifiedCohortFacts) Resolve(ctx context.Context) (authz.Facts, error) {
	if p.source == nil {
		return authz.Facts{}, authz.ErrDenied
	}
	principal, err := p.source.ResolvePrincipal(ctx)
	if err != nil || principal.Facts.Subject == "" || principal.Facts.Issuer == "" || principal.Facts.Tenant != p.tenant || principal.AccountID != p.account || !principal.Facts.ValidUntil.After(time.Now()) {
		return authz.Facts{}, authz.ErrDenied
	}
	return principal.Facts, nil
}
func verifiedCohortSession(ctx context.Context) (string, error) {
	owner, err := svcauth.LocalStateOwner(ctx)
	if err != nil || owner == nil || owner.Binding == "" || !owner.ValidUntil.After(time.Now()) {
		return "", authz.ErrDenied
	}
	return owner.Binding, nil
}

// PrepareBinding resolves the trusted MCP definition and state
// directory once at startup. It never borrows a browser's BFF token or mints a
// policy grant; Client must already represent the configured host identity.
func PrepareBinding(ctx context.Context, rt *executor.Runtime, registration Registration) (Binding, error) {
	if ctx == nil || ctx.Err() != nil || rt == nil || rt.MCPManager == nil || rt.StateStore == nil || rt.VerifiedPrincipalResolver == nil || registration.Client == nil || registration.ConnectionName == "" || registration.ProviderIdentity == "" || registration.Tenant == "" || registration.Account == "" {
		return Binding{}, artifactcohort.ErrInvalid
	}
	configured, err := rt.MCPManager.Options(ctx, registration.ConnectionName)
	if err != nil || configured == nil || configured.PrimitiveProviderIdentity != registration.ProviderIdentity || configured.ClientOptions == nil || configured.ClientOptions.Transport.Type != "streamable" || configured.ClientOptions.Transport.URL == "" {
		return Binding{}, artifactcohort.ErrDenied
	}
	namespaces, err := artifactCohortNamespaceClaims(configured.PrimitiveNamespaces)
	if err != nil {
		return Binding{}, err
	}
	namespace, err := selectSyncNamespace(registration.Namespace, namespaces)
	if err != nil {
		return Binding{}, err
	}
	namespaces = []string{namespace}
	scope := sha256.Sum256([]byte(registration.ProviderIdentity + "\x00" + namespace + "\x00" + registration.Tenant + "\x00" + registration.Account))
	stateDir, err := rt.StateStore.StatePath(ctx, filepath.Join("mcp", "artifacts", hex.EncodeToString(scope[:])))
	if err != nil {
		return Binding{}, err
	}
	backing, err := artifactcache.NewFileBacking(filepath.Join(stateDir, "blobs"))
	if err != nil {
		return Binding{}, err
	}
	store, err := artifactcache.New(artifactcache.Config{SizeMB: 32, MaxEntries: 4096, MaxEntryBytes: artifactcohort.MaxPayloadBytes + 4096, Backing: backing})
	if err != nil {
		return Binding{}, err
	}
	var selectedCohort func(context.Context) (string, error)
	if registration.Cohort != "" {
		chosen := registration.Cohort
		selectedCohort = func(context.Context) (string, error) { return chosen, nil }
	}
	replica, err := artifactcohort.NewReplica(artifactcohort.Config{Enabled: true, Source: registration.ProviderIdentity, Tenant: registration.Tenant, Account: registration.Account, Cohort: selectedCohort, Caller: artifactcohort.NamespaceCaller{Client: registration.Client, Namespace: namespace}, Facts: verifiedCohortFacts{source: rt.VerifiedPrincipalResolver, tenant: registration.Tenant, account: registration.Account}, SessionKey: verifiedCohortSession, Store: store, ManifestStore: artifactcache.FileManifestStore{Directory: filepath.Join(stateDir, "manifests")}})
	if err != nil {
		_ = store.Close()
		return Binding{}, err
	}
	metadata, err := artifactcohort.NewMetadataSource(replica, namespaces)
	if err != nil {
		_ = store.Close()
		return Binding{}, err
	}
	binding := Binding{ConnectionName: registration.ConnectionName, ProviderIdentity: registration.ProviderIdentity, Namespaces: namespaces, Source: replica, Metadata: metadata, Close: func() {
		if registration.Notifications != nil {
			registration.Notifications.Close()
		}
		_ = store.Close()
		switch closer := registration.Client.(type) {
		case interface{ Close() }:
			closer.Close()
		case interface{ Close() error }:
			_ = closer.Close()
		}
	}}
	if configured.Name != "" && configured.Name != registration.ConnectionName {
		binding.ConnectionAliases = []string{configured.Name}
	}
	if registration.Notifications != nil {
		binding.Start = func(context.Context) error { return registration.Notifications.AttachReplica(replica) }
	}
	return binding, nil
}

func selectSyncNamespace(selected string, claims []string) (string, error) {
	if selected == "" && len(claims) == 1 {
		return claims[0], nil
	}
	for _, name := range claims {
		if selected != "" && name == selected {
			return selected, nil
		}
	}
	return "", artifactcohort.ErrInvalid
}

func artifactCohortNamespaceClaims(configured []string) ([]string, error) {
	claims := make([]string, 0, len(configured))
	seen := map[string]bool{}
	for _, raw := range configured {
		namespace := strings.TrimPrefix(raw, "ns://")
		if namespace == "" || strings.ContainsAny(namespace, "/?#:") || strings.TrimSpace(namespace) != namespace {
			return nil, artifactcohort.ErrInvalid
		}
		if !seen[namespace] {
			claims = append(claims, namespace)
			seen[namespace] = true
		}
	}
	return claims, nil
}
