package manager

import (
	"context"
	mcpclient "github.com/viant/mcp/client"
)

// IsCurrentClient checks one existing locator without creating, touching or
// renewing its session. It grants no authority: callers must independently
// verify current configuration, credentials and policy before using the client.
// An entry's underlying client is immutable; replacement always evicts the
// entry. Its creation epoch is immutable too, so exact entry/managed-pointer
// identity pins both the underlying client and its session epoch.
// Registered local clients use these same owner-scoped entries and epochs.
func (m *Manager) IsCurrentClient(ctx context.Context, conversation, name string, client mcpclient.Interface) bool {
	if m == nil || ctx == nil || ctx.Err() != nil {
		return false
	}
	held, ok := client.(*managedClient)
	if !ok || held == nil || held.mgr != m || held.serverName != name {
		return false
	}
	key := m.poolKey(ctx, conversation)
	if held.key != key {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.pool[key][name]
	return current != nil && current == held.entry && current.managed == held && current.client != nil && !current.evicted && !current.closed
}
