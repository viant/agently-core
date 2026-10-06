package reference

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
)

// OnFetch decodes storage compression without changing the payload bytes.
func (p *PayloadView) OnFetch(ctx context.Context) error {
	if p.InlineBody == nil {
		return nil
	}
	inline := []byte(*p.InlineBody)
	if p.Compression == "gzip" {
		decompressor, err := gzip.NewReader(bytes.NewReader(inline))
		if err != nil {
			return fmt.Errorf("decode gzip payload %q: %w", p.Id, err)
		}
		decoded, readErr := io.ReadAll(decompressor)
		closeErr := decompressor.Close()
		if readErr != nil {
			return fmt.Errorf("decode gzip payload %q: %w", p.Id, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close gzip payload %q: %w", p.Id, closeErr)
		}
		inline = decoded
		p.Compression = ""
	}
	*p.InlineBody = bytes.Clone(inline)
	return nil
}
