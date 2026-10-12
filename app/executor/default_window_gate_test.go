package executor

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/manager"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/agently-core/workspace"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type defaultWindowFacts struct{ roles []string }

func (r *defaultWindowFacts) Resolve(context.Context, *permittedview.Request) (*permittedview.Snapshot, error) {
	return &permittedview.Snapshot{AuthorizationVersion: "current", ExpiresAt: time.Now().Add(time.Minute), Principal: map[string]any{"roles": r.roles}}, nil
}
func TestDefaultWindowOriginalApplicationGateUsesCurrentFacts(t *testing.T) {
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	defer workspace.SetRoot(old)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "extension/forge/windows"), 0700))
	for _, key := range []string{"public", "protected"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, "extension/forge/windows", key+".yaml"), []byte("id: "+key+"\nwindowKey: "+key+"\nview: {content: {id: "+key+"}}\n"), 0600))
	}
	layout := []byte("applications:\n - id: public\n   title: Public\n   action: {type: window, windowKey: public}\n - id: protected\n   title: Protected\n   authorization: {resourceType: application}\n   visibleWhen: {source: authorization, field: principal.roles, contains: reader}\n   action: {type: window, windowKey: protected}\n")
	facts := &defaultWindowFacts{}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	rt := &Runtime{MCPManager: mgr, UIBridge: forge.NewService(&forge.Config{}), PermittedResolver: facts}
	b := NewBuilder().WithNativeLayoutDefault(layout)
	require.NoError(t, b.configurePrimitiveProviders(rt))
	defer rt.PrimitiveProviders.Close()
	_, err = resources.NewWindowViewReader(rt.PrimitiveProviders).LoadAll(context.Background())
	require.NoError(t, err)
	_, err = rt.UIBridge.WindowDefinitionGet(context.Background(), &forge.WindowDefinitionGetInput{WindowID: "protected"})
	require.Error(t, err)
	facts.roles = []string{"reader"}
	got, err := rt.UIBridge.WindowDefinitionGet(context.Background(), &forge.WindowDefinitionGetInput{WindowID: "protected"})
	require.NoError(t, err)
	require.NotNil(t, got.Definition)
	facts.roles = nil
	_, err = rt.UIBridge.WindowDefinitionGet(context.Background(), &forge.WindowDefinitionGetInput{WindowID: "protected"})
	require.Error(t, err)
}
