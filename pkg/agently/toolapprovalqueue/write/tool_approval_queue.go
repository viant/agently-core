package write

import "time"

// ToolApprovalQueue is a mutable Datly-compatible row for personal tool approval queue.
type ToolApprovalQueue struct {
	Id               string                `sqlx:"id,primaryKey" validate:"required"`
	UserId           string                `sqlx:"user_id" validate:"required"`
	ConversationId   *string               `sqlx:"conversation_id" json:",omitempty"`
	TurnId           *string               `sqlx:"turn_id" json:",omitempty"`
	MessageId        *string               `sqlx:"message_id" json:",omitempty"`
	ToolName         string                `sqlx:"tool_name" validate:"required"`
	Title            *string               `sqlx:"title" json:",omitempty"`
	Arguments        []byte                `sqlx:"arguments" validate:"required"`
	Metadata         *[]byte               `sqlx:"metadata" json:",omitempty"`
	Status           string                `sqlx:"status" validate:"required"`
	Decision         *string               `sqlx:"decision" json:",omitempty"`
	ApprovedByUserId *string               `sqlx:"approved_by_user_id" json:",omitempty"`
	ApprovedAt       *time.Time            `sqlx:"approved_at" json:",omitempty"`
	ExecutedAt       *time.Time            `sqlx:"executed_at" json:",omitempty"`
	ErrorMessage     *string               `sqlx:"error_message" json:",omitempty"`
	CreatedAt        *time.Time            `sqlx:"created_at" json:",omitempty"`
	UpdatedAt        *time.Time            `sqlx:"updated_at" json:",omitempty"`
	Has              *ToolApprovalQueueHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type MutableToolApprovalQueueView = ToolApprovalQueue

type MutableToolApprovalQueueViews struct {
	ToolApprovalQueues []*MutableToolApprovalQueueView
}

type ToolApprovalQueueHas struct {
	Id               bool
	UserId           bool
	ConversationId   bool
	TurnId           bool
	MessageId        bool
	ToolName         bool
	Title            bool
	Arguments        bool
	Metadata         bool
	Status           bool
	Decision         bool
	ApprovedByUserId bool
	ApprovedAt       bool
	ExecutedAt       bool
	ErrorMessage     bool
	CreatedAt        bool
	UpdatedAt        bool
}

func (q *ToolApprovalQueue) ensureHas() {
	if q.Has == nil {
		q.Has = &ToolApprovalQueueHas{}
	}
}

func (q *ToolApprovalQueue) SetId(v string)     { q.Id = v; q.ensureHas(); q.Has.Id = true }
func (q *ToolApprovalQueue) SetUserId(v string) { q.UserId = v; q.ensureHas(); q.Has.UserId = true }
func (q *ToolApprovalQueue) SetConversationId(v string) {
	q.ConversationId = &v
	q.ensureHas()
	q.Has.ConversationId = true
}
func (q *ToolApprovalQueue) SetTurnId(v string) { q.TurnId = &v; q.ensureHas(); q.Has.TurnId = true }
func (q *ToolApprovalQueue) SetMessageId(v string) {
	q.MessageId = &v
	q.ensureHas()
	q.Has.MessageId = true
}
func (q *ToolApprovalQueue) SetToolName(v string) {
	q.ToolName = v
	q.ensureHas()
	q.Has.ToolName = true
}
func (q *ToolApprovalQueue) SetTitle(v string) {
	q.Title = &v
	q.ensureHas()
	q.Has.Title = true
}
func (q *ToolApprovalQueue) SetArguments(v []byte) {
	q.Arguments = v
	q.ensureHas()
	q.Has.Arguments = true
}
func (q *ToolApprovalQueue) SetMetadata(v []byte) {
	q.Metadata = &v
	q.ensureHas()
	q.Has.Metadata = true
}
func (q *ToolApprovalQueue) SetStatus(v string) { q.Status = v; q.ensureHas(); q.Has.Status = true }
func (q *ToolApprovalQueue) SetDecision(v string) {
	q.Decision = &v
	q.ensureHas()
	q.Has.Decision = true
}
func (q *ToolApprovalQueue) SetApprovedByUserId(v string) {
	q.ApprovedByUserId = &v
	q.ensureHas()
	q.Has.ApprovedByUserId = true
}
func (q *ToolApprovalQueue) SetApprovedAt(v time.Time) {
	q.ApprovedAt = &v
	q.ensureHas()
	q.Has.ApprovedAt = true
}
func (q *ToolApprovalQueue) SetExecutedAt(v time.Time) {
	q.ExecutedAt = &v
	q.ensureHas()
	q.Has.ExecutedAt = true
}
func (q *ToolApprovalQueue) SetErrorMessage(v string) {
	q.ErrorMessage = &v
	q.ensureHas()
	q.Has.ErrorMessage = true
}
func (q *ToolApprovalQueue) SetCreatedAt(v time.Time) {
	q.CreatedAt = &v
	q.ensureHas()
	q.Has.CreatedAt = true
}
func (q *ToolApprovalQueue) SetUpdatedAt(v time.Time) {
	q.UpdatedAt = &v
	q.ensureHas()
	q.Has.UpdatedAt = true
}
