package agent

import (
	"context"
	"fmt"

	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/mcpname"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolasyncconfig "github.com/viant/agently-core/protocol/tool/asyncconfig"
)

type mcpAppToolPolicyKey struct{}
type mcpAppToolPolicy struct {
	service *Service
	name    string
}

// MCPAppToolPolicy resolves the current configured union of bundles and
// explicit tools without accepting guest-selected tools or cached authority.
func (s *Service) MCPAppToolPolicy(ctx context.Context, ag *agentmdl.Agent, name string) ([]string, error) {
	_, bundles, err := s.resolveMCPAppToolSurface(ctx, ag, name)
	return bundles, err
}

// PrepareMCPAppToolContext is a trusted host handoff. The private marker binds
// one canonical tool on this Service to the current full configured surface.
// Its native bundle approval/prompt/async metadata is applied before the guest
// path runs; wire inputs cannot install or broaden this marker.
func (s *Service) PrepareMCPAppToolContext(ctx context.Context, ag *agentmdl.Agent, name string) (context.Context, error) {
	surface, _, err := s.resolveMCPAppToolSurface(ctx, ag, name)
	if err != nil {
		return nil, err
	}
	ctx = toolapprovalqueue.WithState(ctx)
	ctx = toolasyncconfig.WithState(ctx)
	s.applyResolvedToolSurfaceMetadata(ctx, surface)
	return context.WithValue(ctx, mcpAppToolPolicyKey{}, mcpAppToolPolicy{service: s, name: mcpname.Canonical(name)}), nil
}

func (s *Service) resolveMCPAppToolSurface(ctx context.Context, ag *agentmdl.Agent, name string) (*resolvedToolSurface, []string, error) {
	if s == nil || s.registry == nil || ag == nil {
		return nil, nil, fmt.Errorf("native MCP app agent policy is unavailable")
	}

	control, err := s.resolveToolControl(ctx, &QueryInput{Agent: ag})
	if err != nil {
		return nil, nil, err
	}
	surface := &resolvedToolSurface{Complete: true}
	if len(control.Bundles) > 0 {
		bundleSurface, e := s.resolveBundleResult(ctx, control.Bundles)
		if e != nil {
			return nil, nil, e
		}
		surface.Definitions = append(surface.Definitions, bundleSurface.Definitions...)
		surface.ApprovalByID = bundleSurface.ApprovalByID
		surface.AsyncByID = bundleSurface.AsyncByID
		surface.Complete = bundleSurface.Complete
	}
	surface.Definitions, err = s.appendToolSelections(ctx, surface.Definitions, control.Tools)
	if err != nil {
		return nil, nil, err
	}
	surface.Definitions = dedupeDefinitions(surface.Definitions)
	if resolver, ok := s.registry.(interface {
		ResolveMCPIdentity(context.Context, string) (string, string, bool, error)
	}); ok {
		server, method, found, err := resolver.ResolveMCPIdentity(ctx, name)
		if err != nil {
			return nil, nil, err
		}
		if !found || server+"/"+method != name {
			return nil, nil, fmt.Errorf("MCP app tool does not identify an exact current advertised native tool")
		}
	}
	for _, definition := range surface.Definitions {
		if mcpname.Canonical(definition.Name) == mcpname.Canonical(name) {
			return surface, append([]string(nil), control.Bundles...), nil
		}
	}
	return nil, nil, fmt.Errorf("MCP app tool is outside the configured agent surface")
}
