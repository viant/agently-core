package write

type MutableMessageViewOption func(*MutableMessageView)

func NewMutableMessageView(opts ...MutableMessageViewOption) *MutableMessageView {
	ret := &MutableMessageView{Has: &MessageHas{}}
	for _, opt := range opts {
		if opt != nil {
			opt(ret)
		}
	}
	return ret
}

func NewMutableMessageViews(rows ...*MutableMessageView) *MutableMessageViews {
	return &MutableMessageViews{Messages: rows}
}

func WithMessageID(v string) MutableMessageViewOption {
	return func(m *MutableMessageView) { m.SetId(v) }
}

func WithMessageConversationID(v string) MutableMessageViewOption {
	return func(m *MutableMessageView) { m.SetConversationID(v) }
}

func WithMessageTurnID(v string) MutableMessageViewOption {
	return func(m *MutableMessageView) { m.SetTurnID(v) }
}

func WithMessageParentID(v string) MutableMessageViewOption {
	return func(m *MutableMessageView) { m.SetParentMessageID(v) }
}

func WithMessageRole(v string) MutableMessageViewOption {
	return func(m *MutableMessageView) { m.SetRole(v) }
}

func WithMessageType(v string) MutableMessageViewOption {
	return func(m *MutableMessageView) { m.SetType(v) }
}

func WithMessageContent(v string) MutableMessageViewOption {
	return func(m *MutableMessageView) { m.SetContent(v) }
}

func WithMessageIteration(v int) MutableMessageViewOption {
	return func(m *MutableMessageView) { m.SetIteration(v) }
}

func WithMessagePreamble(v string) MutableMessageViewOption {
	return func(m *MutableMessageView) { m.SetPreamble(v) }
}

func WithMessagePhase(v string) MutableMessageViewOption {
	return func(m *MutableMessageView) { m.SetPhase(v) }
}
