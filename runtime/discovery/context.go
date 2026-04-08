package discovery

import (
	"context"
	"strings"
)

type modeKey struct{}

// Mode controls how tool discovery behaves for the current request.
type Mode struct {
	Scheduler     bool
	Strict        bool
	ScheduleID    string
	ScheduleRunID string
}

func WithMode(ctx context.Context, mode Mode) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	mode.ScheduleID = strings.TrimSpace(mode.ScheduleID)
	mode.ScheduleRunID = strings.TrimSpace(mode.ScheduleRunID)
	return context.WithValue(ctx, modeKey{}, mode)
}

func ModeFromContext(ctx context.Context) (Mode, bool) {
	if ctx == nil {
		return Mode{}, false
	}
	mode, ok := ctx.Value(modeKey{}).(Mode)
	if !ok {
		return Mode{}, false
	}
	return mode, true
}

func MergeMode(ctx context.Context, update Mode) context.Context {
	base, _ := ModeFromContext(ctx)
	base.Scheduler = base.Scheduler || update.Scheduler
	if update.Strict {
		base.Strict = true
	}
	if v := strings.TrimSpace(update.ScheduleID); v != "" {
		base.ScheduleID = v
	}
	if v := strings.TrimSpace(update.ScheduleRunID); v != "" {
		base.ScheduleRunID = v
	}
	return WithMode(ctx, base)
}
