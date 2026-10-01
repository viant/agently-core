package run

import (
	"github.com/viant/xdatly/response"

	"time"
)

// Public data shapes retained for Go caller and JSON compatibility.

type RunRowsInput struct {
	Id               string
	TurnId           string
	ConversationId   string
	ScheduleId       string
	WorkerId         string
	RunStatus        string
	ExcludeStatuses  []string
	DefaultPredicate string
	Has              *RunRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type RunRowsInputHas struct {
	Id               bool
	TurnId           bool
	ConversationId   bool
	ScheduleId       bool
	WorkerId         bool
	RunStatus        bool
	ExcludeStatuses  bool
	DefaultPredicate bool
}

type RunRowsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*RunRowsView
	Metrics         response.Metrics
}

type RunRowsView struct {
	AgentId               *string    `sqlx:"agent_id"`
	Attempt               int        `sqlx:"attempt"`
	AuthAudience          *string    `sqlx:"auth_audience"`
	AuthAuthority         *string    `sqlx:"auth_authority"`
	CheckpointData        *string    `sqlx:"checkpoint_data"`
	CheckpointMessageId   *string    `sqlx:"checkpoint_message_id"`
	CheckpointResponseId  *string    `sqlx:"checkpoint_response_id"`
	CompletedAt           *time.Time `sqlx:"completed_at"`
	ConversationId        *string    `sqlx:"conversation_id"`
	ConversationKind      string     `sqlx:"conversation_kind"`
	CreatedAt             time.Time  `sqlx:"created_at"`
	EffectiveUserId       *string    `sqlx:"effective_user_id"`
	ErrorCode             *string    `sqlx:"error_code"`
	ErrorMessage          *string    `sqlx:"error_message"`
	HeartbeatIntervalSec  *int       `sqlx:"heartbeat_interval_sec"`
	Id                    string     `sqlx:"id"`
	Iteration             int        `sqlx:"iteration"`
	LastHeartbeatAt       *time.Time `sqlx:"last_heartbeat_at"`
	LeaseOwner            *string    `sqlx:"lease_owner"`
	LeaseUntil            *time.Time `sqlx:"lease_until"`
	MaxIterations         *int       `sqlx:"max_iterations"`
	Model                 *string    `sqlx:"model"`
	ModelProvider         *string    `sqlx:"model_provider"`
	PreconditionPassed    *int       `sqlx:"precondition_passed"`
	PreconditionRanAt     *time.Time `sqlx:"precondition_ran_at"`
	PreconditionResult    *string    `sqlx:"precondition_result"`
	ResumedFromRunId      *string    `sqlx:"resumed_from_run_id"`
	ScheduleId            *string    `sqlx:"schedule_id"`
	ScheduledFor          *time.Time `sqlx:"scheduled_for"`
	SecurityContext       *string    `sqlx:"security_context"`
	StartedAt             *time.Time `sqlx:"started_at"`
	Status                string     `sqlx:"status"`
	TurnId                *string    `sqlx:"turn_id"`
	UpdatedAt             *time.Time `sqlx:"updated_at"`
	UsageCompletionTokens *int       `sqlx:"usage_completion_tokens"`
	UsageCost             *float64   `sqlx:"usage_cost"`
	UsagePromptTokens     *int       `sqlx:"usage_prompt_tokens"`
	UsageTotalTokens      *int       `sqlx:"usage_total_tokens"`
	UserCredUrl           *string    `sqlx:"user_cred_url"`
	WorkerHost            *string    `sqlx:"worker_host"`
	WorkerId              *string    `sqlx:"worker_id"`
	WorkerPid             *int       `sqlx:"worker_pid"`
}

var RunRowsPathURI = "/v1/api/agently/run/{id}"
