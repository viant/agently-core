package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	windowprotocol "github.com/viant/agently-core/protocol/window"
	identity "github.com/viant/agently-core/protocol/resource"
	schema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type nativeWireFixture struct {
	authCaptureClient
	binding           windowprotocol.ComponentBinding
	calls             int
	during            func()
	seenMeta          bool
	observations      int
	duringObservation func(int)
}

func (f *nativeWireFixture) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	f.observations++
	if f.duringObservation != nil {
		f.duringObservation(f.observations)
	}
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: "read", Meta: map[string]interface{}{componentMetaKey: f.binding}}}}, nil
}
func (f *nativeWireFixture) CallTool(_ context.Context, params *schema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	f.calls++
	raw, _ := json.Marshal(params)
	var wire struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	_ = json.Unmarshal(raw, &wire)
	var pin windowprotocol.ComponentBinding
	if json.Unmarshal(wire.Meta[componentMetaKey], &pin) != nil || pin != f.binding {
		panic("exact binding missing from actual request wire")
	}
	for _, key := range []string{componentMetaKey, "_meta", "component", "revision"} {
		if _, exists := params.Arguments[key]; exists {
			panic("component metadata leaked into business args")
		}
	}
	f.seenMeta = true
	if f.during != nil {
		f.during()
	}
	return &schema.CallToolResult{Content: []schema.CallToolResultContentElem{&schema.TextContent{Type: "text", Text: `{"data":[{"value":1}]}`}}}, nil
}
func nativeRegistryFixture() (*Registry, *nativeWireFixture, *gating.Principal) {
	pin := windowprotocol.ComponentBinding{Kind: "linked", ID: "fixture/pkg/Read", Revision: "artifact-v1", ContentFingerprint: identity.ContentFingerprint([]byte("binary")), SchemaFingerprint: identity.ContentFingerprint([]byte("schema"))}
	client := &nativeWireFixture{binding: pin}
	principal := &gating.Principal{Facts: authz.Facts{Subject: "user", Issuer: "issuer", Tenant: "tenant", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Minute)}, AccountID: "account", IdentityRevision: "identity"}
	principal.Facts.Entities = []authz.Entity{{Type: "document", ID: "own"}}
	principal.Facts.EntityPermissions = []authz.EntityPermission{{Type: "document", ID: "own", Permissions: []string{"read"}}}
	reg := &Registry{mgr: &executeAuthManagerStub{client: client}, internal: map[string]mcpclient.Interface{}, cache: map[string]*toolCacheEntry{}, recentResults: map[string]map[string]recentItem{}}
	reg.SetComponentProducerClassifier(func(service string) bool { return service == "native" })
	reg.SetComponentAuthorityResolver(func(context.Context) (gating.Principal, error) { return *principal, nil })
	return reg, client, principal
}
func TestNativeComponentRegistryPreservesWireAndDirectOperationBinding(t *testing.T) {
	reg, client, _ := nativeRegistryFixture()
	out, err := reg.Execute(context.Background(), "native/read", map[string]interface{}{"filter": "own"})
	if err != nil || !json.Valid([]byte(out)) || client.calls != 1 || !client.seenMeta {
		t.Fatalf("direct native operation not bound: %v", err)
	}
	ctx := requestctx.WithResolvedResource(context.Background(), identity.ResolvedResource{URI: "report://scope/read"})
	if _, err := reg.Execute(ctx, "native/read", nil); err == nil || client.calls != 1 {
		t.Fatal("resource call auto-selected current component")
	}
	if _, err := reg.ExecuteNativeComponent(ctx, "native", "read", client.binding, nil); err != nil || client.calls != 2 {
		t.Fatalf("approved resource component not dispatched: %v", err)
	}
}
func TestNativeComponentRegistryDeniesPostCallDriftAndAuthorityChange(t *testing.T) {
	for _, mode := range []string{"revision", "role", "role-in-place", "flat-entity-in-place", "permission-in-place", "expiry", "account", "verified-context", "action", "cancellation", "final-observation-expiry"} {
		t.Run(mode, func(t *testing.T) {
			reg, client, principal := nativeRegistryFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			actionAllowed := true
			reg.authorizationGuard = func(context.Context, string, map[string]interface{}) error {
				if !actionAllowed {
					return fmt.Errorf("action revoked")
				}
				return nil
			}
			if mode == "final-observation-expiry" {
				client.duringObservation = func(_ int) {
					if client.calls > 0 {
						principal.Facts.ValidUntil = time.Now().Add(-time.Second)
					}
				}
			}
			client.during = func() {
				switch mode {
				case "revision":
					client.binding.Revision = "artifact-v2"
				case "role":
					principal.Facts.Roles = nil
				case "role-in-place":
					principal.Facts.Roles[0] = "revoked"
				case "flat-entity-in-place":
					principal.Facts.Entities[0].ID = "other"
				case "permission-in-place":
					principal.Facts.EntityPermissions[0].Permissions[0] = "revoked"
				case "verified-context":
					principal.Facts.AuthorityRevision = "changed"
				case "action":
					actionAllowed = false
				case "cancellation":
					cancel()
				case "expiry":
					principal.Facts.ValidUntil = time.Now().Add(-time.Second)
				case "account":
					principal.AccountID = "other"
				}
			}
			out, err := reg.Execute(ctx, "native/read", nil)
			if err == nil || out != "" || client.calls != 1 {
				t.Fatal("native result escaped post-call guard")
			}
		})
	}
}
