package tool

import (
	"context"
	"fmt"

	internal "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/mcp/manager"
	toolprotection "github.com/viant/agently-core/protocol/tool/protection"
	svc "github.com/viant/agently-core/protocol/tool/service"
)

// SetAuthorizationGuard installs a pre-dispatch backend action check on the
// default registry. Custom registries must expose the same setter explicitly.
func SetAuthorizationGuard(reg Registry, guard func(context.Context, string, map[string]interface{}) error) bool {
	type setter interface {
		SetAuthorizationGuard(func(context.Context, string, map[string]interface{}) error)
	}
	target, ok := reg.(setter)
	if !ok {
		return false
	}
	target.SetAuthorizationGuard(guard)
	return true
}

// SetResultReuseDisabled prevents cached tool results from crossing account
// contexts in an authz host. Custom registries must support this explicitly.
func SetResultReuseDisabled(reg Registry, disabled bool) bool {
	type setter interface{ SetResultReuseDisabled(bool) }
	target, ok := reg.(setter)
	if !ok {
		return false
	}
	target.SetResultReuseDisabled(disabled)
	return true
}

// NewDefaultRegistry constructs the default MCP-backed tool registry with built-ins.
func NewDefaultRegistry(mgr *manager.Manager) (Registry, error) { return internal.NewWithManager(mgr) }

// SetExecutionProtection installs a guard when reg is the standard concrete registry.
func SetExecutionProtection(reg Registry, guard toolprotection.Guard) bool {
	type setter interface{ SetExecutionProtection(toolprotection.Guard) }
	target, ok := reg.(setter)
	if !ok {
		return false
	}
	target.SetExecutionProtection(guard)
	return true
}

// InjectVirtualAgentTools exposes agents as virtual tools when supported by the registry implementation.
func InjectVirtualAgentTools(reg Registry, agents []*agent.Agent, domain string) {
	type injector interface{ InjectVirtualAgentTools([]*agent.Agent, string) }
	if v, ok := reg.(injector); ok {
		v.InjectVirtualAgentTools(agents, domain)
	}
}

// AddInternalService attempts to register a service as an internal MCP client on the default registry.
func AddInternalService(reg Registry, s svc.Service) error {
	type adder interface{ AddInternalService(s svc.Service) error }
	if v, ok := reg.(adder); ok {
		return v.AddInternalService(s)
	}
	if reg == nil {
		return fmt.Errorf("tool registry is nil")
	}
	if s == nil {
		return fmt.Errorf("internal service is nil")
	}
	return fmt.Errorf("tool registry does not support internal service registration for %q", s.Name())
}
