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
	for _, run := range i.Runs {
		if run == nil {
			continue
		}
		if run.Has == nil {
			run.Has = &RunHas{}
		}
		if _, ok := i.CurByID[run.Id]; !ok {
			if run.Status == "" {
				run.SetStatus("pending")
			}
			if run.ConversationKind == nil {
				run.SetConversationKind("interactive")
			}
			if run.Attempt == nil {
				run.SetAttempt(1)
			}
			if run.Iteration == nil {
				run.SetIteration(0)
			}
			if run.CreatedAt == nil {
				run.SetCreatedAt(time.Now())
			}
		}
	}
	return nil
}

func (i *Input) indexSlice() {
	i.CurByID = map[string]*MutableRunView{}
	for _, it := range i.Cur {
		if it != nil {
			i.CurByID[it.Id] = it
		}
	}
}
