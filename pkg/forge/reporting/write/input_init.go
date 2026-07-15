package write

import (
	"context"
	"time"

	"github.com/viant/xdatly/handler"
)

func (i *Input) Init(ctx context.Context, sess handler.Session, output *Output) error {
	if err := sess.Stater().Bind(ctx, i); err != nil {
		return err
	}
	if i.Artifact == nil {
		return nil
	}
	now := time.Now().UTC()
	if i.Artifact.CreatedAt.IsZero() {
		i.Artifact.CreatedAt = now
	}
	if i.Artifact.UpdatedAt == nil {
		i.Artifact.UpdatedAt = &now
	}
	return nil
}
