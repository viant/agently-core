package write

type MutableModelCallViewOption func(*MutableModelCallView)

func NewMutableModelCallView(opts ...MutableModelCallViewOption) *MutableModelCallView {
	ret := &MutableModelCallView{Has: &ModelCallHas{}}
	for _, opt := range opts {
		if opt != nil {
			opt(ret)
		}
	}
	return ret
}

func NewMutableModelCallViews(rows ...*MutableModelCallView) *MutableModelCallViews {
	return &MutableModelCallViews{ModelCalls: rows}
}

func WithModelCallMessageID(v string) MutableModelCallViewOption {
	return func(m *MutableModelCallView) { m.SetMessageID(v) }
}

func WithModelCallTurnID(v string) MutableModelCallViewOption {
	return func(m *MutableModelCallView) { m.SetTurnID(v) }
}

func WithModelCallProvider(v string) MutableModelCallViewOption {
	return func(m *MutableModelCallView) { m.SetProvider(v) }
}

func WithModelCallModel(v string) MutableModelCallViewOption {
	return func(m *MutableModelCallView) { m.SetModel(v) }
}

func WithModelCallModelKind(v string) MutableModelCallViewOption {
	return func(m *MutableModelCallView) { m.SetModelKind(v) }
}

func WithModelCallStatus(v string) MutableModelCallViewOption {
	return func(m *MutableModelCallView) { m.SetStatus(v) }
}

func WithModelCallRunID(v string) MutableModelCallViewOption {
	return func(m *MutableModelCallView) { m.SetRunID(v) }
}

func WithModelCallIteration(v int) MutableModelCallViewOption {
	return func(m *MutableModelCallView) { m.SetIteration(v) }
}
