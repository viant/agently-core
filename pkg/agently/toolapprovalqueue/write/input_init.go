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
	i.indexSlice()
	now := time.Now().UTC()
	for _, q := range i.Queues {
		if q == nil {
			continue
		}
		if q.Has == nil {
			q.Has = &ToolApprovalQueueHas{}
		}
		current, exists := i.CurByID[q.Id]
		if exists {
			if !q.Has.UserId {
				q.UserId = current.UserId
			}
			if !q.Has.ToolName {
				q.ToolName = current.ToolName
			}
			if !q.Has.Arguments {
				q.Arguments = append([]byte(nil), current.Arguments...)
			}
			if !q.Has.Status {
				q.Status = current.Status
			}
			if q.CreatedAt == nil {
				q.CreatedAt = current.CreatedAt
			}
		} else {
			if !q.Has.Status || q.Status == "" {
				q.SetStatus("pending")
			}
			if q.CreatedAt == nil {
				q.SetCreatedAt(now)
			}
		}
		if q.UpdatedAt == nil {
			q.SetUpdatedAt(now)
		}
	}
	return nil
}

func (i *Input) indexSlice() {
	i.CurByID = map[string]*ToolApprovalQueue{}
	for _, item := range i.Cur {
		if item == nil || item.Id == "" {
			continue
		}
		i.CurByID[item.Id] = item
	}
}
