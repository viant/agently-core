package workspace

import "context"

// SetLayoutDecorator adds trusted host-owned destinations after resource-owned
// navigation has been admitted. It grants no access to those resources.
// Configure it before serving requests; a nil decorator preserves the layout.
func (h *MetadataHandler) SetLayoutDecorator(decorate func(context.Context, *Layout) error) {
	h.layoutDecorator = decorate
}
