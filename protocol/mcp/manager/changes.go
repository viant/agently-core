package manager

import (
	"context"
	"github.com/viant/jsonrpc"
	protoclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
	"sync/atomic"
)

// OnCatalogChange subscribes host registries to upstream list-change events.
// Listeners must not perform network I/O from the transport callback.
func (m *Manager) OnCatalogChange(listener func(string)) func() {
	if listener == nil {
		return func() {}
	}
	m.mu.Lock()
	if m.changeListeners == nil {
		m.changeListeners = map[uint64]func(string){}
	}
	m.changeListenerID++
	id := m.changeListenerID
	m.changeListeners[id] = listener
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.changeListeners, id); m.mu.Unlock() }
}

type changeHandler struct {
	protoclient.Handler
	manager  *Manager
	server   string
	sequence int64
}

func (h *changeHandler) OnNotification(ctx context.Context, n *jsonrpc.Notification) {
	if n != nil {
		switch n.Method {
		case "notifications/resources/list_changed", "notifications/resources/updated", "notifications/tools/list_changed", "notifications/prompts/list_changed", "notifications/skills/list_changed":
			h.manager.mu.Lock()
			listeners := make([]func(string), 0, len(h.manager.changeListeners))
			for _, listener := range h.manager.changeListeners {
				listeners = append(listeners, listener)
			}
			h.manager.mu.Unlock()
			for _, listener := range listeners {
				listener(h.server)
			}
		}
	}
	if h.Handler != nil {
		h.Handler.OnNotification(ctx, n)
	}
}
func (h *changeHandler) Implements(method string) bool {
	return h.Handler != nil && h.Handler.Implements(method)
}
func (h *changeHandler) Init(ctx context.Context, c *schema.ClientCapabilities) {
	if h.Handler != nil {
		h.Handler.Init(ctx, c)
	}
}
func (h *changeHandler) Notify(ctx context.Context, n *jsonrpc.Notification) error {
	if h.Handler != nil {
		return h.Handler.Notify(ctx, n)
	}
	return nil
}
func (h *changeHandler) NextRequestID() jsonrpc.RequestId {
	if h.Handler != nil {
		return h.Handler.NextRequestID()
	}
	return atomic.AddInt64(&h.sequence, 1)
}
func (h *changeHandler) LastRequestID() jsonrpc.RequestId {
	if h.Handler != nil {
		return h.Handler.LastRequestID()
	}
	return atomic.LoadInt64(&h.sequence)
}
func (h *changeHandler) ListRoots(ctx context.Context, r *jsonrpc.TypedRequest[*schema.ListRootsRequest]) (*schema.ListRootsResult, *jsonrpc.Error) {
	if h.Handler != nil {
		return h.Handler.ListRoots(ctx, r)
	}
	return nil, jsonrpc.NewMethodNotFound("client roots unavailable", nil)
}
func (h *changeHandler) CreateMessage(ctx context.Context, r *jsonrpc.TypedRequest[*schema.CreateMessageRequest]) (*schema.CreateMessageResult, *jsonrpc.Error) {
	if h.Handler != nil {
		return h.Handler.CreateMessage(ctx, r)
	}
	return nil, jsonrpc.NewMethodNotFound("client sampling unavailable", nil)
}
func (h *changeHandler) Elicit(ctx context.Context, r *jsonrpc.TypedRequest[*schema.ElicitRequest]) (*schema.ElicitResult, *jsonrpc.Error) {
	if h.Handler != nil {
		return h.Handler.Elicit(ctx, r)
	}
	return nil, jsonrpc.NewMethodNotFound("client elicitation unavailable", nil)
}
