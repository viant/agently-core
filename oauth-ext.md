# Agently Multi-Provider OAuth Extension

## Status

Implementation-ready plan for delegated, per-user OAuth access to MCP servers
whose identity provider differs from the Agently workspace identity provider.

The first release reuses the existing user_oauth_token schema. It requires a
small distributed single-use OAuth-state store for callback replay protection.
That store may use an existing distributed CAS service; otherwise add the
oauth_link_state table described below. A later release may normalize linked
identities or support multiple resource-specific token sets per provider.

## Problem

Agently currently authenticates a user through the workspace OAuth provider
and can forward that access token or ID token to MCP servers. This is correct
only when the workspace and MCP share a compatible issuer, audience, resource,
token type, and scopes.

It is not correct for the verified Dev6 deployment:

~~~text
MCP resource: https://mcp6.dev.viant.ai/mcp
OAuth issuer: https://idp-dev6.adelphic-dev.com/
~~~

Agently needs to:

1. Associate the canonical Agently user with additional OAuth providers.
2. Acquire a provider token only when that provider is first required.
3. Persist it under the canonical Agently user.
4. Reuse and refresh it without prompting on later calls.
5. Select it before MCP initialization and discovery.
6. Learn safe authentication metadata from a 401 challenge when explicit MCP
   configuration is incomplete.

## Verified Dev6 behavior

The Dev6 MCP endpoint returns:

~~~http
WWW-Authenticate: Bearer resource_metadata=https://mcp6.dev.viant.ai/.well-known/oauth-protected-resource
~~~

Its protected-resource metadata identifies:

~~~yaml
authorizationServers:
  - https://idp-dev6.adelphic-dev.com/
resource: https://mcp6.dev.viant.ai/mcp
bearerMethodsSupported:
  - header
~~~

The authorization server supports authorization_code and refresh_token,
requires S256 PKCE, and accepts client_secret_basic or client_secret_post for
confidential clients.

Cursor's existing test client uses:

~~~text
http://localhost:8787/callback
~~~

Production Agently must use its own confidential client and registered HTTPS
callback.

With plan:create, plan:edit, and plan:read, tool discovery exposes:

~~~text
create_media_plan
edit_media_plan
get_media_plan
~~~

## Goals

- Support multiple OAuth providers for one canonical Agently user.
- Preserve the workspace OAuth identity as the effective request user for the
  entire request, including MCP discovery and tool execution.
- Allow MCP definitions to reference a registered OAuth provider and client.
- Resolve the correct token before contacting the MCP server.
- Reuse a compatible workspace token only after strict validation.
- Persist delegated tokens in the existing encrypted user_oauth_token table.
- Reuse existing refresh leases, CAS updates, and retry cooldowns.
- Keep OAuth tokens and client secrets out of browsers and logs.
- Preserve user isolation across conversations, reconnects, and schedulers.
- Avoid repeated authentication after the first successful provider link.

## Primary identity invariant

The workspace OAuth provider remains the sole source of the effective Agently
user.

Delegated MCP OAuth adds a credential to that user; it does not switch the
request principal to the MCP provider subject.

For every request:

~~~text
EffectiveUserID       = workspace OAuth subject/email
CanonicalUserID       = Agently users.id for that workspace identity
WorkspaceProvider     = workspace OAuth provider
MCP credential        = outbound-only token selected for one MCP server
MCP provider subject  = linked credential metadata, never EffectiveUserID
~~~

Consequences:

- Ownership, reporting, scheduling, conversation visibility, and audit
  attribution continue to use the workspace effective user.
- The MCP provider must never overwrite authctx.User, authctx.Provider,
  authctx.Bearer, authctx.IDToken, or the workspace session.
- The MCP access token is injected only into a child outbound MCP context under
  the MCP transport token key.
- Returning from an MCP call restores no identity state because the parent
  request context was never mutated.
- The canonical user ID links the effective workspace identity to persisted
  delegated credentials.
- Session and bearer authentication must call the same
  ResolveCanonicalWorkspaceUser function after token verification. The
  bearer-only path must not implement a second, weaker subject-to-user mapping.
- Comparisons of issuer, audience, resource, and scope use parsed exact values;
  prefix and substring matching are forbidden.

## Non-goals for version one

- Multiple linked accounts for the same provider.
- Transparently overwriting one provider token with a token for an incompatible
  resource. Version one rejects this case and requires a separate namespaced
  provider reference.
- A database-backed learned MCP metadata registry.
- Dynamic confidential-client registration from Agently.
- Automatic acquisition of every advertised MCP scope.
- Replacement of the workspace authentication provider.

## Architecture

~~~text
Workspace session
    |
    | canonical users.id
    v
MCP auth requirement
    |
    +--> compatible workspace JWT ------------------+
    |                                               |
    +--> encrypted provider token                   |
    |       |                                       |
    |       +--> valid -----------------------------+--> MCP request
    |       +--> expiring --> provider refresh -----+
    |
    +--> missing/revoked --> OAuthLinkRequired
                                |
                                v
                    provider authorization callback
                                |
                                v
                    encrypted token persistence
~~~

Authentication acquisition is lazy, while token resolution is eager:

- Do not prompt for every configured MCP during workspace login.
- On first MCP access, resolve its token before the first MCP request.
- Do not wait for a 401 when provider and resource are explicitly configured.

## OAuth provider registry

Add provider definitions under:

~~~text
<workspace>/oauth/providers/<provider>.yaml
~~~

Example:

~~~yaml
id: adelphic-dev6
issuer: https://idp-dev6.adelphic-dev.com/
discoveryURL: https://idp-dev6.adelphic-dev.com/.well-known/openid-configuration

defaultClient: steward-web

clients:
  steward-web:
    configURL: <ADELPHIC_DEV6_STEWARD_CLIENT_CONFIG>
    redirectURI: https://<steward-dev6-host>/v1/api/auth/mcp/callback
    confidential: true
    usePKCE: true
    refreshLead: 15m
    clockSkew: 30s
~~~

The SCY resource referenced by configURL contains the client ID, client secret,
authorization endpoint, and token endpoint. Secret material must never appear
in MCP YAML, browser payloads, logs, or database metadata.

The provider registry owns:

- Issuer and discovery metadata.
- OAuth client registrations.
- Secret resource references.
- Redirect URIs.
- PKCE and token-endpoint authentication policy.

## MCP authentication configuration

Example:

~~~yaml
name: viant-mcp-dev6

transport:
  type: streamable
  url: https://mcp6.dev.viant.ai/mcp

auth:
  mode: oauth
  providerRef: adelphic-dev6
  clientRef: steward-web
  resource: https://mcp6.dev.viant.ai/mcp
  tokenType: accessToken
  resolution: eager
  workspaceTokenReuse: ifCompatible
  scopes:
    - plan:create
    - plan:edit
    - plan:read
~~~

The MCP definition owns:

- Provider and client references.
- Resource and audience requirement.
- Requested scopes.
- Access-token versus ID-token selection.
- Workspace-token reuse policy.
- Eager versus challenge-only resolution.

## viant/mcp OAuth ownership

OAuth protocol behavior belongs in github.com/viant/mcp. The viant/mcp change
is required for this implementation, not an optional follow-up.

