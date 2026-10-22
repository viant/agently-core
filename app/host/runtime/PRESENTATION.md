# Verified UI presentation facts

An operator can supply roles, exposure flags and account context independently
of resource authorization:

```yaml
ui:
  presentation:
    identityFile: identity/presentation.json
```

The file contains the existing `oauth.HTTPIdentityConfig` JSON for a trusted
`oidc-userinfo` endpoint. Configure issuer, audience, algorithms, JWKS, signed
user/account mappings, profile mappings and cache/lease settings explicitly.
Do not include entity-permission mappings for this presentation-only use.

The host wraps the existing HTTP identity provider with `IDTokenIdentityProvider`
and registers `WithPresentationPrincipalResolver`. The resolver supplies one
snapshot per projection; HTTP user-info cache behavior remains owned by the
identity adapter. A managing producer's existing presentation snapshot wins.
Failures are surfaced rather than represented as an empty permission set.

This binding installs no ACL store, entity gate, tool authorizer or datasource
permission resolver, and does not change datasource caching. Resource and data
authorization remain with their owning providers. The existing `ui.authorization`
configuration remains a separate compatibility mechanism; a legacy MCP tool is
not a substitute for verified presentation facts.

If a tenant identifier is required by the identity adapter, an operator may use
the verified issuer as a presentation identity domain. This is not a business
tenant or a grant: it must not be reused for resource/account routing or
authorization. The signed account remains separate.

Window/navigation metadata uses user-scoped caches with the existing refresh and
notification behavior. A cached menu does not promise re-evaluation on every
request or automatically inherit the user-info cache TTL.
