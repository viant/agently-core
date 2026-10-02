package message

import (
	base "github.com/viant/agently-core/internal/datly/message/base"
	"github.com/viant/xdatly/response"

	"time"
)

// Public data shapes retained for Go caller and JSON compatibility.

type MessageRowsInput struct {
	ConversationId  string
	TurnId          string
	Id              string
	Roles           []string
	Types           []string
	Interim         int
	Phase           string
	Iteration       int
	CreatedSince    time.Time
	CreatedBefore   time.Time
	CursorBefore    string
	CursorAfter     string
	TurnTask        bool
	AssistantFinal  bool
	AssistantStatus bool
	IncludeModelCal bool
	IncludeToolCall bool
	Has             *MessageRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type MessageRowsInputHas struct {
	ConversationId  bool
	TurnId          bool
	Id              bool
	Roles           bool
	Types           bool
	Interim         bool
	Phase           bool
	Iteration       bool
	CreatedSince    bool
	CreatedBefore   bool
	CursorBefore    bool
	CursorAfter     bool
	TurnTask        bool
	AssistantFinal  bool
	AssistantStatus bool
	IncludeModelCal bool
	IncludeToolCall bool
}

type MessageRowsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*MessageRowsView
	Metrics         response.Metrics
}

type MessageRowsView = base.MessageBaseView

var MessageRowsPathURI = "/v1/api/agently/message/list/list"
