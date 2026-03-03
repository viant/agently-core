package write

type MutableToolCallViewOption func(*MutableToolCallView)

func NewMutableToolCallView(opts ...MutableToolCallViewOption) *MutableToolCallView {
	ret := &MutableToolCallView{Has: &ToolCallHas{}}
	for _, opt := range opts {
		if opt != nil {
			opt(ret)
		}
	}
	return ret
}

func NewMutableToolCallViews(rows ...*MutableToolCallView) *MutableToolCallViews {
	return &MutableToolCallViews{ToolCalls: rows}
}

func WithToolCallMessageID(v string) MutableToolCallViewOption {
	return func(t *MutableToolCallView) { t.SetMessageID(v) }
}

func WithToolCallTurnID(v string) MutableToolCallViewOption {
	return func(t *MutableToolCallView) { t.SetTurnID(v) }
}

func WithToolCallOpID(v string) MutableToolCallViewOption {
	return func(t *MutableToolCallView) { t.SetOpID(v) }
}

func WithToolCallToolName(v string) MutableToolCallViewOption {
	return func(t *MutableToolCallView) { t.SetToolName(v) }
}

func WithToolCallToolKind(v string) MutableToolCallViewOption {
	return func(t *MutableToolCallView) { t.SetToolKind(v) }
}

func WithToolCallStatus(v string) MutableToolCallViewOption {
	return func(t *MutableToolCallView) { t.SetStatus(v) }
}

func WithToolCallErrorMessage(v string) MutableToolCallViewOption {
	return func(t *MutableToolCallView) { t.SetErrorMessage(v) }
}

func WithToolCallRunID(v string) MutableToolCallViewOption {
	return func(t *MutableToolCallView) { t.SetRunID(v) }
}

func WithToolCallIteration(v int) MutableToolCallViewOption {
	return func(t *MutableToolCallView) { t.SetIteration(v) }
}
