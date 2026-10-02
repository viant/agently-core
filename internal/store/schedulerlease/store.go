// Package schedulerlease invokes the canonical generated schedule and run
// writers for scheduler lease claims and releases.
package schedulerlease

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	runwrite "github.com/viant/agently-core/internal/datly/run/write"
	schedulewrite "github.com/viant/agently-core/internal/datly/schedule/write"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

type Store struct{ Invoker dexec.ComponentInvoker }

var scheduleTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[schedulewrite.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/scheduler/"},
}
var runTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[runwrite.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/run"},
}

func (s *Store) Schedule(ctx context.Context, id, owner string, until time.Time, mode string) (bool, error) {
	if s == nil || s.Invoker == nil {
		return false, fmt.Errorf("scheduler component invoker is required")
	}
	id, owner = strings.TrimSpace(id), strings.TrimSpace(owner)
	if id == "" || owner == "" || mode != "claim" && mode != "release" {
		return false, fmt.Errorf("schedule lease identity, owner and mode are required")
	}
	row := &schedulewrite.Schedule{}
	row.SetId(id)
	if mode == "claim" {
		if until.IsZero() {
			return false, fmt.Errorf("schedule lease expiry is required")
		}
		at := until.UTC()
		row.SetLeaseUntil(&at)
	}
	input := &schedulewrite.Input{}
	input.SetLeaseMode(mode)
	input.SetLeaseOwner(owner)
	input.SetLeaseNow(time.Now().UTC())
	input.SetSchedules([]*schedulewrite.Schedule{row})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: scheduleTarget, Input: input})
	if err != nil {
		return false, err
	}
	out, ok := value.(*schedulewrite.Output)
	if !ok || out == nil {
		return false, fmt.Errorf("schedule lease writer returned %T", value)
	}
	return out.LeaseResult, nil
}

func (s *Store) Run(ctx context.Context, id, owner string, until time.Time, mode string) (bool, error) {
	if s == nil || s.Invoker == nil {
		return false, fmt.Errorf("scheduler component invoker is required")
	}
	id, owner = strings.TrimSpace(id), strings.TrimSpace(owner)
	if id == "" || owner == "" || mode != "claim" && mode != "release" {
		return false, fmt.Errorf("run lease identity, owner and mode are required")
	}
	row := &runwrite.MutableRunView{}
	row.SetId(id)
	if mode == "claim" {
		if until.IsZero() {
			return false, fmt.Errorf("run lease expiry is required")
		}
		at := until.UTC()
		row.SetLeaseUntil(&at)
	}
	input := &runwrite.Input{}
	input.SetLeaseMode(mode)
	input.SetLeaseOwner(owner)
	input.SetLeaseNow(time.Now().UTC())
	input.SetRuns([]*runwrite.MutableRunView{row})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: runTarget, Input: input})
	if err != nil {
		return false, err
	}
	out, ok := value.(*runwrite.Output)
	if !ok || out == nil {
		return false, fmt.Errorf("run lease writer returned %T", value)
	}
	return out.LeaseResult, nil
}

func (s *Store) TryClaimSchedule(ctx context.Context, id, owner string, until time.Time) (bool, error) {
	return s.Schedule(ctx, id, owner, until, "claim")
}
func (s *Store) ReleaseSchedule(ctx context.Context, id, owner string) (bool, error) {
	return s.Schedule(ctx, id, owner, time.Time{}, "release")
}
func (s *Store) TryClaimRun(ctx context.Context, id, owner string, until time.Time) (bool, error) {
	return s.Run(ctx, id, owner, until, "claim")
}
func (s *Store) ReleaseRun(ctx context.Context, id, owner string) (bool, error) {
	return s.Run(ctx, id, owner, time.Time{}, "release")
}
