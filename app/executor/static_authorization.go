package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/runtime/requestctx"
	"strings"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	svcauth "github.com/viant/agently-core/service/auth"
	policy "github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	forgetypes "github.com/viant/forge/backend/types"
)

// StaticAuthorizationRegistration joins trusted service instances with exact
// host-owned mappings. The identity provider, ACL store and gate evaluator are
// supplied by the host; no product capability or account is built into Core.
type AuthorizationDecisionScope interface {
	BeginDecision(context.Context) (context.Context, func() error, error)
	WithoutDecision(context.Context) context.Context
	MetadataReadActive(context.Context) bool
}

type StaticAuthorizationRegistration struct {
	ExecutionContext  func(context.Context) context.Context
	DecisionScope     AuthorizationDecisionScope
	AuthoritySnapshot policy.AuthoritySnapshotResolver
	// ComponentAuthoritySnapshot must bypass metadata and pure-decision scopes.
	// Execution uses fresh authority while metadata may retain explicit batching.
	ComponentAuthoritySnapshot policy.AuthoritySnapshotResolver
	ResourceRevisionBindings   []policy.ResourceRevisionBinding
	ResourceRevisionMappings   authz.SelectionStore
	ProviderRef                string
	CapabilityMappingRef       string
	PolicyVersion              string
	Service                    *authz.Service
	Account                    func(context.Context, authz.Facts) (string, error)
	AuthorityRevision          func(context.Context, authz.Facts, string) (string, time.Time, error)
	AccountProjection          permittedview.AccountProjection
	GateEvaluator              policy.EvaluatorBridge
	EntityPermission           func(context.Context, authz.Facts, authz.Entity, string) (bool, error)
	EntityPermissionWithLease  func(context.Context, authz.Facts, authz.Entity, string) (bool, time.Time, error)
	EntityRoles                func(context.Context, authz.Facts, authz.Entity) ([]string, error)
	WholeResources             []policy.ResourceBinding
	Capabilities               []permittedview.CapabilityBinding
	BackendResources           []policy.BackendBinding
	BackendMapper              policy.BackendMapper // trusted dynamic lookup for IDs created after startup
	ProtectTools               bool                 // opt in only when every dispatchable tool is mapped
}

type PreparedAuthorization struct {
	windowDependencyVerifier func(context.Context, identity.ResolvedResource, *forgetypes.WindowTarget, *forgetypes.WindowResourceVariant) error
	windowTargetVerifier     func(context.Context, identity.ResolvedResource, forgetypes.WindowTarget, string) error
	decisionScope            AuthorizationDecisionScope
	registered               bool
	authoritySnapshot        policy.AuthoritySnapshotResolver
	backend                  policy.BackendMapper
	checker                  *policy.ActionAuthorizer
	authorityRevision        func(context.Context, authz.Facts, string) (string, time.Time, error)
	revisionBindings         []policy.ResourceRevisionBinding
	revisionMappings         authz.SelectionStore
	providerRef              string
	mappingRef               string
	provider                 AuthorizationProvider
	v1                       permittedview.CapabilityMapping
	v2                       permittedview.CapabilityMappingV2
	window                   func(context.Context, string) (bool, error)
}

// WithWindowTargetVerifier verifies the exact parent/variant/profile selection
// independently from ACL admission. It must be registered by the trusted host.
func (p *PreparedAuthorization) WithWindowTargetVerifier(verify func(context.Context, identity.ResolvedResource, forgetypes.WindowTarget, string) error) error {
	if p == nil || p.registered || verify == nil {
		return fmt.Errorf("window target verifier must be supplied before registration")
	}
	p.windowTargetVerifier = verify
	return nil
}

// WithWindowDependencyVerifier supplies fresh checks for original child pins.
// Reference-bearing formats fail closed until a trusted host registers it.
func (p *PreparedAuthorization) WithWindowDependencyVerifier(verify func(context.Context, identity.ResolvedResource, *forgetypes.WindowTarget, *forgetypes.WindowResourceVariant) error) error {
	if p == nil || p.registered || verify == nil {
		return fmt.Errorf("window dependency verifier must be supplied before registration")
	}
	p.windowDependencyVerifier = verify
	return nil
}

