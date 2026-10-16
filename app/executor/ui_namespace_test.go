package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/service/policy"
	ui "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type uiOwnedClient struct {
	client        *http.Client
	base, session string
}

func (c *uiOwnedClient) rpc(t *testing.T, method string, args any) (json.RawMessage, error) {
	t.Helper()
	post := func(body any) (int, []byte, error) {
		raw, _ := json.Marshal(body)
		r, _ := http.NewRequest(http.MethodPost, c.base+"/ui", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
		if c.session != "" {
			r.Header.Set("Mcp-Session-Id", c.session)
		}
		resp, err := c.client.Do(r)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
			c.session = sid
		}
		raw, err = io.ReadAll(resp.Body)
		return resp.StatusCode, raw, err
	}
	if c.session == "" {
		if _, _, err := post(nil); err != nil {
			return nil, err
		}
	}
	code, raw, err := post(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": args})
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, policy.ErrDenied
	}
	var result struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Error) > 0 {
		return nil, policy.ErrDenied
	}
	return result.Result, nil
}
func namespaceBFFFixture(t *testing.T, snapshots ...policy.AuthoritySnapshotResolver) (*ui.Service, func(string) *uiOwnedClient, func(string) context.Context, *atomic.Bool, func()) {
	t.Helper()
	expired := &atomic.Bool{}
	sessions := auth.NewManager(0, nil)
	for _, actor := range []string{"alice", "bob"} {
		sessions.Put(context.Background(), &auth.Session{ID: "owned-" + actor, Subject: actor, Tokens: &scyauth.Token{Token: oauth2.Token{AccessToken: "owned-access-" + actor}, IDToken: "owned-id-" + actor}})
	}
	snapshot := func(ctx context.Context) (gating.Principal, error) {
		subject := auth.EffectiveUserID(ctx)
		if subject == "" || expired.Load() && auth.IDToken(ctx) != "owned-id-alice-refreshed" {
			return gating.Principal{}, policy.ErrIdentityRejected
		}
		return gating.Principal{Facts: authz.Facts{Subject: subject, Issuer: "owned-issuer", Tenant: "owned-tenant", ValidUntil: time.Now().Add(time.Minute)}, AccountID: "owned-account"}, nil
	}
	if len(snapshots) > 0 {
		snapshot = snapshots[0]
	}
	resolver := trustedUINamespace(snapshot)
	svc := ui.NewService(&ui.Config{NamespaceResolver: resolver})
	server := httptest.NewServer(auth.Protect(&auth.Config{Enabled: true, CookieName: "owned_session", OAuth: &auth.OAuth{Mode: "mixed"}}, sessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			svc.Hub().ServeWS(w, r)
		} else {
			svc.Hub().ServeHTTPRPC(w, r)
		}
	})))
	clients := func(actor string) *uiOwnedClient {
		jar, _ := cookiejar.New(nil)
		u, _ := url.Parse(server.URL)
		jar.SetCookies(u, []*http.Cookie{{Name: "owned_session", Value: "owned-" + actor}})
		return &uiOwnedClient{base: server.URL, client: &http.Client{Jar: jar, Timeout: 3 * time.Second}}
	}
	contexts := func(actor string) context.Context { return auth.InjectUser(context.Background(), actor) }
	return svc, clients, contexts, expired, server.Close
}
func fixtureNamespace(t *testing.T, actor string) string {
	t.Helper()
	ns, err := trustedUINamespace(func(context.Context) (gating.Principal, error) {
		return gating.Principal{Facts: authz.Facts{Subject: actor, Issuer: "owned-issuer", Tenant: "owned-tenant", ValidUntil: time.Now().Add(time.Minute)}, AccountID: "owned-account"}, nil
	})(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ns.Namespace
}
func TestTrustedUINamespaceActualBFFHTTPActorIsolationAndACK(t *testing.T) {
	svc, newClient, actorCtx, _, close := namespaceBFFFixture(t)
	defer close()
	alice, bob := newClient("alice"), newClient("bob")
	for _, c := range []*uiOwnedClient{alice, bob} {
		if _, err := c.rpc(t, "ui.hello", map[string]any{"clientId": "shared-client", "token": "forged-caller-token"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		c     *uiOwnedClient
		owner string
	}{{alice, "alice"}, {bob, "bob"}} {
		if _, err := item.c.rpc(t, "ui.snapshot", map[string]any{"clientId": "shared-client", "data": map[string]any{"owner": item.owner}}); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(string(svc.Hub().Snapshot(fixtureNamespace(t, "alice"), "shared-client")), "bob") {
		t.Fatal("Bob overwrote Alice namespace")
	}
	for _, method := range []string{"ui.snapshot", "ui.snapshot.get", "ui.snapshot.status", "ui.poll"} {
		if _, err := bob.rpc(t, method, map[string]any{"clientId": "foreign-client", "timeoutMs": 1, "data": map[string]any{"spoof": true}}); err == nil {
			t.Fatalf("foreign client accepted by %s", method)
		}
	}
	if _, err := bob.rpc(t, "ui.hello", map[string]any{"clientId": "foreign-client"}); err == nil {
		t.Fatal("bound session switched claimed client")
	}
	sid := bob.session
	bob.session = alice.session
	if _, err := bob.rpc(t, "ui.snapshot.get", map[string]any{"clientId": "shared-client"}); err == nil {
		t.Fatal("foreign MCP session accepted")
	}
	bob.session = sid
	completion := make(chan error, 1)
	go func() {
		_, err := svc.UICommand(actorCtx("alice"), &ui.UICommandInput{ClientID: "shared-client", Method: "ui.window.setTitle", Params: map[string]any{"title": "owned"}, TimeoutMs: 3000})
		completion <- err
	}()
	raw, err := alice.rpc(t, "ui.poll", map[string]any{"clientId": "shared-client", "timeoutMs": 1000})
	if err != nil {
		t.Fatal(err)
	}
	var command struct {
		Params struct {
			ID string `json:"id"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &command) != nil || command.Params.ID == "" {
		t.Fatal("owned command missing")
	}
	response := map[string]any{"id": command.Params.ID, "ok": true, "result": map[string]any{"owned": true}}
	if _, err := bob.rpc(t, "ui.response", response); err == nil {
		t.Fatal("foreign known command ACK accepted")
	}
	if _, err := alice.rpc(t, "ui.response", response); err != nil {
		t.Fatal(err)
	}
	if err := <-completion; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UICommand(actorCtx("alice"), &ui.UICommandInput{ClientID: "shared-client", Namespace: "caller-spoof", Method: "ui.window.setTitle"}); err == nil || err.Error() != "UI namespace is unavailable" {
		t.Fatal("exact tool namespace guard missing")
	}
}
func TestTrustedUINamespaceRefreshLeaseAndMissingIdentity(t *testing.T) {
	principal := gating.Principal{Facts: authz.Facts{Subject: "owned", Issuer: "issuer", Tenant: "tenant", ValidUntil: time.Now().Add(time.Minute), Roles: []string{"one"}}, AccountID: "account", IdentityRevision: "one"}
	resolver := trustedUINamespace(func(context.Context) (gating.Principal, error) { return principal, nil })
	before, err := resolver(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	principal.Facts.Roles = []string{"two"}
	principal.IdentityRevision = "two"
	principal.Facts.ValidUntil = time.Now().Add(2 * time.Minute)
	after, err := resolver(context.Background())
	if err != nil || before.Namespace != after.Namespace {
		t.Fatal("refresh retargeted stable namespace")
	}
	principal.Facts.ValidUntil = time.Now().Add(-time.Second)
	if _, err := resolver(context.Background()); err == nil {
		t.Fatal("expired identity accepted")
	}
	principal.Facts.ValidUntil = time.Now().Add(time.Minute)
	principal.AccountID = ""
	if _, err := resolver(context.Background()); err == nil {
		t.Fatal("incomplete identity accepted")
	}
	if _, err := trustedUINamespace(nil)(context.Background()); err == nil {
		t.Fatal("missing resolver accepted")
	}
}
func TestTrustedUINamespaceActualBFFWSAndExpiredResolver(t *testing.T) {
	svc, newClient, actorCtx, expired, close := namespaceBFFFixture(t)
	defer close()
	alice := newClient("alice")
	u, _ := url.Parse(alice.base)
	header := http.Header{}
	for _, cookie := range alice.client.Jar.Cookies(u) {
		header.Add("Cookie", cookie.String())
	}
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(alice.base, "http")+"/ui", header)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.WriteJSON(map[string]any{"type": "ui.hello", "clientId": "owned-ws", "token": "caller-forged"}); err != nil {
		t.Fatal(err)
	}
	ns := fixtureNamespace(t, "alice")
	for deadline := time.Now().Add(time.Second); len(svc.Hub().ListClients(ns)) == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	done := make(chan error, 1)
	go func() {
		_, err := svc.UICommand(actorCtx("alice"), &ui.UICommandInput{ClientID: "owned-ws", Method: "ui.window.setTitle", TimeoutMs: 2000})
		done <- err
	}()
	var command struct {
		ID string `json:"id"`
	}
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := ws.ReadJSON(&command); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteJSON(map[string]any{"id": command.ID, "ok": true, "result": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	expired.Store(true)
	if err := ws.WriteJSON(map[string]any{"type": "ui.snapshot", "clientId": "owned-ws", "data": map[string]any{"owner": "expired"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("expired WS identity continued")
	}
	bad, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(alice.base, "http")+"/ui", header)
	if bad != nil {
		bad.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatal("expired identity upgraded websocket")
	}
}
func TestTrustedUIExpiredIdleReceiverRejectsBeforeDelivery(t *testing.T) {
	svc, newClient, actorCtx, expired, close := namespaceBFFFixture(t)
	defer close()
	alice := newClient("alice")
	if _, err := alice.rpc(t, "ui.hello", map[string]any{"clientId": "idle-http"}); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(alice.base)
	header := http.Header{}
	for _, cookie := range alice.client.Jar.Cookies(u) {
		header.Add("Cookie", cookie.String())
	}
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(alice.base, "http")+"/ui", header)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.WriteJSON(map[string]any{"type": "ui.hello", "clientId": "idle-ws"}); err != nil {
		t.Fatal(err)
	}
	ns := fixtureNamespace(t, "alice")
	for deadline := time.Now().Add(time.Second); len(svc.Hub().ListClients(ns)) < 2 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	expired.Store(true)
	fresh := auth.InjectTokens(actorCtx("alice"), &scyauth.Token{IDToken: "owned-id-alice-refreshed"})
	for _, clientID := range []string{"idle-http", "idle-ws"} {
		if _, err := svc.UICommand(fresh, &ui.UICommandInput{ClientID: clientID, Method: "ui.window.setTitle", TimeoutMs: 1000}); err == nil || err.Error() != "UI receiver identity unavailable" {
			t.Fatal("fresh requester delivered to expired receiver")
		}
	}
	ws.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("expired socket received frame")
	}
	expired.Store(false)
	raw, err := alice.rpc(t, "ui.poll", map[string]any{"clientId": "idle-http", "timeoutMs": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "null" {
		t.Fatal("expired receiver had queued command")
	}
	ws.Close()
	for deadline := time.Now().Add(time.Second); len(svc.Hub().ListClients(ns)) > 1 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if len(svc.Hub().ListClients(ns)) != 1 {
		t.Fatal("socket lifecycle retained receiver")
	}
}
func TestTrustedUIQueuedOriginalDeadlineIsNotRenewedByPoll(t *testing.T) {
	svc, newClient, actorCtx, _, close := namespaceBFFFixture(t)
	defer close()
	alice := newClient("alice")
	if _, err := alice.rpc(t, "ui.hello", map[string]any{"clientId": "owned-idle"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UICommand(actorCtx("alice"), &ui.UICommandInput{ClientID: "owned-idle", Method: "ui.window.setTitle", TimeoutMs: 10}); err == nil {
		t.Fatal("unacknowledged command did not expire")
	}
	raw, err := alice.rpc(t, "ui.poll", map[string]any{"clientId": "owned-idle", "timeoutMs": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "null" {
		t.Fatal("fresh poll renewed expired original command")
	}
}
func TestTrustedUIKnownOriginalCommandCannotACKAfterDeadline(t *testing.T) {
	svc, newClient, actorCtx, _, close := namespaceBFFFixture(t)
	defer close()
	alice := newClient("alice")
	if _, err := alice.rpc(t, "ui.hello", map[string]any{"clientId": "owned-expiring"}); err != nil {
		t.Fatal(err)
	}
	completion := make(chan error, 1)
	go func() {
		_, err := svc.UICommand(actorCtx("alice"), &ui.UICommandInput{ClientID: "owned-expiring", Method: "ui.window.setTitle", TimeoutMs: 80})
		completion <- err
	}()
	raw, err := alice.rpc(t, "ui.poll", map[string]any{"clientId": "owned-expiring", "timeoutMs": 1000})
	if err != nil {
		t.Fatal(err)
	}
	var command struct {
		Params struct {
			ID string `json:"id"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &command) != nil || command.Params.ID == "" {
		t.Fatal("original command missing")
	}
	if err := <-completion; err == nil {
		t.Fatal("unacknowledged operation did not expire")
	}
	if _, err := alice.rpc(t, "ui.response", map[string]any{"id": command.Params.ID, "ok": true, "result": map[string]any{}}); err == nil {
		t.Fatal("same owner renewed expired original command")
	}
}
func TestTrustedNamespaceForcesFreshProviderFacts(t *testing.T) {
	source, _, _, ctx, close := windowDecisionFixture(t, true)
	defer close()
	scoped, finish, err := source.BeginDecision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	before := source.calls.Load()
	resolver := trustedUINamespace(source.prepared.provider.AuthoritySnapshot, source.prepared.provider.ExecutionContext)
	if _, err := resolver(scoped); err != nil {
		t.Fatal(err)
	}
	if source.calls.Load() != before+1 {
		t.Fatal("namespace reused provider decision facts")
	}
}
func TestTrustedUIReceiverLeaseSurvivesNeitherFreshHelloNorQueuedPoll(t *testing.T) {
	short := atomic.Bool{}
	until := time.Now().Add(time.Minute)
	snapshot := func(ctx context.Context) (gating.Principal, error) {
		lease := time.Now().Add(time.Minute)
		if short.Load() && auth.IDToken(ctx) == "owned-id-alice" {
			lease = until
		}
		return gating.Principal{Facts: authz.Facts{Subject: auth.EffectiveUserID(ctx), Issuer: "owned-issuer", Tenant: "owned-tenant", ValidUntil: lease}, AccountID: "owned-account"}, nil
	}
	svc, newClient, actorCtx, _, close := namespaceBFFFixture(t, snapshot)
	defer close()
	alice := newClient("alice")
	if _, err := alice.rpc(t, "ui.hello", map[string]any{"clientId": "leased-client"}); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(25 * time.Millisecond)
	short.Store(true)
	fresh := auth.InjectTokens(actorCtx("alice"), &scyauth.Token{IDToken: "owned-id-alice-refreshed"})
	if _, err := svc.UICommand(fresh, &ui.UICommandInput{ClientID: "leased-client", Method: "ui.window.setTitle", TimeoutMs: 1000}); err == nil {
		t.Fatal("receiver shorter lease was renewed")
	}
	short.Store(false)
	for i := 0; i < 2; i++ {
		if _, err := alice.rpc(t, "ui.hello", map[string]any{"clientId": "leased-client"}); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := alice.rpc(t, "ui.poll", map[string]any{"clientId": "leased-client", "timeoutMs": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "null" {
		t.Fatal("fresh hello renewed old queued deadline")
	}
	complete := make(chan error, 1)
	go func() {
		_, err := svc.UICommand(fresh, &ui.UICommandInput{ClientID: "leased-client", Method: "ui.window.setTitle", TimeoutMs: 1000})
		complete <- err
	}()
	raw, err = alice.rpc(t, "ui.poll", map[string]any{"clientId": "leased-client", "timeoutMs": 1000})
	if err != nil {
		t.Fatal(err)
	}
	var command struct {
		Params struct {
			ID string `json:"id"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &command) != nil || command.Params.ID == "" {
		t.Fatal("fresh owned command absent")
	}
	if _, err := alice.rpc(t, "ui.response", map[string]any{"id": command.Params.ID, "ok": true, "result": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if err := <-complete; err != nil {
		t.Fatal(err)
	}
}

type delayedOwnedJSON struct{ started, release chan struct{} }

func (v delayedOwnedJSON) MarshalJSON() ([]byte, error) {
	close(v.started)
	<-v.release
	return []byte(`{"owned":true}`), nil
}
func (v delayedOwnedJSON) Release() { close(v.release) }
func TestTrustedUIHTTPPollDiscardsFrameIfIdentityLeaseExpiresDuringEncoding(t *testing.T) {
	short := atomic.Bool{}
	until := time.Now().Add(time.Minute)
	snapshot := func(ctx context.Context) (gating.Principal, error) {
		lease := time.Now().Add(time.Minute)
		if short.Load() && auth.IDToken(ctx) == "owned-id-alice" {
			lease = until
		}
		return gating.Principal{Facts: authz.Facts{Subject: auth.EffectiveUserID(ctx), Issuer: "owned-issuer", Tenant: "owned-tenant", ValidUntil: lease}, AccountID: "owned-account"}, nil
	}
	svc, newClient, actorCtx, _, close := namespaceBFFFixture(t, snapshot)
	defer close()
	alice := newClient("alice")
	if _, err := alice.rpc(t, "ui.hello", map[string]any{"clientId": "poll-expiry"}); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(100 * time.Millisecond)
	short.Store(true)
	payload := delayedOwnedJSON{make(chan struct{}), make(chan struct{})}
	done := make(chan error, 1)
	fresh := auth.InjectTokens(actorCtx("alice"), &scyauth.Token{IDToken: "owned-id-alice-refreshed"})
	go func() {
		_, err := svc.UICommand(fresh, &ui.UICommandInput{ClientID: "poll-expiry", Method: "ui.window.setTitle", Params: payload, TimeoutMs: 1000})
		done <- err
	}()
	polled := make(chan error, 1)
	go func() {
		_, err := alice.rpc(t, "ui.poll", map[string]any{"clientId": "poll-expiry", "timeoutMs": 1000})
		polled <- err
	}()
	select {
	case <-payload.started:
	case <-time.After(3 * time.Second):
		t.Fatal("frame encoding did not start")
	}
	time.Sleep(time.Until(until) + 20*time.Millisecond)
	payload.Release()
	if err := <-polled; err == nil {
		t.Fatal("expired poll released encoded frame")
	}
	if err := <-done; err == nil {
		t.Fatal("receiver lease expiry released success")
	}
}
func TestTrustedUIFreshPollIdentityCannotExtendPendingReceiverDeadlineDuringEncoding(t *testing.T) {
	short := atomic.Bool{}
	receiverCaptured := make(chan struct{})
	until := time.Now().Add(time.Minute)
	snapshot := func(ctx context.Context) (gating.Principal, error) {
		lease := time.Now().Add(time.Minute)
		if auth.IDToken(ctx) == "owned-id-alice" && short.CompareAndSwap(true, false) {
			lease = until
			close(receiverCaptured)
		}
		return gating.Principal{Facts: authz.Facts{Subject: auth.EffectiveUserID(ctx), Issuer: "owned-issuer", Tenant: "owned-tenant", ValidUntil: lease}, AccountID: "owned-account"}, nil
	}
	svc, newClient, actorCtx, _, cleanup := namespaceBFFFixture(t, snapshot)
	defer cleanup()
	alice := newClient("alice")
	if _, err := alice.rpc(t, "ui.hello", map[string]any{"clientId": "original-receiver-lease"}); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(100 * time.Millisecond)
	short.Store(true)
	payload := delayedOwnedJSON{make(chan struct{}), make(chan struct{})}
	done := make(chan error, 1)
	fresh := auth.InjectTokens(actorCtx("alice"), &scyauth.Token{IDToken: "owned-id-alice-refreshed"})
	go func() {
		_, err := svc.UICommand(fresh, &ui.UICommandInput{ClientID: "original-receiver-lease", Method: "ui.window.setTitle", Params: payload, TimeoutMs: 1000})
		done <- err
	}()
	select {
	case <-receiverCaptured:
	case <-time.After(3 * time.Second):
		t.Fatal("receiver lease was not captured")
	}
	polled := make(chan error, 1)
	go func() {
		_, err := alice.rpc(t, "ui.poll", map[string]any{"clientId": "original-receiver-lease", "timeoutMs": 1000})
		polled <- err
	}()
	select {
	case <-payload.started:
	case <-time.After(3 * time.Second):
		t.Fatal("frame encoding did not start")
	}
	time.Sleep(time.Until(until) + 20*time.Millisecond)
	payload.Release()
	if err := <-polled; err == nil {
		t.Fatal("fresh poll released frame past original receiver deadline")
	}
	if err := <-done; err == nil {
		t.Fatal("fresh facts renewed original command")
	}
}
