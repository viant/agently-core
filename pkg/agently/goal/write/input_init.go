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
	now := time.Now()
	for _, goal := range i.Goals {
		if goal == nil {
			continue
		}
		if goal.Has == nil {
			goal.Has = &GoalHas{}
		}
		if _, ok := i.CurGoalById[goal.Id]; !ok {
			if !goal.Has.CreatedAt {
				goal.SetCreatedAt(now)
			}
			if !goal.Has.TokensUsed {
				goal.SetTokensUsed(0)
			}
			if !goal.Has.TimeUsedSeconds {
				goal.SetTimeUsedSeconds(0)
			}
			if !goal.Has.AutonomousTurnsUsed {
				goal.SetAutonomousTurnsUsed(0)
			}
			if !goal.Has.ConsecutiveNoProgress {
				goal.SetConsecutiveNoProgress(0)
			}
			if !goal.Has.LastContinuationFingerprint {
				goal.SetLastContinuationFingerprint("")
			}
		}
		if !goal.Has.UpdatedAt {
			goal.SetUpdatedAt(now)
		}
	}
	return nil
}

func (i *Input) indexSlice() { i.CurGoalById = GoalSlice(i.CurGoals).IndexById() }
