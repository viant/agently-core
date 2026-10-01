package read

import (
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

type GeneratedFileView struct {
	ID             string     `sqlx:"id"`
	ConversationID string     `sqlx:"conversation_id"`
	TurnID         *string    `sqlx:"turn_id"`
	MessageID      *string    `sqlx:"message_id"`
	Provider       string     `sqlx:"provider"`
	Mode           string     `sqlx:"mode"`
	CopyMode       string     `sqlx:"copy_mode"`
	Status         string     `sqlx:"status"`
	PayloadID      *string    `sqlx:"payload_id"`
	ContainerID    *string    `sqlx:"container_id"`
	ProviderFileID *string    `sqlx:"provider_file_id"`
	Filename       *string    `sqlx:"filename"`
	MimeType       *string    `sqlx:"mime_type"`
	SizeBytes      *int       `sqlx:"size_bytes"`
	Checksum       *string    `sqlx:"checksum"`
	ErrorMessage   *string    `sqlx:"error_message"`
	ExpiresAt      *time.Time `sqlx:"expires_at"`
	CreatedAt      *time.Time `sqlx:"created_at"`
	UpdatedAt      *time.Time `sqlx:"updated_at"`
}

var URI = "/v2/api/agently/generated-file"
