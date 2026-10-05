package write

import (
	context "context"
	"errors"
	"fmt"
	read "github.com/viant/agently-core/internal/datly/run/read"

	"github.com/viant/agently-core/internal/datly/invariant"
	datlypredicate "github.com/viant/agently-core/internal/datly/predicate"
	dexec "github.com/viant/datly/exec"
	rhandler "github.com/viant/datly/runtime/handler"
	"github.com/viant/datly/spec"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
	"time"
)

// Lifecycle customizes role Input.Runs.
type Lifecycle struct {
	Input   *Input                 `bind:"kind=input"`
	Invoker dexec.ComponentInvoker `bind:"kind=component_invoker"`
}

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (input *Input) Init(ctx context.Context) error {
	if input.LeaseMode != "" {
		if input.LeaseMode != "claim" && input.LeaseMode != "release" {
			return fmt.Errorf("unsupported run lease operation")
		}
		input.LeaseOwner = strings.TrimSpace(input.LeaseOwner)
		if input.LeaseOwner == "" || len(input.Runs) != 1 || input.Runs[0] == nil || strings.TrimSpace(input.Runs[0].Id) == "" || input.Runs[0].Condition != nil {
			return fmt.Errorf("run lease requires one identity and owner without a patch condition")
		}
		row := input.Runs[0]
		row.SetId(strings.TrimSpace(row.Id))
		if input.LeaseMode == "claim" && (row.LeaseUntil == nil || row.LeaseUntil.IsZero()) {
			return fmt.Errorf("run lease expiry is required")
		}
		if input.LeaseNow.IsZero() {
			input.LeaseNow = time.Now().UTC()
		}
		indexes, err := input.ReadIndexes(ctx)
		if err != nil {
			return err
		}
		if !indexes.CurrentWriterById.Has(row.Id) {
			input.Runs = nil
			return nil
		}
		input.SetExpectations([]*datlypredicate.RunExpected{{Id: row.Id, LeaseMode: input.LeaseMode, LeaseOwner: input.LeaseOwner, LeaseNow: input.LeaseNow}})
		return nil
	}
	guarded := false
	for _, row := range input.Runs {
		if row != nil && row.Condition != nil {
			guarded = true
		}
	}
	if !guarded {
		return nil
	}
	indexes, err := input.ReadIndexes(ctx)
	if err != nil {
		return err
	}
	expectations := make([]*datlypredicate.RunExpected, 0, len(input.Runs))
	seen := map[string]bool{}
	for _, row := range input.Runs {
		if row == nil {
			continue
		}
		if seen[row.Id] {
			return fmt.Errorf("duplicate identity in conditional run batch")
		}
		seen[row.Id] = true
		if row.Condition != nil {
			if !indexes.CurrentWriterById.Has(row.Id) {
				return fmt.Errorf("conditional run patch requires existing run")
			}
			if row.Has == nil {
				return fmt.Errorf("conditional run patch requires marked columns")
			}
			if columns := unsupportedConditionalColumns(row.Has); len(columns) > 0 {
				return fmt.Errorf("conditional run patch does not support columns: %s", strings.Join(columns, ", "))
			}
			condition := row.Condition
			if condition.LeaseOwner == nil || strings.TrimSpace(*condition.LeaseOwner) == "" {
				return fmt.Errorf("conditional run patch requires observed owner")
			}
			status := strings.TrimSpace(condition.Status)
			if (status == "") != (condition.Attempt == nil) {
				return fmt.Errorf("conditional run claim requires status and attempt together")
			}
			if status == "" && row.Has.Attempt {
				return fmt.Errorf("owner guard cannot change attempt")
			}
			if status != "" && (row.Has.Status || row.Has.ErrorCode || row.Has.ErrorMessage || row.Has.Iteration || row.Has.CompletedAt) {
				return fmt.Errorf("conditional run claim cannot change runtime result fields")
			}
		}
		expectations = append(expectations, &datlypredicate.RunExpected{Id: row.Id, Condition: row.Condition})
	}
	input.SetExpectations(expectations)
	return nil
}

func (hooks *Lifecycle) Recover(ctx context.Context, input *Input, out *Output, outcome rhandler.MutationOutcome) (rhandler.Recovery, error) {
	if outcome.Mutation.Table != "run" || len(input.Runs) != 1 || (input.Runs[0].Condition == nil && input.LeaseMode == "") {
		return rhandler.RecoveryNone, nil
	}
	var conflict *xhandler.Conflict
	if !errors.As(outcome.Mutation.Error, &conflict) {
		return rhandler.RecoveryNone, nil
	}
	if input.LeaseMode != "" {
		out.LeaseResult = false
		if input.LeaseMode == "claim" && hooks.Invoker != nil {
			query := &read.RunRowsInput{}
			query.SetId(input.Runs[0].Id)
			query.SetInternalMode(true)
			query.SetReadMode("rows")
			subject := ""
			query.SetVisibilitySubject(&subject)
			value, err := hooks.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"}}, Input: query})
			if err != nil {
				return rhandler.RecoveryNone, err
			}
			for _, row := range value.(*read.RunRowsOutput).Data {
				if row.Id == input.Runs[0].Id && row.CompletedAt == nil && row.LeaseOwner != nil && *row.LeaseOwner == input.LeaseOwner && row.LeaseUntil != nil && !row.LeaseUntil.Before(input.LeaseNow) {
					out.LeaseResult = true
				}
			}
		}
		return rhandler.RecoveryAccept, nil
	}
	// The legacy conditional PATCH acknowledges a zero-row update as a no-op.
	// Native recovery retains rollback evidence and never executes replacement DML.
	return rhandler.RecoveryAccept, nil
}