Extend github.com/viant/mcp.ClientAuth:

~~~go
type ClientAuth struct {
    // Existing fields
    OAuth2ConfigURL     []string
    EncryptionKey      string
    UseIdToken         bool
    BackendForFrontend *bool
    PassUserToken      *bool

    // New fields
    Mode                string
    ProviderRef         string
    ClientRef           string
    Resource            string
    Scopes              []string
    TokenType           string
    Resolution          string
    WorkspaceTokenReuse string
    InlineProvider      *OAuthProvider
}
~~~

providerRef and InlineProvider are mutually exclusive. Legacy
OAuth2ConfigURL, BackendForFrontend, UseIdToken, and PassUserToken
configurations continue to decode and use their existing behavior unless the
new mode/provider fields are present.

Add generic viant/mcp interfaces:

~~~go
type ProviderRegistry interface {
    ResolveProvider(ctx context.Context, ref string) (*OAuthProvider, error)
    MatchIssuer(ctx context.Context, issuer string) (*OAuthProvider, error)
}

type CredentialResolver interface {
    Resolve(ctx context.Context, requirement Requirement) (*Credential, error)
    Refresh(ctx context.Context, requirement Requirement) (*Credential, error)
    Invalidate(ctx context.Context, requirement Requirement) error
}
~~~

viant/mcp owns:

- OAuth provider/client and requirement configuration types.
- Inline-provider and providerRef validation.
- Protected-resource and authorization-server metadata discovery.
- Requirement compilation from MCP configuration and trusted metadata.
- Pre-request credential resolution hooks.
- HTTP and JSON-RPC authorization attachment.
- A single refresh/retry decision point after 401.
- Disabling internal browser fallback when an external resolver is installed.
- Generic OAuth errors and link-required signaling.
- Persistent auth-store correctness, including retaining loaded OAuth client
  configurations when a host injects a token store.

viant/mcp does not own:

- Agently EffectiveUserID or CanonicalUserID.
- Workspace sessions, users, ownership, or authorization policy.
- Agently database encryption or user_oauth_token rows.
- Provider-registry file loading and administrator policy.
- Hosted Agently callback routes, CSRF, UI, or audit records.

agently-core implements the generic registry and credential interfaces using
workspace configuration, canonical users, encrypted storage, routing brokers,
and hosted OAuth endpoints. It passes those implementations to viant/mcp.

This boundary prevents viant/mcp from depending on Agently while allowing all
MCP clients to share correct OAuth protocol behavior.

After landing the viant/mcp change:

1. Add focused viant/mcp unit and interoperability tests.
2. Release or pin the required viant/mcp revision.
3. Update agently-core/go.mod.
4. Remove any temporary Agently-only auth decoding shim.

## Canonical identity

Agently currently uses:

- Session.UserID as the canonical users.id for persistence.
- EffectiveUserID as the provider subject/email used by ownership filters.

EffectiveUserID must continue to come from the workspace OAuth session for the
full request lifetime. Do not replace it globally or locally around an MCP
call. Add a separate persistence identity:

~~~go
func WithCanonicalUserID(ctx context.Context, id string) context.Context
func CanonicalUserID(ctx context.Context) string
~~~

Populate the canonical value whenever an authenticated session is restored.

Keep authctx.Provider set to the workspace OAuth provider. If MCP diagnostics
need the delegated provider name, carry it under a distinct MCP-specific
context key or in TokenRequirement; never reuse the workspace provider key.

For bearer-only requests without a session:

1. Verify the workspace token with the same verifier used by session auth.
2. Call ResolveCanonicalWorkspaceUser with the verified provider and subject.
3. Do not create another main user for the MCP provider.
4. Fail closed when canonical ownership cannot be determined reliably.

The session and bearer paths must share:

~~~go
type VerifiedWorkspaceIdentity struct {
    Provider string
    Issuer   string
    Subject  string
    Email    string
}

func ResolveCanonicalWorkspaceUser(
    ctx context.Context,
    identity VerifiedWorkspaceIdentity,
) (string, error)
~~~

The resolver accepts only already-verified identities and applies identical
issuer, audience, subject, disabled-user, and canonical-user checks for both
entry paths. Cache successful subject mappings briefly, but invalidate them
when a user or provider configuration changes.

Canonical identity cache entries have a hard maximum TTL of 30 seconds.
Provider reload and local user-status changes invalidate them immediately.
Deployments with a cross-pod invalidation bus publish user-disabled,
user-deleted, and provider-changed events; deployments without one converge
within the hard TTL and recheck active status before delegated token use.

The external MCP provider subject may be stored as encrypted credential
metadata for conflict detection and diagnostics, but it must not become the
effective user.

Expected files:

- internal/auth/context.go
- internal/auth/context_test.go
- service/auth/runtime_types.go
- service/auth/runtime_auth.go
- service/auth/middleware.go

## Token storage without changing the token-table schema

The existing table supports multiple providers per user:

~~~text
PRIMARY KEY (user_id, provider)
~~~

Version one uses a fixed-length, globally unambiguous storage key:

~~~text
user_id  = canonical Agently users.id
provider = mcp:v1:<hex sha256(workspaceNamespace + NUL + providerRef)>
~~~

The full SHA-256 hex digest is used; it is never truncated. The resulting key
is 71 ASCII characters. Phase 0 validates that the live
user_oauth_token.provider column supports at least that width and fails startup
otherwise. MySQL truncation is never accepted.

workspaceNamespace is an immutable configured identifier, not a filesystem
path or display name. Changing it requires an explicit token-key migration.
Provider references must be unique inside a workspace, and encrypted metadata
retains the human-readable workspace namespace, providerRef, issuer, and
resource for validation and diagnostics.

Version one permits one resource token per storage key. Before writing a token,
compare it with any existing row. When issuer or resource differs, reject the
write with provider_resource_conflict rather than overwriting the credential.
Operators must configure another providerRef for the second resource. A broader
scope set for the same issuer/resource may replace a narrower set only after a
successful, state-bound authorization flow.

Extend the encrypted token payload with:

~~~go
type encToken struct {
    AccessToken  string
    RefreshToken string
    IDToken      string
    ExpiresAt    string
    Issuer       string
    Resource     string
    Scopes       []string
    TokenType    string
    Subject      string
}
~~~

JSON field names must remain compatible with the existing payload and all new
fields must be optional for backward compatibility.

The same metadata fields must be added to every mirrored representation and
conversion path:

- service/auth.OAuthToken.
- internal/auth/token.OAuthToken.
- Conversions between OAuthToken and scyauth.Token.
- tokenstore_adapter Get, Put, and CASPut.
- Refresh-broker return values.

Metadata must survive load, refresh, CAS write, and reload. A refresh that
drops issuer, resource, scopes, token type, or provider subject is a blocking
error and must not overwrite the stored row.

## Exact provider lookup

TokenStoreDAO.Get currently falls back to another provider row when the
requested provider is missing. That is unsafe in a multi-provider system.

The fallback is also load-bearing for existing callers that use aliases such
as oauth or jwt while rows are stored under the configured workspace provider.
Do not remove it without a compatibility audit.

