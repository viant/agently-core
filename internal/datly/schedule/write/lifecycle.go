package write

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	reader "github.com/viant/agently-core/internal/datly/schedule/read"

	"github.com/viant/agently-core/internal/datly/invariant"
	dexec "github.com/viant/datly/exec"
	rhandler "github.com/viant/datly/runtime/handler"
	"github.com/viant/datly/spec"
	xhandler "github.com/viant/xdatly/handler"
	"reflect"
	"strings"
	"time"
)

type Lifecycle struct {
	Input   *Input                 `bind:"kind=input"`
	Invoker dexec.ComponentInvoker `bind:"kind=component_invoker"`
}

func LifecycleDatlyType() reflect.Type { return reflect.TypeFor[Lifecycle]() }

var LifecycleHooks = new(Lifecycle)
var LifecycleDatly = LifecycleDatlyType()

func (input *Input) Init(ctx context.Context) error {
	indexes, err := input.ReadIndexes(ctx)
	if err != nil {
		return err
	}
	if input.LeaseMode != "" {
		if input.LeaseMode != "claim" && input.LeaseMode != "release" {
			return fmt.Errorf("unsupported lease operation")
		}
		input.LeaseOwner = strings.TrimSpace(input.LeaseOwner)
		if input.LeaseOwner == "" || len(input.Schedules) != 1 || input.Schedules[0] == nil || strings.TrimSpace(input.Schedules[0].Id) == "" || input.Schedules[0].ShouldDelete {
			return fmt.Errorf("lease operation requires one schedule identity and owner")
		}
		input.Schedules[0].SetId(strings.TrimSpace(input.Schedules[0].Id))
		if input.LeaseMode == "claim" && (input.Schedules[0].LeaseUntil == nil || input.Schedules[0].LeaseUntil.IsZero()) {
			return fmt.Errorf("leaseUntil is required")
		}
		if input.LeaseNow.IsZero() {
			input.LeaseNow = time.Now().UTC()
		}
		if !indexes.CurrentWriterById.Has(input.Schedules[0].Id) {
			input.Schedules = nil
		}
		return nil
	}
	// Legacy schedule deletion ignores empty identities before acknowledging them.
	filtered := make([]*Schedule, 0, len(input.Schedules))
	for _, rec := range input.Schedules {
		if rec != nil && rec.ShouldDelete && rec.Id == "" {
			continue
		}
		filtered = append(filtered, rec)
	}
	if input.Schedules != nil {
		input.Schedules = filtered
	}
	for _, rec := range input.Schedules {
		if rec != nil && !rec.ShouldDelete && !indexes.CurrentWriterById.Has(rec.Id) && strings.TrimSpace(rec.Id) == "" {
			rec.SetId(uuid.NewString())
		}
	}
	return nil
}

func (h *Lifecycle) Init(ctx context.Context, rec *Schedule, state xhandler.LifecycleContext[Schedule, xhandler.NoParent, Output]) error {
	if h.Input != nil && h.Input.OrphanDetach {
		if h.Input.LeaseMode != "" {
			return fmt.Errorf("maintenance modes are mutually exclusive")
		}
		return invariant.ValidateOrphanDetach(rec, state.Previous, h.Input.OrphanColumn, []string{"conversation_id", "goal_id"}, "Id")
	}

	if rec == nil || rec.ShouldDelete {
		return nil
	}
	if h.Input.LeaseMode != "" {
		rec.Has = &ScheduleHas{Id: true}
		if h.Input.LeaseMode == "claim" {
			owner := h.Input.LeaseOwner
			rec.SetLeaseOwner(&owner)
			until := rec.LeaseUntil.UTC()
			rec.SetLeaseUntil(&until)
		} else {
			rec.SetLeaseOwner(nil)
			rec.SetLeaseUntil(nil)
		}
		return nil
	}
	now := time.Now().UTC()
	userID := strings.TrimSpace(strPtrValue(h.Input.VisibilitySubject))
	if state.Previous == nil {
		if strings.TrimSpace(strPtrValue(rec.CreatedByUserId)) == "" && userID != "" {
			rec.SetCreatedByUserId(&userID)
		}
		if strings.TrimSpace(rec.Visibility) == "" {
			if userID == "" {
				rec.SetVisibility("public")
			} else {
				rec.SetVisibility("private")
			}
		} else if userID == "" && strings.EqualFold(strings.TrimSpace(rec.Visibility), "private") {
			rec.SetVisibility("public")
		}
		if rec.Timezone == "" {
			rec.SetTimezone("UTC")
		}
		if rec.Has == nil || !rec.Has.ScheduleType {
			rec.SetScheduleType("adhoc")
		}
		if rec.CreatedAt == nil {
			rec.SetCreatedAt(&now)
		}
		return nil
	}
	clearNextRunAtOnScheduleChange(rec, state.Previous)
	if strings.TrimSpace(strPtrValue(state.Previous.CreatedByUserId)) == "" && strings.TrimSpace(strPtrValue(rec.CreatedByUserId)) == "" && userID != "" {
		rec.SetCreatedByUserId(&userID)
	}
	if rec.UpdatedAt == nil {
		rec.SetUpdatedAt(&now)
	}
	return nil
}
func (h *Lifecycle) Validate(context.Context, *Schedule, xhandler.LifecycleContext[Schedule, xhandler.NoParent, Output]) error {
	return nil
}
func (h *Lifecycle) AfterSequence(context.Context, *Schedule, xhandler.LifecycleContext[Schedule, xhandler.NoParent, Output]) error {
	return nil
}
func (h *Lifecycle) AfterQueue(context.Context, *Schedule, xhandler.LifecycleContext[Schedule, xhandler.NoParent, Output]) error {
	return nil
}
func (h *Lifecycle) Finalize(_ context.Context, input *Input, out *Output, outcome xhandler.Outcome) error {
	if input.LeaseMode != "" {
		out.LeaseResult = outcome.Error == nil && len(input.Schedules) == 1 && (outcome.State() == xhandler.TransactionCommitted || outcome.State() == xhandler.TransactionCallerPending)
	}
	return nil
}

