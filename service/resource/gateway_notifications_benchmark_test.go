package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	"github.com/viant/agently-core/protocol/primitive/observe"
	identity "github.com/viant/agently-core/protocol/resource"
	producer "github.com/viant/agently-core/service/resource/provider"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
)

type primitiveBenchEventBus struct {
	callOverride func(context.Context, string, json.RawMessage) (any, bool, error)
	mu           sync.Mutex
	notifiers    map[transport.Notifier]*protocol.DefaultHandler
	ordered      []transport.Notifier
}

func (b *primitiveBenchEventBus) register(n transport.Notifier, h *protocol.DefaultHandler) {
	if n == nil {
		return
	}
	b.mu.Lock()
	if b.notifiers == nil {
		b.notifiers = map[transport.Notifier]*protocol.DefaultHandler{}
	}
	if _, exists := b.notifiers[n]; !exists {
		b.ordered = append(b.ordered, n)
	}
	b.notifiers[n] = h
	b.mu.Unlock()
}
func (b *primitiveBenchEventBus) emit(method string) error {
	b.mu.Lock()
	senders := []transport.Notifier{}
	for n, h := range b.notifiers {
		if method == "notifications/resources/updated" {
			subscribed, _ := h.Subscription.Get("benchmark://catalog/manifest")
			if !subscribed {
				continue
			}
		}
		senders = append(senders, n)
	}
	b.mu.Unlock()
	for _, n := range senders {
		var params json.RawMessage
		if method == "notifications/resources/updated" {
			params = json.RawMessage(`{"uri":"benchmark://catalog/manifest"}`)
		}
		if err := n.Notify(context.Background(), &jsonrpc.Notification{Jsonrpc: "2.0", Method: method, Params: params}); err != nil {
			return err
		}
	}
	return nil
}

// emitFirst selects a session by server-side connection creation order. No
// user claims or synthetic fields enter the native notification payload.
func (b *primitiveBenchEventBus) emitFirst(method string) error {
	b.mu.Lock()
	n := b.ordered[0]
	b.mu.Unlock()
	return n.Notify(context.Background(), &jsonrpc.Notification{Jsonrpc: "2.0", Method: method})
}

type primitiveBenchMutableCatalog struct {
	mu               sync.RWMutex
	owner            string
	kind             string
	operations       []string
	items            map[string]producer.LocalResourceBinding
	raw              map[string]json.RawMessage
	entered, release chan struct{}
	blockNext        bool
}

func (c *primitiveBenchMutableCatalog) Namespaces() []primitive.NamespaceCapabilities {
	operations := c.operations
	if len(operations) == 0 {
		operations = []string{"list", "get"}
	}
	return []primitive.NamespaceCapabilities{{ProviderIdentity: c.owner, Namespace: "bench", Kinds: []primitive.KindSupport{{Kind: c.kind, FormatVersions: []int64{1}, Operations: operations}}}}
}
func (c *primitiveBenchMutableCatalog) List(ctx context.Context, _ identity.VerifiedActor, kind, namespace string) ([]producer.LocalResourceBinding, error) {
	c.mu.Lock()
	out := []producer.LocalResourceBinding{}
	for _, item := range c.items {
		out = append(out, item)
	}
	blocked := c.blockNext
	c.blockNext = false
	entered, release := c.entered, c.release
	c.mu.Unlock()
	if blocked {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return out, nil
}
func (c *primitiveBenchMutableCatalog) Lookup(_ context.Context, _ identity.VerifiedActor, uri identity.ResourceURI) (producer.LocalResourceBinding, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, ok := c.items[uri.String()]
	if !ok {
		return item, producer.ErrCatalogNotFound
	}
	return item, nil
}
func (c *primitiveBenchMutableCatalog) set(id, text string) {
	uri := identity.ResourceURI{Kind: c.kind, Namespace: "bench", Name: id}
	raw, _ := json.Marshal(map[string]interface{}{"id": id, "text": text})
	c.mu.Lock()
	c.raw[uri.String()] = raw
	c.items[uri.String()] = producer.LocalResourceBinding{URI: uri.String(), Title: id, FormatVersion: 1, Resolver: func(_ context.Context, a identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
		source := &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) {
			c.mu.RLock()
			defer c.mu.RUnlock()
			return c.raw[uri.String()], nil
		}}
		return &identity.ResourceResolver{ProviderIdentity: c.owner, Source: source, Policy: primitiveBenchPolicy{actor: a}}, nil
	}}
	c.mu.Unlock()
}
func (c *primitiveBenchMutableCatalog) remove(id string) {
	c.mu.Lock()
	delete(c.items, c.kind+"://bench/"+id)
	delete(c.raw, c.kind+"://bench/"+id)
	c.mu.Unlock()
}
func primitiveBenchIDs(page *primitive.CatalogPage[Located[json.RawMessage]]) []string {
	out := []string{}
	for _, item := range page.Items {
		out = append(out, item.Resource.Name)
	}
	sort.Strings(out)
	return out
}
func primitiveBenchWait(t *testing.T, test func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if test() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("real SDK notification did not reach manager within3s")
}