func PrepareStaticAuthorization(registration StaticAuthorizationRegistration) (*PreparedAuthorization, error) {
	if registration.ProviderRef == "" || strings.TrimSpace(registration.ProviderRef) != registration.ProviderRef || registration.CapabilityMappingRef == "" || strings.TrimSpace(registration.CapabilityMappingRef) != registration.CapabilityMappingRef || registration.PolicyVersion == "" || registration.Service == nil || registration.Service.Store == nil || registration.Service.Provider == nil || registration.Account == nil || registration.AuthorityRevision == nil || registration.GateEvaluator == nil {
		return nil, fmt.Errorf("static authorization registration is incomplete")
	}
	if registration.EntityPermission == nil && registration.EntityPermissionWithLease == nil {
		for _, binding := range registration.Capabilities {
			if !binding.Global {
				return nil, fmt.Errorf("resource capabilities require a verified entity-permission provider")
			}
		}
		for _, binding := range registration.BackendResources {
			if binding.EntityType != "" {
				return nil, fmt.Errorf("entity-scoped backend actions require a verified entity-permission provider")
			}
		}
	}
	whole, err := policy.NewStaticResourceMapper(registration.WholeResources)
	if err != nil {
		return nil, err
	}
	v1, v2, err := permittedview.NewStaticCapabilityMappings(registration.Capabilities)
	if err != nil {
		return nil, err
	}
	backend, err := policy.NewStaticBackendMapper(registration.BackendResources)
	if err != nil {
		return nil, err
	}
	backend = policy.ChainBackendMappers(backend, registration.BackendMapper)
	entityPermission := registration.EntityPermission
	if entityPermission != nil {
		original := entityPermission
		entityPermission = func(ctx context.Context, facts authz.Facts, entity authz.Entity, permission string) (bool, error) {
			return original(svcauth.AuthzIDTokenContext(ctx), facts, entity, permission)
		}
	}
	entityPermissionWithLease := registration.EntityPermissionWithLease
	if entityPermissionWithLease != nil {
		original := entityPermissionWithLease
		entityPermissionWithLease = func(ctx context.Context, facts authz.Facts, entity authz.Entity, permission string) (bool, time.Time, error) {
			return original(svcauth.AuthzIDTokenContext(ctx), facts, entity, permission)
		}
	}
	checker := &policy.ActionAuthorizer{Service: registration.Service, Account: registration.Account, GateEvaluator: registration.GateEvaluator, EntityPermission: entityPermission, EntityPermissionWithLease: entityPermissionWithLease}
	if len(registration.ResourceRevisionBindings) > 0 {
		if _, err := policy.NewResourceRevisionPolicy(checker, policy.OperationWindowView, registration.ResourceRevisionBindings, registration.ResourceRevisionMappings, registration.AuthorityRevision, nil, policy.WithAuthoritySnapshot(registration.AuthoritySnapshot)); err != nil {
			return nil, err
		}
	}
	projection := registration.AccountProjection
	if projection == nil {
		projection = permittedview.NumericAccountProjection
	}
	window := checker.WindowCallback(whole)
	provider := AuthorizationProvider{AuthoritySnapshot: registration.AuthoritySnapshot, ComponentAuthoritySnapshot: registration.ComponentAuthoritySnapshot, Service: registration.Service, Account: registration.Account, AuthorityRevision: registration.AuthorityRevision, AccountProjection: projection, GateEvaluator: registration.GateEvaluator, EntityPermission: entityPermission, EntityPermissionWithLease: entityPermissionWithLease, EntityRoles: registration.EntityRoles, PolicyVersion: registration.PolicyVersion, PolicyResource: whole, WindowAuthorize: window, DatasourceAuthorize: checker.DatasourceCallback(backend), ReportAuthorize: checker.ReportCallback(backend)}
	provider.ExecutionContext = registration.ExecutionContext
	if registration.DecisionScope != nil {
		provider.WindowReadDecisionScope = func(ctx context.Context) (context.Context, func() error, error) {
			return registration.DecisionScope.BeginDecision(registration.DecisionScope.WithoutDecision(requestctx.WithoutWindowReadDecision(ctx)))
		}
	}
	if provider.ComponentAuthoritySnapshot != nil {
		original := provider.ComponentAuthoritySnapshot
		provider.ComponentAuthoritySnapshot = func(ctx context.Context) (gating.Principal, error) {
			ctx = requestctx.WithoutWindowReadDecision(ctx)
			if registration.DecisionScope != nil {
				ctx = registration.DecisionScope.WithoutDecision(ctx)
			}
			return original(ctx)
		}
	}
	if provider.ExecutionContext != nil || registration.DecisionScope != nil {
		original := provider.ExecutionContext
		provider.ExecutionContext = func(ctx context.Context) context.Context {
			ctx = requestctx.WithoutWindowReadDecision(ctx)
			if registration.DecisionScope != nil {
				ctx = registration.DecisionScope.WithoutDecision(ctx)
			}
			if original != nil {
				ctx = original(ctx)
			}
			return requestctx.WithoutWindowReadDecision(ctx)
		}
	}
	if registration.ProtectTools {
		provider.ToolAuthorize = checker.ToolCallback(backend)
		if provider.ExecutionContext != nil {
			original := provider.ToolAuthorize
			provider.ToolAuthorize = func(ctx context.Context, name string, args map[string]interface{}) error {
				return original(provider.ExecutionContext(ctx), name, args)
			}
		}
	}
	return &PreparedAuthorization{decisionScope: registration.DecisionScope, authoritySnapshot: registration.AuthoritySnapshot, backend: backend, checker: checker, authorityRevision: registration.AuthorityRevision, revisionBindings: append([]policy.ResourceRevisionBinding(nil), registration.ResourceRevisionBindings...), revisionMappings: registration.ResourceRevisionMappings, providerRef: registration.ProviderRef, mappingRef: registration.CapabilityMappingRef, provider: provider, v1: v1, v2: v2, window: window}, nil
}