Required rollout:

1. Add a metric and debug-only structured event for every fallback hit.
2. Inventory empty, oauth, jwt, and configured provider-name callers.
3. Normalize trusted workspace aliases to the configured workspace provider.
4. Migrate call sites to pass that canonical provider.
5. Enable exact lookup for delegated MCP storage immediately.
6. Remove generic fallback only after shadow telemetry shows no unexplained
   dependency.

Final semantics:

- A delegated provider storage key requires an exact match.
- A normalized workspace provider requires an exact match.
- Empty-provider fallback may remain temporarily for identified legacy calls.
- jwt or oauth never falls through to an arbitrary provider row.

Use separate APIs during migration:

~~~go
GetExact(ctx, userID, providerStorageKey string) (*OAuthToken, error)
GetLegacy(ctx, userID, providerAlias string) (*OAuthToken, error)
~~~

GetLegacy is instrumented, temporary, and unavailable to delegated MCP code.

Expected files:

- service/auth/token_store.go
- service/auth/tokenstore_datly.go
- service/auth/tokenstore_adapter.go
- service/auth/tokenstore_datly_test.go
- internal/auth/token/provider.go

## Provider registry service

Add:

~~~go
type ProviderRegistry interface {
    Provider(ctx context.Context, ref string) (*OAuthProvider, error)
    Client(ctx context.Context, providerRef, clientRef string) (*OAuthClient, error)
    MatchIssuer(ctx context.Context, issuer string) (*OAuthProvider, error)
}
~~~

Loader requirements:

- Normalize issuer trailing slashes.
- Resolve defaultClient.
- Expand environment templates.
- Load client secrets only during exchange or refresh.
- Validate redirect URIs and HTTPS requirements.
- Compute a non-secret configuration fingerprint.
- Support workspace hot reload.
- Never expose secrets through workspace metadata APIs.
- Treat provider files as trust anchors. Production write access is restricted
  to workspace administrators and reviewed deployment changes.
- Audit provider add, change, removal, and hot reload using only the non-secret
  configuration fingerprint.
- Reject duplicate normalized issuers unless client selection is unambiguous.
- Support global, provider-level, and MCP-level delegated-auth kill switches.

MatchIssuer hard-fails when more than one provider entry has the normalized
issuer. Challenge learning never uses ordering as a tie-break. An explicitly
configured providerRef may select one of several clients for the same issuer,
but its clientRef must still resolve uniquely.

Suggested packages:

~~~text
workspace/repository/oauthprovider
service/auth/providerregistry
github.com/viant/mcp/client/auth/config
~~~

The Agently provider-registry service extends and implements the generic
viant/mcp ProviderRegistry interface.

## MCP token requirement

Compile MCP configuration into:

~~~go
type TokenRequirement struct {
    ServerName  string
    ProviderRef string
    ClientRef   string
    Issuer      string
    Resource    string
    Scopes      []string
    TokenType   TokenType
    ReusePolicy WorkspaceTokenReusePolicy
}
~~~

Compile once per MCP configuration revision.

Validation:

- Provider and client references must exist.
- Resource must be an absolute HTTPS URL in production.
- Resource origin must match the MCP transport origin unless allowlisted.
- Scopes must be normalized and deduplicated.
- accessToken is the default.
- idToken requires explicit configuration.
- auth.mode oauth with ProviderRef rejects PassUserToken=true. Use
  workspaceTokenReuse=ifCompatible to request validated workspace-token reuse;
  the legacy forwarding flag is not meaningful for delegated mode.

## Eager token resolver

Add:

~~~go
type TokenResolver interface {
    Requirement(ctx context.Context, server string) (*TokenRequirement, error)
    Resolve(ctx context.Context, requirement *TokenRequirement) (*Resolution, error)
    Invalidate(ctx context.Context, requirement *TokenRequirement) error
}

type Resolution struct {
    Token       string
    TokenType   TokenType
    ExpiresAt   time.Time
    Source      ResolutionSource
    ProviderRef string
    Resource    string
    Scopes      []string
}
~~~

Resolution order:

1. Enforce global, provider, and MCP kill switches.
2. Preserve and validate the effective workspace user.
3. Require its canonical user ID.
4. Load the MCP token requirement.
5. Inspect the verified workspace token.
6. Reuse it only when fully compatible.
7. Exact-load the delegated provider token using the canonical user ID.
8. Validate issuer, resource, scopes, type, and expiration.
9. Refresh when expired or within refresh lead time.
10. Return OAuthLinkRequired when no usable token remains.

A disabled switch blocks reuse, refresh, initiate, and callback persistence.
It returns a typed provider_disabled error and emits an audit event. It does
not silently fall back to workspace credentials.

Resolution returns an outbound credential. It must not install delegated token
values into the general Agently authentication context.

The delegated path must not call token.Provider.EnsureTokens. That API returns
a context populated through authctx.WithTokens, WithBearer, and WithIDToken.
Keep it for workspace authentication. Delegated authentication uses the
value-returning TokenResolver and injects only the MCP transport token into an
outbound child context.

The canonical token-store adapter must branch:

- Workspace tokens use existing owner resolution after provider-alias
  normalization.
- Delegated tokens use CanonicalUserID directly and never call
  resolveOAuthTokenOwnerID with the delegated provider.
- Missing CanonicalUserID fails closed and never creates or upserts a user.

## Workspace JWT compatibility

Workspace-token reuse requires all of:

- Verified signature.
- Normalized issuer equality.
- Resource or audience compatibility.
- Required scopes are a subset of granted scopes.
- Token has not expired.
- Token type matches the MCP requirement.

Do not treat an opaque token as compatible unless verified introspection data
is already available. Never fall back from accessToken to idToken.

All issuer, audience, and resource comparisons are parsed exact comparisons.
For example, an audience ending in /mcp2 does not satisfy a requirement ending
in /mcp. Arrays of audiences are matched by exact member equality.

## Provider-aware refresh

Reuse the existing token manager, encrypted store, distributed lease, CAS,
retry cooldown, and invalid_grant handling.

The default refresh lead is 15 minutes for every registry and inline provider.
A provider may override it explicitly. Request-time resolution and the
background refresh watcher must call the same ShouldRefresh function; they
must not maintain different thresholds.

To prevent immediate refresh loops for short-lived access tokens, use:

~~~text
effectiveRefreshLead = min(configuredRefreshLead, originalTokenLifetime * 20%)
minimum useful lead  = 30 seconds
~~~

When original lifetime is unavailable, use the configured lead but enforce a
per-token refresh cooldown. Add small deterministic jitter within the refresh
window so multiple pods and users do not refresh simultaneously.

Add a broker router:

~~~go
type RoutingBroker struct {
    Registry BrokerRegistry
}
~~~

RoutingBroker implements the existing token.Broker interface and routes
Refresh by token.Key.Provider. Keep one token.Manager so existing leases,
singleflight, miss caches, retry caches, and instance ownership remain shared.
Do not create a token.Manager per provider.

Refresh behavior:

