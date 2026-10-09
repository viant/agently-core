package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/policy"
	ui "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	"github.com/viant/authz/oauth"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

type windowActorKey struct{}
type windowFactsKey struct{}
type windowFactState struct {
	principal gating.Principal
	actor     string
	closed    atomic.Bool
}
type windowHTTPTrace struct{ call func(context.Context) }
type slowWindowIdentity struct {
	httpTrace atomic.Pointer[windowHTTPTrace]
	url       string
	client    *http.Client
	calls     atomic.Int64
	revoked   atomic.Bool
	lease     atomic.Int64
	prepared  *PreparedAuthorization
}

func (p *slowWindowIdentity) ResolvePrincipal(ctx context.Context) (gating.Principal, error) {
	actor, _ := ctx.Value(windowActorKey{}).(string)
	if actor == "" {
		return gating.Principal{}, authz.ErrIdentityDenied
	}
	if state, _ := ctx.Value(windowFactsKey{}).(*windowFactState); state != nil {
		if state.closed.Load() || state.actor != actor || !state.principal.Facts.ValidUntil.After(time.Now()) {
			return gating.Principal{}, authz.ErrIdentityDenied
		}
		return state.principal, nil
	}
	if trace := p.httpTrace.Load(); trace != nil {
		trace.call(ctx)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	req.Header.Set("X-Owned-Actor", actor)
	response, err := p.client.Do(req)
	if err != nil {
		return gating.Principal{}, err
	}
	defer response.Body.Close()
	var principal gating.Principal
	if json.NewDecoder(response.Body).Decode(&principal) != nil {
		return principal, authz.ErrUnavailable
	}
	return principal, nil
}
func (p *slowWindowIdentity) Resolve(ctx context.Context) (authz.Facts, error) {
	v, e := p.ResolvePrincipal(ctx)
	return v.Facts, e
}
func (p *slowWindowIdentity) Account(ctx context.Context, facts authz.Facts) (string, error) {
	v, e := p.ResolvePrincipal(ctx)
	return v.AccountID, e
}
func (p *slowWindowIdentity) AuthorityRevision(ctx context.Context, facts authz.Facts, account string) (string, time.Time, error) {
	v, e := p.ResolvePrincipal(ctx)
	return v.IdentityRevision, v.Facts.ValidUntil, e
}
func (p *slowWindowIdentity) BeginDecision(ctx context.Context) (context.Context, func() error, error) {
	if state, _ := ctx.Value(windowFactsKey{}).(*windowFactState); state != nil {
		_, err := p.ResolvePrincipal(ctx)
		return ctx, func() error { _, err := p.ResolvePrincipal(ctx); return err }, err
	}
	principal, err := p.ResolvePrincipal(ctx)
	if err != nil {
		return nil, nil, err
	}
	actor, _ := ctx.Value(windowActorKey{}).(string)
	state := &windowFactState{principal: principal, actor: actor}
	return context.WithValue(ctx, windowFactsKey{}, state), func() error {
		state.closed.Store(true)
		current, err := p.ResolvePrincipal(ctx)
		if err != nil {
			return err
		}
		if !principal.Facts.ValidUntil.After(time.Now()) || !gating.SamePrincipalAuthority(principal, current) {
			return authz.ErrIdentityDenied
		}
		return nil
	}, nil
}
func (p *slowWindowIdentity) WithoutDecision(ctx context.Context) context.Context {
	return context.WithValue(ctx, windowFactsKey{}, (*windowFactState)(nil))
}
func (p *slowWindowIdentity) MetadataReadActive(context.Context) bool { return false }

type windowWireSource struct {
	mu  sync.Mutex
	raw json.RawMessage
}

func (s *windowWireSource) bytes() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(json.RawMessage(nil), s.raw...)
}
func (s *windowWireSource) Candidates(context.Context, identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(s.bytes())}}, nil
}
func (s *windowWireSource) ReadCandidate(context.Context, identity.ResourceURI, identity.ResourceCandidate) (json.RawMessage, error) {
	return s.bytes(), nil
}

type emptyWindowOptions struct{}

func (emptyWindowOptions) Names(context.Context) ([]string, error) { return nil, nil }
func (emptyWindowOptions) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return nil, errors.New("no external providers")
}

type windowWireCatalog struct{ *resources.WindowCatalog }

