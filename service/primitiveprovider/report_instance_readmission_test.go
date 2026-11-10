package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/reporting/catalog"
	engine "github.com/viant/forge/backend/reporting/engine"
)

type reportInstancePrincipal struct{ owner, revision string }
type reportInstancePrincipalKey struct{}
type reportInstanceCatalog struct {
	mu               sync.Mutex
	selectionBarrier func()

	pin                               identity.ResolvedResource
	lists, selects, reads             int
	denied                            bool
	changedProvider, changedCandidate bool
}

func (c *reportInstanceCatalog) MetadataRead(ctx context.Context) (context.Context, func() error, error) {
	return ctx, func() error { return ctx.Err() }, nil
}

func (c *reportInstanceCatalog) CurrentActor(ctx context.Context) (identity.VerifiedActor, error) {
	p := ctx.Value(reportInstancePrincipalKey{}).(reportInstancePrincipal)
	return identity.VerifiedActor{Subject: p.owner, Issuer: "verified", TenantID: "example", AccountID: "account", IdentityRevision: p.revision, ValidUntil: time.Now().Add(time.Hour)}, nil
}

func (c *reportInstanceCatalog) List(context.Context, catalog.ReportCatalogQuery) (*catalog.ReportCatalogResult, error) {
	c.mu.Lock()
	c.lists++
	c.mu.Unlock()
	return nil, fmt.Errorf("catalog enumeration is forbidden")
}
func (c *reportInstanceCatalog) Read(ctx context.Context, _ identity.ResourceRef, pin *identity.ResolvedResource) ([]byte, *identity.ResolvedResource, error) {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	fresh, err := c.current(ctx)
	if err != nil {
		return nil, nil, err
	}
	if pin != nil && !identity.SameResolvedResource(*pin, *fresh) {
		return nil, nil, identity.ErrResourceDenied
	}
	return []byte(`{}`), fresh, nil
}
func (c *reportInstanceCatalog) Reader(context.Context, string) (engine.ResourceReader, error) {
	return reportInstanceReader{c}, nil
}
func (c *reportInstanceCatalog) current(ctx context.Context) (*identity.ResolvedResource, error) {
	if c.denied {
		return nil, identity.ErrResourceDenied
	}
	p := c.pin
	p.AuthorityBinding = ctx.Value(reportInstancePrincipalKey{}).(reportInstancePrincipal).revision
	if c.changedProvider {
		p.ProviderIdentity = "sibling-provider"
	}
	if c.changedCandidate {
		p.ContentFingerprint = "different"
	}
	return &p, nil
}

type reportInstanceReader struct{ catalog *reportInstanceCatalog }

func (r reportInstanceReader) Resolve(context.Context, identity.ResourceRef) (*identity.ResolvedResource, error) {
	return nil, fmt.Errorf("unscoped resolution is forbidden")
}
func (r reportInstanceReader) ReadResolved(ctx context.Context, pin identity.ResolvedResource) (json.RawMessage, *identity.ResolvedResource, error) {
	raw, fresh, err := r.catalog.Read(ctx, identity.ResourceRef{URI: pin.URI}, &pin)
	return raw, fresh, err
}
func (r reportInstanceReader) ResolveForProvider(ctx context.Context, ref identity.ResourceRef, provider string) (*identity.ResolvedResource, error) {
	r.catalog.mu.Lock()
	r.catalog.selects++
	r.catalog.mu.Unlock()
	if r.catalog.selectionBarrier != nil {
		r.catalog.selectionBarrier()
	}
	if provider != r.catalog.pin.ProviderIdentity || ref.URI != r.catalog.pin.URI || ref.Revision != r.catalog.pin.Selector() {
		return nil, identity.ErrResourceDenied
	}
	return r.catalog.current(ctx)
}

type reportInstanceWindows struct{ WindowDefinitionCatalog }

func (*reportInstanceWindows) UsesResourceResolution() bool { return true }