- Refresh when now reaches expiresAt minus effectiveRefreshLead.
- Serialize per canonical user and provider.
- Reuse distributed refresh leases.
- Preserve the old refresh token when no replacement is returned.
- CAS-update encrypted storage.
- Apply cooldown after transient failure.
- Invalidate only the affected provider after invalid_grant.
- Return OAuthLinkRequired after permanent failure.
- Treat a returned refresh scope as authoritative. Persist the newly granted
  scope set rather than preserving stale scopes. If it no longer covers the
  MCP requirement, retain the valid narrowed token for compatible consumers
  but return OAuthLinkRequired for the current MCP.
- Preserve the previous refresh token when the provider omits a rotated value.
- When no refresh token exists, use the access token while valid and return
  OAuthLinkRequired at the refresh threshold without repeatedly calling the
  token endpoint.
- Temporary refresh failures use exponential backoff and may continue using
  the current access token until its actual expiration.

The background watcher must route each token row to its provider broker rather
than assuming the workspace OAuth client.

Implementation must replace the current workspace-only 30-minute default in
service/auth Config.tokenRefreshLead with 15 minutes and remove independent
threshold decisions from runtime_refresh and token.Manager call sites.
Provider-aware code computes the effective lead before invoking the broker.

### All-provider refresh contract

The refresh watcher processes every supported OAuth provider:

~~~text
workspace provider
delegated registry providers
inline MCP providers
future registered providers
~~~

It covers both active sessions and persisted tokens belonging to users without
an active in-memory session.

The persistent scan uses the largest configured refresh lead as its broad
horizon. Each candidate is then evaluated with that token's provider-specific
ShouldRefresh policy. This prevents the workspace provider's lead time from
controlling or excluding another provider.

For each candidate:

1. Parse and validate the provider storage key and encrypted metadata.
2. Resolve the exact provider and client configuration.
3. Revalidate that the canonical workspace user is active.
4. Apply the provider's effective refresh lead, defaulting to 15 minutes.
5. Acquire the distributed user/provider refresh lease.
6. Call only that provider's token endpoint with its client, resource, and
   granted scopes.
7. Persist rotated tokens and authoritative returned scopes through CAS.
8. Release the lease and record a provider-specific outcome.

Provider failures are isolated. A timeout, throttling response, invalid_grant,
disabled provider, or configuration error for one provider must not stop the
watcher from processing other providers.

Use bounded worker concurrency plus provider-specific rate limits and jitter.
Unknown providers, missing brokers, malformed metadata, and disabled providers
are skipped without modifying stored credentials.

Tokens without refresh tokens are not sent to a token endpoint. They remain
usable until the configured refresh threshold, after which interactive
consumers receive OAuthLinkRequired and headless consumers receive an
actionable auth-required state.

Safety rules:

- Unknown or disabled providers are skipped and reported; they are never sent
  to the workspace broker.
- Missing broker configuration is not invalid_grant and must never delete a
  token.
- The watcher may process delegated rows only after routing is deployed.
- Link endpoints remain disabled until the routing broker safety tests pass.
- Refresh preserves all delegated metadata across conversions and CAS writes.

## Distributed single-use OAuth state

Encrypted state must use authenticated encryption with associated data (AEAD),
a five-to-ten-minute TTL, and key rotation support. Encryption alone does not
make state single-use.

Add:

~~~go
type OAuthStateStore interface {
    CreateOrGetPending(ctx context.Context, record *OAuthStateRecord) (
        stored *OAuthStateRecord,
        created bool,
        err error,
    )
    Consume(ctx context.Context, stateHash, canonicalUserID, sessionHash string) error
    DeleteExpired(ctx context.Context, before time.Time) error
}
~~~

CreateOrGetPending uses a unique flow hash derived from canonical user,
provider, resource, and normalized scopes. It deduplicates initiation across
pods, not only within one process.

The caller that creates the row owns the flow and receives the authorization
URL. A concurrent caller that receives created=false does not attempt to
reconstruct the first flow's PKCE verifier or authorization URL. It receives a
typed oauth_link_pending response and polls status until the owner completes
or the record expires.

In one transaction, CreateOrGetPending returns an existing unexpired pending
record or replaces a consumed/expired record with a new state hash. The unique
flow row must not permanently block later linking.

Consume performs an atomic pending-to-consumed transition and fails when the
record is absent, expired, already consumed, belongs to another canonical user,
or is bound to another workspace session.

The default SQL implementation uses an additive table:

~~~sql
CREATE TABLE oauth_link_state (
    state_hash         VARCHAR(64) NOT NULL,
    flow_hash          VARCHAR(64) NOT NULL,
    user_id            VARCHAR(36) NOT NULL,
    session_hash       VARCHAR(64) NOT NULL,
    provider           VARCHAR(128) NOT NULL,
    expires_at         DATETIME NOT NULL,
    consumed_at        DATETIME,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (state_hash),
    UNIQUE KEY ux_oauth_link_state_flow (flow_hash)
);
~~~

Do not store authorization codes, PKCE verifiers, client secrets, or tokens in
this table. Consume state before token exchange. If exchange then fails, the
user starts a new authorization flow.

### Datly data-layer requirement

Implement oauth_link_state through Agently's Datly component conventions.
Auth handlers and services must not issue raw database/sql queries.

Add generated-style components under:

~~~text
pkg/agently/user/oauth/linkstate
pkg/agently/user/oauth/linkstate/read
pkg/agently/user/oauth/linkstate/write
pkg/agently/user/oauth/linkstate/consume
pkg/agently/user/oauth/linkstate/deleteexpired
~~~

Author the read contract in DQL:

~~~text
dql/oauth/linkstate/read.dql
~~~

The DQL read component accepts narrowly scoped criteria:

~~~text
stateHash
flowHash
canonicalUserID
sessionHash
pendingOnly
~~~

It returns non-secret state metadata needed by CreateOrGetPending, Consume,
status polling, and cleanup. The DQL must not project encrypted OAuth state,
PKCE verifiers, authorization codes, tokens, or client secrets.

Generate and retain the corresponding pkg/agently component implementation
through the repository's normal workflow. Generated repo/, dev/, deployment
Datly, and other transient build artifacts must not be committed. Do not
hand-maintain transient generated metadata when DQL is authoritative.

Requirements:

- Use DQL as the authoritative source for the read component and embed its
  generated SQL resource with the component package.
- Register read, create, consume, and cleanup components with datly.Service.
- Implement CreateOrGetPending and atomic pending-to-consumed transition
  through a Datly write/custom handler transaction.
- Use affected-row/CAS semantics to distinguish owner, pending, consumed, and
  expired outcomes.
- Keep connector and dialect handling inside the Datly/repository layer.
- Expose only a narrow OAuthStateStore adapter to service/auth.
- Define components during auth runtime initialization.
- Test through datly.Service using the same SQLite/MySQL-compatible patterns as
  existing user OAuth token and session components.

Schema DDL remains in the normal SQLite and versioned MySQL migration files.
No OAuth HTTP handler may depend directly on sql.DB.

The auth runtime's existing background maintenance loop owns DeleteExpired.
It records deleted-row count and oldest-expired age metrics.

## OAuth link endpoints

Add:

