package service

import (
	"context"
	"encoding/json"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/forge/backend/types"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport/server/base"
	"testing"
	"time"
)

type admittedPresentationCatalog struct {
	WindowDefinitionCatalog
	reads int
}

func (*admittedPresentationCatalog) IsDefaultPresentationWindow(pin identity.ResolvedResource) bool {
	return pin.ProviderIdentity == "internal"
}
func (c *admittedPresentationCatalog) RevalidateWindowResource(_ context.Context, _ string, pin identity.ResolvedResource, _ *types.WindowTarget) (*identity.ResolvedResource, error) {
	c.reads++
	return &pin, nil
}

func (c *admittedPresentationCatalog) Get(_ context.Context, in *WindowDefinitionGetInput) (*WindowDefinitionGetOutput, error) {
	return &WindowDefinitionGetOutput{Definition: &types.Window{Resource: in.ResolvedResource, DataSource: map[string]types.DataSource{"records": {}}}}, nil
}

func TestNativePresentationBindsServerAdmittedSnapshotWithoutReload(t *testing.T) {
	svc, ctx, _ := feedOwnershipFixture(t)
	svc.hub.httpStreamable()
	bridge := svc.hub.httpBridge
	bridge.bindSession(context.WithValue(ctx, jsonrpc.SessionKey, &base.Session{Id: "owned-session"}), "client-a", "alice", nil, svc.hub.clients["alice"]["client-a"])
	ctx = requestctx.WithUIBridgeSessionID(ctx, "owned-session")
	catalog := &admittedPresentationCatalog{}
	svc.cfg.WindowDefinitions = catalog
	pin := identity.ResolvedResource{URI: "window://workspace/media", ProviderIdentity: "internal", AuthorityBinding: "public-local-definition", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte(`{"original":true}`))}, ValidUntil: time.Now().Add(time.Minute)}
	admitted := &types.Window{Resource: &pin, ResourceTarget: &types.WindowTarget{Platform: "web", SelectionToken: "original"}}
	handled, err := svc.BindAdmittedNativePresentationWindow(ctx, "client-a", "instance", "media", admitted)
	if !handled || err != nil || catalog.reads != 0 {
		t.Fatalf("binding reloaded admitted source: handled=%t err=%v reads=%d", handled, err, catalog.reads)
	}
	held, ok := svc.getWindowPin(windowPinKey{Namespace: "alice", ClientID: "client-a", WindowID: "instance"})
	if !ok || held.Resource != pin || !types.SameWindowTarget(held.Target, admitted.ResourceTarget) {
		t.Fatal("admitted original pin/target lost")
	}
	if _, err = svc.BindAdmittedNativePresentationWindow(ctx, "foreign-client", "instance", "media", admitted); err == nil {
		t.Fatal("foreign client borrowed instance")
	}
	foreign := requestctx.WithUIBridgeSessionID(context.WithValue(context.Background(), feedOwnerKey{}, "bob"), "owned-session")
	if _, err = svc.BindAdmittedNativePresentationWindow(foreign, "client-a", "instance", "media", admitted); err == nil {
		t.Fatal("foreign owner borrowed session")
	}
	changed := pin
	changed.ContentFingerprint = identity.ContentFingerprint([]byte(`{"changed":true}`))
	if _, err = svc.BindAdmittedNativePresentationWindow(ctx, "client-a", "instance", "media", &types.Window{Resource: &changed, ResourceTarget: admitted.ResourceTarget}); err == nil {
		t.Fatal("existing instance candidate replaced")
	}
	// Provider-selected metadata is delivered through the same mechanism even
	// when the resource is external and has no additional OPEN declaration.
	external := pin
	external.URI = "window://external/records"
	external.ProviderIdentity = "external-provider"
	external.AuthorityBinding = "provider-selected-user"
	handled, err = svc.BindAdmittedNativePresentationWindow(ctx, "client-a", "external-instance", external.URI,
		&types.Window{Resource: &external, ResourceTarget: admitted.ResourceTarget})
	if !handled || err != nil || catalog.reads != 0 {
		t.Fatalf("provider-selected delivery was not retained without another read: handled=%t err=%v reads=%d", handled, err, catalog.reads)
	}
	held, ok = svc.getWindowPin(windowPinKey{Namespace: "alice", ClientID: "client-a", WindowID: "external-instance"})
	if !ok || held.Resource != external {
		t.Fatal("external window delivery was lost")
	}
	filtered, err := svc.filterResourceSnapshot(ctx, "alice", "client-a", json.RawMessage(`{"conversationId":"conversation","selected":{"windowId":"external-instance"},"windows":[{"windowId":"external-instance","windowKey":"window://external/records","conversationId":"conversation","dataSources":{"records":{"collection":[{"id":321}]},"unrelated":{"collection":[{"id":999}]}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var visible struct {
		Selected struct {
			WindowID string `json:"windowId"`
		} `json:"selected"`
		Windows []struct {
			DataSources map[string]json.RawMessage `json:"dataSources"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(filtered, &visible); err != nil {
		t.Fatal(err)
	}
	if visible.Selected.WindowID != "external-instance" || len(visible.Windows) != 1 || len(visible.Windows[0].DataSources) != 1 || visible.Windows[0].DataSources["records"] == nil {
		t.Fatalf("UI observation lost the delivered window or included an undeclared source: %s", filtered)
	}
}
