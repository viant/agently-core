package workspace

import (
	"context"
	"encoding/json"
)

// LayoutMenuRead contains already authorized visible applications and a final
// exact-pin check. Revision identifies the composed delivery, not source bytes.
type LayoutMenuRead struct {
	Applications json.RawMessage
	Revision     string
	Revalidate   func(context.Context) error
	Close        func() error
}
type LayoutMenuReader func(context.Context) (*LayoutMenuRead, error)

// SetLayoutMenuReader explicitly enables bounded menu delivery. A configured
// denial never falls back to the unfiltered layout applications.
func (h *MetadataHandler) SetLayoutMenuReader(reader LayoutMenuReader) {
	if h != nil {
		h.layoutMenuReader = reader
	}
}
