package write

type MutablePayloadViewOption func(*MutablePayloadView)

func NewMutablePayloadView(opts ...MutablePayloadViewOption) *MutablePayloadView {
	ret := &MutablePayloadView{Has: &PayloadHas{}}
	for _, opt := range opts {
		if opt != nil {
			opt(ret)
		}
	}
	return ret
}

func NewMutablePayloadViews(rows ...*MutablePayloadView) *MutablePayloadViews {
	return &MutablePayloadViews{Payloads: rows}
}

func WithPayloadID(v string) MutablePayloadViewOption {
	return func(p *MutablePayloadView) { p.SetId(v) }
}

func WithPayloadKind(v string) MutablePayloadViewOption {
	return func(p *MutablePayloadView) { p.SetKind(v) }
}

func WithPayloadMimeType(v string) MutablePayloadViewOption {
	return func(p *MutablePayloadView) { p.SetMimeType(v) }
}

func WithPayloadSizeBytes(v int) MutablePayloadViewOption {
	return func(p *MutablePayloadView) { p.SetSizeBytes(v) }
}

func WithPayloadStorage(v string) MutablePayloadViewOption {
	return func(p *MutablePayloadView) { p.SetStorage(v) }
}

func WithPayloadInlineBody(v []byte) MutablePayloadViewOption {
	return func(p *MutablePayloadView) { p.SetInlineBody(v) }
}
