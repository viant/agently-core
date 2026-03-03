package write

import "time"

// TurnQueue is a mutable Datly-compatible row for conversation turn queue.
type TurnQueue struct {
	Id             string        `sqlx:"id,primaryKey" validate:"required"`
	ConversationId string        `sqlx:"conversation_id"`
	TurnId         string        `sqlx:"turn_id"`
	MessageId      string        `sqlx:"message_id"`
	QueueSeq       *int64        `sqlx:"queue_seq" json:",omitempty"`
	Status         string        `sqlx:"status"`
	CreatedAt      *time.Time    `sqlx:"created_at" json:",omitempty"`
	UpdatedAt      *time.Time    `sqlx:"updated_at" json:",omitempty"`
	Has            *TurnQueueHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type MutableTurnQueueView = TurnQueue

type MutableTurnQueueViews struct {
	TurnQueues []*MutableTurnQueueView
}

type TurnQueueHas struct {
	Id             bool
	ConversationId bool
	TurnId         bool
	MessageId      bool
	QueueSeq       bool
	Status         bool
	CreatedAt      bool
	UpdatedAt      bool
}

func (q *TurnQueue) ensureHas() {
	if q.Has == nil {
		q.Has = &TurnQueueHas{}
	}
}

func (q *TurnQueue) SetId(v string) { q.Id = v; q.ensureHas(); q.Has.Id = true }
func (q *TurnQueue) SetConversationId(v string) {
	q.ConversationId = v
	q.ensureHas()
	q.Has.ConversationId = true
}
func (q *TurnQueue) SetTurnId(v string)    { q.TurnId = v; q.ensureHas(); q.Has.TurnId = true }
func (q *TurnQueue) SetMessageId(v string) { q.MessageId = v; q.ensureHas(); q.Has.MessageId = true }
func (q *TurnQueue) SetQueueSeq(v int64) {
	q.QueueSeq = &v
	q.ensureHas()
	q.Has.QueueSeq = true
}
func (q *TurnQueue) SetStatus(v string) { q.Status = v; q.ensureHas(); q.Has.Status = true }
func (q *TurnQueue) SetCreatedAt(v time.Time) {
	q.CreatedAt = &v
	q.ensureHas()
	q.Has.CreatedAt = true
}
func (q *TurnQueue) SetUpdatedAt(v time.Time) {
	q.UpdatedAt = &v
	q.ensureHas()
	q.Has.UpdatedAt = true
}
