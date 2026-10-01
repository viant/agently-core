package generatedfile

import (
	read "github.com/viant/agently-core/internal/datly/generatedfile/read"
	"time"

	"github.com/viant/xdatly/response"
)

type Input struct {
	ConversationID string
	TurnID         string
	MessageID      string
	ID             string
	Provider       string
	Status         string
	Since          *time.Time
	Has            *Has `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type Has struct {
	ConversationID bool
	TurnID         bool
	MessageID      bool
	ID             bool
	Provider       bool
	Status         bool
	Since          bool
}

type Output struct {
	response.Status `json:",omitempty"`
	Data            []*GeneratedFileView
	Metrics         response.Metrics
}

type GeneratedFileView = read.GeneratedFileView

var URI = "/v2/api/agently/generated-file"