func (p *PreparedAuthorization) WindowAuthorizer(ctx context.Context, windowID string) (bool, error) {
	if p == nil || p.window == nil {
		return false, policy.ErrDenied
	}
	return p.window(ctx, windowID)
}

func (p *PreparedAuthorization) Register(builder *Builder) error {
	if p == nil || builder == nil {
		return fmt.Errorf("prepared authorization and builder are required")
	}
	p.registered = true
	builder.WithAuthorizationProvider(p.providerRef, p.provider)
	builder.WithCapabilityMapping(p.mappingRef, p.v1)
	builder.WithCapabilityMappingV2(p.mappingRef, p.v2)
	return nil
}

// ResourceResolver shares authz version selection and execution checks between
// local/remote windows and reporting callers, with a host-owned content source.
func (p *PreparedAuthorization) ResourceResolver(operation string, source identity.ResourceSource, selection func(context.Context, string) ([]authz.Entity, string, error)) (*identity.ResourceResolver, error) {
	if p == nil || source == nil {
		return nil, policy.ErrDenied
	}
	authority, err := policy.NewResourceRevisionPolicy(p.checker, operation, p.revisionBindings, p.revisionMappings, p.authorityRevision, selection, policy.WithAuthoritySnapshot(p.authoritySnapshot))
	if err != nil {
		return nil, err
	}
	return &identity.ResourceResolver{Source: source, Policy: authority}, nil
}

