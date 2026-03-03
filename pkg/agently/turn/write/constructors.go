package write

type MutableTurnViewOption func(*MutableTurnView)

func NewMutableTurnView(opts ...MutableTurnViewOption) *MutableTurnView {
	ret := &MutableTurnView{Has: &TurnHas{}}
	for _, opt := range opts {
		if opt != nil {
			opt(ret)
		}
	}
	return ret
}

func NewMutableTurnViews(rows ...*MutableTurnView) *MutableTurnViews {
	return &MutableTurnViews{Turns: rows}
}

func WithTurnID(v string) MutableTurnViewOption {
	return func(t *MutableTurnView) { t.SetId(v) }
}

func WithTurnConversationID(v string) MutableTurnViewOption {
	return func(t *MutableTurnView) { t.SetConversationID(v) }
}

func WithTurnStatus(v string) MutableTurnViewOption {
	return func(t *MutableTurnView) { t.SetStatus(v) }
}

func WithTurnRunID(v string) MutableTurnViewOption {
	return func(t *MutableTurnView) { t.SetRunID(v) }
}