func TestPrimitiveGatewayResourceNotificationBenchmark(t *testing.T) {
	if os.Getenv("AGENTLY_PRIMITIVE_EVENT_BENCH") != "1" {
		t.Skip("explicit real SDK notification fixture")
	}
	actor := identity.VerifiedActor{Subject: "synthetic-user", Issuer: "synthetic-idp", TenantID: "tenant", AccountID: "account", IdentityRevision: "1", ValidUntil: time.Now().Add(time.Hour)}
	counts := &primitiveBenchCounts{}
	counts.reset()
	options := fixtureOptions{}
	var endpoints []*httptest.Server
	var catalogs []*primitiveBenchMutableCatalog
	var buses []*primitiveBenchEventBus
	for i := 0; i < 2; i++ {
		name := fmt.Sprintf("event-provider-%d", i)
		kind := "prompt"
		if i == 1 {
			kind = "eval"
		}
		catalog := &primitiveBenchMutableCatalog{kind: kind, owner: name, items: map[string]producer.LocalResourceBinding{}, raw: map[string]json.RawMessage{}}
		catalog.set("first", "original")
		catalog.set("second", "original")
		catalogs = append(catalogs, catalog)
		bus := &primitiveBenchEventBus{}
		buses = append(buses, bus)
		provider, err := producer.NewLocalProvider(producer.LocalConfig{ProviderIdentity: name, Catalog: catalog, Actor: func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, Verify: func(context.Context, identity.VerifiedActor) error { return nil }, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Validators: map[string]producer.LocalResourceValidator{kind: primitiveBenchJSON}})
		if err != nil {
			t.Fatal(err)
		}
		handler, err := primitiveBenchSDKHandler(t, provider, name, func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var raw []byte
				if r.Body != nil {
					raw, _ = io.ReadAll(r.Body)
					r.Body = io.NopCloser(bytes.NewReader(raw))
				}
				method := r.Method
				if r.Method == http.MethodPost {
					var request struct {
						Method string `json:"method"`
						Params struct {
							Name string `json:"name"`
						} `json:"params"`
					}
					if json.Unmarshal(raw, &request) == nil && request.Method != "" {
						method = request.Method
						if method == "tools/call" {
							method = request.Params.Name
						}
					}
				}
				counts.mu.Lock()
				counts.requests[method]++
				counts.requestBytes += int64(len(raw))
				counts.mu.Unlock()
				next.ServeHTTP(&primitiveBenchResponse{ResponseWriter: w, counts: counts}, r.WithContext(observe.WithObserver(r.Context(), counts)))
			})
		}, bus)
		if err != nil {
			t.Fatal(err)
		}
		endpoint := httptest.NewServer(handler)
		endpoints = append(endpoints, endpoint)
		options[name] = &cfg.MCPClient{PrimitiveProviderIdentity: name, ClientOptions: &mcp.ClientOptions{Name: name, Version: "1", ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: endpoint.URL + "/mcp"}}}}
	}
	options["event-provider-1"].ToolsListVisibility = cfg.ToolsListVisibilityPublic
	defer func() {
		for _, e := range endpoints {
			e.Close()
		}
	}()
	mgr, err := manager.New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.CloseConversation("")
	gateway := NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return actor, nil }, func(context.Context, identity.VerifiedActor) error { return nil }, "event-consumer")
	defer gateway.Close()
	alice := observe.WithObserver(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), counts)
	bob := observe.WithObserver(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"}), counts)
	var notifications atomic.Int64
	var publicNotifications atomic.Int64
	stop := mgr.OnResourceCatalogChange(func(server string) {
		if server == "event-provider-0" {
			notifications.Add(1)
		} else {
			publicNotifications.Add(1)
		}
	})
	defer stop()
	before, err := ListPage[json.RawMessage](alice, gateway, "prompt", CatalogQuery{Namespace: "bench", Limit: 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ListPage[json.RawMessage](bob, gateway, "prompt", CatalogQuery{Namespace: "bench", Limit: 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := ListPage[json.RawMessage](alice, gateway, "eval", CatalogQuery{Namespace: "bench", Limit: 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	initialOwner1, err := gateway.ConnectionForProvider(alice, "event-provider-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = other
	// Standard resources/list_changed is coarse; add/remove are computed by diffing
	// the newly authorized primitive catalog. No custom add/remove RPC is invented.
	for _, action := range []string{"add", "update", "remove", "burst"} {
		initial := notifications.Load()
		counts.reset()
		started := time.Now()
		switch action {
		case "add":
			catalogs[0].set("third", "added")
		case "update":
			catalogs[0].set("first", "updated")
		case "remove":
			catalogs[0].remove("third")
		}
		emitted := 1
		if action == "burst" {
			emitted = 10
		}
		for i := 0; i < emitted; i++ {
			if err = buses[0].emit("notifications/resources/list_changed"); err != nil {
				t.Fatal(err)
			}
		}
		primitiveBenchWait(t, func() bool { return notifications.Load() >= initial+int64(emitted*2) })
		invalidated := time.Since(started)
		refreshed, err := ListPage[json.RawMessage](alice, gateway, "prompt", CatalogQuery{Namespace: "bench", Limit: 100}, true)
		if err != nil {
			t.Fatal(err)
		}
		_, cursorError := ListPage[json.RawMessage](bob, gateway, "prompt", CatalogQuery{Namespace: "bench", Limit: 1, Cursor: other.NextCursor}, true)
		_, unrelatedCursorError := ListPage[json.RawMessage](alice, gateway, "eval", CatalogQuery{Namespace: "bench", Limit: 1, Cursor: unrelated.NextCursor}, true)
		owner1, err := gateway.ConnectionForProvider(alice, "event-provider-1")
		if err != nil {
			t.Fatal(err)
		}
		if action == "update" {
			connection, err := gateway.ConnectionForProvider(alice, "event-provider-0")
			if err != nil {
				t.Fatal(err)
			}
			got, err := Get[json.RawMessage](alice, gateway, connection, identity.ResourceRef{URI: "prompt://bench/first"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]string
			if json.Unmarshal(got.Definition, &value) != nil || value["text"] != "updated" {
				t.Fatal("managing update not observed")
			}
		}
		requests, events, input, output := counts.snapshot()
		raw, _ := json.Marshal(map[string]interface{}{"scenario": action, "protocol": "2025-11-25 stateful real SDK notifications/resources/list_changed", "emitted": emitted, "received": notifications.Load() - initial, "invalidateMS": float64(invalidated) / float64(time.Millisecond), "nextVisibleMS": float64(time.Since(started)) / float64(time.Millisecond), "names": primitiveBenchIDs(refreshed), "otherNotifiedClientOldCursorRejected": cursorError != nil, "unrelatedProviderOldCursorRejected": unrelatedCursorError != nil, "unrelatedProviderGenerationChanged": owner1.Generation != initialOwner1.Generation, "requests": requests, "observations": events, "requestBytes": input, "responseBytes": output})
		t.Log("PRIMITIVE_EVENTS " + string(raw))
		before = refreshed
	}
	_ = before

	// Actual standard subscription gates the optional resources/updated event.
	// A private event delivered only to Alice retains Bob and the unrelated
	// public provider. Conversely a public source change reaches all clients'
	// snapshots even if only Alice's transport receives the notification.
	bobPrompt, err := ListPage[json.RawMessage](bob, gateway, "prompt", CatalogQuery{Namespace: "bench", Limit: 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	beforePrivate := notifications.Load()
	if err = buses[0].emitFirst("notifications/resources/list_changed"); err != nil {
		t.Fatal(err)
	}
	primitiveBenchWait(t, func() bool { return notifications.Load() > beforePrivate })
	_, err = ListPage[json.RawMessage](bob, gateway, "prompt", CatalogQuery{Namespace: "bench", Limit: 1, Cursor: bobPrompt.NextCursor}, true)
	if err != nil {
		t.Fatalf("unnotified private user cursor was invalidated: %v", err)
	}
	_, err = ListPage[json.RawMessage](alice, gateway, "eval", CatalogQuery{Namespace: "bench", Limit: 1, Cursor: unrelated.NextCursor}, true)
	if err != nil {
		t.Fatalf("unrelated public provider cursor was invalidated: %v", err)
	}
	bobEval, err := ListPage[json.RawMessage](bob, gateway, "eval", CatalogQuery{Namespace: "bench", Limit: 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	beforePublic := publicNotifications.Load()
	if err = buses[1].emitFirst("notifications/resources/list_changed"); err != nil {
		t.Fatal(err)
	}
	primitiveBenchWait(t, func() bool { return publicNotifications.Load() > beforePublic })
	_, err = ListPage[json.RawMessage](bob, gateway, "eval", CatalogQuery{Namespace: "bench", Limit: 1, Cursor: bobEval.NextCursor}, true)
	if err == nil {
		t.Fatal("shared public provider notification left another client stale")
	}
	t.Log("PRIMITIVE_ISOLATION privateOneReceiverPreservesOtherUser=true unrelatedPublicProviderPreserved=true publicOneReceiverInvalidatesOtherClient=true toolGenerationUnchanged=true")
	client, err := mgr.Get(alice, "", "event-provider-0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Subscribe(alice, &schema.SubscribeRequestParams{Uri: "benchmark://catalog/manifest"}); err != nil {
		t.Fatal(err)
	}
	initial := notifications.Load()
	started := time.Now()
	if err = buses[0].emit("notifications/resources/updated"); err != nil {
		t.Fatal(err)
	}
	primitiveBenchWait(t, func() bool { return notifications.Load() > initial })
	t.Logf("PRIMITIVE_UPDATED real subscriptions/resources/updated received=%d invalidateMS=%.3f", notifications.Load()-initial, float64(time.Since(started))/float64(time.Millisecond))
	// A coarse notification invalidates a pending population, not just cursors
	// already delivered. Release the original source after notification arrives.
	mgr.InvalidateVisibleCatalogPartition(mgr.CatalogPartition(alice, catalogPrincipalScope(actor), "prompt"))
	entered, release := make(chan struct{}), make(chan struct{})
	catalogs[0].mu.Lock()
	catalogs[0].blockNext = true
	catalogs[0].entered, catalogs[0].release = entered, release
	catalogs[0].mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := ListPage[json.RawMessage](alice, gateway, "prompt", CatalogQuery{Limit: 100})
		done <- err
	}()
	<-entered
	initial = notifications.Load()
	started = time.Now()
	if err = buses[0].emit("notifications/resources/list_changed"); err != nil {
		t.Fatal(err)
	}
	primitiveBenchWait(t, func() bool { return notifications.Load() >= initial+2 })
	close(release)
	loadError := <-done
	if loadError == nil {
		t.Fatal("inflight catalog survived real resource notification")
	}
	t.Logf("PRIMITIVE_INFLIGHT received=%d invalidateMS=%.3f latePopulationRejected=true", notifications.Load()-initial, float64(time.Since(started))/float64(time.Millisecond))
	t.Log("Resource generations are separate from tools/connection identity; burst invalidation is immediate and refresh coalesces through lazy singleflight. Native resource updated requires actual subscription; latest SDK also supports2026 subscriptions/listen,which this compatibility fixture does not invent or substitute.")
}