// WithWindowResourceResolver installs the same canonical admission for Core's
// UI lifecycle and Forge's definition catalog before registration.
func (p *PreparedAuthorization) WithWindowResourceResolver(resolve func(context.Context, string) (*identity.ResourceResolver, identity.ResourceRef, error)) error {
	if p == nil || resolve == nil {
		return policy.ErrDenied
	}
	p.window = func(ctx context.Context, id string) (bool, error) {
		resolver, ref, err := resolve(ctx, id)
		if err != nil {
			return false, err
		}
		if resolver == nil {
			return false, policy.ErrDenied
		}
		_, err = resolver.Resolve(ctx, ref)
		if errors.Is(err, identity.ErrResourceDenied) {
			return false, nil
		}
		return err == nil, err
	}
	p.provider.WindowAuthorize = p.window
	p.provider.DatasourceResourceRevalidator = func(ctx context.Context, pin identity.ResolvedResource) (*identity.ResolvedResource, error) {
		resolver, ref, err := resolve(ctx, pin.URI)
		if err != nil {
			return nil, err
		}
		if resolver == nil || ref.URI != pin.URI {
			return nil, policy.ErrDenied
		}
		_, fresh, err := resolver.ReadResolved(ctx, pin)
		return fresh, err
	}

	p.provider.DatasourceDefinitionAuthorize = func(ctx context.Context, ds *dsproto.DataSource, inputs map[string]interface{}) error {
		pin, ok := requestctx.ResolvedResourceFromContext(ctx)
		if !ok || ds == nil {
			return policy.ErrDenied
		}
		resolver, ref, err := resolve(ctx, pin.URI)
		if err != nil {
			return err
		}
		if resolver == nil || ref.URI != pin.URI {
			return policy.ErrDenied
		}
		raw, _, err := resolver.ReadResolved(ctx, *pin)
		if err != nil {
			return err
		}
		target, _ := requestctx.WindowTargetFromContext(ctx)
		variant, err := p.selectWindowResource(raw, target)
		if err != nil || variant == nil || target == nil || p.windowTargetVerifier == nil || p.windowTargetVerifier(ctx, *pin, *target, variant.Fingerprint) != nil {
			return policy.ErrDenied
		}
		if len(variant.DataSourceResources) > 0 && (p.windowDependencyVerifier == nil || p.windowDependencyVerifier(ctx, *pin, target, variant) != nil) {
			return policy.ErrDenied
		}
		descriptor, err := json.Marshal(ds)
		fingerprint := identity.ContentFingerprint(descriptor)
		if variant.DataSources != nil {
			fingerprint, err = forgetypes.WindowDescriptorFingerprint(descriptor)
		}
		if err != nil || variant.Window.ResourceDependencies[ds.ID] == "" || fingerprint != variant.Window.ResourceDependencies[ds.ID] {
			return policy.ErrDenied
		}
		resource, action, selected, permission, err := p.backend(ctx, "datasource.fetch", pin.URI+"#"+ds.ID, inputs)
		if err != nil {
			return err
		}
		uri, err := identity.ParseResourceURI(pin.URI)
		if err != nil || resource.Kind != uri.Kind || resource.ID != pin.URI || resource.Version != pin.Selector() {
			return policy.ErrDenied
		}
		return p.checker.AuthorizeMany(ctx, resource, action, selected, permission)
	}
	p.provider.DatasourceDefinitionResolver = func(ctx context.Context, pin identity.ResolvedResource, target *forgetypes.WindowTarget, id string) (*dsproto.DataSource, error) {
		if target == nil || p.windowTargetVerifier == nil {
			return nil, policy.ErrDenied
		}
		resolver, ref, err := resolve(ctx, pin.URI)
		if err != nil || resolver == nil || ref.URI != pin.URI {
			return nil, policy.ErrDenied
		}
		raw, _, err := resolver.ReadResolved(ctx, pin)
		if err != nil {
			return nil, err
		}
		variant, err := p.selectWindowResource(raw, target)
		if err != nil {
			return nil, err
		}
		if err = p.windowTargetVerifier(ctx, pin, *target, variant.Fingerprint); err != nil {
			return nil, err
		}
		if len(variant.DataSourceResources) > 0 {
			if p.windowDependencyVerifier == nil {
				return nil, policy.ErrDenied
			}
			if err = p.windowDependencyVerifier(ctx, pin, target, variant); err != nil {
				return nil, err
			}
		}
		definition := variant.DataSources[id]
		if definition == nil {
			return nil, policy.ErrDenied
		}
		var ds dsproto.DataSource
		if json.Unmarshal(definition, &ds) != nil || ds.ID != id {
			return nil, policy.ErrDenied
		}
		return &ds, nil
	}
	// Canonical datasource guard above performs the exact version-bound action
	// check; the legacy datasource-ID mapper cannot choose a second authority.
	p.provider.DatasourceAuthorize = func(ctx context.Context, _ string, _ map[string]interface{}) error {
		if _, ok := requestctx.ResolvedResourceFromContext(ctx); !ok {
			return policy.ErrDenied
		}
		return nil
	}

	return nil
}
func (p *PreparedAuthorization) selectWindowResource(raw json.RawMessage, target *forgetypes.WindowTarget) (*forgetypes.WindowResourceVariant, error) {
	if p.windowDependencyVerifier != nil {
		return forgetypes.SelectWindowResourceWithReferences(raw, target)
	}
	return forgetypes.SelectWindowResource(raw, target)
}

