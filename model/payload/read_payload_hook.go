package payload

import (
	"context"
	"github.com/viant/agently-core/internal/datly/payload/reference"
)

// OnFetch shares the canonical byte-preserving decoder with native readers.
func (p *PayloadView) OnFetch(ctx context.Context) error {
	payload := &reference.PayloadView{Id: p.Id, InlineBody: p.InlineBody, Compression: p.Compression}
	if err := payload.OnFetch(ctx); err != nil {
		return err
	}
	p.InlineBody, p.Compression = payload.InlineBody, payload.Compression
	return nil
}
