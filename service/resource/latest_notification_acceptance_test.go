package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	producer "github.com/viant/agently-core/service/resource/provider"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

func TestDefaultSDKManagerReceivesAdvertisedResourceChangesAfterFirstRequest(t *testing.T) {
	ctx := context.Background()
	root := configuredSnapshotRoot(t)
	definition := configuredSnapshotDefinition()
	snapshot, err := NewNativeAssetSnapshot(ctx, root, []NativeSnapshotDefinition{definition})
	require.NoError(t, err)
	actor, verify := PublicReadActors()
	bindings, err := snapshot.Bindings("latest-source", func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return authoredTestPolicy{a}, nil
	})
	require.NoError(t, err)
	uri, err := identity.ParseResourceURI(definition.URI)
	require.NoError(t, err)
	p, err := NewLocalProvider(LocalConfig{ProviderIdentity: "latest-source", Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: bindings, Validators: map[string]LocalResourceValidator{"window": ValidateWindowBundle}, NativeResources: []producer.NativeResource{{Metadata: schema.Resource{Uri: definition.URI, Name: "overview"}, Read: func(ctx context.Context) ([]byte, error) {
		candidates, e := snapshot.Candidates(ctx, uri)
		if e != nil {
			return nil, e
		}
		return snapshot.ReadCandidate(ctx, uri, candidates[0])
	}}}})
	require.NoError(t, err)
	var latest, listen, active atomic.Int32
	listenStops := make(chan context.CancelFunc, 4)
	handler, err := NewMCPHandler(p, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(schema.HeaderProtocolVersion) == schema.LatestProtocolVersion {
				latest.Add(1)
			}
			if r.Header.Get(schema.HeaderMethod) == schema.MethodSubscriptionsListen {
				listen.Add(1)
				listenCtx, stopListen := context.WithCancel(r.Context())
				defer stopListen()
				listenStops <- stopListen
				r = r.WithContext(listenCtx)
				active.Add(1)
				defer active.Add(-1)
			}
			next.ServeHTTP(w, r)
		})
	})
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	defer server.Close()
	mgr, err := manager.New(fixtureOptions{"latest": {ClientOptions: &mcp.ClientOptions{Name: "default-event-test", Version: "1", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL + "/mcp"}}}, PrimitiveProviderIdentity: "latest-source", ResourceCacheScopes: map[string]string{"studio": "public"}}})
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	g := NewGateway(mgr, actor, verify, "consumer")
	defer g.Close()
	requestCtx, cancelRequest := context.WithCancel(ctx)
	first, err := ListPage[json.RawMessage](requestCtx, g, "window", CatalogQuery{Namespace: "studio", Limit: 10}, true)
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	cancelRequest() // stream lifetime belongs to the managed client, not this request
	require.Greater(t, latest.Load(), int32(0), "default SDK negotiates current protocol")
	client, err := mgr.Get(ctx, "", "latest")
	require.NoError(t, err)
	discovery, err := client.(mcpclient.DiscoveryInterface).Discover(ctx)
	require.NoError(t, err)
	require.NotNil(t, discovery.Capabilities.Resources)
	require.NotNil(t, discovery.Capabilities.Resources.ListChanged)
	require.True(t, *discovery.Capabilities.Resources.ListChanged, "owning server advertises standard change support")
	var events atomic.Int32
	stop := mgr.OnResourceCatalogChange(func(string) { events.Add(1) })
	defer stop()
	file := filepath.Join(root, "window.json")
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, bytes.Replace(raw, []byte("Original"), []byte("Updated"), 1), 0600))
	require.NoError(t, snapshot.Reload(ctx))
	require.Eventually(t, func() bool { return events.Load() > 0 }, time.Second, time.Millisecond, "negotiated %s: callbacks=%d subscriptions/listen=%d", schema.LatestProtocolVersion, events.Load(), listen.Load())
	current, err := ListPage[json.RawMessage](ctx, g, "window", CatalogQuery{Namespace: "studio", Limit: 10}, true)
	require.NoError(t, err)
	require.Contains(t, string(current.Items[0].Definition), "Updated")
	require.Equal(t, int32(1), active.Load())
	// Failed source publication preserves cached bytes and emits no event.
	priorEvents := events.Load()
	require.NoError(t, os.WriteFile(file, []byte("invalid definition"), 0600))
	require.Error(t, snapshot.Reload(ctx))
	require.Equal(t, priorEvents, events.Load())
	cached, err := ListPage[json.RawMessage](ctx, g, "window", CatalogQuery{Namespace: "studio", Limit: 10}, true)
	require.NoError(t, err)
	require.Equal(t, current.Items[0].Definition, cached.Items[0].Definition)
	mgr.CloseConversation("")
	require.Eventually(t, func() bool { return active.Load() == 0 }, time.Second, time.Millisecond)
	// A fresh connection establishes its own subscription; the old one remains closed.
	_, err = ListPage[json.RawMessage](ctx, g, "window", CatalogQuery{Namespace: "studio", Limit: 10}, true)
	require.NoError(t, err)
	require.Equal(t, int32(2), listen.Load())
	require.Equal(t, int32(1), active.Load())
	require.NoError(t, os.WriteFile(file, bytes.Replace(raw, []byte("Original"), []byte("Reconnected"), 1), 0600))
	require.NoError(t, snapshot.Reload(ctx))
	require.Eventually(t, func() bool { return events.Load() > priorEvents }, time.Second, time.Millisecond)
	reconnected, err := ListPage[json.RawMessage](ctx, g, "window", CatalogQuery{Namespace: "studio", Limit: 10}, true)
	require.NoError(t, err)
	require.Contains(t, string(reconnected.Items[0].Definition), "Reconnected")
	<-listenStops // closed original stream
	stopCurrent := <-listenStops
	beforeLoss := events.Load()
	stopCurrent()
	require.Eventually(t, func() bool { return active.Load() == 0 && events.Load() > beforeLoss }, time.Second, time.Millisecond)
	_, err = ListPage[json.RawMessage](ctx, g, "window", CatalogQuery{Namespace: "studio", Limit: 10}, true)
	require.NoError(t, err)
	require.Equal(t, int32(3), listen.Load(), "lost stream retires connection; next explicit read reconnects")

}

