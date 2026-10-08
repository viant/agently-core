package manager

import (
	"context"
	"encoding/json"
	"fmt"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpclient "github.com/viant/mcp/client"
	"strings"
)

type localRegistration struct {
	options *mcpcfg.MCPClient
	factory func(context.Context) (mcpclient.Interface, error)
}

func (m *Manager) localRegistration(name string) (localRegistration, bool) {
	m.localMu.RLock()
	defer m.localMu.RUnlock()
	registration, ok := m.local[name]
	return registration, ok
}
func cloneLocalConfig(config *mcpcfg.MCPClient) *mcpcfg.MCPClient {
	raw, _ := json.Marshal(config)
	var copied mcpcfg.MCPClient
	_ = json.Unmarshal(raw, &copied)
	return &copied
}

// RegisterLocal reserves one exact connection name and uses ordinary SDK
// clients and the manager's existing per-user pools. Names may not shadow a
// configured external connection. The factory must not capture caller authority;
// each operation's request context must reach the local authenticated handler.
func (m *Manager) RegisterLocal(ctx context.Context, name string, options *mcpcfg.MCPClient, factory func(context.Context) (mcpclient.Interface, error)) error {
	if m == nil || ctx == nil || ctx.Err() != nil || name == "" || strings.TrimSpace(name) != name || options == nil || options.ClientOptions == nil || factory == nil {
		return fmt.Errorf("invalid local MCP registration")
	}
	if options.Auth != nil {
		return fmt.Errorf("local MCP registration cannot contain transport credentials")
	}
	if options.PrimitiveProviderIdentity == "" {
		return fmt.Errorf("local MCP registration requires declared provider identity")
	}
	// Require inventory when a provider exists: checking Options alone cannot
	// distinguish a missing entry from providers with default configurations.
	m.localMu.Lock()
	defer m.localMu.Unlock()
	if _, exists := m.local[name]; exists {
		return fmt.Errorf("duplicate local MCP connection %q", name)
	}
	if m.prov != nil {
		lister, ok := m.prov.(providerLister)
		if !ok {
			return fmt.Errorf("local MCP registration requires external connection inventory")
		}
		names, err := lister.Names(ctx)
		if err != nil {
			return err
		}
		for _, external := range names {
			if external == name {
				return fmt.Errorf("local MCP connection collides with configured provider %q", name)
			}
		}
	}
	if m.local == nil {
		m.local = map[string]localRegistration{}
	}
	copied := cloneLocalConfig(options)
	if copied.ClientOptions == nil {
		return fmt.Errorf("local MCP metadata must be serializable")
	}
	copied.Name = name
	copied.ToolsListVisibility = mcpcfg.ToolsListVisibilityPrivate
	m.mu.Lock()
	for _, pending := range m.inflight {
		if _, exists := pending[name]; exists {
			m.mu.Unlock()
			return fmt.Errorf("local MCP connection already has an inflight transport %q", name)
		}
	}
	for _, pool := range m.pool {
		if entry := pool[name]; entry != nil {
			m.mu.Unlock()
			return fmt.Errorf("local MCP connection already has a pooled transport %q", name)
		}
	}
	m.mu.Unlock()
	m.local[name] = localRegistration{options: copied, factory: factory}
	return nil
}

func (m *Manager) checkLocalCollision(ctx context.Context, name string) error {
	if m.prov == nil {
		return nil
	}
	lister, ok := m.prov.(providerLister)
	if !ok {
		return fmt.Errorf("local MCP connection inventory unavailable")
	}
	names, err := lister.Names(ctx)
	if err != nil {
		return err
	}
	for _, external := range names {
		if external == name {
			return fmt.Errorf("local MCP connection collides with configured provider %q", name)
		}
	}
	return nil
}