func (h *Lifecycle) Recover(ctx context.Context, input *Input, out *Output, outcome rhandler.MutationOutcome) (rhandler.Recovery, error) {
	if input.LeaseMode == "" || outcome.Mutation.Table != "schedule" {
		return rhandler.RecoveryNone, nil
	}
	var conflict *xhandler.Conflict
	if !errors.As(outcome.Mutation.Error, &conflict) {
		return rhandler.RecoveryNone, nil
	}
	out.LeaseResult = false
	if input.LeaseMode == "claim" && h.Invoker != nil && len(input.Schedules) == 1 {
		query := &reader.ScheduleInput{}
		query.SetId(input.Schedules[0].Id)
		query.SetInternalMode(true)
		query.SetVisibilitySubject(input.VisibilitySubject)
		value, err := h.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[reader.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/scheduler/schedule/{id}"}}, Input: query})
		if err != nil {
			return rhandler.RecoveryNone, err
		}
		for _, row := range value.(*reader.ScheduleOutput).Data {
			if row.Id == input.Schedules[0].Id && row.Enabled && row.LeaseOwner != nil && *row.LeaseOwner == input.LeaseOwner && row.LeaseUntil != nil && !row.LeaseUntil.Before(input.LeaseNow) {
				out.LeaseResult = true
			}
		}
	}
	return rhandler.RecoveryAccept, nil
}

func clearNextRunAtOnScheduleChange(rec *Schedule, cur *Schedule) {
	if rec == nil || cur == nil || rec.Has == nil {
		return
	}

	changed := false

	if rec.Has.StartAt && !timePtrEqual(rec.StartAt, cur.StartAt) {
		changed = true
	}
	if rec.Has.EndAt && !timePtrEqual(rec.EndAt, cur.EndAt) {
		changed = true
	}
	if rec.Has.CronExpr && !stringPtrEqual(rec.CronExpr, cur.CronExpr) {
		changed = true
	}
	if rec.Has.IntervalSeconds && !intPtrEqual(rec.IntervalSeconds, cur.IntervalSeconds) {
		changed = true
	}
	if rec.Has.Timezone && strings.TrimSpace(rec.Timezone) != strings.TrimSpace(cur.Timezone) {
		changed = true
	}
	if rec.Has.ScheduleType && strings.TrimSpace(rec.ScheduleType) != strings.TrimSpace(cur.ScheduleType) {
		changed = true
	}
	if rec.Has.Enabled && rec.Enabled && !cur.Enabled {
		changed = true
	}

	if !changed {
		return
	}

	rec.NextRunAt = nil
	if rec.Has == nil {
		rec.Has = &ScheduleHas{}
	}
	rec.Has.NextRunAt = true
}

func timePtrEqual(a *time.Time, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}

func stringPtrEqual(a *string, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return strings.TrimSpace(*a) == strings.TrimSpace(*b)
}

func intPtrEqual(a *int, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func strPtrValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
