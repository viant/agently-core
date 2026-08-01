package tool

import (
	"context"
	"testing"

	"github.com/viant/agently-core/protocol/mcp/manager"
	toolprotection "github.com/viant/agently-core/protocol/tool/protection"
)

type factoryProtectionGuard struct{}

func (factoryProtectionGuard) IsProtected(name string) bool {
	return name == "delivery/send" || name == "delivery:send" || name == "delivery/send|data.value"
}

func (factoryProtectionGuard) Claim(context.Context, string, map[string]interface{}) (toolprotection.Claim, error) {
	return toolprotection.Claim{}, nil
}

func (factoryProtectionGuard) Finish(context.Context, toolprotection.Claim, toolprotection.State) {}

func TestSetExecutionProtectionWiresDefaultRegistryOnlyWhenExplicit(t *testing.T) {
	mgr, err := manager.New(nil)
	if err != nil {
		t.Fatalf("manager.New() error = %v", err)
	}
	registry, err := NewDefaultRegistry(mgr)
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}
	resolver, ok := registry.(RetryResolver)
	if !ok {
		t.Fatal("default registry does not expose retry policy resolution")
	}
	if _, configured := resolver.ToolRetryable("delivery/send"); configured {
		t.Fatal("default registry protected an unconfigured tool")
	}
	if !SetExecutionProtection(registry, factoryProtectionGuard{}) {
		t.Fatal("SetExecutionProtection() did not recognize the default registry")
	}
	for _, alias := range []string{"delivery/send", "delivery:send", "delivery/send|data.value"} {
		retryable, configured := resolver.ToolRetryable(alias)
		if retryable || !configured {
			t.Fatalf("ToolRetryable(%q) = %v, %v; want false, true", alias, retryable, configured)
		}
	}
}
