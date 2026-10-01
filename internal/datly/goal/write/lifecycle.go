package write

import (
	context "context"
	"fmt"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
	"time"
)

// Lifecycle customizes role Input.Goals.
type Lifecycle struct{}

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *Goal, state xhandler.LifecycleContext[Goal, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if entity.Has == nil {
		entity.Has = &GoalHas{}
	}
	now := time.Now()
	if state.Previous == nil {
		if !entity.Has.CreatedAt {
			entity.SetCreatedAt(&now)
		}
		zero := int64(0)
		if !entity.Has.TokensUsed {
			entity.SetTokensUsed(&zero)
		}
		if !entity.Has.TimeUsedSeconds {
			entity.SetTimeUsedSeconds(&zero)
		}
		if !entity.Has.AutonomousTurnsUsed {
			entity.SetAutonomousTurnsUsed(&zero)
		}
		if !entity.Has.ConsecutiveNoProgress {
			entity.SetConsecutiveNoProgress(&zero)
		}
		empty := ""
		if !entity.Has.LastContinuationFingerprint {
			entity.SetLastContinuationFingerprint(&empty)
		}
	}
	if !entity.Has.UpdatedAt {
		entity.SetUpdatedAt(&now)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Goal, state xhandler.LifecycleContext[Goal, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if strings.TrimSpace(entity.Id) == "" {
		return fmt.Errorf("id is required")
	}
	if state.Previous == nil {
		for _, field := range []struct {
			name  string
			value *string
		}{
			{"conversation_id", entity.ConversationId}, {"objective", entity.Objective}, {"status", entity.Status},
		} {
			if field.value == nil || strings.TrimSpace(*field.value) == "" {
				return fmt.Errorf("%s is required for insert", field.name)
			}
		}
	}
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Goal, state xhandler.LifecycleContext[Goal, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Goal, state xhandler.LifecycleContext[Goal, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
