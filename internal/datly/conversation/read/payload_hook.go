package read

import (
	"context"

	"github.com/viant/agently-core/internal/datly/payload/reference"
)

// OnFetch presents embedded payloads through the canonical payload read hook.
// The database string still contains the original bytes at this point.
func (p *ModelCallStreamPayloadView) OnFetch(ctx context.Context) error {
	if p.InlineBody == nil {
		return nil
	}
	body := []byte(*p.InlineBody)
	payload := &reference.PayloadView{InlineBody: &body, Compression: p.Compression}
	if err := payload.OnFetch(ctx); err != nil {
		return err
	}
	decoded := string(*payload.InlineBody)
	p.InlineBody = &decoded
	p.Compression = payload.Compression
	return nil
}
