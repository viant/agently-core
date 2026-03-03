package scheduler

import "time"

// Schedule represents a scheduled task configuration.
type Schedule struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	AgentRef        string     `json:"agentRef"`
	Enabled         bool       `json:"enabled"`
	ScheduleType    string     `json:"scheduleType"` // cron, adhoc, interval
	CronExpr        *string    `json:"cronExpr,omitempty"`
	IntervalSeconds *int       `json:"intervalSeconds,omitempty"`
	Timezone        string     `json:"timezone,omitempty"`
	TaskPrompt      *string    `json:"taskPrompt,omitempty"`
	TaskPromptURI   *string    `json:"taskPromptUri,omitempty"`
	UserCredURL     *string    `json:"userCredUrl,omitempty"` // scy-encoded credential reference for auth restoration
	NextRunAt       *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt       *time.Time `json:"lastRunAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// ScheduleStore abstracts schedule persistence.
type ScheduleStore interface {
	Get(id string) (*Schedule, error)
	List() ([]*Schedule, error)
	Upsert(s *Schedule) error
	Delete(id string) error
	ListDue(now time.Time) ([]*Schedule, error)
}
