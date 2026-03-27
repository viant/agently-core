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
	for _, user := range i.Users {
		if user == nil {
			continue
		}
		if _, ok := i.CurUserById[user.Id]; !ok {
			if user.CreatedAt == nil {
				user.SetCreatedAt(now)
			}
			if user.Disabled == nil {
				user.SetDisabled(0)
			}
			continue
		}
		if user.UpdatedAt == nil {
			user.SetUpdatedAt(now)
		}
	}
	return nil
}

func (i *Input) indexSlice() {
	i.CurUserById = map[string]*User{}
	for _, user := range i.CurUser {
		if user != nil {
			i.CurUserById[user.Id] = user
		}
	}
}
