package write

import (
	"context"
	"time"

	"github.com/viant/xdatly/handler"
)

func (i *Input) Init(ctx context.Context, sess handler.Session, _ *Output) error {
	if err := sess.Stater().Bind(ctx, i); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, q := range i.Queues {
		if q == nil {
			continue
		}
		if q.Has == nil {
			q.Has = &ToolApprovalQueueHas{}
		}
		if !q.Has.Status || q.Status == "" {
			q.SetStatus("pending")
		}
		if q.CreatedAt == nil {
			q.SetCreatedAt(now)
		}
		if q.UpdatedAt == nil {
			q.SetUpdatedAt(now)
		}
	}
	return nil
}
