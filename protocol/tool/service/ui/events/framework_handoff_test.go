package events

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/viant/afs"
	httpui "github.com/viant/agently-core/adapter/http/ui"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/forge/backend/service/meta"
	"github.com/viant/forge/backend/types"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type frameworkHandoffCatalog struct {
	window        *types.Window
	denied        bool
	lastRef       *identity.ResourceRef
	lastProvider  string
	lastNamespace string
}

func (*frameworkHandoffCatalog) UsesResourceResolution() bool { return true }
func (*frameworkHandoffCatalog) AuthzReady() bool             { return true }
func (c *frameworkHandoffCatalog) List(context.Context, *forge.WindowDefinitionListInput) (*forge.WindowDefinitionListOutput, error) {
	return &forge.WindowDefinitionListOutput{}, nil
}
func (c *frameworkHandoffCatalog) Get(_ context.Context, in *forge.WindowDefinitionGetInput) (*forge.WindowDefinitionGetOutput, error) {
	if c.denied || in.WindowID != "chat/new" {
		return nil, identity.ErrResourceDenied
	}
	if in.Resource != nil {
		uri, err := identity.ParseResourceURI(in.Resource.URI)
		if err != nil || in.ProviderIdentity != c.window.Resource.ProviderIdentity || in.Namespace != uri.Namespace {
			return nil, identity.ErrResourceDenied
		}
		c.lastProvider = in.ProviderIdentity
		c.lastNamespace = in.Namespace
		copy := *in.Resource
		c.lastRef = &copy
	}
	return &forge.WindowDefinitionGetOutput{WindowID: "chat/new", Definition: c.window}, nil
}
func (c *frameworkHandoffCatalog) RevalidateWindowResource(_ context.Context, key string, pin identity.ResolvedResource, _ *types.WindowTarget) (*identity.ResolvedResource, error) {
	if c.denied || key != "chat/new" || pin != *c.window.Resource {
		return nil, identity.ErrResourceDenied
	}
	copy := pin
	return &copy, nil
}
func (c *frameworkHandoffCatalog) CheckWindowContent(context.Context, string, identity.ResolvedResource, *types.WindowTarget) error {
	if c.denied {
		return identity.ErrResourceDenied
	}
	return nil
}
func TestFrameworkAdmissionGETEstablishesExactReportEventInstance(t *testing.T) {
	catalog := &frameworkHandoffCatalog{window: &types.Window{Resource: &identity.ResolvedResource{ProviderIdentity: "configured-framework", URI: "window://agently/chat-new", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("owned-framework"))}, AuthorityBinding: "verified-owner", ValidUntil: time.Now().Add(time.Minute)}}}
	type actorKey struct{}
	bridge := forge.NewService(&forge.Config{WindowDefinitions: catalog, NamespaceResolver: func(ctx context.Context) (forge.NamespaceIdentity, error) {
		owner, _ := ctx.Value(actorKey{}).(string)
		if owner == "" {
			owner = "verified-owner"
		}
		return forge.NamespaceIdentity{Namespace: owner, ValidUntil: time.Now().Add(2 * time.Minute)}, nil
	}})
	session := ""
	rpc := func(method string, params map[string]interface{}) {
		body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": "owned", "method": method, "params": params})
		request := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if session != "" {
			request.Header.Set("Mcp-Session-Id", session)
		}
		response := httptest.NewRecorder()
		bridge.Hub().ServeHTTPRPC(response, request)
		if received := response.Header().Get("Mcp-Session-Id"); received != "" {
			session = received
		}
		var envelope map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &envelope)
		if envelope["error"] != nil {
			t.Fatal("owned bridge handshake failed", envelope["error"])
		}
	}
	rpc("ui.hello", map[string]interface{}{"clientId": "active-client"})
	rpc("ui.snapshot", map[string]interface{}{"clientId": "active-client", "data": map[string]interface{}{"conversationId": "conv-1", "windows": []interface{}{map[string]interface{}{"windowId": "chat/new", "windowKey": "chat/new"}}}})

	svc := New(bridge)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	event := &RecordInput{Kind: "report.context_updated", Detail: map[string]interface{}{"reportId": "owned-inline"}}
	if err := svc.record(ctx, event, &RecordOutput{}); err == nil {
		t.Fatal("anchor without trusted admission unexpectedly recorded")
	}
	handler := httpui.NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, httpui.WithWindowResourceProvider(bridge))
	get := func(query string) int {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/window/chat/new"+query, nil))
		return r.Code
	}
	if code := get("?applyPermission=true"); code != 200 {
		t.Fatal("original admission failed", code)
	}
	if err := svc.record(ctx, event, &RecordOutput{}); err == nil {
		t.Fatal("unbound admission implicitly acquired instance")
	}
	if code := get("?applyPermission=true&windowId=chat/new&clientId=foreign-client"); code < 400 {
		t.Fatal("foreign client bound", code)
	}
	if code := get("?applyPermission=true&windowId=other&clientId=active-client"); code < 400 {
		t.Fatal("arbitrary framework instance bound", code)
	}
	foreign := httptest.NewRecorder()
	handler.ServeHTTP(foreign, httptest.NewRequest(http.MethodGet, "/window/chat/new?applyPermission=true&windowId=chat/new&clientId=active-client", nil).WithContext(context.WithValue(context.Background(), actorKey{}, "foreign-actor")))
	if foreign.Code < 400 {
		t.Fatal("foreign actor reused active client")
	}
	if code := get("?applyPermission=true&windowId=chat/new&clientId=active-client"); code != 200 {
		t.Fatal("trusted instance handoff failed", code)
	}
	var output RecordOutput
	if err := svc.record(ctx, event, &output); err != nil || !output.Recorded || output.Event.WindowID != "chat/new" || output.Event.ClientID != "active-client" || output.Event.Resource == nil {
		t.Fatal("context update did not retain exact admitted pin", err)
	}
	if pin, err := bridge.WindowResource(ctx, "foreign-namespace", "active-client", "chat/new", "chat/new"); err == nil || pin != nil {
		t.Fatal("pin crossed namespace")
	}
	catalog.denied = true
	if err := svc.record(ctx, event, &RecordOutput{}); err == nil {
		t.Fatal("revoked managing authority recorded report event")
	}
}

