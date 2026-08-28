package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/service/auth/providerregistry"
	"github.com/viant/datly"
	authcfg "github.com/viant/mcp/client/auth/config"
)

// DelegatedMCPAuth bundles the workspace OAuth provider registry and the
// Agently credential resolver installed into viant/mcp for MCP definitions
// with auth.mode=oauth. Legacy MCP auth configurations are unaffected: the
// manager installs these only for delegated configs.
type DelegatedMCPAuth struct {
	registry *providerregistry.Registry
	resolver *DelegatedCredentialResolver
}

// NewDelegatedMCPAuth builds the delegated MCP auth stack over the encrypted
// user_oauth_token store. It returns nil only when the required persistence
// layer (datly DAO) is unavailable — delegated configs then fail loudly at
// client creation because no credential resolver is installed. When storage
// exists but no encryption key can be derived (neither auth.tokenEncryptionKey
// nor a workspace OAuth client configURL), the resolver is installed in a
// fail-loud state: every delegated resolution returns an actionable
// configuration error instead of silently disabling delegated auth.
func NewDelegatedMCPAuth(cfg *Config, dao *datly.Service) *DelegatedMCPAuth {
	if dao == nil {
		return nil
	}
	registry := providerregistry.New()
	namespace := ""
	if cfg != nil {
		namespace = cfg.WorkspaceNamespace
	}
	delegatedSalt := cfg.DelegatedTokenEncryptionSalt()
	if delegatedSalt == "" {
		resolver := NewDelegatedCredentialResolver(cfg, nil, registry, namespace)
		resolver.initErr = fmt.Errorf("delegated mcp oauth requires an encryption key for token storage: set auth.tokenEncryptionKey (env-expandable) or configure the workspace auth.oauth.client.configURL")
		return &DelegatedMCPAuth{registry: registry, resolver: resolver}
	}
	workspaceSalt := ""
	if cfg != nil && cfg.OAuth != nil && cfg.OAuth.Client != nil {
		workspaceSalt = strings.TrimSpace(cfg.OAuth.Client.ConfigURL)
	}
	store := NewTokenStoreDAO(dao, firstNonEmpty(workspaceSalt, delegatedSalt), WithDelegatedSalt(delegatedSalt))
	resolver := NewDelegatedCredentialResolver(cfg, store, registry, namespace)
	return &DelegatedMCPAuth{registry: registry, resolver: resolver}
}

// Registry exposes the generic viant/mcp provider registry.
func (d *DelegatedMCPAuth) Registry() authcfg.ProviderRegistry {
	if d == nil {
		return nil
	}
	return d.registry
}

// ProviderRegistry exposes the Agently registry surface (kill switches,
// fingerprint, refresh leads).
func (d *DelegatedMCPAuth) ProviderRegistry() *providerregistry.Registry {
	if d == nil {
		return nil
	}
	return d.registry
}

// Resolver exposes the generic viant/mcp credential resolver.
func (d *DelegatedMCPAuth) Resolver() authcfg.CredentialResolver {
	if d == nil {
		return nil
	}
	return d.resolver
}

// TokenRefresher exposes the watcher-facing delegated refresh surface.
func (d *DelegatedMCPAuth) TokenRefresher() DelegatedTokenRefresher {
	if d == nil {
		return nil
	}
	return d.resolver
}

// ClearResolverCooldown drops the refresh retry cooldown for one canonical
// user and delegated storage key on this instance. Used by cache-invalidation
// wiring so a re-link on another resolver instance takes effect immediately.
func (d *DelegatedMCPAuth) ClearResolverCooldown(canonicalUserID, storageKey string) {
	if d == nil || d.resolver == nil {
		return
	}
	canonicalUserID = strings.TrimSpace(canonicalUserID)
	storageKey = strings.TrimSpace(storageKey)
	if canonicalUserID == "" || storageKey == "" {
		return
	}
	d.resolver.clearCooldown(canonicalUserID, storageKey)
}

// SetUserLookup installs the canonical by-ID active-status lookup: with it in
// place, disabled or deleted canonical users cannot resolve or refresh
// delegated credentials. Any UserService implementing UserByIDLookup applies.
func (d *DelegatedMCPAuth) SetUserLookup(users interface{}) {
	if d == nil || d.resolver == nil {
		return
	}
	if lookup, ok := users.(UserByIDLookup); ok && lookup != nil {
		d.resolver.users = lookup
	}
}

// CleanupDelegatedCredentials is the user-lifecycle hook for deactivation and
// deletion: it deletes every delegated (mcp:v1) token row belonging to the
// canonical user after attempting best-effort provider-side revocation.
// Providers without revocation support are audited and cleaned locally.
func (d *DelegatedMCPAuth) CleanupDelegatedCredentials(ctx context.Context, canonicalUserID string) error {
	if d == nil || d.resolver == nil || d.resolver.store == nil {
		return nil
	}
	canonicalUserID = strings.TrimSpace(canonicalUserID)
	if canonicalUserID == "" {
		return fmt.Errorf("canonical user id is required for delegated credential cleanup")
	}
	lister, ok := d.resolver.store.(DelegatedTokenLister)
	if !ok {
		return fmt.Errorf("delegated token store does not support listing; cleanup unavailable")
	}
	rows, err := lister.ListDelegated(ctx, canonicalUserID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, stored := range rows {
		if stored == nil {
			continue
		}
		storageKey := strings.TrimSpace(stored.Provider)
		if !IsDelegatedProviderKey(storageKey) {
			continue
		}
		// Best-effort provider revocation before local deletion; failures and
		// unsupported providers are audited inside the revoke helper.
		if providerRef := strings.TrimSpace(stored.ProviderRef); providerRef != "" {
			if provider, pErr := d.registry.ResolveProvider(ctx, providerRef); pErr == nil && provider != nil {
				if client, clientName, cErr := provider.Client(strings.TrimSpace(stored.ClientRef)); cErr == nil {
					resolved := &resolvedProvider{refKey: providerRef, provider: provider, client: client, clientName: clientName, storageKey: storageKey}
					if service := newMCPLinkRevoker(d); service != nil {
						service.revokeBestEffort(ctx, resolved, stored)
					}
				}
			}
		}
		if delErr := d.resolver.store.Delete(ctx, canonicalUserID, storageKey); delErr != nil && firstErr == nil {
			firstErr = delErr
		}
		d.resolver.clearCooldown(canonicalUserID, storageKey)
		NotifyMCPAuthChange(MCPAuthChangeEvent{
			Kind:            "disconnected",
			CanonicalUserID: canonicalUserID,
			ProviderRef:     strings.TrimSpace(stored.ProviderRef),
			StorageKey:      storageKey,
		})
	}
	return firstErr
}

// newMCPLinkRevoker builds a minimal link service carrying only what
// revocation needs; nil when no state key material exists (revocation then
// falls back to local deletion only).
func newMCPLinkRevoker(d *DelegatedMCPAuth) *mcpLinkService {
	if d == nil {
		return nil
	}
	return &mcpLinkService{
		delegated:        d,
		now:              time.Now,
		loadClientConfig: loadOAuthClientConfig,
		httpClient:       defaultOAuthMetadataHTTPClient,
	}
}
