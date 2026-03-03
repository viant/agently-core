package write

import "time"

type MutableConversationViews struct {
	Conversations []*MutableConversationView
}

type MutableConversationView struct {
	Id                       string           `sqlx:"id,primaryKey" validate:"required"`
	Summary                  *string          `sqlx:"summary" json:",omitempty"`
	LastActivity             *time.Time       `sqlx:"last_activity" json:",omitempty"`
	UsageInputTokens         *int             `sqlx:"usage_input_tokens" json:",omitempty"`
	UsageOutputTokens        *int             `sqlx:"usage_output_tokens" json:",omitempty"`
	UsageEmbeddingTokens     *int             `sqlx:"usage_embedding_tokens" json:",omitempty"`
	CreatedAt                *time.Time       `sqlx:"created_at" json:",omitempty"`
	UpdatedAt                *time.Time       `sqlx:"updated_at" json:",omitempty"`
	CreatedByUserID          *string          `sqlx:"created_by_user_id" json:",omitempty"`
	AgentId                  *string          `sqlx:"agent_id" json:",omitempty"`
	DefaultModelProvider     *string          `sqlx:"default_model_provider" json:",omitempty"`
	DefaultModel             *string          `sqlx:"default_model" json:",omitempty"`
	DefaultModelParams       *string          `sqlx:"default_model_params" json:",omitempty"`
	Title                    *string          `sqlx:"title" json:",omitempty"`
	ConversationParentId     *string          `sqlx:"conversation_parent_id" json:",omitempty"`
	ConversationParentTurnId *string          `sqlx:"conversation_parent_turn_id" json:",omitempty"`
	Metadata                 *string          `sqlx:"metadata" json:",omitempty"`
	Visibility               *string          `sqlx:"visibility" json:",omitempty"`
	Shareable                int              `sqlx:"shareable" json:",omitempty"`
	Status                   *string          `sqlx:"status" json:",omitempty"`
	Scheduled                *int             `sqlx:"scheduled" json:",omitempty"`
	ScheduleId               *string          `sqlx:"schedule_id" json:",omitempty"`
	ScheduleRunId            *string          `sqlx:"schedule_run_id" json:",omitempty"`
	ScheduleKind             *string          `sqlx:"schedule_kind" json:",omitempty"`
	ScheduleTimezone         *string          `sqlx:"schedule_timezone" json:",omitempty"`
	ScheduleCronExpr         *string          `sqlx:"schedule_cron_expr" json:",omitempty"`
	ExternalTaskRef          *string          `sqlx:"external_task_ref" json:",omitempty"`
	Has                      *ConversationHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ConversationHas struct {
	Id                       bool
	Summary                  bool
	LastActivity             bool
	UsageInputTokens         bool
	UsageOutputTokens        bool
	UsageEmbeddingTokens     bool
	CreatedAt                bool
	UpdatedAt                bool
	CreatedByUserID          bool
	AgentId                  bool
	DefaultModelProvider     bool
	DefaultModel             bool
	DefaultModelParams       bool
	Title                    bool
	ConversationParentId     bool
	ConversationParentTurnId bool
	Metadata                 bool
	Visibility               bool
	Shareable                bool
	Status                   bool
	Scheduled                bool
	ScheduleId               bool
	ScheduleRunId            bool
	ScheduleKind             bool
	ScheduleTimezone         bool
	ScheduleCronExpr         bool
	ExternalTaskRef          bool
}

// Compatibility aliases.
type Conversation = MutableConversationView
type Conversations = MutableConversationViews

func (c *MutableConversationView) ensureHas() {
	if c.Has == nil {
		c.Has = &ConversationHas{}
	}
}

func (c *MutableConversationView) SetId(value string) { c.Id = value; c.ensureHas(); c.Has.Id = true }
func (c *MutableConversationView) SetSummary(value string) {
	c.Summary = &value
	c.ensureHas()
	c.Has.Summary = true
}
func (c *MutableConversationView) SetLastActivity(value time.Time) {
	c.LastActivity = &value
	c.ensureHas()
	c.Has.LastActivity = true
}
func (c *MutableConversationView) SetUsageInputTokens(value int) {
	c.UsageInputTokens = &value
	c.ensureHas()
	c.Has.UsageInputTokens = true
}
func (c *MutableConversationView) SetUsageOutputTokens(value int) {
	c.UsageOutputTokens = &value
	c.ensureHas()
	c.Has.UsageOutputTokens = true
}
func (c *MutableConversationView) SetUsageEmbeddingTokens(value int) {
	c.UsageEmbeddingTokens = &value
	c.ensureHas()
	c.Has.UsageEmbeddingTokens = true
}
func (c *MutableConversationView) SetCreatedAt(value time.Time) {
	c.CreatedAt = &value
	c.ensureHas()
	c.Has.CreatedAt = true
}
func (c *MutableConversationView) SetUpdatedAt(value time.Time) {
	c.UpdatedAt = &value
	c.ensureHas()
	c.Has.UpdatedAt = true
}
func (c *MutableConversationView) SetCreatedByUserID(value string) {
	c.CreatedByUserID = &value
	c.ensureHas()
	c.Has.CreatedByUserID = true
}
func (c *MutableConversationView) SetAgentId(value string) {
	c.AgentId = &value
	c.ensureHas()
	c.Has.AgentId = true
}
func (c *MutableConversationView) SetDefaultModelProvider(value string) {
	c.DefaultModelProvider = &value
	c.ensureHas()
	c.Has.DefaultModelProvider = true
}
func (c *MutableConversationView) SetDefaultModel(value string) {
	c.DefaultModel = &value
	c.ensureHas()
	c.Has.DefaultModel = true
}
func (c *MutableConversationView) SetDefaultModelParams(value string) {
	c.DefaultModelParams = &value
	c.ensureHas()
	c.Has.DefaultModelParams = true
}
func (c *MutableConversationView) SetTitle(value string) {
	c.Title = &value
	c.ensureHas()
	c.Has.Title = true
}
func (c *MutableConversationView) SetConversationParentId(value string) {
	c.ConversationParentId = &value
	c.ensureHas()
	c.Has.ConversationParentId = true
}
func (c *MutableConversationView) SetConversationParentTurnId(value string) {
	c.ConversationParentTurnId = &value
	c.ensureHas()
	c.Has.ConversationParentTurnId = true
}
func (c *MutableConversationView) SetMetadata(value string) {
	c.Metadata = &value
	c.ensureHas()
	c.Has.Metadata = true
}
func (c *MutableConversationView) SetVisibility(value string) {
	c.Visibility = &value
	c.ensureHas()
	c.Has.Visibility = true
}
func (c *MutableConversationView) SetShareable(value int) {
	c.Shareable = value
	c.ensureHas()
	c.Has.Shareable = true
}
func (c *MutableConversationView) SetStatus(value string) {
	c.Status = &value
	c.ensureHas()
	c.Has.Status = true
}
func (c *MutableConversationView) SetScheduled(value int) {
	c.Scheduled = &value
	c.ensureHas()
	c.Has.Scheduled = true
}
func (c *MutableConversationView) SetScheduleId(value string) {
	c.ScheduleId = &value
	c.ensureHas()
	c.Has.ScheduleId = true
}
func (c *MutableConversationView) SetScheduleRunId(value string) {
	c.ScheduleRunId = &value
	c.ensureHas()
	c.Has.ScheduleRunId = true
}
func (c *MutableConversationView) SetScheduleKind(value string) {
	c.ScheduleKind = &value
	c.ensureHas()
	c.Has.ScheduleKind = true
}
func (c *MutableConversationView) SetScheduleTimezone(value string) {
	c.ScheduleTimezone = &value
	c.ensureHas()
	c.Has.ScheduleTimezone = true
}
func (c *MutableConversationView) SetScheduleCronExpr(value string) {
	c.ScheduleCronExpr = &value
	c.ensureHas()
	c.Has.ScheduleCronExpr = true
}
func (c *MutableConversationView) SetExternalTaskRef(value string) {
	c.ExternalTaskRef = &value
	c.ensureHas()
	c.Has.ExternalTaskRef = true
}