// The owning inventory publishes one coarse standard change after each committed
// edit. Added/removed identities are derived from the refreshed authorized list.
type latestEventCatalog struct {
	*primitiveBenchMutableCatalog
	producer.ResourceChanges
}

func TestDefaultSDKResourceChangesRefreshAddedRemovedAndCachedGet(t *testing.T) {
	ctx := context.Background()
	owner := "mutable-source"
	catalog := &latestEventCatalog{primitiveBenchMutableCatalog: &primitiveBenchMutableCatalog{owner: owner, kind: "prompt", items: map[string]producer.LocalResourceBinding{}, raw: map[string]json.RawMessage{}}}
	catalog.set("first", "original")
	actor, verify := PublicReadActors()
	p, err := NewLocalProvider(LocalConfig{ProviderIdentity: owner, Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Catalog: catalog, Validators: map[string]LocalResourceValidator{"prompt": func(int64, json.RawMessage) error { return nil }}, NativeResources: []producer.NativeResource{{Metadata: schema.Resource{Uri: "prompt://bench/first", Name: "first"}, Read: func(context.Context) ([]byte, error) { return []byte(`{"text":"original"}`), nil }}}})
	require.NoError(t, err)
	handler, err := NewMCPHandler(p, func(next http.Handler) http.Handler { return next })
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	defer server.Close()
	mgr, err := manager.New(fixtureOptions{"mutable": {ClientOptions: &mcp.ClientOptions{Name: "mutable-event-test", Version: "1", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL + "/mcp"}}}, PrimitiveProviderIdentity: owner, ResourceCacheScopes: map[string]string{"bench": "public"}}})
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	g := NewGateway(mgr, actor, verify, "consumer")
	defer g.Close()
	query := CatalogQuery{Namespace: "bench", Limit: 10}
	first, err := ListPage[json.RawMessage](ctx, g, "prompt", query, true)
	require.NoError(t, err)
	require.Equal(t, []string{"first"}, primitiveBenchIDs(first))
	uri, err := identity.ParseResourceURI("prompt://bench/first")
	require.NoError(t, err)
	old, err := Get[json.RawMessage](ctx, g, first.Items[0].Connection, identity.ResourceRef{URI: uri.String()}, nil)
	require.NoError(t, err)
	var events atomic.Int32
	stop := mgr.OnResourceCatalogChange(func(string) { events.Add(1) })
	defer stop()
	publish := func() {
		t.Helper()
		before := events.Load()
		catalog.Publish(ctx)
		require.Eventually(t, func() bool { return events.Load() > before }, time.Second, time.Millisecond)
	}
	catalog.set("first", "updated")
	catalog.set("second", "added")
	publish()
	current, err := ListPage[json.RawMessage](ctx, g, "prompt", query, true)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"first", "second"}, primitiveBenchIDs(current))
	updated, err := Get[json.RawMessage](ctx, g, first.Items[0].Connection, identity.ResourceRef{URI: uri.String()}, nil)
	require.NoError(t, err)
	require.NotEqual(t, old.Definition, updated.Definition)
	require.Contains(t, string(updated.Definition), "updated")
	catalog.remove("first")
	publish()
	remaining, err := ListPage[json.RawMessage](ctx, g, "prompt", query, true)
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, primitiveBenchIDs(remaining))
	_, err = Get[json.RawMessage](ctx, g, first.Items[0].Connection, identity.ResourceRef{URI: uri.String()}, nil)
	require.Error(t, err, "removed definition cannot survive the owning notification")
}

func TestDefaultSDKEventlessPrimitiveServerRemainsUsableWithoutListen(t *testing.T) {
	ctx := context.Background()
	catalog := &primitiveBenchMutableCatalog{owner: "eventless", kind: "prompt", items: map[string]producer.LocalResourceBinding{}, raw: map[string]json.RawMessage{}}
	catalog.set("one", "eventless definition")
	actor, verify := PublicReadActors()
	p, err := NewLocalProvider(LocalConfig{ProviderIdentity: "eventless", Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Catalog: catalog, Validators: map[string]LocalResourceValidator{"prompt": func(int64, json.RawMessage) error { return nil }}})
	require.NoError(t, err)
	var listens atomic.Int32
	handler, err := NewMCPHandler(p, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(schema.HeaderMethod) == schema.MethodSubscriptionsListen {
				listens.Add(1)
			}
			next.ServeHTTP(w, r)
		})
	})
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	defer server.Close()
	mgr, err := manager.New(fixtureOptions{"eventless": {ClientOptions: &mcp.ClientOptions{Name: "eventless-test", Version: "1", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL + "/mcp"}}}, PrimitiveProviderIdentity: "eventless"}})
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	g := NewGateway(mgr, actor, verify, "consumer")
	defer g.Close()
	page, err := ListPage[json.RawMessage](ctx, g, "prompt", CatalogQuery{Namespace: "bench", Limit: 10}, true)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, int32(0), listens.Load(), "optional event support does not force subscriptions on generic servers")
}