// An expired private framework instance cannot record even an advisory report
// event. A new explicit selected OPEN admits the same selector under current
// authority; it does not extend or reinterpret the old pin.
func TestExpiredFrameworkEventRequiresFreshSameSelectorOPEN(t *testing.T) {
	type ownerKey struct{}
	catalog := &frameworkHandoffCatalog{window: &types.Window{Resource: &identity.ResolvedResource{ProviderIdentity: "configured-framework", URI: "window://agently/chat-new", ResourceCandidate: identity.ResourceCandidate{Kind: identity.StampedCandidate, Revision: "version-one", ContentFingerprint: identity.ContentFingerprint([]byte("owned-framework"))}, AuthorityBinding: "verified-owner"}}}
	// Use the real configured private metadata + composite routing path. The
	// public alias chat/new deliberately differs from the canonical name chat-new.
	composite := privateFrameworkComposite(t, catalog, func(ctx context.Context) bool {
		name, _ := ctx.Value(ownerKey{}).(string)
		return name == "" || name == "verified-owner"
	})
	bridge := forge.NewService(&forge.Config{WindowDefinitions: composite, NamespaceResolver: func(ctx context.Context) (forge.NamespaceIdentity, error) {
		name, _ := ctx.Value(ownerKey{}).(string)
		if name == "" {
			name = "verified-owner"
		}
		return forge.NamespaceIdentity{Namespace: name, ValidUntil: time.Now().Add(time.Minute)}, nil
	}})
	session := ""
	for _, method := range []string{"ui.hello", "ui.snapshot"} {
		params := map[string]interface{}{"clientId": "active-client"}
		if method == "ui.snapshot" {
			params["data"] = map[string]interface{}{"conversationId": "conv-lease", "windows": []interface{}{map[string]interface{}{"windowId": "chat/new", "windowKey": "chat/new"}}}
		}
		body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": "lease", "method": method, "params": params})
		request := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if session != "" {
			request.Header.Set("Mcp-Session-Id", session)
		}
		response := httptest.NewRecorder()
		bridge.Hub().ServeHTTPRPC(response, request)
		if id := response.Header().Get("Mcp-Session-Id"); id != "" {
			session = id
		}
		var envelope map[string]interface{}
		if json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope["error"] != nil {
			t.Fatal("bridge handshake failed")
		}
	}
	// A second tab belongs to the same actor but has a different negotiated session.
	otherBody, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": "other", "method": "ui.hello", "params": map[string]interface{}{"clientId": "other-client"}})
	otherRequest := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(otherBody))
	otherRequest.Header.Set("Content-Type", "application/json")
	bridge.Hub().ServeHTTPRPC(httptest.NewRecorder(), otherRequest)
	handler := httpui.NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, httpui.WithWindowResourceProvider(bridge))
	expected := ""
	open := func(ctx context.Context, clientID string) int {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/window/chat/new?applyPermission=true&windowId=chat/new&clientId="+clientID+expected, nil).WithContext(ctx)
		request.Header.Set("Mcp-Session-Id", session)
		handler.ServeHTTP(response, request)
		return response.Code
	}
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-lease")
	// Set the short lease only after handshake so setup time cannot consume it.
	expiredAt := time.Now().Add(150 * time.Millisecond)
	catalog.window.Resource.ValidUntil = expiredAt
	if code := open(ctx, "active-client"); code != 200 {
		t.Fatal("initial OPEN failed", code)
	}
	svc := New(bridge)
	input := &RecordInput{ClientID: "active-client", Kind: "report.context_updated", Detail: map[string]interface{}{"reportId": "inline-owned"}}
	var first RecordOutput
	if err := svc.record(ctx, input, &first); err != nil || !first.Recorded {
		t.Fatal("initial event failed", err)
	}
	// The original task-entry admission has expired before a later streaming report.
	time.Sleep(time.Until(expiredAt) + time.Millisecond)
	if err := svc.record(ctx, input, &RecordOutput{}); err == nil || err.Error() != "window resource unavailable" {
		t.Fatal("expired event was not rejected", err)
	}
	// Merely making fresh authority available cannot renew an already-held pin.
	fresh := *catalog.window.Resource
	fresh.ValidUntil = time.Now().Add(time.Minute)
	catalog.window.Resource = &fresh
	if pin, err := bridge.WindowResource(ctx, "verified-owner", "active-client", "chat/new", "chat/new"); err == nil || pin != nil {
		t.Fatal("old private pin implicitly renewed")
	}
	if code := open(ctx, "other-client"); code < 400 {
		t.Fatal("same-owner foreign session/client admitted", code)
	}
	if code := open(ctx, "foreign-client"); code < 400 {
		t.Fatal("foreign client admitted", code)
	}
	foreignCtx := context.WithValue(ctx, ownerKey{}, "foreign-owner")
	if code := open(foreignCtx, "active-client"); code < 400 {
		t.Fatal("foreign actor admitted", code)
	}
	selector, _ := json.Marshal(map[string]interface{}{"uri": fresh.URI, "providerIdentity": fresh.ProviderIdentity, "kind": fresh.Kind, "revision": fresh.Revision, "contentFingerprint": fresh.ContentFingerprint})
	expected = "&expectedResource=" + url.QueryEscape(string(selector))
	for _, mismatch := range []string{"fingerprint", "provider", "revision"} {
		changed := fresh
		switch mismatch {
		case "fingerprint":
			changed.ContentFingerprint = identity.ContentFingerprint([]byte("changed-frame"))
		case "provider":
			changed.ProviderIdentity = "other-provider"
		case "revision":
			changed.Revision = "version-two"
		}
		catalog.window.Resource = &changed
		if code := open(ctx, "active-client"); code < 400 {
			t.Fatal("changed original frame admitted", mismatch, code)
		}
	}
	catalog.window.Resource = &fresh
	forbidden, _ := json.Marshal(map[string]interface{}{"uri": fresh.URI, "providerIdentity": fresh.ProviderIdentity, "kind": fresh.Kind, "revision": fresh.Revision, "contentFingerprint": fresh.ContentFingerprint, "authorityBinding": "forged"})
	expected = "&expectedResource=" + url.QueryEscape(string(forbidden))
	if code := open(ctx, "active-client"); code < 400 {
		t.Fatal("authority accepted as selector integrity", code)
	}
	expected = "&expectedResource=" + url.QueryEscape(string(selector))
	if code := open(ctx, "active-client"); code != 200 {
		t.Fatal("fresh same-selector OPEN failed", code)
	}
	if catalog.lastRef == nil || catalog.lastRef.URI != fresh.URI || catalog.lastRef.Revision != "version-one" || catalog.lastProvider != fresh.ProviderIdentity || catalog.lastNamespace != "agently" {
		t.Fatal("fresh OPEN did not retain original explicit selector")
	}
	var renewed RecordOutput
	if err := svc.record(ctx, input, &renewed); err != nil || !renewed.Recorded {
		t.Fatal("fresh admitted event failed", err)
	}
	if renewed.Event.WindowID != first.Event.WindowID || renewed.Event.ClientID != first.Event.ClientID || renewed.Event.Resource.URI != first.Event.Resource.URI || !renewed.Event.Resource.ValidUntil.After(first.Event.Resource.ValidUntil) {
		t.Fatal("fresh OPEN changed routing or retained expired lease")
	}
	if !first.Event.Resource.ValidUntil.Equal(expiredAt) {
		t.Fatal("old event authority mutated")
	}
	catalog.denied = true
	if code := open(ctx, "active-client"); code < 400 {
		t.Fatal("revoked OPEN admitted", code)
	}
	if err := svc.record(ctx, input, &RecordOutput{}); err == nil {
		t.Fatal("revoked authority recorded event")
	}
}

