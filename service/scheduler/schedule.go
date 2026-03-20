package scheduler

import "time"

// Schedule represents a scheduled task configuration.
type Schedule struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Description     *string    `json:"description,omitempty"`
	CreatedByUserID *string    `json:"createdByUserId,omitempty"`
	Visibility      string     `json:"visibility,omitempty"`
	AgentRef        string     `json:"agentRef"`
	ModelOverride   *string    `json:"modelOverride,omitempty"`
	UserCredURL     *string    `json:"userCredUrl,omitempty"`
	Enabled         bool       `json:"enabled"`
	StartAt         *time.Time `json:"startAt,omitempty"`
	EndAt           *time.Time `json:"endAt,omitempty"`
	ScheduleType    string     `json:"scheduleType"`
	CronExpr        *string    `json:"cronExpr,omitempty"`
	IntervalSeconds *int       `json:"intervalSeconds,omitempty"`
	Timezone        string     `json:"timezone,omitempty"`
	TimeoutSeconds  int        `json:"timeoutSeconds,omitempty"`
	TaskPromptURI   *string    `json:"taskPromptUri,omitempty"`
	TaskPrompt      *string    `json:"taskPrompt,omitempty"`
	NextRunAt       *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt       *time.Time `json:"lastRunAt,omitempty"`
	LastStatus      *string    `json:"lastStatus,omitempty"`
	LastError       *string    `json:"lastError,omitempty"`
	LeaseOwner      *string    `json:"leaseOwner,omitempty"`
	LeaseUntil      *time.Time `json:"leaseUntil,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
}

// ScheduleStore is kept as a compatibility alias for the persisted scheduler store.
type ScheduleStore = Store
