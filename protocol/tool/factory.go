package tool

import (
	"fmt"

	internal "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/mcp/manager"
	svc "github.com/viant/agently-core/protocol/tool/service"
)

// NewDefaultRegistry constructs the default MCP-backed tool registry with built-ins.
func NewDefaultRegistry(mgr *manager.Manager) (Registry, error) { return internal.NewWithManager(mgr) }

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
