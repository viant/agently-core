package modelcall

import (
	"context"

	"github.com/viant/agently-core/runtime/evidence"
)

func (o *recorderObserver) finishEvidenceStream(ctx context.Context, messageID string) error {
	guard := evidence.PublicationFromContext(ctx)
	if guard == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return &evidence.Rejection{Cause: err}
	}
	remainder, err := guard.Stream(ctx, messageID, "", true)
	if err != nil {
		return &evidence.Rejection{Cause: err}
	}
	if remainder != "" {
		o.publishCanonicalStreamDeltaNow(ctx, []byte(remainder))
	}
	return nil
}
