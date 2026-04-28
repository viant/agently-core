package write

import "time"

type MutableRunView struct {
	Id                    string     `sqlx:"id,primaryKey" validate:"required"`
	TurnID                *string    `sqlx:"turn_id" json:",omitempty"`
	ScheduleID            *string    `sqlx:"schedule_id" json:",omitempty"`
	ConversationID        *string    `sqlx:"conversation_id" json:",omitempty"`
	ConversationKind      *string    `sqlx:"conversation_kind" json:",omitempty"`
	Attempt               *int       `sqlx:"attempt" json:",omitempty"`
	ResumedFromRunID      *string    `sqlx:"resumed_from_run_id" json:",omitempty"`
	Status                string     `sqlx:"status" validate:"required"`
	ErrorCode             *string    `sqlx:"error_code" json:",omitempty"`
	ErrorMessage          *string    `sqlx:"error_message" json:",omitempty"`
	Iteration             *int       `sqlx:"iteration" json:",omitempty"`
	MaxIterations         *int       `sqlx:"max_iterations" json:",omitempty"`
	CheckpointResponseID  *string    `sqlx:"checkpoint_response_id" json:",omitempty"`
	CheckpointMessageID   *string    `sqlx:"checkpoint_message_id" json:",omitempty"`
	CheckpointData        *string    `sqlx:"checkpoint_data" json:",omitempty"`
	AgentID               *string    `sqlx:"agent_id" json:",omitempty"`
	ModelProvider         *string    `sqlx:"model_provider" json:",omitempty"`
	Model                 *string    `sqlx:"model" json:",omitempty"`
	WorkerID              *string    `sqlx:"worker_id" json:",omitempty"`
	WorkerPID             *int       `sqlx:"worker_pid" json:",omitempty"`
	WorkerHost            *string    `sqlx:"worker_host" json:",omitempty"`
	LeaseOwner            *string    `sqlx:"lease_owner" json:",omitempty"`
	LeaseUntil            *time.Time `sqlx:"lease_until" json:",omitempty"`
	LastHeartbeatAt       *time.Time `sqlx:"last_heartbeat_at" json:",omitempty"`
	SecurityContext       *string    `sqlx:"security_context" json:",omitempty"`
	UserCredURL           *string    `sqlx:"user_cred_url" json:",omitempty"`
	EffectiveUserID       *string    `sqlx:"effective_user_id" json:",omitempty"`
	AuthAuthority         *string    `sqlx:"auth_authority" json:",omitempty"`
	AuthAudience          *string    `sqlx:"auth_audience" json:",omitempty"`
	HeartbeatIntervalSec  *int       `sqlx:"heartbeat_interval_sec" json:",omitempty"`
	ScheduledFor          *time.Time `sqlx:"scheduled_for" json:",omitempty"`
	PreconditionRanAt     *time.Time `sqlx:"precondition_ran_at" json:",omitempty"`
	PreconditionPassed    *int       `sqlx:"precondition_passed" json:",omitempty"`
	PreconditionResult    *string    `sqlx:"precondition_result" json:",omitempty"`
	UsagePromptTokens     *int       `sqlx:"usage_prompt_tokens" json:",omitempty"`
	UsageCompletionTokens *int       `sqlx:"usage_completion_tokens" json:",omitempty"`
	UsageTotalTokens      *int       `sqlx:"usage_total_tokens" json:",omitempty"`
	UsageCost             *float64   `sqlx:"usage_cost" json:",omitempty"`
	CreatedAt             *time.Time `sqlx:"created_at" json:",omitempty"`
	UpdatedAt             *time.Time `sqlx:"updated_at" json:",omitempty"`
	StartedAt             *time.Time `sqlx:"started_at" json:",omitempty"`
	CompletedAt           *time.Time `sqlx:"completed_at" json:",omitempty"`
	Has                   *RunHas    `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type MutableRunViews struct {
	Runs []*MutableRunView
}

type RunHas struct {
	Id                    bool
	TurnID                bool
	ScheduleID            bool
	ConversationID        bool
	ConversationKind      bool
	Attempt               bool
	ResumedFromRunID      bool
	Status                bool
	ErrorCode             bool
	ErrorMessage          bool
	Iteration             bool
	MaxIterations         bool
	CheckpointResponseID  bool
	CheckpointMessageID   bool
	CheckpointData        bool
	AgentID               bool
	ModelProvider         bool
	Model                 bool
	WorkerID              bool
	WorkerPID             bool
	WorkerHost            bool
	LeaseOwner            bool
	LeaseUntil            bool
	LastHeartbeatAt       bool
	HeartbeatIntervalSec  bool
	SecurityContext       bool
	UserCredURL           bool
	EffectiveUserID       bool
	ScheduledFor          bool
	PreconditionRanAt     bool
	PreconditionPassed    bool
	PreconditionResult    bool
	UsagePromptTokens     bool
	UsageCompletionTokens bool
	UsageTotalTokens      bool
	UsageCost             bool
	CreatedAt             bool
	UpdatedAt             bool
	StartedAt             bool
	CompletedAt           bool
}

func (r *MutableRunView) ensureHas() {
	if r.Has == nil {
		r.Has = &RunHas{}
	}
}

func (r *MutableRunView) SetId(v string)     { r.Id = v; r.ensureHas(); r.Has.Id = true }
func (r *MutableRunView) SetTurnID(v string) { r.TurnID = &v; r.ensureHas(); r.Has.TurnID = true }
func (r *MutableRunView) SetScheduleID(v string) {
	r.ScheduleID = &v
	r.ensureHas()
	r.Has.ScheduleID = true
}
func (r *MutableRunView) SetConversationID(v string) {
	r.ConversationID = &v
	r.ensureHas()
	r.Has.ConversationID = true
}
func (r *MutableRunView) SetConversationKind(v string) {
	r.ConversationKind = &v
	r.ensureHas()
	r.Has.ConversationKind = true
}
func (r *MutableRunView) SetAttempt(v int) { r.Attempt = &v; r.ensureHas(); r.Has.Attempt = true }
func (r *MutableRunView) SetResumedFromRunID(v string) {
	r.ResumedFromRunID = &v
	r.ensureHas()
	r.Has.ResumedFromRunID = true
}
func (r *MutableRunView) SetStatus(v string) { r.Status = v; r.ensureHas(); r.Has.Status = true }
func (r *MutableRunView) SetErrorCode(v string) {
	r.ErrorCode = &v
	r.ensureHas()
	r.Has.ErrorCode = true
}
func (r *MutableRunView) SetErrorMessage(v string) {
	r.ErrorMessage = &v
	r.ensureHas()
	r.Has.ErrorMessage = true
}
func (r *MutableRunView) SetIteration(v int) { r.Iteration = &v; r.ensureHas(); r.Has.Iteration = true }
func (r *MutableRunView) SetMaxIterations(v int) {
	r.MaxIterations = &v
	r.ensureHas()
	r.Has.MaxIterations = true
}
func (r *MutableRunView) SetCheckpointResponseID(v string) {
	r.CheckpointResponseID = &v
	r.ensureHas()
	r.Has.CheckpointResponseID = true
}
func (r *MutableRunView) SetCheckpointMessageID(v string) {
	r.CheckpointMessageID = &v
	r.ensureHas()
	r.Has.CheckpointMessageID = true
}
func (r *MutableRunView) SetCheckpointData(v string) {
	r.CheckpointData = &v
	r.ensureHas()
	r.Has.CheckpointData = true
}
func (r *MutableRunView) SetAgentID(v string) { r.AgentID = &v; r.ensureHas(); r.Has.AgentID = true }
func (r *MutableRunView) SetModelProvider(v string) {
	r.ModelProvider = &v
	r.ensureHas()
	r.Has.ModelProvider = true
}
func (r *MutableRunView) SetModel(v string)    { r.Model = &v; r.ensureHas(); r.Has.Model = true }
func (r *MutableRunView) SetWorkerID(v string) { r.WorkerID = &v; r.ensureHas(); r.Has.WorkerID = true }
func (r *MutableRunView) SetWorkerPID(v int)   { r.WorkerPID = &v; r.ensureHas(); r.Has.WorkerPID = true }
func (r *MutableRunView) SetWorkerHost(v string) {
	r.WorkerHost = &v
	r.ensureHas()
	r.Has.WorkerHost = true
}
func (r *MutableRunView) SetLeaseOwner(v string) {
	r.LeaseOwner = &v
	r.ensureHas()
	r.Has.LeaseOwner = true
}
func (r *MutableRunView) SetLeaseUntil(v time.Time) {
	r.LeaseUntil = &v
	r.ensureHas()
	r.Has.LeaseUntil = true
}
func (r *MutableRunView) SetLastHeartbeatAt(v time.Time) {
	r.LastHeartbeatAt = &v
	r.ensureHas()
	r.Has.LastHeartbeatAt = true
}
func (r *MutableRunView) SetSecurityContext(v string) {
	r.SecurityContext = &v
	r.ensureHas()
	r.Has.SecurityContext = true
}
func (r *MutableRunView) SetUserCredURL(v string) {
	r.UserCredURL = &v
	r.ensureHas()
	r.Has.UserCredURL = true
}
func (r *MutableRunView) SetEffectiveUserID(v string) {
	r.EffectiveUserID = &v
	r.ensureHas()
	r.Has.EffectiveUserID = true
}
func (r *MutableRunView) SetHeartbeatIntervalSec(v int) {
	r.HeartbeatIntervalSec = &v
	r.ensureHas()
	r.Has.HeartbeatIntervalSec = true
}
func (r *MutableRunView) SetScheduledFor(v time.Time) {
	r.ScheduledFor = &v
	r.ensureHas()
	r.Has.ScheduledFor = true
}
func (r *MutableRunView) SetPreconditionRanAt(v time.Time) {
	r.PreconditionRanAt = &v
	r.ensureHas()
	r.Has.PreconditionRanAt = true
}
func (r *MutableRunView) SetPreconditionPassed(v int) {
	r.PreconditionPassed = &v
	r.ensureHas()
	r.Has.PreconditionPassed = true
}
func (r *MutableRunView) SetPreconditionResult(v string) {
	r.PreconditionResult = &v
	r.ensureHas()
	r.Has.PreconditionResult = true
}
func (r *MutableRunView) SetUsagePromptTokens(v int) {
	r.UsagePromptTokens = &v
	r.ensureHas()
	r.Has.UsagePromptTokens = true
}
func (r *MutableRunView) SetUsageCompletionTokens(v int) {
	r.UsageCompletionTokens = &v
	r.ensureHas()
	r.Has.UsageCompletionTokens = true
}
func (r *MutableRunView) SetUsageTotalTokens(v int) {
	r.UsageTotalTokens = &v
	r.ensureHas()
	r.Has.UsageTotalTokens = true
}
func (r *MutableRunView) SetUsageCost(v float64) {
	r.UsageCost = &v
	r.ensureHas()
	r.Has.UsageCost = true
}
func (r *MutableRunView) SetCreatedAt(v time.Time) {
	r.CreatedAt = &v
	r.ensureHas()
	r.Has.CreatedAt = true
}
func (r *MutableRunView) SetUpdatedAt(v time.Time) {
	r.UpdatedAt = &v
	r.ensureHas()
	r.Has.UpdatedAt = true
}
func (r *MutableRunView) SetStartedAt(v time.Time) {
	r.StartedAt = &v
	r.ensureHas()
	r.Has.StartedAt = true
}
func (r *MutableRunView) SetCompletedAt(v time.Time) {
	r.CompletedAt = &v
	r.ensureHas()
	r.Has.CompletedAt = true
}
