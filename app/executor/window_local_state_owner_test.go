package executor

import (
	"context"
	"github.com/viant/agently-core/service/auth"
	ui "github.com/viant/agently-core/service/primitiveprovider"
	"testing"
)

func TestDefaultLocalStateOwnerWiresOnlyVerifiedIngressProjection(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := &ui.Config{}
		runtime := &Runtime{UIBridge: ui.NewService(cfg), AuthConfig: &auth.Config{Enabled: enabled}}
		NewBuilder().configureDefaultUIOwner(runtime)
		if (cfg.LocalStateOwnerResolver != nil) != enabled {
			t.Fatal("host callback registration mismatch")
		}
		if enabled {
			owner, err := cfg.LocalStateOwnerResolver(auth.InjectUser(context.Background(), "unverified-injection"))
			if err != nil || owner != nil {
				t.Fatal("public injection created owner")
			}
		}
	}
}
