package manager

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	authctx "github.com/viant/agently-core/internal/auth"
)

type resourceCatalogScope struct {
	server   string
	receiver [32]byte
}

// resourceReceiver uses trusted host context, never notification payload claims.
// An absent principal is conservative broadcast scope. Credentials and sessions
// remain in snapshot keys; invalidation follows the same owner's variants.
func (m *Manager) resourceReceiver(ctx context.Context) [32]byte {
	if canonical := authctx.CanonicalUserID(ctx); canonical != "" {
		return sha256.Sum256([]byte("canonical:" + canonical))
	}
	user := authctx.EffectiveUserID(ctx)
	if m.userIDFn != nil {
		user = m.userIDFn(ctx)
	}
	if user == "" {
		return [32]byte{}
	}
	raw, _ := json.Marshal([]string{user, authctx.EffectiveUserID(ctx), authctx.Provider(ctx)})
	return sha256.Sum256(raw)
}

// ResourceCatalogGeneration fences metadata only, independently of transport
// and tools/list generations. Native resource writes therefore cannot change
// the connection identity of their own successful action.
func (m *Manager) ResourceCatalogGeneration(ctx context.Context, server string) [2]uint64 {
	receiver := m.resourceReceiver(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resourceCatalogGenerationLocked(server, receiver)
}
func (m *Manager) resourceCatalogGenerationLocked(server string, receiver [32]byte) [2]uint64 {
	broadcast := m.resourceCatalogGeneration[resourceCatalogScope{server: server}]
	if receiver == ([32]byte{}) {
		return [2]uint64{broadcast, 0}
	}
	return [2]uint64{broadcast, m.resourceCatalogGeneration[resourceCatalogScope{server: server, receiver: receiver}]}
}

// OnResourceCatalogChange reports immediate native resource invalidation.
// Listeners run outside the mutex and must not perform network I/O. Refresh is
// lazy: bursts share the existing VisibleCatalog population singleflight.
func (m *Manager) OnResourceCatalogChange(listener func(string)) func() {
	if listener == nil {
		return func() {}
	}
	m.mu.Lock()
	if m.resourceChangeListeners == nil {
		m.resourceChangeListeners = map[uint64]func(string){}
	}
	m.changeListenerID++
	id := m.changeListenerID
	m.resourceChangeListeners[id] = listener
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.resourceChangeListeners, id); m.mu.Unlock() }
}
func (m *Manager) notifyResourceCatalogChange(server string, receiver [32]byte, public bool) {
	if public {
		receiver = [32]byte{}
	}
	m.mu.Lock()
	if m.resourceCatalogGeneration == nil {
		m.resourceCatalogGeneration = map[resourceCatalogScope]uint64{}
	}
	m.resourceCatalogGeneration[resourceCatalogScope{server: server, receiver: receiver}]++
	for key, held := range m.visibleCatalogs {
		// Missing dependency metadata is conservative, never evidence of isolation.
		affected := held.sources == nil || held.sources[server]
		if affected && (receiver == ([32]byte{}) || held.receiver == receiver || held.receiver == ([32]byte{})) {
			delete(m.visibleCatalogs, key)
		}
	}
	listeners := make([]func(string), 0, len(m.resourceChangeListeners))
	for _, listener := range m.resourceChangeListeners {
		listeners = append(listeners, listener)
	}
	m.mu.Unlock()
	for _, listener := range listeners {
		listener(server)
	}
}

// WithCatalogSources tags the existing snapshot store with configured server
// dependencies. It does not change the stable owner/category mutation partition.
func (m *Manager) WithCatalogSources(ctx context.Context, partition VisibleCatalogPartition, names []string) VisibleCatalogPartition {
	sources := make(map[string]bool, len(names))
	for _, name := range names {
		sources[name] = true
	}
	partition.sources = &sources
	versions := make(map[string][2]uint64, len(names))
	receiver := m.resourceReceiver(ctx)
	m.mu.Lock()
	for _, name := range names {
		versions[name] = m.resourceCatalogGenerationLocked(name, receiver)
	}
	m.mu.Unlock()
	partition.sourceVersions = &versions
	return partition
}
func sameCatalogPartition(left, right VisibleCatalogPartition) bool {
	return left.manager == right.manager && left.owner == right.owner && left.category == right.category
}