func (hooks *Lifecycle) Init(ctx context.Context, entity *MutableRunView, state xhandler.LifecycleContext[MutableRunView, xhandler.NoParent, Output]) error {
	if err := invariant.ValidateNativeProtocolFields(entity, state.Previous != nil); err != nil {
		return err
	}
	if entity != nil && state.Previous == nil {
		zero := 0
		entity.SetProtocolRevision(&zero)
		entity.SetProtocolLastSequence(&zero)
		entity.SetProtocolLeaseRevision(&zero)
	}
	if entity != nil {
		if entity.Has != nil && entity.Has.RunKind && entity.RunKind != "execution" {
			return fmt.Errorf("native run kind cannot be cleared or changed")
		}
		if entity.RunKind != "" && entity.RunKind != "execution" {
			return fmt.Errorf("native run writer only accepts execution runs")
		}
		if state.Previous == nil {
			entity.SetRunKind("execution")
		}
	}
	if hooks.Input != nil && hooks.Input.OrphanDetach {
		if hooks.Input.LeaseMode != "" {
			return fmt.Errorf("maintenance modes are mutually exclusive")
		}
		if entity != nil && entity.Condition != nil {
			return fmt.Errorf("orphan detach cannot include a run patch condition")
		}
		return invariant.ValidateOrphanDetach(entity, state.Previous, hooks.Input.OrphanColumn, []string{"turn_id", "schedule_id", "conversation_id", "resumed_from_run_id", "checkpoint_message_id"}, "Id")
	}

	if entity != nil && hooks.Input.LeaseMode != "" {
		entity.Has = &MutableRunViewHas{Id: true}
		if hooks.Input.LeaseMode == "claim" {
			owner := hooks.Input.LeaseOwner
			entity.SetLeaseOwner(&owner)
			until := entity.LeaseUntil.UTC()
			entity.SetLeaseUntil(&until)
		} else {
			entity.SetLeaseOwner(nil)
			entity.SetLeaseUntil(nil)
		}
		return nil
	}
	if entity == nil || state.Previous != nil {
		return nil
	}
	if entity.Status == "" {
		entity.SetStatus("pending")
	}
	if entity.ConversationKind == nil {
		value := "interactive"
		entity.SetConversationKind(&value)
	}
	if entity.Attempt == nil {
		value := 1
		entity.SetAttempt(&value)
	}
	if entity.Iteration == nil {
		value := 0
		entity.SetIteration(&value)
	}
	if entity.CreatedAt == nil {
		value := time.Now().UTC()
		entity.SetCreatedAt(&value)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *MutableRunView, state xhandler.LifecycleContext[MutableRunView, xhandler.NoParent, Output]) error {
	if entity != nil && entity.RunKind != "" && entity.RunKind != "execution" {
		return fmt.Errorf("native run writer only accepts execution runs")
	}
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *MutableRunView, state xhandler.LifecycleContext[MutableRunView, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *MutableRunView, state xhandler.LifecycleContext[MutableRunView, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	if input.LeaseMode != "" {
		output.LeaseResult = outcome.Error == nil && len(input.Runs) == 1 && (outcome.State() == xhandler.TransactionCommitted || outcome.State() == xhandler.TransactionCallerPending)
	}
	return nil
}

func unsupportedConditionalColumns(has *MutableRunViewHas) []string {
	var out []string
	check := func(set bool, name string) {
		if set {
			out = append(out, name)
		}
	}
	check(has.TurnId, "turn_id")
	check(has.ScheduleId, "schedule_id")
	check(has.ConversationId, "conversation_id")
	check(has.ConversationKind, "conversation_kind")
	check(has.ResumedFromRunId, "resumed_from_run_id")
	check(has.MaxIterations, "max_iterations")
	check(has.CheckpointResponseId, "checkpoint_response_id")
	check(has.CheckpointMessageId, "checkpoint_message_id")
	check(has.CheckpointData, "checkpoint_data")
	check(has.AgentId, "agent_id")
	check(has.ModelProvider, "model_provider")
	check(has.Model, "model")
	check(has.WorkerId, "worker_id")
	check(has.WorkerPid, "worker_pid")
	check(has.SecurityContext, "security_context")
	check(has.UserCredUrl, "user_cred_url")
	check(has.EffectiveUserId, "effective_user_id")
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
