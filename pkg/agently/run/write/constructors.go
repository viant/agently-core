package write

type MutableRunViewOption func(*MutableRunView)

func NewMutableRunView(opts ...MutableRunViewOption) *MutableRunView {
	ret := &MutableRunView{Has: &RunHas{}}
	for _, opt := range opts {
		if opt != nil {
			opt(ret)
		}
	}
	return ret
}

func NewMutableRunViews(rows ...*MutableRunView) *MutableRunViews {
	return &MutableRunViews{Runs: rows}
}

func WithRunID(v string) MutableRunViewOption {
	return func(r *MutableRunView) { r.SetId(v) }
}

func WithRunStatus(v string) MutableRunViewOption {
	return func(r *MutableRunView) { r.SetStatus(v) }
}

func WithRunTurnID(v string) MutableRunViewOption {
	return func(r *MutableRunView) { r.SetTurnID(v) }
}

func WithRunConversationID(v string) MutableRunViewOption {
	return func(r *MutableRunView) { r.SetConversationID(v) }
}

func WithRunIteration(v int) MutableRunViewOption {
	return func(r *MutableRunView) { r.SetIteration(v) }
}
