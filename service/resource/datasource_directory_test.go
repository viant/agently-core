package resource

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
)

func TestDatasourceDirectoryPreservesNativeDescriptorAndRejectsUnknownFields(t *testing.T) {
	actor := identity.VerifiedActor{Subject: "alice", Issuer: "fixture", TenantID: "team", AccountID: "account", IdentityRevision: "one", ValidUntil: time.Now().Add(time.Hour)}
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{{"valid", "id: authored-id\nselectors: {data: reply.rows}\nbackend:\n  kind: mcp_tool\n  service: arbitrary\n  method: original_method\n  pinned: {identity: 9007199254740993}\ncache: {ttl: 2m}\n", false}, {"fallback", "selectors: {data: reply.rows}\nbackend: {kind: mcp_tool, service: arbitrary, method: original_method}\n", false}, {"unknown", "id: source\nprivatePolicy: protected\nbackend: {kind: mcp_tool, service: arbitrary, method: original_method}\n", true}, {"unknownBackend", "id: source\nbackend: {kind: mcp_tool, service: arbitrary, method: original_method, permissions: granted}\n", true}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "sources"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(root, "sources/source.yaml"), []byte(tc.body), 0600))
			bindings, err := ExtensionBindings(context.Background(), root, []ExtensionDirectory{DatasourceDirectory("team", "sources")}, func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) (identity.ResourceRevisionPolicy, error) {
				return localFixturePolicy{actor: actor}, nil
			})
			require.NoError(t, err)
			require.Len(t, bindings, 1)
			resolver, err := bindings[0].Resolver(context.Background(), actor, "read")
			require.NoError(t, err)
			pin, err := resolver.Resolve(context.Background(), identity.ResourceRef{URI: bindings[0].URI})
			if tc.bad {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			raw, _, err := resolver.ReadResolved(context.Background(), *pin)
			require.NoError(t, err)
			descriptor, err := DecodeDatasourceDefinition(raw)
			require.NoError(t, err)
			require.Equal(t, "arbitrary", descriptor.Backend.Service)
			require.Equal(t, "original_method", descriptor.Backend.Method)
			if tc.name == "valid" {
				require.Equal(t, "authored-id", descriptor.ID)
				require.Equal(t, json.Number("9007199254740993"), descriptor.Backend.Pinned["identity"])
			} else {
				require.Equal(t, "source", descriptor.ID)
			}
		})
	}
}