~~~text
GET    /v1/api/auth/mcp/{server}/status
POST   /v1/api/auth/mcp/{server}/initiate
GET    /v1/api/auth/mcp/callback
DELETE /v1/api/auth/mcp/{server}
~~~

### Initiate

Require:

- An authenticated workspace session.
- Explicit handler-level authentication because /v1/api/auth routes bypass
  normal middleware rejection.
- CSRF protection for cookie-authenticated POST and DELETE requests.
- A canonical users.id.
- A known MCP and provider registry entry.
- An allowlisted return URL.
- Per-canonical-user and per-source-IP rate limits. Status responses are
  non-enumerable and return the same external shape for unknown servers,
  unknown providers, and unlinked users.

Encrypted state contains:

~~~go
type MCPAuthState struct {
    CanonicalUserID string
    SessionIDHash   string
    ServerName      string
    ProviderRef     string
    ClientRef       string
    Resource        string
    Scopes          []string
    CodeVerifier    string
    ReturnURL       string
    Nonce           string
    ExpiresAt       time.Time
}
~~~

Build an authorization-code URL with S256 PKCE and the configured hosted
callback. Store the state hash through OAuthStateStore before returning the
authorization URL. Deduplicate concurrent initiation using a singleflight key
of canonical user, provider, resource, and normalized scopes.

Initiate responses are:

~~~json
{
  "status": "connect",
  "authorizationURL": "<provider URL>"
}
~~~

for the flow owner, or:

~~~json
{
  "status": "pending",
  "retryAfterSeconds": 2
}
~~~

for concurrent callers. The pending response contains no state blob, PKCE
verifier, authorization code, or reusable authorization URL.

The browser authorization-code flow requires a cookie-backed workspace session
for initiate and callback. Bearer-only clients receive `unsupported_flow`.
The CLI also supports an explicit out-of-band provider bootstrap after it has
created a cookie-backed workspace session:

- `--oob`, `--oauth-config`, and `--oauth-scopes` authenticate the workspace
  user and establish the canonical/effective user context.
- `--mcp-oob`, `--mcp-oauth-config`, `--mcp-oauth-scopes`, and
  `--mcp-oauth-resource` obtain a distinct provider grant locally.
- The CLI submits that grant to `POST /v1/api/auth/mcp/{server}/oob` with the
  active workspace cookie and per-session CSRF token.
- The server treats the submitted token envelope as untrusted: it re-resolves
  provider policy, verifies signature or introspection, issuer, audience,
  expiry, subject, and scopes, then persists it under the canonical workspace
  user using the normal encrypted delegated-provider storage key.
- Future calls need only workspace auth; the stored MCP token is reused and
  refreshed by the existing delegated-token lifecycle.

The OOB endpoint never accepts bearer-only workspace identity, never trusts
client-supplied scope metadata, and never weakens resource/audience matching.

### Callback

1. Require the same active workspace session used at initiation.
2. Decrypt and authenticate state.
3. Atomically consume its hash through OAuthStateStore.
4. Validate expiration, session binding, nonce, and return URL.
5. Reload provider/client configuration and verify its fingerprint.
6. Exchange the code.
7. Cryptographically validate the granted token.
8. Extract the provider subject from that verified token.
9. Reject an incompatible issuer/resource overwrite.
10. Persist the token under the main canonical user and provider.
11. Evict all relevant clients and negative caches.
12. Emit an auth-connected event and security audit record.
13. Redirect or close the popup.

The callback must not:

- Create a new main Agently user.
- Change EffectiveUserID from the workspace OAuth subject.
- Change authctx.Provider from the workspace OAuth provider.
- Install the MCP token as the workspace bearer or ID token.
- Replace the workspace session.
- Replace the workspace cookie.
- Return tokens to the browser.

Rate-limit callback failures by state hash and source IP without revealing
whether a state, user, provider, or authorization code exists.

Token validation requires:

- An algorithm allowlist; reject none and algorithm confusion.
- Signature verification using the provider's trusted JWKS.
- Exact normalized issuer equality.
- Exact audience/resource membership.
- Expiration, not-before, issued-at, and configured clock-skew checks.
- Granted scopes sufficient for the stored requirement.
- Subject extracted only after successful verification.
- Introspection through the configured provider when the access token is
  opaque.

Opaque tokens fail closed. Persist and use an opaque token only when the
provider registry defines a trusted introspection endpoint and authenticated
introspection confirms active=true, exact issuer/resource or audience,
required scopes, subject, and expiration. Providers without supported
introspection cannot be used for delegated opaque-token authentication.

### Status

Return only non-secret information:

~~~json
{
  "server": "viant-mcp-dev6",
  "provider": "adelphic-dev6",
  "connected": true,
  "scopes": ["plan:create", "plan:edit", "plan:read"],
  "expiresAt": "2026-08-28T01:00:00Z"
}
~~~

For legacy token rows lacking the new encrypted metadata, connected may still
be true when a usable token exists, while scopes and expiresAt are omitted.
Missing legacy metadata must not be represented as a disconnected user or as
an empty authoritative scope set.

### Disconnect

Delete only the exact canonical user/provider token, then evict matching MCP
clients and refresh-cache entries. Do not log out the workspace session.

Suggested files:

- service/auth/mcp_oauth_handlers.go
- service/auth/mcp_oauth_state.go
- service/auth/mcp_oauth_callback.go
- service/auth/mcp_oauth_test.go
- service/auth/extension_core.go

## MCP manager integration

Resolve authentication before:

- Initialize.
- Discover and tools/list.
- Prompt and resource discovery.
- Tool execution.
- Reconnect.
- Background ping.

Primary integration points:

- protocol/mcp/manager/auth_token.go
- protocol/mcp/manager/manager.go
- internal/tool/registry/registry.go
- service/augmenter/mcpfs/mcpfs.go

The manager becomes the single owner of token selection:

~~~go
requirement, err := resolver.Requirement(ctx, serverName)
resolution, err := resolver.Resolve(ctx, requirement)
ctx = authtransport.ContextWithToken(ctx, resolution.Token)
~~~

Do not call the workspace token provider for a delegated provider unless the
compatibility check passes.

The context passed here must be a child outbound context. EffectiveUserID,
CanonicalUserID, and the workspace provider remain inherited and unchanged;
only the MCP transport credential is added.

### Transport-authorizer ownership

For auth.mode oauth with providerRef or InlineProvider, viant/mcp is the sole
transport-level OAuth coordinator and the Agently CredentialResolver is the
sole credential-policy implementation.

viant/mcp performs pre-request resolution, attaches the returned credential,
and coordinates one 401 refresh/retry. When the external Agently resolver is
installed, viant/mcp disables its legacy OAuth2ConfigURL browser flow and never
opens a browser itself. Agently performs persistence, refresh exchange,
link-required decisions, and hosted callback handling through the resolver.

Legacy workspace-BFF clients retain the existing transport authorizer. The
selection is mutually exclusive and decided when Manager.newClient compiles
the MCP configuration.

The delegated path permits:

~~~text
one initial request
one refresh attempt
one retry
one OAuthLinkRequired result
~~~

Tests must assert exact request, refresh, and retry counts.

### Cache invalidation

After link, disconnect, invalid_grant, provider reload, or scope change, clear:

- MCP manager client pool entry for canonical user and server.
- Registry discovery failure and cooldown.
- Delegated resolver positive cache.
- Delegated resolver retry cooldown where appropriate.
- Provider metadata cache when its fingerprint changed.

Do not negative-cache delegated token-store misses across requests. This lets
another pod observe a newly linked token immediately from the shared store
without requiring cross-pod cache broadcasts.

## PassUserToken enforcement

PassUserToken exists in viant/mcp, but Agently currently reconstructs and
passes the workspace token regardless.

When forwarding is disabled:

~~~go
outboundCtx := authctx.WithoutTokens(ctx)
outboundCtx = authtransport.ContextWithToken(outboundCtx, resolution.Token)
~~~

This masking is limited to the outbound child context. It must not replace the
request context used by Agently services. The tool registry must stop
independently selecting a token after the manager has resolved delegated
authentication. Initialization, discovery, execution, and reconnect must all
use the same resolution.

Enable delegated auth through one feature gate that atomically selects the new
manager-owned path and disables legacy registry token injection for that MCP.
There must be no transition state where both paths can attach credentials.

## MCP challenge learning

Explicit configuration is preferred. For incomplete configurations, Agently
may learn from:

~~~http
401 WWW-Authenticate: Bearer resource_metadata=<URL>
~~~

Learn:

~~~go
type MCPAuthBinding struct {
    ServerName      string
    Origin          string
    MetadataURL     string
    ProviderRef     string
    ClientRef       string
    Issuer          string
    Resource        string
    ScopesSupported []string
    ETag            string
    ExpiresAt       time.Time
}
~~~

Validation:

- HTTPS metadata in production.
- Resource must match MCP origin.
- Authorization issuer must match an approved provider.
- Strict fetch timeout and redirect limits.
- Unknown issuers never receive known client credentials.
- Explicit MCP configuration overrides learned state.
- Changed issuer/resource metadata invalidates the binding.
- Learning resolves ClientRef through the provider defaultClient. It fails
  closed when no unambiguous default exists.
- Explicitly configured providers perform a one-time protected-resource
  metadata fetch and exact comparison at startup or first use. A mismatch is a
  configuration error, not an opportunity to silently rewrite providerRef.

Version one stores learned bindings in memory and StateStore. Production MCP
definitions should declare providerRef explicitly so correctness is not tied
to node-local learned state.

Do not request all scopes_supported automatically. Required scopes come from
trusted MCP configuration or tool-level authorization metadata.

Metadata discovery is singleflight-coalesced by normalized MCP origin and
metadata URL, with bounded TTL and failure cooldown, to prevent fetch storms.

## Error model

Add:

~~~go
type OAuthLinkRequiredError struct {
    ServerName  string
    ProviderRef string
    Resource    string
    Scopes      []string
    InitiateURL string
}
~~~

API representation:

~~~json
{
  "code": "mcp_oauth_link_required",
  "server": "viant-mcp-dev6",
  "provider": "adelphic-dev6",
  "resource": "https://mcp6.dev.viant.ai/mcp",
  "scopes": ["plan:create", "plan:edit", "plan:read"],
  "connectURL": "/v1/api/auth/mcp/viant-mcp-dev6/initiate"
}
~~~

On MCP 401:

1. Validate protected-resource metadata.
2. Invalidate the rejected in-memory token.
3. Refresh once when possible.
4. Retry exactly once.
5. Return OAuthLinkRequired after permanent failure.

Concurrent requests must not open multiple OAuth flows for the same user,
provider, resource, and scope set.

## UI behavior

The UI should:

1. Recognize mcp_oauth_link_required.
2. Show a provider-specific Connect action.
3. Open the initiation URL in a popup.
4. Wait for callback completion.
5. Retry discovery or the original operation.
6. Show provider connection status and scopes.
7. Provide Disconnect.
8. Treat oauth_link_pending as an existing flow: poll provider status, retry
   the MCP call after connection, and permit a new initiate only after expiry
   or explicit cancellation.

The UI must never receive client secrets, tokens, authorization codes, or raw
encrypted OAuth state.

Expected changes in viant/agently:

- API error decoding.
- Connect popup.
- Callback completion page.
- Provider status UI.
- Retry after connection.
- Mobile redirect integration.

## Scheduler behavior

Schedulers may reuse and refresh an existing persisted delegated token.

When none exists, return an actionable auth-required state. Headless execution
must never open a browser, fall back to the workspace token, or reuse an
anonymous token.

Schedule creation records the canonical workspace user ID, workspace provider,
and verified effective subject. Each run revalidates that the canonical user is
active and still authorized before resolving a delegated token. A disabled or
deleted user cannot use a previously persisted delegated credential.

User deactivation and deletion trigger best-effort provider revocation followed
by deletion of delegated token rows. If a provider lacks revocation support,
delete local credentials and emit an auditable revocation-not-supported event.

## Security requirements

- Rotate the exposed Dev6 client secret before production use.
- Register an Agently-specific hosted callback.
- Use exact HTTPS hosted callbacks.
- Require S256 PKCE.
- Bind state to user, session, provider, client, resource, scopes, nonce,
  return URL, and expiration.
- Reject replayed state.
- Maintain an AEAD keyring containing the active key and previous decryption
  keys for at least state TTL plus clock skew. New state always uses the active
  key; retired keys are removed only after that grace period.
- Verify token signature, issuer, audience/resource, expiration, and scopes.
- Use access tokens for Dev6 MCP calls.
- Never log secrets, tokens, codes, or full authorization URLs.
- Redact authorization headers.
- Preserve isolation in pools, stores, cookies, and refresh locks.
- Never fall back to a different provider after an exact miss.
- Require admin review for provider-registry changes.
- Require CSRF protection on initiate and disconnect.
- Record security audit events for link, disconnect, failed link, provider
  changes, invalid_grant, user deactivation cleanup, and kill-switch use.
- Provide global, provider, and MCP kill switches plus a documented bulk local
  token invalidation and provider-revocation procedure.
- Alert on invalid_grant bursts, callback failure bursts, provider metadata
  mismatches, and any kill-switch activation.

## Implementation phases

### Phase 0: Compatibility audit and API decision

1. Instrument TokenStoreDAO fallback hits without logging tokens.
2. Inventory oauth, jwt, empty, and configured provider aliases.
3. Define CanonicalWorkspaceProvider normalization.
4. Land the required viant/mcp ClientAuth, provider model, external resolver,
   and single 401 coordinator before downstream YAML implementation.
5. Define the mutually exclusive legacy-BFF and delegated-resolver paths.
6. Validate the live provider-column width for the fixed 71-character key.
7. Define immutable workspaceNamespace configuration and migration behavior.

Exit criteria:

- Every existing fallback dependency is classified.
- Provider aliases normalize deterministically.
- Existing single-provider deployments have regression coverage.
- The MCP YAML ownership decision is final.
- Storage-key derivation and live column width are validated.

### Phase 1: Configuration and canonical identity

1. Add provider/client configuration types.
2. Implement workspace provider repository.
3. Extend MCP auth configuration.
4. Add canonical user context support.
5. Add configuration validation and tests.

Exit criteria:

- Dev6 provider and MCP configuration load.
- Invalid references fail validation.
- Canonical identity is available on authenticated requests.

### Phase 2: Exact encrypted token persistence

1. Extend encrypted token metadata and JSON compatibility.
2. Extend both mirrored OAuthToken types.
3. Extend every scyauth and adapter conversion.
4. Add exact delegated provider lookup.
5. Preserve instrumented legacy aliases only where identified in Phase 0.
6. Add storage and refresh-CAS metadata round-trip tests.
7. Add provider/resource conflict rejection.
8. Define authoritative refresh-scope narrowing behavior.

Exit criteria:

- Workspace and Dev6 tokens coexist for one user.
- A missing Dev6 token never returns the workspace token.
- Refresh and CAS preserve issuer, resource, scopes, token type, and subject.
- Incompatible resource relinking cannot overwrite an existing credential.
- Narrowed refresh scopes are persisted and insufficient scopes require
  relinking for the affected MCP.

### Phase 3: Resolver and refresh

1. Implement requirement compilation.
2. Implement workspace JWT compatibility.
3. Implement provider token loading.
4. Add broker registry.
5. Implement one RoutingBroker over the existing token.Manager.
6. Reuse leases, CAS, cooldown, and invalid_grant handling.
7. Skip unknown/disabled providers without mutation.
8. Split context-returning workspace resolution from value-returning delegated
   resolution.
9. Bypass provider-based owner resolution for delegated storage.

Exit criteria:

- Stored tokens are injected eagerly.
- Expired tokens refresh silently.
- Revoked tokens produce OAuthLinkRequired.
- Unknown providers are never sent to the workspace broker.
- Missing broker configuration never deletes or modifies a token row.
- The background watcher is safe before link endpoints are enabled.
- Delegated resolution leaves the parent auth context unchanged.

### Phase 4: OAuth linking endpoints

1. Add the oauth_link_state SQLite and versioned MySQL migrations.
2. Author dql/oauth/linkstate/read.dql as the authoritative read contract.
3. Generate the Datly read component and embedded SQL artifacts.
4. Add Datly create, atomic consume, and delete-expired write components.
5. Register those components during auth runtime initialization.
6. Implement OAuthStateStore only as an adapter over datly.Service.
7. Add initiate, status, callback, and disconnect handlers.
8. Add AEAD state and distributed atomic consume.
9. Validate provider subject and selected-token expiration.
10. Store callback tokens under canonical user/provider.
11. Add popup completion response.
12. Enforce handler-level session authentication and CSRF.
13. Add endpoint rate limits and non-enumerable errors.
14. Add OAuthStateStore cleanup ownership and metrics.
15. Add AEAD keyring rotation grace behavior.
16. Fail closed for opaque tokens without trusted introspection.

Exit criteria:

- Endpoints work correctly in isolation.
- Callback does not modify the workspace session.
- Replayed, expired, cross-session, and cross-user state is rejected.
- Incompatible resource overwrite is rejected.
- Read access is DQL-generated and handlers contain no raw database/sql calls.
- Atomic consume is verified through Datly with affected-row/CAS semantics.

### Phase 5: MCP manager integration

1. Resolve auth before initialization and discovery.
2. Enforce PassUserToken.
3. Remove duplicated registry token selection.
4. Install the Agently CredentialResolver into viant/mcp.
5. Disable only viant/mcp's legacy interactive browser flow when the external
   resolver is installed.
6. Use viant/mcp's single 401 coordinator for one-refresh/one-retry handling.
7. Clear all relevant caches after auth changes.
8. Keep delegated token misses uncached.

Exit criteria:

- Explicitly configured connected providers avoid initial 401.
- Workspace tokens are never sent to Dev6.
- Reconnect and discovery use the same provider token.
- First Dev6 use requires one authorization and later sessions reuse storage.
- Exactly one component owns token injection and 401 recovery.

### Phase 5b: Challenge learning

1. Add validated protected-resource metadata loading.
2. Match only approved provider issuers.
3. Resolve an unambiguous defaultClient.
4. Add explicit-config metadata cross-checks.
5. Persist only non-secret learned bindings through StateStore.

Exit criteria:

- Challenge learning cannot redirect credentials to an unknown issuer.
- Explicit configuration wins and mismatches fail closed.

### Phase 6: UI and mobile

1. Surface link-required responses.
2. Implement Connect and Disconnect.
3. Implement popup completion and retry.
4. Add mobile provider redirects.
5. Add connection status UI.

Mobile does not reuse the web callback's cookie-session assumption. Before
mobile support is enabled, define a separate native/device flow with an
app-bound initiation handle, registered deep link, PKCE, and one-time state
consume. Until then, delegated MCP linking is web-session only.

### Phase 7: Production rollout

1. Create a Steward Dev6 OAuth client.
2. Register the hosted Agently callback.
3. Store the rotated secret in SCY/secret manager.
4. Deploy provider and MCP configuration.
5. Enable behind a feature flag.
6. Canary with awitas_viant_devtest.
7. Validate refresh across restart and multiple pods.
8. Enable for additional users.

### Phase 8: Real Agently custom-workspace verification

After all agently-core and viant/agently changes are complete, validate the
feature through the actual application at:

~~~text
/Users/awitas/go/src/github.com/viant/agently
~~~

Create a dedicated non-secret test workspace containing:

~~~text
config.yaml
oauth/providers/adelphic-dev6.yaml
mcp/viant-mcp-dev6.yaml
agents/<test-agent>.yaml
~~~

Secret values remain in SCY resources or environment references and are never
committed to the workspace fixture.

Run the real Agently server/CLI with AGENTLY_WORKSPACE pointing to this custom
workspace and verify:

1. Workspace OAuth login establishes EffectiveUserID and CanonicalUserID.
2. MCP discovery resolves providerRef eagerly without forwarding the workspace
   token to Dev6.
3. First use returns mcp_oauth_link_required and a working hosted initiation
   URL.
4. Initiate, provider authorization, and callback complete through the real
   HTTP routes.
5. EffectiveUserID and workspace provider are unchanged after linking.
6. tools/list exposes create_media_plan, edit_media_plan, and get_media_plan
   for the granted plan scopes.
7. Restarting Agently reuses the encrypted stored token without
   reauthentication.
8. A forced near-expiry access token refreshes through the Dev6 provider at
   the 15-minute policy threshold.
9. When tokenType is idToken or useIdToken is enabled, refresh and
   Credential.ExpiresAt use the verified ID-token exp rather than access-token
   expiry.
10. A missing ID token after refresh retains the old ID token only while it is
    valid, then requires relinking without access-token fallback.
11. A headless scheduled run created by the workspace user reuses the delegated
    token and never opens a browser.
12. Missing/revoked scheduled credentials produce actionable auth-required
    status without affecting other schedules.
13. Existing legacy BFF/useIdToken MCP workspace fixtures still behave exactly
    as before.
14. Inline-provider and registry-provider MCP configurations both refresh
    through their correct provider.
15. Disconnect removes only the delegated provider token and leaves the
    workspace session active.

Capture sanitized request/response evidence and token fingerprints only. Never
record raw cookies, authorization codes, client secrets, access tokens,
refresh tokens, or ID tokens.

