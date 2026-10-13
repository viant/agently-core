package resource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	producer "github.com/viant/agently-core/service/resource/provider"
	"github.com/viant/mcp"
)

type resourceEventWriter struct {
	primitive.ResourceAuthoring
	catalog  *primitiveBenchMutableCatalog
	bus      *primitiveBenchEventBus
	writes   atomic.Int64
	received *atomic.Int64
}

func (w *resourceEventWriter) GetResource(_ context.Context, uri string) (*primitive.ResourceDescriptor, error) {
	return &primitive.ResourceDescriptor{ResourceResult: primitive.ResourceResult{URI: uri}}, nil
}

func (w *resourceEventWriter) CreateResource(ctx context.Context, in primitive.CreateResourceInput) (*primitive.ResourceResult, error) {
	uri, err := identity.ParseResourceURI(in.URI)
	if err != nil {
		return nil, err
	}
	w.catalog.set(uri.Name, "saved")
	w.writes.Add(1)
	before := w.received.Load()
	if err = w.bus.emit("notifications/resources/list_changed"); err != nil {
		return nil, err
	}
	// Make the regression deterministic: resource invalidation completes before
	// the successful owner response returns to Invoke's terminal binding check.
	deadline := time.Now().Add(3 * time.Second)
	for w.received.Load() == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if w.received.Load() == before {
		return nil, context.DeadlineExceeded
	}
	return &primitive.ResourceResult{URI: uri.String()}, nil
}
func TestRealMCPResourceNotificationRefreshesNamespacesAndPreservesSuccessfulWrite(t *testing.T) {
	actor := identity.VerifiedActor{Subject: "alice", Issuer: "issuer", TenantID: "tenant", AccountID: "account", IdentityRevision: "1", ValidUntil: time.Now().Add(time.Minute)}
	catalog := &primitiveBenchMutableCatalog{owner: "owner", kind: "prompt", operations: []string{"list", "get", "create"}, items: map[string]producer.LocalResourceBinding{}, raw: map[string]json.RawMessage{}}
	catalog.set("first", "original")
	catalog.set("second", "original")
	bus := &primitiveBenchEventBus{}
	var received atomic.Int64
	writer := &resourceEventWriter{catalog: catalog, bus: bus, received: &received}
	provider, err := producer.NewLocalProvider(producer.LocalConfig{ProviderIdentity: "owner", Catalog: catalog, Authoring: writer, Actor: func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, Verify: func(context.Context, identity.VerifiedActor) error { return nil }, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Validators: map[string]producer.LocalResourceValidator{"prompt": primitiveBenchJSON}})
	require.NoError(t, err)
	var added atomic.Bool
	bus.callOverride = func(ctx context.Context, method string, raw json.RawMessage) (any, bool, error) {
		if !added.Load() {
			return nil, false, nil
		}
		switch method {
		case "namespaces/list":
			value, err := provider.NamespaceList(ctx, primitive.NamespaceListRequest{})
			if err != nil {
				return nil, true, err
			}
			value.Namespaces = append(value.Namespaces, primitive.Namespace{Name: "newnamespace", Kinds: []string{"prompt"}})
			return value, true, nil
		case "namespaces/get":
			var request primitive.NamespaceGetRequest
			_ = json.Unmarshal(raw, &request)
			if request.Namespace == "newnamespace" {
				value, err := provider.NamespaceGet(ctx, primitive.NamespaceGetRequest{Namespace: "bench"})
				value.Namespace = "newnamespace"
				return value, true, err
			}
		}
		return nil, false, nil
	}
	handler, err := primitiveBenchSDKHandler(t, provider, "owner", func(next http.Handler) http.Handler { return next }, bus)
	require.NoError(t, err)
	endpoint := httptest.NewServer(handler)
	defer endpoint.Close()
	options := fixtureOptions{"remote": &cfg.MCPClient{PrimitiveProviderIdentity: "owner", ClientOptions: &mcp.ClientOptions{Name: "resource-events", Version: "1", ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: endpoint.URL + "/mcp"}}}}}
	mgr, err := manager.New(options)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	stop := mgr.OnResourceCatalogChange(func(string) { received.Add(1) })
	defer stop()
	gateway := NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(context.Context, identity.VerifiedActor) error { return nil }, "consumer")
	defer gateway.Close()
	ctx := authctx.WithCanonicalUserID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "alice")
	before, err := gateway.Discover(ctx)
	require.NoError(t, err)
	require.Len(t, before, 1)
	initialConnection := before[0].Connection
	initialToolGeneration := mgr.ToolCatalogGeneration("remote")
	added.Store(true)
	require.NoError(t, bus.emit("notifications/resources/list_changed"))
	primitiveBenchWait(t, func() bool { return received.Load() > 0 })
	after, err := gateway.Discover(ctx)
	require.NoError(t, err)
	require.Len(t, after, 2, "new namespace must be discovered despite unchanged tool schemas and transport")
	require.Equal(t, initialConnection, after[0].Connection)
	require.Equal(t, initialToolGeneration, mgr.ToolCatalogGeneration("remote"))
	// The advertised extra namespace is only metadata. Restore the provider's
	// real catalog before the authoring lifecycle below.
	added.Store(false)
	require.NoError(t, bus.emit("notifications/resources/list_changed"))
	primitiveBenchWait(t, func() bool { return received.Load() > 1 })
	warm, err := ListPage[json.RawMessage](ctx, gateway, "prompt", CatalogQuery{Namespace: "bench", Limit: 1}, true)
	require.NoError(t, err)
	uri, err := identity.ParseResourceURI("prompt://bench/saved")
	require.NoError(t, err)
	sequenceCtx, finish, err := gateway.BeginReadSequence(ctx, nil)
	require.NoError(t, err)
	_, err = Get[json.RawMessage](sequenceCtx, gateway, initialConnection, identity.ResourceRef{URI: "prompt://bench/first"}, nil)
	require.NoError(t, err)
	saved, err := Invoke[*primitive.ResourceResult](sequenceCtx, gateway, initialConnection, uri, "create", primitive.CreateResourceInput{URI: uri.String(), FormatVersion: 1, Definition: json.RawMessage(`{"text":"saved"}`)})
	require.NoError(t, err, "owning write must not fail because its resource notification arrived before the successful response")
	require.Equal(t, uri.String(), saved.URI)
	require.EqualValues(t, 1, writer.writes.Load())
	_, err = Get[json.RawMessage](sequenceCtx, gateway, initialConnection, identity.ResourceRef{URI: uri.String()}, nil)
	require.NoError(t, err, "resource notification refreshes metadata within the phase without becoming a client policy denial")
	require.NoError(t, finish())
	_, err = ListPage[json.RawMessage](ctx, gateway, "prompt", CatalogQuery{Namespace: "bench", Limit: 1, Cursor: warm.NextCursor}, true)
	require.Error(t, err)
	refreshed, err := ListPage[json.RawMessage](ctx, gateway, "prompt", CatalogQuery{Namespace: "bench", Limit: 100}, true)
	require.NoError(t, err)
	require.Contains(t, primitiveBenchIDs(refreshed), "saved")
	require.Equal(t, initialToolGeneration, mgr.ToolCatalogGeneration("remote"))
}
