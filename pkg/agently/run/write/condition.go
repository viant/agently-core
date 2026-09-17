package write

import (
	"fmt"
	"strings"
	"time"
)

// RunPatchCondition adds expected-value criteria to a sparse run PATCH row.
//
// When a MutableRunView carries a condition, the handler issues one atomic
// UPDATE whose WHERE clause matches the run id plus every supplied expected
// value. Zero affected rows means another owner or state change won and no
// column is written. Rows without a condition behave exactly as before.
//
// Criteria are equality-only because the sqlx update builder derives the
// WHERE clause from primary-key tagged columns. Heartbeats rotate lease_owner,
// so comparing the observed owner atomically defeats a concurrent renewal.
type RunPatchCondition struct {
	// Status is the expected current run status (required for claim shapes).
	Status string `json:",omitempty"`
	// LeaseOwner is the expected current lease owner. nil means "no owner
	// criterion" (legacy rows with NULL lease_owner); it must be combined
	// with Status and Attempt.
	LeaseOwner *string `json:",omitempty"`
	// Attempt is the expected current attempt.
	Attempt *int `json:",omitempty"`
}

// runOwnedMutation updates runtime columns of a run still owned by the
// expected lease owner: UPDATE run SET ... WHERE id = ? AND lease_owner = ?.
type runOwnedMutation struct {
	Status               *string    `sqlx:"status"`
	ErrorCode            *string    `sqlx:"error_code"`
	ErrorMessage         *string    `sqlx:"error_message"`
	Iteration            *int       `sqlx:"iteration"`
	LeaseOwner           *string    `sqlx:"lease_owner"`
	LeaseUntil           *time.Time `sqlx:"lease_until"`
	LastHeartbeatAt      *time.Time `sqlx:"last_heartbeat_at"`
	HeartbeatIntervalSec *int       `sqlx:"heartbeat_interval_sec"`
	WorkerHost           *string    `sqlx:"worker_host"`
	CompletedAt          *time.Time `sqlx:"completed_at"`
	UpdatedAt            *time.Time `sqlx:"updated_at"`

	Id                 string `sqlx:"id,primaryKey"`
	ExpectedLeaseOwner string `sqlx:"lease_owner,primaryKey"`

	Has *runOwnedMutationHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type runOwnedMutationHas struct {
	Status               bool
	ErrorCode            bool
	ErrorMessage         bool
	Iteration            bool
	LeaseOwner           bool
	LeaseUntil           bool
	LastHeartbeatAt      bool
	HeartbeatIntervalSec bool
	WorkerHost           bool
	CompletedAt          bool
	UpdatedAt            bool
	Id                   bool
	ExpectedLeaseOwner   bool
}

// runClaimMutation claims a run whose observed status, attempt and lease
// owner are unchanged: WHERE id = ? AND status = ? AND attempt = ? AND lease_owner = ?.
type runClaimMutation struct {
	Attempt              *int       `sqlx:"attempt"`
	LeaseOwner           *string    `sqlx:"lease_owner"`
	LeaseUntil           *time.Time `sqlx:"lease_until"`
	LastHeartbeatAt      *time.Time `sqlx:"last_heartbeat_at"`
	HeartbeatIntervalSec *int       `sqlx:"heartbeat_interval_sec"`
	WorkerHost           *string    `sqlx:"worker_host"`
	UpdatedAt            *time.Time `sqlx:"updated_at"`

	Id                 string `sqlx:"id,primaryKey"`
	ExpectedStatus     string `sqlx:"status,primaryKey"`
	ExpectedAttempt    int    `sqlx:"attempt,primaryKey"`
	ExpectedLeaseOwner string `sqlx:"lease_owner,primaryKey"`

	Has *runClaimMutationHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type runClaimMutationHas struct {
	Attempt              bool
	LeaseOwner           bool
	LeaseUntil           bool
	LastHeartbeatAt      bool
	HeartbeatIntervalSec bool
	WorkerHost           bool
	UpdatedAt            bool
	Id                   bool
	ExpectedStatus       bool
	ExpectedAttempt      bool
	ExpectedLeaseOwner   bool
}

// conditionalRecord converts a sparse run row plus its condition into the
// record shape whose trailing primary-key columns express the criteria.
func conditionalRecord(rec *MutableRunView) (interface{}, error) {
	if rec == nil || rec.Condition == nil {
		return nil, fmt.Errorf("conditional run patch requires a condition")
	}
	if strings.TrimSpace(rec.Id) == "" {
		return nil, fmt.Errorf("conditional run patch requires run id")
	}
	if rec.Has == nil {
		return nil, fmt.Errorf("conditional run patch requires marked columns")
	}
	cond := rec.Condition
	has := rec.Has
	if unsupported := unsupportedConditionalColumns(has); len(unsupported) > 0 {
		return nil, fmt.Errorf("conditional run patch does not support columns: %s", strings.Join(unsupported, ", "))
	}
	switch {
	case strings.TrimSpace(cond.Status) == "" && cond.Attempt == nil:
		if cond.LeaseOwner == nil || strings.TrimSpace(*cond.LeaseOwner) == "" {
			return nil, fmt.Errorf("conditional run patch requires lease owner or status+attempt criteria")
		}
		if has.Attempt {
			return nil, fmt.Errorf("conditional run patch cannot change attempt under lease-owner criteria")
		}
		out := &runOwnedMutation{Id: rec.Id, ExpectedLeaseOwner: strings.TrimSpace(*cond.LeaseOwner)}
		out.Has = &runOwnedMutationHas{Id: true, ExpectedLeaseOwner: true}
		out.Status, out.Has.Status = optionalString(rec.Status, has.Status)
		out.ErrorCode, out.Has.ErrorCode = rec.ErrorCode, has.ErrorCode
		out.ErrorMessage, out.Has.ErrorMessage = rec.ErrorMessage, has.ErrorMessage
		out.Iteration, out.Has.Iteration = rec.Iteration, has.Iteration
		out.LeaseOwner, out.Has.LeaseOwner = rec.LeaseOwner, has.LeaseOwner
		out.LeaseUntil, out.Has.LeaseUntil = rec.LeaseUntil, has.LeaseUntil
		out.LastHeartbeatAt, out.Has.LastHeartbeatAt = rec.LastHeartbeatAt, has.LastHeartbeatAt
		out.HeartbeatIntervalSec, out.Has.HeartbeatIntervalSec = rec.HeartbeatIntervalSec, has.HeartbeatIntervalSec
		out.WorkerHost, out.Has.WorkerHost = rec.WorkerHost, has.WorkerHost
		out.CompletedAt, out.Has.CompletedAt = rec.CompletedAt, has.CompletedAt
		out.UpdatedAt, out.Has.UpdatedAt = rec.UpdatedAt, has.UpdatedAt
		return out, nil
	case strings.TrimSpace(cond.Status) != "" && cond.Attempt != nil:
		for _, forbidden := range []struct {
			set  bool
			name string
		}{{has.Status, "status"}, {has.ErrorCode, "error_code"}, {has.ErrorMessage, "error_message"}, {has.Iteration, "iteration"}, {has.CompletedAt, "completed_at"}} {
			if forbidden.set {
				return nil, fmt.Errorf("conditional run claim cannot change %s", forbidden.name)
			}
		}
		if cond.LeaseOwner == nil || strings.TrimSpace(*cond.LeaseOwner) == "" {
			return nil, fmt.Errorf("conditional run claim requires observed lease owner")
		}
		out := &runClaimMutation{Id: rec.Id, ExpectedStatus: strings.TrimSpace(cond.Status), ExpectedAttempt: *cond.Attempt, ExpectedLeaseOwner: strings.TrimSpace(*cond.LeaseOwner)}
		out.Has = &runClaimMutationHas{Id: true, ExpectedStatus: true, ExpectedAttempt: true, ExpectedLeaseOwner: true}
		out.Attempt, out.Has.Attempt = rec.Attempt, has.Attempt
		out.LeaseOwner, out.Has.LeaseOwner = rec.LeaseOwner, has.LeaseOwner
		out.LeaseUntil, out.Has.LeaseUntil = rec.LeaseUntil, has.LeaseUntil
		out.LastHeartbeatAt, out.Has.LastHeartbeatAt = rec.LastHeartbeatAt, has.LastHeartbeatAt
		out.HeartbeatIntervalSec, out.Has.HeartbeatIntervalSec = rec.HeartbeatIntervalSec, has.HeartbeatIntervalSec
		out.WorkerHost, out.Has.WorkerHost = rec.WorkerHost, has.WorkerHost
		out.UpdatedAt, out.Has.UpdatedAt = rec.UpdatedAt, has.UpdatedAt
		return out, nil
	default:
		return nil, fmt.Errorf("conditional run patch requires both status and attempt criteria for a claim")
	}
}

func optionalString(v string, set bool) (*string, bool) {
	if !set {
		return nil, false
	}
	return &v, true
}

// unsupportedConditionalColumns lists marked columns that no conditional
// mutation shape can carry, so misuse fails loudly instead of silently
// dropping a write.
func unsupportedConditionalColumns(has *RunHas) []string {
	var out []string
	check := func(set bool, name string) {
		if set {
			out = append(out, name)
		}
	}
	check(has.TurnID, "turn_id")
	check(has.ScheduleID, "schedule_id")
	check(has.ConversationID, "conversation_id")
	check(has.ConversationKind, "conversation_kind")
	check(has.ResumedFromRunID, "resumed_from_run_id")
	check(has.MaxIterations, "max_iterations")
	check(has.CheckpointResponseID, "checkpoint_response_id")
	check(has.CheckpointMessageID, "checkpoint_message_id")
	check(has.CheckpointData, "checkpoint_data")
	check(has.AgentID, "agent_id")
	check(has.ModelProvider, "model_provider")
	check(has.Model, "model")
	check(has.WorkerID, "worker_id")
	check(has.WorkerPID, "worker_pid")
	check(has.SecurityContext, "security_context")
	check(has.UserCredURL, "user_cred_url")
	check(has.EffectiveUserID, "effective_user_id")
	check(has.ScheduledFor, "scheduled_for")
	check(has.PreconditionRanAt, "precondition_ran_at")
	check(has.PreconditionPassed, "precondition_passed")
	check(has.PreconditionResult, "precondition_result")
	check(has.UsagePromptTokens, "usage_prompt_tokens")
	check(has.UsageCompletionTokens, "usage_completion_tokens")
	check(has.UsageTotalTokens, "usage_total_tokens")
	check(has.UsageCost, "usage_cost")
	check(has.CreatedAt, "created_at")
	check(has.StartedAt, "started_at")
	return out
}