// AuthorizeResolvedWindow checks a named UI operation against the exact pinned
// resource version. The surrounding bridge revalidates content and principal
// binding both before and after command dispatch.
func (p *PreparedAuthorization) AuthorizeResolvedWindow(ctx context.Context, pin identity.ResolvedResource, method string, params map[string]any) error {
	if p == nil || p.backend == nil || !pin.ValidUntil.After(time.Now()) {
		return policy.ErrDenied
	}
	resource, action, selected, permission, err := p.backend(ctx, method, pin.URI, params)
	if err != nil {
		return err
	}
	uri, err := identity.ParseResourceURI(pin.URI)
	if err != nil || resource.Kind != uri.Kind || resource.ID != pin.URI || resource.Version != pin.Selector() {
		return policy.ErrDenied
	}
	return p.checker.AuthorizeMany(ctx, resource, action, selected, permission)
}

// SetResourceRevisionMappings completes trusted construction before registration.
// It never reads identity-dependent selection data at startup.
func (p *PreparedAuthorization) SetResourceRevisionMappings(store authz.SelectionStore) error {
	if p == nil || p.registered || store == nil {
		return fmt.Errorf("selection store required before authorization registration")
	}
	if _, err := policy.NewResourceRevisionPolicy(p.checker, policy.OperationWindowView, p.revisionBindings, store, p.authorityRevision, nil, policy.WithAuthoritySnapshot(p.authoritySnapshot)); err != nil {
		return err
	}
	p.revisionMappings = store
	return nil
}