type privateFrameworkSource struct {
	controls *frameworkHandoffCatalog
	raw      json.RawMessage
	allowed  func(context.Context) bool
}

func (s *privateFrameworkSource) Candidates(ctx context.Context, u identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	if u.String() != s.controls.window.Resource.URI || !s.allowed(ctx) || s.controls.denied {
		return nil, identity.ErrResourceDenied
	}
	return []identity.ResourceCandidate{s.controls.window.Resource.ResourceCandidate}, nil
}
func (s *privateFrameworkSource) ReadCandidate(ctx context.Context, u identity.ResourceURI, c identity.ResourceCandidate) (json.RawMessage, error) {
	if !s.allowed(ctx) || s.controls.denied || u.String() != s.controls.window.Resource.URI || c != s.controls.window.Resource.ResourceCandidate {
		return nil, identity.ErrResourceDenied
	}
	return append(json.RawMessage(nil), s.raw...), nil
}
func (s *privateFrameworkSource) SelectRevision(ctx context.Context, ref identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if !s.allowed(ctx) || s.controls.denied || len(c) != 1 || !s.controls.window.Resource.ValidUntil.After(time.Now()) {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: s.controls.window.Resource.AuthorityBinding, ValidUntil: s.controls.window.Resource.ValidUntil}, nil
}

