package write

type MutableConversationViewOption func(*MutableConversationView)

func NewMutableConversationView(opts ...MutableConversationViewOption) *MutableConversationView {
	ret := &MutableConversationView{Has: &ConversationHas{}}
	for _, opt := range opts {
		if opt != nil {
			opt(ret)
		}
	}
	return ret
}

func NewMutableConversationViews(rows ...*MutableConversationView) *MutableConversationViews {
	return &MutableConversationViews{Conversations: rows}
}

func WithConversationID(v string) MutableConversationViewOption {
	return func(c *MutableConversationView) { c.SetId(v) }
}

func WithConversationStatus(v string) MutableConversationViewOption {
	return func(c *MutableConversationView) { c.SetStatus(v) }
}

func WithConversationSummary(v string) MutableConversationViewOption {
	return func(c *MutableConversationView) { c.SetSummary(v) }
}

func WithConversationVisibility(v string) MutableConversationViewOption {
	return func(c *MutableConversationView) { c.SetVisibility(v) }
}

func WithConversationShareable(v int) MutableConversationViewOption {
	return func(c *MutableConversationView) { c.SetShareable(v) }
}

func NewConversationStatus(id, status string) *MutableConversationView {
	return NewMutableConversationView(
		WithConversationID(id),
		WithConversationStatus(status),
	)
}
