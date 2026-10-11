package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
)

func TestPublicNativeMenuStartupPrecedenceAndProtectedGate(t *testing.T) {
	for _, authored := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "authored"}[authored], func(t *testing.T) {
			root := t.TempDir()
			old := workspace.Root()
			workspace.SetRoot(root)
			defer workspace.SetRoot(old)
			raw := []byte("applications:\n  - id: public\n    label: Embedded\n    action: {type: window, windowKey: chat/new}\n  - id: secret\n    label: Secret\n    authorization: {resourceType: application}\n    visibleWhen: {source: authorization, field: principal.roles, contains: reader}\n    action: {type: window, windowKey: private}\n")
			if authored {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "ui"), 0700))
				require.NoError(t, os.WriteFile(filepath.Join(root, "ui/layout.yaml"), []byte("applications:\n  - id: authored\n    label: Authored\n    action: {type: window, windowKey: chat/new}\n"), 0600))
			}
			builder := NewBuilder().WithNativeLayoutDefault(raw)
			raw[0] = 'X'
			mgr, err := manager.New(nil)
			require.NoError(t, err)
			rt := &Runtime{MCPManager: mgr}
			require.NoError(t, builder.configurePrimitiveProviders(rt))
			defer rt.PrimitiveProviders.Close()
			require.NotNil(t, rt.NativeLayoutSource)
			require.NoError(t, rt.NativeLayoutSource.Check(context.Background()))
			uri, _ := identity.ParseResourceURI("menu://workspace/layout")
			connection, err := rt.PrimitiveProviders.ConnectionForResource(context.Background(), uri, resources.InternalProviderIdentity)
			require.NoError(t, err)
			got, err := resources.Get[json.RawMessage](context.Background(), rt.PrimitiveProviders, connection, identity.ResourceRef{URI: uri.String()}, nil)
			require.NoError(t, err)
			require.NotContains(t, string(got.Definition), "Secret")
			if authored {
				require.Contains(t, string(got.Definition), "Authored")
				require.NotContains(t, string(got.Definition), "Embedded")
				require.Equal(t, "ui/layout.yaml", rt.NativeLayoutSource.Source)
				require.NoError(t, os.WriteFile(filepath.Join(root, "ui/layout.yaml"), []byte("applications: []"), 0600))
				require.Error(t, rt.NativeLayoutSource.Check(context.Background()))
			} else {
				require.Contains(t, string(got.Definition), "Embedded")
				require.Equal(t, "embedded", rt.NativeLayoutSource.Source)
				require.NoError(t, os.MkdirAll(filepath.Join(root, "ui"), 0700))
				require.NoError(t, os.WriteFile(filepath.Join(root, "ui/layout.yaml"), []byte("applications: []"), 0600))
				require.Error(t, rt.NativeLayoutSource.Check(context.Background()))
				_, err = resources.Get[json.RawMessage](context.Background(), rt.PrimitiveProviders, connection, identity.ResourceRef{URI: uri.String()}, nil)
				require.Error(t, err, "later authored source cannot replace captured embedded menu without restart")
			}
		})
	}
}