func (c windowWireCatalog) RevalidateResource(ctx context.Context, key string, pin identity.ResolvedResource) (*identity.ResolvedResource, error) {
	result, err := c.Get(ctx, &ui.WindowDefinitionGetInput{WindowID: key, ResolvedResource: &pin})
	if err != nil {
		return nil, err
	}
	if result == nil || result.Definition == nil {
		return nil, identity.ErrResourceDenied
	}
	return result.Definition.Resource, nil
}

func windowDecisionFixture(t *testing.T, scoped bool) (*slowWindowIdentity, *ui.Service, *windowWireSource, context.Context, func()) {
	t.Helper()
	provider := &slowWindowIdentity{client: &http.Client{Timeout: time.Second}}
	provider.lease.Store(int64(time.Minute))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider.calls.Add(1)
		time.Sleep(3 * time.Millisecond)
		actor := r.Header.Get("X-Owned-Actor")
		roles := []string{"reader"}
		if provider.revoked.Load() {
			roles = nil
		}
		json.NewEncoder(w).Encode(gating.Principal{Facts: authz.Facts{Subject: actor, Issuer: "owned-issuer", Tenant: "owned", Roles: roles, ValidUntil: time.Now().Add(time.Duration(provider.lease.Load()))}, AccountID: "owned-account", IdentityRevision: "owned-identity"})
	}))
	provider.url = server.URL
	const uri = "window://platform/overview"
	resource := authz.Resource{Kind: "window", ID: uri, Tenant: "owned", Version: "working"}
	family := authz.ResourceFamily{Kind: resource.Kind, ID: uri, Tenant: resource.Tenant}
	bundle, err := oauth.NewStaticAuthorization(oauth.StaticAuthorizationConfig{Identity: provider, AllowsTenant: func(s string) bool { return s == "owned" }, Policies: []authz.Document{{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{"describe": {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}, Requirements: []gating.Binding{{Resource: resource, Action: "describe", Document: gating.RequirementsDocument{Revision: "one", Requirements: gating.Requirements{SchemaVersion: 1}}}}})
	if err != nil {
		t.Fatal(err)
	}
	selections, _ := authz.NewStaticSelectionStore([]authz.SelectionDocument{{Resource: family, Revision: 1, DefaultVersion: "working"}})
	prepared, err := PrepareStaticAuthorization(StaticAuthorizationRegistration{DecisionScope: provider, AuthoritySnapshot: provider.ResolvePrincipal, ComponentAuthoritySnapshot: provider.ResolvePrincipal, ProviderRef: "owned", CapabilityMappingRef: "owned", PolicyVersion: "one", Service: bundle.ACL, Account: provider.Account, AuthorityRevision: provider.AuthorityRevision, GateEvaluator: bundle.Gates, ResourceRevisionBindings: []policy.ResourceRevisionBinding{{Operation: policy.OperationWindowView, URI: uri, Resource: family, Action: "describe"}}, ResourceRevisionMappings: selections})
	if err != nil {
		t.Fatal(err)
	}
	provider.prepared = prepared
	variant := types.WindowResourceVariant{Window: &types.Window{WindowKey: "overview", View: types.View{Content: &types.Container{ID: "authored"}}}, DataSources: map[string]json.RawMessage{}}
	fingerprint, _ := types.WindowVariantFingerprint(variant)
	encoded, _ := json.Marshal(types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Targets: []types.WindowTargetBinding{{Target: types.WindowTarget{}, Variant: fingerprint}}, Variants: map[string]types.WindowResourceVariant{fingerprint: variant}})
	source := &windowWireSource{raw: encoded}
	verify := func(ctx context.Context, held identity.VerifiedActor) error {
		current, err := prepared.ResourceActor(ctx)
		if err != nil {
			return err
		}
		if current.Subject != held.Subject || current.IdentityRevision != held.IdentityRevision || !held.Valid(time.Now()) {
			return identity.ErrResourceDenied
		}
		return nil
	}
	local, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: "internal", Validators: map[string]resources.LocalResourceValidator{"window": resources.ValidateWindowBundle}, Actor: prepared.ResourceActor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: []resources.LocalResourceBinding{{URI: uri, FormatVersion: 2, Resolver: func(ctx context.Context, _ identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
		resolver, err := prepared.ResourceResolver(policy.OperationWindowView, source, nil)
		if resolver != nil {
			resolver.ProviderIdentity = "internal"
		}
		return resolver, err
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := manager.New(emptyWindowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = mgr.RegisterLocal(context.Background(), "internal", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "internal"}, func(context.Context) (mcpclient.Interface, error) { return resources.NewLocalMCPClient(local) }); err != nil {
		t.Fatal(err)
	}
	gateway := resources.NewGateway(mgr, prepared.ResourceActor, verify, "host")
	proof, _ := types.NewWindowTargetHMAC(make([]byte, 32))
	remote := &resources.WindowCatalog{ContentCurrent: map[string]resources.WindowContentCheck{"internal": func(ctx context.Context, pin identity.ResolvedResource) error {
		if pin.ProviderIdentity != "internal" || pin.URI != uri {
			return identity.ErrResourceDenied
		}
		parsed, e := identity.ParseResourceURI(uri)
		if e != nil {
			return e
		}
		raw, e := source.ReadCandidate(ctx, parsed, pin.ResourceCandidate)
		if e != nil {
			return e
		}
		if identity.ContentFingerprint(raw) != pin.ContentFingerprint {
			return identity.ErrResourceStale
		}
		return nil
	}}, Gateway: gateway, TargetProof: proof, Admission: func(ctx context.Context, pin identity.ResolvedResource, _ *types.Window) error {
		resolver, e := prepared.ResourceResolver(policy.OperationWindowView, source, nil)
		if resolver != nil {
			resolver.ProviderIdentity = "internal"
		}
		if e != nil {
			return e
		}
		_, _, e = resolver.ReadResolved(ctx, pin)
		return e
	}}
	config := &ui.Config{WindowDefinitions: windowWireCatalog{remote}, ResolvedWindowAuthorizer: func(ctx context.Context, pin identity.ResolvedResource, _ string, _ map[string]any) error { return nil }}
	if scoped {
		config.WindowReadDecisionScope = prepared.provider.WindowReadDecisionScope
	}
	svc := ui.NewService(config)
	ctx := context.WithValue(context.Background(), windowActorKey{}, "alice")
	resolver0, _ := prepared.ResourceResolver(policy.OperationWindowView, source, nil)
	_, policyErr := resolver0.Resolve(ctx, identity.ResourceRef{URI: uri})
	if policyErr != nil {
		t.Fatalf("fixture policy direct: %v", policyErr)
	}
	direct, directErr := remote.Get(ctx, &ui.WindowDefinitionGetInput{WindowID: uri})
	if directErr != nil {
		t.Fatalf("direct actualGateway Get: %v", directErr)
	}
	_, exactErr := remote.Get(ctx, &ui.WindowDefinitionGetInput{WindowID: uri, ResolvedResource: direct.Definition.Resource, Target: direct.Definition.ResourceTarget})
	if exactErr != nil {
		t.Fatalf("exact originalpin get: %v", exactErr)
	}
	provider.calls.Store(0)
	return provider, svc, source, ctx, func() { server.Close() }
}

func runWindowDecisionOpen(t *testing.T, svc *ui.Service, ctx context.Context, onCommand func()) error {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		svc.Hub().ServeWS(w, r.WithContext(context.WithValue(r.Context(), windowActorKey{}, ctx.Value(windowActorKey{}))))
	}))
	defer server.Close()
	socket, _, err := websocket.DefaultDialer.Dial("ws"+server.URL[4:], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	actor, _ := ctx.Value(windowActorKey{}).(string)
	clientID := "owned-client-" + actor
	socket.WriteJSON(map[string]any{"type": "ui.hello", "clientId": clientID})
	for deadline := time.Now().Add(time.Second); len(svc.Hub().ListClients("default")) == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	worker := make(chan error, 1)
	go func() {
		var command struct {
			ID string `json:"id"`
		}
		socket.SetReadDeadline(time.Now().Add(20 * time.Second))
		if e := socket.ReadJSON(&command); e != nil {
			worker <- e
			return
		}
		if onCommand != nil {
			onCommand()
		}
		worker <- socket.WriteJSON(map[string]any{"type": "ui.response", "id": command.ID, "ok": true, "result": map[string]any{"windowId": "owned-window"}})
	}()
	_, err = svc.UICommand(ctx, &ui.UICommandInput{ClientID: clientID, Method: "ui.window.open", Params: map[string]any{"windowKey": "window://platform/overview", "resource": identity.ResourceRef{URI: "window://platform/overview"}}, TimeoutMs: 20000})
	if err == nil {
		if e := <-worker; e != nil {
			t.Fatal(e)
		}
	}
	return err
}

func TestWindowPureScopeActualLocalGatewayReducesSlowIdentityReads(t *testing.T) {
	var baseline, batched int64
	for _, scoped := range []bool{false, true} {
		provider, svc, _, ctx, close := windowDecisionFixture(t, scoped)
		started := time.Now()
		err := runWindowDecisionOpen(t, svc, ctx, nil)
		elapsed := time.Since(started)
		calls := provider.calls.Load()
		close()
		if err != nil {
			t.Fatalf("scoped=%v calls=%d: %v", scoped, calls, err)
		}
		t.Logf("scoped=%v sourceHTTPCalls=%d elapsed=%s", scoped, calls, elapsed)
		if scoped {
			batched = calls
		} else {
			baseline = calls
		}
	}
	if batched > 6 || baseline <= batched*3 {
		t.Fatalf("scope did not bound source reads baseline=%d scoped=%d", baseline, batched)
	}
}

func TestWindowPureScopeRevocationBeforeQueueAndAfterACK(t *testing.T) {
	for _, phase := range []string{"before-queue", "after-ack"} {
		t.Run(phase, func(t *testing.T) {
			provider, svc, _, ctx, close := windowDecisionFixture(t, true)
			defer close()
			queued := atomic.Bool{}
			if phase == "before-queue" {
				begin := svcBeginFixture(provider)
				svc.ConfigureWindowReadDecisionScope(func(ctx context.Context) (context.Context, func() error, error) {
					scoped, finish, err := begin(ctx)
					if err != nil {
						return nil, nil, err
					}
					return scoped, func() error { provider.revoked.Store(true); return finish() }, nil
				})
			}
			err := runWindowDecisionOpen(t, svc, ctx, func() {
				queued.Store(true)
				if phase == "after-ack" {
					provider.revoked.Store(true)
				}
			})
			if err == nil {
				t.Fatal("revoked operation released success")
			}
			if phase == "before-queue" && queued.Load() {
				t.Fatal("pre-scope finish denial queued UI content")
			}
			if phase == "after-ack" && !queued.Load() {
				t.Fatal("post-ACK revocation was not exercised")
			}
		})
	}
}
func svcBeginFixture(provider *slowWindowIdentity) ui.WindowReadDecisionScope {
	return func(ctx context.Context) (context.Context, func() error, error) {
		return provider.BeginDecision(provider.WithoutDecision(ctx))
	}
}
func TestWindowPureScopeOriginalExpiryAndContentDriftAfterACK(t *testing.T) {
	for _, cause := range []string{"expired-original", "changed-content"} {
		t.Run(cause, func(t *testing.T) {
			provider, svc, source, ctx, close := windowDecisionFixture(t, true)
			defer close()
			if cause == "expired-original" {
				provider.lease.Store(int64(80 * time.Millisecond))
			}
			queued := atomic.Bool{}
			err := runWindowDecisionOpen(t, svc, ctx, func() {
				queued.Store(true)
				if cause == "expired-original" {
					time.Sleep(120 * time.Millisecond)
				} else {
					source.mu.Lock()
					source.raw = append(append(json.RawMessage(nil), source.raw...), []byte(" ")...)
					source.mu.Unlock()
				}
			})
			if !queued.Load() {
				t.Fatal("original pin expiry/drift while awaiting ACK was not exercised")
			}
			if err == nil {
				t.Fatal("stale or expired original pin released output")
			}
		})
	}
}
func TestWindowPureScopeTwoActorsNeverShareAuthorityState(t *testing.T) {
	provider, svc, _, ctx, close := windowDecisionFixture(t, true)
	defer close()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, actor := range []string{"alice", "bob"} {
		wg.Add(1)
		go func(actor string) {
			defer wg.Done()
			actorCtx := context.WithValue(ctx, windowActorKey{}, actor)
			errs <- runWindowDecisionOpen(t, svc, actorCtx, nil)
		}(actor)
	}
	wg.Wait()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if provider.calls.Load() < 8 {
		t.Fatal("actors did not receive independent before/after source reads")
	}
}
func TestWindowPureMarkerRemovedFromExecutionAndComponentAuthority(t *testing.T) {
	provider, _, _, ctx, close := windowDecisionFixture(t, true)
	defer close()
	marked, clear := requestctx.WithWindowReadDecision(ctx)
	defer clear()
	scoped, finish, err := provider.BeginDecision(marked)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	cleared := provider.prepared.provider.ExecutionContext(scoped)
	if requestctx.WindowReadDecisionActive(cleared) {
		t.Fatal("execution marker retained")
	}
	if state, _ := cleared.Value(windowFactsKey{}).(*windowFactState); state != nil {
		t.Fatal("execution provider snapshot retained")
	}
	before := provider.calls.Load()
	if _, err := provider.prepared.provider.ComponentAuthoritySnapshot(scoped); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != before+1 {
		t.Fatal("component reused pure authority facts")
	}
}
