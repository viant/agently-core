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
	for _, m := range i.Messages {
		if m == nil {
			continue
		}
		i.mergeMissingFieldsFromCurrent(m)
		if m.Has == nil {
			m.Has = &MessageHas{}
		}
		if _, ok := i.CurMessageById[m.Id]; !ok {
			m.SetCreatedAt(now)
			// ensure non-null default fields
			if m.Interim == nil {
				m.SetInterim(0)
			}
		}
	}
	return nil
}

func (i *Input) indexSlice() {
	i.CurMessageById = map[string]*Message{}
	for _, m := range i.CurMessage {
		if m != nil {
			i.CurMessageById[m.Id] = m
		}
	}
}

func (i *Input) mergeMissingFieldsFromCurrent(m *Message) {
	if i == nil || m == nil || m.Has == nil {
		return
	}
	current, ok := i.CurMessageById[m.Id]
	if !ok || current == nil {
		return
	}
	if !m.Has.ConversationID && current.ConversationID != "" {
		m.SetConversationID(current.ConversationID)
	}
	if !m.Has.TurnID && current.TurnID != nil && *current.TurnID != "" {
		m.SetTurnID(*current.TurnID)
	}
	if !m.Has.ParentMessageID && current.ParentMessageID != nil && *current.ParentMessageID != "" {
		m.SetParentMessageID(*current.ParentMessageID)
	}
	if !m.Has.Role && current.Role != "" {
		m.SetRole(current.Role)
	}
	if !m.Has.Type && current.Type != "" {
		m.SetType(current.Type)
	}
	if !m.Has.Mode && current.Mode != nil && *current.Mode != "" {
		m.SetMode(*current.Mode)
	}
	if !m.Has.Iteration && current.Iteration != nil {
		m.SetIteration(*current.Iteration)
	}
	if !m.Has.Phase && current.Phase != nil && *current.Phase != "" {
		m.SetPhase(*current.Phase)
	}
}
