package reference

import (
	"bytes"
	"compress/gzip"
	"context"
)

// OnFetch preserves the legacy binary-payload presentation contract.
func (p *PayloadView) OnFetch(ctx context.Context) error {
	if p.InlineBody == nil {
		return nil
	}
	inline := []byte(*p.InlineBody)
	if p.Compression == "gzip" {
		decompressor, err := gzip.NewReader(bytes.NewReader(inline))
		if err == nil {
			var decoded bytes.Buffer
			_, _ = decoded.ReadFrom(decompressor)
			_ = decompressor.Close()
			inline = decoded.Bytes()
			p.Compression = ""
		}
	}
	*p.InlineBody = bytes.TrimSpace(inline)
	return nil
}
