package write

import "time"

var PackageName = "goal/write"

type Goal struct {
	Id              string     `sqlx:"id,primaryKey" validate:"required"`
	ConversationID  *string    `sqlx:"conversation_id" json:",omitempty"`
	Objective       *string    `sqlx:"objective" json:",omitempty"`
	Status          *string    `sqlx:"status" json:",omitempty"`
	StatusReason    *string    `sqlx:"status_reason" json:",omitempty"`
	PauseReason     *string    `sqlx:"pause_reason" json:",omitempty"`
	ControllerSpec  *string    `sqlx:"controller_spec" json:",omitempty"`
	TokenBudget     *int64     `sqlx:"token_budget" json:",omitempty"`
	TokensUsed      *int64     `sqlx:"tokens_used" json:",omitempty"`
	TimeUsedSeconds *int64     `sqlx:"time_used_seconds" json:",omitempty"`
	CreatedAt       *time.Time `sqlx:"created_at" json:",omitempty"`
	UpdatedAt       *time.Time `sqlx:"updated_at" json:",omitempty"`
	Has             *GoalHas   `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type MutableGoalView = Goal

type MutableGoalViews struct {
	Goals []*MutableGoalView
}

type GoalHas struct {
	Id              bool
	ConversationID  bool
	Objective       bool
	Status          bool
	StatusReason    bool
	PauseReason     bool
	ControllerSpec  bool
	TokenBudget     bool
	TokensUsed      bool
	TimeUsedSeconds bool
	CreatedAt       bool
	UpdatedAt       bool
}

func ensureHas(h **GoalHas) {
	if *h == nil {
		*h = &GoalHas{}
	}
}

func (g *Goal) SetId(v string) { g.Id = v; ensureHas(&g.Has); g.Has.Id = true }
func (g *Goal) SetConversationID(v string) {
	g.ConversationID = &v
	ensureHas(&g.Has)
	g.Has.ConversationID = true
}
func (g *Goal) SetObjective(v string) {
	g.Objective = &v
	ensureHas(&g.Has)
	g.Has.Objective = true
}
func (g *Goal) SetStatus(v string) {
	g.Status = &v
	ensureHas(&g.Has)
	g.Has.Status = true
}
func (g *Goal) SetStatusReason(v string) {
	g.StatusReason = &v
	ensureHas(&g.Has)
	g.Has.StatusReason = true
}
func (g *Goal) SetPauseReason(v string) {
	g.PauseReason = &v
	ensureHas(&g.Has)
	g.Has.PauseReason = true
}
func (g *Goal) SetControllerSpec(v string) {
	g.ControllerSpec = &v
	ensureHas(&g.Has)
	g.Has.ControllerSpec = true
}
func (g *Goal) SetTokenBudget(v int64) {
	g.TokenBudget = &v
	ensureHas(&g.Has)
	g.Has.TokenBudget = true
}
func (g *Goal) SetTokensUsed(v int64) {
	g.TokensUsed = &v
	ensureHas(&g.Has)
	g.Has.TokensUsed = true
}
func (g *Goal) SetTimeUsedSeconds(v int64) {
	g.TimeUsedSeconds = &v
	ensureHas(&g.Has)
	g.Has.TimeUsedSeconds = true
}
func (g *Goal) SetCreatedAt(v time.Time) {
	g.CreatedAt = &v
	ensureHas(&g.Has)
	g.Has.CreatedAt = true
}
func (g *Goal) SetUpdatedAt(v time.Time) {
	g.UpdatedAt = &v
	ensureHas(&g.Has)
	g.Has.UpdatedAt = true
}

type GoalSlice []*Goal
type IndexedGoal map[string]*Goal

func (s GoalSlice) IndexById() IndexedGoal {
	res := IndexedGoal{}
	for i, it := range s {
		if it != nil {
			res[it.Id] = s[i]
		}
	}
	return res
}
