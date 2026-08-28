package auth

import (
	"strings"
	"sync"
)

// MCPAuthChangeEvent describes a delegated MCP credential change (link,
// disconnect, invalid_grant). Subscribers evict caches only — the event never
// changes EffectiveUserID, sessions or the workspace provider.
type MCPAuthChangeEvent struct {
	// Kind is one of "linked", "disconnected".
	Kind string
	// CanonicalUserID keys credential storage and resolver cooldowns.
	CanonicalUserID string
	// EffectiveUserID keys the MCP manager client pool.
	EffectiveUserID string
	ServerName      string
	ProviderRef     string
	StorageKey      string
}

// mcpAuthChangeBus is the process-wide listener registry connecting the
// hosted link endpoints to caches owned elsewhere (MCP manager client pool,
// delegated resolver cooldowns, discovery failure state). It mirrors the
// shared inline-provider overlay: cross-pod visibility comes from the shared
// store, not from this bus.
type mcpAuthChangeBus struct {
	mu        sync.RWMutex
	listeners []func(MCPAuthChangeEvent)
}

var sharedMCPAuthChangeBus = &mcpAuthChangeBus{}

// RegisterMCPAuthChangeListener subscribes to delegated MCP credential
// changes. Wiring code (e.g. the executor builder) registers MCP manager pool
// eviction here.
func RegisterMCPAuthChangeListener(listener func(MCPAuthChangeEvent)) {
	if listener == nil {
		return
	}
	sharedMCPAuthChangeBus.mu.Lock()
	sharedMCPAuthChangeBus.listeners = append(sharedMCPAuthChangeBus.listeners, listener)
	sharedMCPAuthChangeBus.mu.Unlock()
}

// NotifyMCPAuthChange publishes a credential change to every listener.
func NotifyMCPAuthChange(event MCPAuthChangeEvent) {
	if strings.TrimSpace(event.CanonicalUserID) == "" && strings.TrimSpace(event.EffectiveUserID) == "" {
		return
	}
	sharedMCPAuthChangeBus.mu.RLock()
	listeners := make([]func(MCPAuthChangeEvent), len(sharedMCPAuthChangeBus.listeners))
	copy(listeners, sharedMCPAuthChangeBus.listeners)
	sharedMCPAuthChangeBus.mu.RUnlock()
	for _, listener := range listeners {
		listener(event)
	}
}