type recordingPrivateFrameworkCatalog struct {
	*forge.MetadataWindowCatalog
	controls *frameworkHandoffCatalog
}

func (c *recordingPrivateFrameworkCatalog) Get(ctx context.Context, in *forge.WindowDefinitionGetInput) (*forge.WindowDefinitionGetOutput, error) {
	if in.Resource != nil {
		copy := *in.Resource
		c.controls.lastRef = &copy
		c.controls.lastProvider = in.ProviderIdentity
		c.controls.lastNamespace = in.Namespace
	}
	return c.MetadataWindowCatalog.Get(ctx, in)
}
func privateFrameworkComposite(t *testing.T, controls *frameworkHandoffCatalog, allowed func(context.Context) bool) *resources.CompositeWindowCatalog {
	t.Helper()
	variant := types.WindowResourceVariant{Window: &types.Window{WindowKey: "chat/new", View: types.View{Content: &types.Container{ID: "original-framework", Chat: &types.Chat{}}}}, DataSources: map[string]json.RawMessage{}}
	fingerprint, err := types.WindowVariantFingerprint(variant)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Targets: []types.WindowTargetBinding{{Target: types.WindowTarget{}, Variant: fingerprint}}, Variants: map[string]types.WindowResourceVariant{fingerprint: variant}})
	if err != nil {
		t.Fatal(err)
	}
	controls.window.Resource.ContentFingerprint = identity.ContentFingerprint(raw)
	source := &privateFrameworkSource{controls: controls, raw: raw, allowed: allowed}
	local, err := forge.NewMetadataWindowCatalog(meta.New(afs.New(), t.TempDir(), nil), "/private", []forge.SavedWindow{{Key: "chat", SubKey: "new", WindowDefinitionSummary: forge.WindowDefinitionSummary{WindowID: "chat/new", Title: "Original framework", ProviderIdentity: controls.window.Resource.ProviderIdentity}}}, forge.WithWindowAuthorizer(func(ctx context.Context, key string) (bool, error) {
		return key == "chat/new" && allowed(ctx) && !controls.denied, nil
	}), forge.WithWindowResourceResolver(func(ctx context.Context, key string) (*identity.ResourceResolver, identity.ResourceRef, error) {
		if key != "chat/new" || !allowed(ctx) || controls.denied {
			return nil, identity.ResourceRef{}, identity.ErrResourceDenied
		}
		return &identity.ResourceResolver{ProviderIdentity: controls.window.Resource.ProviderIdentity, Source: source, Policy: source}, identity.ResourceRef{URI: controls.window.Resource.URI}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := manager.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	gateway := resources.NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) {
		return identity.VerifiedActor{}, identity.ErrResourceDenied
	}, func(context.Context, identity.VerifiedActor) error { return identity.ErrResourceDenied }, "private-framework")
	proof, err := types.NewWindowTargetHMAC(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	remote := &resources.WindowCatalog{Gateway: gateway, TargetProof: proof}
	return &resources.CompositeWindowCatalog{Local: &recordingPrivateFrameworkCatalog{MetadataWindowCatalog: local, controls: controls}, LocalProviderIdentity: controls.window.Resource.ProviderIdentity, Remote: remote}
}