// ResourceAuthority checks explicit lifecycle selectors directly. Selection map
// access must not recursively invoke omitted revision selection.
func (p *PreparedAuthorization) ResourceAuthority() identity.ResourceAuthority {
	return identity.ResourceAuthorityFunc(func(ctx context.Context, uri identity.ResourceURI, selector, action string) (result identity.VerifiedActor, resultErr error) {
		pureRead := uri.Kind == "window" && (action == "describe" || action == "retrieve" || action == "selection.read")
		if !pureRead {
			ctx = requestctx.WithoutWindowReadDecision(ctx)
		}
		// Read-only logical controls retain an explicitly opened metadata-list scope.
		// Mutation/content checks use a separate fresh pure-decision scope only.
		if p != nil && p.decisionScope != nil && !((action == "describe" || action == "selection.read") && p.decisionScope.MetadataReadActive(ctx)) {
			clean := ctx
			if !pureRead || !requestctx.WindowReadDecisionActive(ctx) {
				clean = p.decisionScope.WithoutDecision(ctx)
			}
			scoped, finish, err := p.decisionScope.BeginDecision(clean)
			if err != nil {
				return identity.VerifiedActor{}, err
			}
			if scoped == nil || finish == nil {
				return identity.VerifiedActor{}, policy.ErrIdentityRejected
			}
			ctx = scoped
			defer func() {
				if err := finish(); err != nil {
					result = identity.VerifiedActor{}
					resultErr = err
				}
			}()
		}
		if p == nil || selector == "" || action == "" || ctx == nil || ctx.Err() != nil {
			return identity.VerifiedActor{}, policy.ErrDenied
		}
		if selector != "logical" && selector != "working" {
			var stamp int64
			if _, err := fmt.Sscan(selector, &stamp); err != nil || stamp <= 0 || fmt.Sprint(stamp) != selector {
				return identity.VerifiedActor{}, policy.ErrDenied
			}
		}
		before, err := p.resourcePrincipal(ctx)
		if err != nil {
			return identity.VerifiedActor{}, err
		}
		actor := identity.VerifiedActor{Subject: before.Facts.Subject, Issuer: before.Facts.Issuer, TenantID: before.Facts.Tenant, AccountID: before.AccountID, IdentityRevision: before.IdentityRevision, ValidUntil: before.Facts.ValidUntil}
		if !actor.Valid(time.Now()) {
			return identity.VerifiedActor{}, policy.ErrIdentityRejected
		}
		var bound *policy.ResourceRevisionBinding
		for i := range p.revisionBindings {
			b := &p.revisionBindings[i]
			if b.URI == uri.String() && b.Operation == "resource."+action {
				if bound != nil {
					return identity.VerifiedActor{}, policy.ErrDenied
				}
				bound = b
			}
		}
		if bound == nil || bound.Resource.Kind != uri.Kind || bound.Resource.ID != uri.String() || bound.Resource.Tenant != actor.TenantID && bound.Resource.Tenant != "*" {
			return identity.VerifiedActor{}, policy.ErrDenied
		}
		lease, err := p.checker.AuthorizeManyWithLease(ctx, authz.Resource{Kind: uri.Kind, ID: uri.String(), Tenant: actor.TenantID, Version: selector}, bound.Action, nil, "")
		if err != nil {
			return identity.VerifiedActor{}, err
		}
		after, err := p.resourcePrincipal(ctx)
		if err != nil {
			return identity.VerifiedActor{}, err
		}
		if !policy.SameAuthorityFacts(before.Facts, after.Facts, time.Now()) || before.AccountID != after.AccountID || before.IdentityRevision != after.IdentityRevision {
			return identity.VerifiedActor{}, policy.ErrIdentityRejected
		}
		if after.Facts.ValidUntil.Before(lease) {
			lease = after.Facts.ValidUntil
		}
		if actor.ValidUntil.Before(lease) {
			lease = actor.ValidUntil
		}
		actor.ValidUntil = lease
		if !actor.Valid(time.Now()) {
			return identity.VerifiedActor{}, policy.ErrIdentityRejected
		}
		return actor, nil
	})
}

func (p *PreparedAuthorization) resourcePrincipal(ctx context.Context) (gating.Principal, error) {
	if p == nil || p.checker == nil || p.checker.Service == nil || p.checker.Service.Provider == nil || p.checker.Account == nil || p.authorityRevision == nil {
		return gating.Principal{}, policy.ErrIdentityRejected
	}
	if p.authoritySnapshot != nil {
		return p.authoritySnapshot(ctx)
	}
	facts, err := p.checker.Service.Provider.Resolve(ctx)
	if err != nil {
		return gating.Principal{}, err
	}
	account, err := p.checker.Account(ctx, facts)
	if err != nil {
		return gating.Principal{}, err
	}
	revision, lease, err := p.authorityRevision(ctx, facts, account)
	if err != nil {
		return gating.Principal{}, err
	}
	if lease.Before(facts.ValidUntil) {
		facts.ValidUntil = lease
	}
	return gating.Principal{Facts: facts, AccountID: account, IdentityRevision: revision}, nil
}
func (p *PreparedAuthorization) ResourceActor(ctx context.Context) (identity.VerifiedActor, error) {
	principal, err := p.resourcePrincipal(ctx)
	if err != nil {
		return identity.VerifiedActor{}, err
	}
	actor := identity.VerifiedActor{Subject: principal.Facts.Subject, Issuer: principal.Facts.Issuer, TenantID: principal.Facts.Tenant, AccountID: principal.AccountID, IdentityRevision: principal.IdentityRevision, ValidUntil: principal.Facts.ValidUntil}
	if !actor.Valid(time.Now()) {
		return identity.VerifiedActor{}, policy.ErrIdentityRejected
	}
	return actor, nil
}