func TestReportInstanceRevalidationUsesCurrentOwnerAndOriginalProvider(t *testing.T) {
	pin := identity.ResolvedResource{URI: "report://example/summary", ProviderIdentity: "original-provider", AuthorityBinding: "browser-revision", ResourceCandidate: identity.ResourceCandidate{Kind: "published", ContentFingerprint: identity.ContentFingerprint([]byte(`{}`))}}
	catalog := &reportInstanceCatalog{pin: pin}
	svc := NewService(&Config{WindowDefinitions: &reportInstanceWindows{}, ReportCatalog: catalog, NamespaceResolver: func(ctx context.Context) (NamespaceIdentity, error) {
		p, ok := ctx.Value(reportInstancePrincipalKey{}).(reportInstancePrincipal)
		if !ok {
			return NamespaceIdentity{}, identity.ErrResourceDenied
		}
		return NamespaceIdentity{Namespace: p.owner, ValidUntil: time.Now().Add(time.Hour)}, nil
	}})
	key := windowPinKey{"verified-owner", "client", "persisted-report"}
	browser := context.WithValue(context.Background(), reportInstancePrincipalKey{}, reportInstancePrincipal{key.Namespace, "browser-revision"})
	cli := context.WithValue(context.Background(), reportInstancePrincipalKey{}, reportInstancePrincipal{key.Namespace, "cli-revision"})
	require.NoError(t, svc.rememberWindowPin(browser, key, windowResourcePin{WindowKey: pin.URI, Resource: pin}))
	raw := json.RawMessage(`{"conversationId":"chat","windows":[{"windowId":"persisted-report","windowKey":"report://example/summary","conversationId":"chat","viewState":{"report":{"status":"ready"}}}]}`)
	// Strict callers cannot reuse the browser's different credential authority.
	_, err := svc.ReportDefinition(cli, &ReportDefinitionInput{Resource: identity.ResourceRef{URI: pin.URI}, ResolvedResource: &pin})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	readsBefore := catalog.reads
	filtered, err := svc.FilterResourceSnapshot(cli, key.Namespace, key.ClientID, raw)
	require.NoError(t, err)
	require.Contains(t, string(filtered), `"persisted-report"`)
	require.Contains(t, string(filtered), `"status":"ready"`)
	require.Equal(t, 1, catalog.selects)
	require.Equal(t, readsBefore, catalog.reads, "a retained read makes one provider-scoped selection, without a second definition read")
	held, ok := svc.getWindowPin(key)
	require.True(t, ok)
	require.Equal(t, "cli-revision", held.Resource.AuthorityBinding)
	// Every native consumer shares the same current server pin.
	current, err := svc.WindowResource(cli, key.Namespace, key.ClientID, key.WindowID, pin.URI)
	require.NoError(t, err)
	require.Equal(t, "cli-revision", current.AuthorityBinding)
	command, err := svc.authorizePinnedCommand(cli, key.Namespace, key.ClientID, "ui.control.export", map[string]any{"windowId": key.WindowID})
	require.NoError(t, err)
	require.Equal(t, "cli-revision", command.Resource.AuthorityBinding)
	// Browser input remains a locator to the exact candidate and original grant.
	require.NotNil(t, held.PresentedResource)
	require.Equal(t, pin, *held.PresentedResource)
	for _, failure := range []string{"sibling-owner", "revoked", "provider", "candidate"} {
		t.Run(failure, func(t *testing.T) {
			testCtx := cli
			catalog.denied = false
			catalog.changedProvider = false
			catalog.changedCandidate = false
			switch failure {
			case "sibling-owner":
				testCtx = context.WithValue(context.Background(), reportInstancePrincipalKey{}, reportInstancePrincipal{"another-owner", "cli-revision"})
			case "revoked":
				catalog.denied = true
			case "provider":
				catalog.changedProvider = true
			case "candidate":
				catalog.changedCandidate = true
			}
			visible, err := svc.FilterResourceSnapshot(testCtx, key.Namespace, key.ClientID, raw)
			require.NoError(t, err)
			require.NotContains(t, string(visible), `"persisted-report"`)
			_, err = svc.WindowResource(testCtx, key.Namespace, key.ClientID, key.WindowID, pin.URI)
			require.Error(t, err)
			_, err = svc.authorizePinnedCommand(testCtx, key.Namespace, key.ClientID, "ui.control.export", map[string]any{"windowId": key.WindowID})
			require.Error(t, err)
		})
	}
	require.Zero(t, catalog.lists)
}

func TestReportInstanceConcurrentOwnerReadmissionsKeepOperationAuthority(t *testing.T) {
	pin := identity.ResolvedResource{URI: "report://example/summary", ProviderIdentity: "original-provider", AuthorityBinding: "browser-revision", ResourceCandidate: identity.ResourceCandidate{Kind: "published", ContentFingerprint: identity.ContentFingerprint([]byte(`{}`))}}
	catalog := &reportInstanceCatalog{pin: pin}
	svc := NewService(&Config{WindowDefinitions: &reportInstanceWindows{}, ReportCatalog: catalog, NamespaceResolver: func(ctx context.Context) (NamespaceIdentity, error) {
		p := ctx.Value(reportInstancePrincipalKey{}).(reportInstancePrincipal)
		return NamespaceIdentity{Namespace: p.owner, ValidUntil: time.Now().Add(time.Hour)}, nil
	}})
	key := windowPinKey{"verified-owner", "client", "persisted-report"}
	browser := context.WithValue(context.Background(), reportInstancePrincipalKey{}, reportInstancePrincipal{key.Namespace, "browser-revision"})
	require.NoError(t, svc.rememberWindowPin(browser, key, windowResourcePin{WindowKey: pin.URI, Resource: pin}))
	var selected sync.WaitGroup
	selected.Add(2)
	catalog.selectionBarrier = func() { selected.Done(); selected.Wait() }
	type result struct {
		resource *identity.ResolvedResource
		err      error
		revision string
	}
	completed := make(chan result, 2)
	for _, revision := range []string{"credential-one", "credential-two"} {
		go func(revision string) {
			ctx := context.WithValue(context.Background(), reportInstancePrincipalKey{}, reportInstancePrincipal{key.Namespace, revision})
			resource, err := svc.WindowResource(ctx, key.Namespace, key.ClientID, key.WindowID, pin.URI)
			completed <- result{resource, err, revision}
		}(revision)
	}
	for i := 0; i < 2; i++ {
		r := <-completed
		require.NoError(t, r.err)
		require.NotNil(t, r.resource)
		require.Equal(t, r.revision, r.resource.AuthorityBinding)
	}
	held, ok := svc.getWindowPin(key)
	require.True(t, ok)
	require.Contains(t, []string{"credential-one", "credential-two"}, held.Resource.AuthorityBinding)
	// A candidate replacement while admission is in flight cannot be borrowed by
	// the fresh operation, nor overwritten by its earlier selection.
	catalog.selectionBarrier = func() {
		svc.resourceMu.Lock()
		changed := svc.resourcePins[key]
		changed.Resource.ContentFingerprint = "concurrently-replaced"
		svc.resourcePins[key] = changed
		svc.resourceMu.Unlock()
	}
	ctx := context.WithValue(context.Background(), reportInstancePrincipalKey{}, reportInstancePrincipal{key.Namespace, "credential-three"})
	_, err := svc.WindowResource(ctx, key.Namespace, key.ClientID, key.WindowID, pin.URI)
	require.Error(t, err)
	held, _ = svc.getWindowPin(key)
	require.Equal(t, "concurrently-replaced", held.Resource.ContentFingerprint)
}
