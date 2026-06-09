package write

type MutableGoalViewOption func(*MutableGoalView)

func NewMutableGoalView(opts ...MutableGoalViewOption) *MutableGoalView {
	ret := &MutableGoalView{Has: &GoalHas{}}
	for _, opt := range opts {
		if opt != nil {
			opt(ret)
		}
	}
	return ret
}

func NewMutableGoalViews(rows ...*MutableGoalView) *MutableGoalViews {
	return &MutableGoalViews{Goals: rows}
}

func WithGoalID(v string) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetId(v) }
}

func WithGoalConversationID(v string) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetConversationID(v) }
}

func WithGoalObjective(v string) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetObjective(v) }
}

func WithGoalStatus(v string) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetStatus(v) }
}

func WithGoalStatusReason(v string) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetStatusReason(v) }
}

func WithGoalPauseReason(v string) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetPauseReason(v) }
}

func WithGoalControllerSpec(v string) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetControllerSpec(v) }
}

func WithGoalTokenBudget(v int64) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetTokenBudget(v) }
}

func WithGoalTokensUsed(v int64) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetTokensUsed(v) }
}

func WithGoalTimeUsedSeconds(v int64) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetTimeUsedSeconds(v) }
}

func WithGoalAutonomousTurnsUsed(v int64) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetAutonomousTurnsUsed(v) }
}

func WithGoalConsecutiveNoProgress(v int64) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetConsecutiveNoProgress(v) }
}

func WithGoalLastContinuationFingerprint(v string) MutableGoalViewOption {
	return func(g *MutableGoalView) { g.SetLastContinuationFingerprint(v) }
}