Exit criteria:

- The full browser/API flow works against Dev6 using the real Agently runtime.
- Restart and scheduler tests prove durable token reuse.
- Legacy workspace regression passes.
- No workspace identity or token leaks to the delegated MCP.
- No secrets are added to Git status or application logs.

## Unit tests

- Workspace provider alias normalization and fallback telemetry.
- Existing jwt, oauth, empty, and configured-provider lookup behavior.
- Provider registry loading and default client selection.
- Issuer normalization.
- Invalid provider/client references.
- Resource-origin validation.
- Scope normalization and subset checks.
- Workspace token compatibility.
- ID-token rejection for access-token requirements.
- Exact token lookup.
- Encrypted metadata round trip.
- Metadata round trip through refresh, CASPut, and reload.
- Canonical user context propagation.
- Session and bearer paths use the same canonical resolver.
- Delegated owner resolution uses CanonicalUserID without user upsert.
- Provider broker routing.
- Unknown broker rows are skipped without refresh or deletion.
- Refresh lease and CAS behavior.
- Distributed state atomic consume, expiration, and replay.
- Callback session binding.
- Initiate and disconnect reject unauthenticated and CSRF-invalid requests.
- EffectiveUserID remains the workspace subject during delegated MCP calls.
- Workspace provider context remains unchanged after resolution and refresh.
- Delegated access tokens never appear through authctx.Bearer or
  authctx.IDToken.
- PassUserToken false behavior.
- Challenge metadata validation.
- Provider/resource storage conflict rejection.
- Fixed-length storage-key derivation and column-width validation.
- Refresh scope narrowing updates stored scopes and blocks insufficient use.
- Default refresh begins 15 minutes before expiration.
- Provider refreshLead override is honored.
- Short-lived tokens use the 20-percent lifetime clamp.
- Request-time and background refresh make the same threshold decision.
- Refresh jitter and cooldown prevent immediate repeated refresh.
- Broad store scan includes every configured provider and then applies each
  provider's own refresh policy.
- A refresh failure for one provider does not stop other providers.
- Inline and registry providers route to their exact client/token endpoint.
- Opaque token without introspection fails closed.
- Legacy interactive browser auth is disabled when the external resolver is
  installed, while the single viant/mcp 401 coordinator remains active.
- Exactly one refresh and one retry occur after delegated 401.
- YAML parsing covers the inline ClientOptions auth ownership decision.

## Integration tests

- Workspace and MCP use different issuers.
- Two users link the same provider independently.
- Workspace and delegated tokens coexist.
- Token survives restart.
- Another pod loads and refreshes the token.
- Workspace, registry, and inline providers refresh in one watcher cycle.
- One provider timeout does not block successful refresh for another.
- Temporary refresh failure preserves credentials.
- invalid_grant invalidates only Dev6.
- Scope expansion causes one new authorization.
- Callback does not replace workspace cookies.
- Callback and MCP execution preserve the effective workspace user.
- Link success is immediately visible from another pod.
- Disconnect removes only the selected provider.
- Disabled workspace users cannot use stored delegated credentials.
- Cross-pod CreateOrGetPending returns one connect URL to the owner and pending
  responses to concurrent callers.
- AEAD state created before rotation decrypts during the grace window.
- Endpoint rate limits and non-enumerable errors behave consistently.
- Scheduler reuses an existing token.
- Scheduler fails clearly when no token exists.

## Dev6 acceptance test

1. Sign in as awitas_viant_devtest.
2. Initiate Dev6 linking.
3. Request plan:create, plan:edit, and plan:read.
4. Complete the Agently-hosted callback.
5. Confirm tools/list returns all three media-plan tools.
6. Restart Agently and repeat without authorization.
7. Force access-token expiration and confirm silent refresh.
8. Revoke refresh access and confirm one link-required response.
9. Confirm logs contain no token or secret values.
10. Confirm parallel first-use calls expose one owner authorization URL while
    all other callers receive oauth_link_pending.
11. Confirm a provider with no broker is skipped without token deletion.
12. Repeat the acceptance flow through the real viant/agently executable and
    custom workspace defined in Phase 8.

## Observability

Record:

~~~text
mcp_auth_resolution
mcp_auth_cache_hit
mcp_auth_store_hit
mcp_auth_workspace_token_reused
mcp_auth_refresh_started
mcp_auth_refresh_succeeded
mcp_auth_refresh_failed
mcp_auth_link_required
mcp_auth_link_succeeded
mcp_auth_disconnect
mcp_auth_metadata_learned
mcp_auth_metadata_rejected
mcp_auth_exchange_after_state_consume_failed
mcp_auth_state_cleanup
mcp_auth_kill_switch_activated
~~~

Safe dimensions include MCP server, provider reference, resolution source,
error classification, and runtime mode. Never record tokens, codes, secrets,
full authorization URLs, or raw provider subjects.

## Acceptance criteria

- MCP configuration can reference an OAuth provider and client.
- EffectiveUserID and workspace-provider context remain unchanged before,
  during, and after MCP authorization and tool execution.
- Agently resolves authentication before MCP initialization.
- Workspace tokens are reused only after complete compatibility validation.
- Dev6 tokens are encrypted under the canonical Agently user.
- Version one does not alter user_oauth_token. It requires a distributed
  OAuthStateStore and uses the additive oauth_link_state table only when no
  existing distributed CAS implementation is available.
- Refresh uses the existing lease and CAS mechanism.
- Missing provider tokens never fall back to another provider.
- Successful links survive conversations, restarts, and pods.
- Permanent refresh failure requests relinking without prompt storms.
- Validated 401 metadata can produce an eager learned binding.
- Explicit MCP configuration overrides learned metadata.
- Browsers never receive OAuth tokens or client secrets.
- Existing workspace-only deployments retain their current behavior.
- Background refresh never mutates a row whose provider has no broker.
- Refresh preserves all delegated metadata through CAS persistence.
- Parallel first-use calls create one authorization flow; only its owner
  receives the URL and other callers receive pending.
- Provider/resource conflicts fail instead of overwriting tokens.
- User deactivation prevents future delegated-token use.
- Kill switches are enforced before reuse, refresh, initiation, and callback
  persistence.
- Opaque tokens are rejected unless trusted introspection validates them.
- Provider storage keys are fixed-length, collision-resistant, and validated
  against the live column width.
- Refresh scope narrowing is persisted and never masked by stale metadata.
- All providers refresh through their own broker at the shared default
  15-minute lead, subject to the short-lifetime clamp.
- Background refresh covers active sessions and persisted tokens for inactive
  sessions while rejecting disabled canonical users.

## Future schema extensions

Add a migration only when these requirements become necessary:

1. Multiple external accounts for one provider: add user_oauth_identity.
2. Multiple resource token sets for one provider: add resource to
   user_oauth_token and key by user, provider, and resource.
3. Distributed learned bindings: add mcp_auth_binding.

The existing encrypted token table remains sufficient for the Dev6 provider
and its MCP resource. Production callback replay protection additionally
requires OAuthStateStore. Use an existing distributed CAS implementation when
available; otherwise create the additive oauth_link_state table defined above.
