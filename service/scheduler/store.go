package scheduler

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/viant/agently-core/app/store/data"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	schrun "github.com/viant/agently-core/pkg/agently/scheduler/run"
	runlease "github.com/viant/agently-core/pkg/agently/scheduler/run/lease"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	schlease "github.com/viant/agently-core/pkg/agently/scheduler/schedule/lease"
	schedwrite "github.com/viant/agently-core/pkg/agently/scheduler/schedule/write"
	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"
)

// Store provides persisted scheduler reads/writes backed by Datly components.
type Store interface {
	Get(ctx context.Context, id string) (*schedulepkg.ScheduleView, error)
	List(ctx context.Context) ([]*schedulepkg.ScheduleView, error)
	ListRuns(ctx context.Context, in *schrun.RunListInput) ([]*schrun.RunView, error)
	ListForRunDue(ctx context.Context) ([]*schedulepkg.ScheduleView, error)
	PatchSchedule(ctx context.Context, schedule *schedwrite.Schedule) error
	PatchRuns(ctx context.Context, rows []*agrunwrite.MutableRunView) error
	ListRunsForDue(ctx context.Context, scheduleID string, scheduledFor *time.Time, excludeStatuses []string) ([]*schrun.RunView, error)
	TryClaimSchedule(ctx context.Context, scheduleID, leaseOwner string, leaseUntil time.Time) (bool, error)
	ReleaseScheduleLease(ctx context.Context, scheduleID, leaseOwner string) (bool, error)
	TryClaimRun(ctx context.Context, runID, leaseOwner string, leaseUntil time.Time) (bool, error)
	ReleaseRunLease(ctx context.Context, runID, leaseOwner string) (bool, error)
}

type datlyStore struct {
	dao  *datly.Service
	data data.Service
}

var schedulerComponentsByDAO sync.Map

func NewDatlyStore(ctx context.Context, dao *datly.Service, dataSvc data.Service) (Store, error) {
	if dao == nil {
		return nil, errors.New("scheduler store requires a non-nil datly service")
	}
	s := &datlyStore{dao: dao, data: dataSvc}
	if s.data == nil {
		s.data = data.NewService(dao)
	}
	if err := s.init(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *datlyStore) init(ctx context.Context) error {
	key := reflect.ValueOf(s.dao).Pointer()
	if _, loaded := schedulerComponentsByDAO.LoadOrStore(key, struct{}{}); loaded {
		return nil
	}
	if err := schedulepkg.DefineScheduleComponent(ctx, s.dao); err != nil {
		return err
	}
	if err := schedulepkg.DefineScheduleListComponent(ctx, s.dao); err != nil {
		return err
	}
	if err := schedulepkg.DefineScheduleRunDueListComponent(ctx, s.dao); err != nil {
		return err
	}
	if err := schrun.DefineRunComponent(ctx, s.dao); err != nil {
		return err
	}
	if err := schrun.DefineRunListComponent(ctx, s.dao); err != nil {
		return err
	}
	if err := schrun.DefineRunDueComponent(ctx, s.dao); err != nil {
		return err
	}
	if _, err := schedwrite.DefineComponent(ctx, s.dao); err != nil {
		return err
	}
	if _, err := schlease.DefineClaimLeaseComponent(ctx, s.dao); err != nil {
		return err
	}
	if _, err := schlease.DefineReleaseLeaseComponent(ctx, s.dao); err != nil {
		return err
	}
	if _, err := runlease.DefineClaimLeaseComponent(ctx, s.dao); err != nil {
		return err
	}
	if _, err := runlease.DefineReleaseLeaseComponent(ctx, s.dao); err != nil {
		return err
	}
	return nil
}

func (s *datlyStore) Get(ctx context.Context, id string) (*schedulepkg.ScheduleView, error) {
	if s == nil || s.dao == nil {
		return nil, nil
	}
	in := &schedulepkg.ScheduleInput{Id: id, Has: &schedulepkg.ScheduleInputHas{Id: true}}
	out := &schedulepkg.ScheduleOutput{}
	uri := strings.ReplaceAll(schedulepkg.SchedulePathURI, "{id}", id)
	if _, err := s.dao.Operate(ctx, datly.WithURI(uri), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	return out.Data[0], nil
}

func (s *datlyStore) List(ctx context.Context) ([]*schedulepkg.ScheduleView, error) {
	if s == nil || s.dao == nil {
		return nil, nil
	}
	out := &schedulepkg.ScheduleOutput{}
	if _, err := s.dao.Operate(ctx,
		datly.WithURI(schedulepkg.SchedulePathListURI),
		datly.WithInput(&schedulepkg.ScheduleListInput{}),
		datly.WithOutput(out),
	); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (s *datlyStore) ListRuns(ctx context.Context, in *schrun.RunListInput) ([]*schrun.RunView, error) {
	if s == nil || s.dao == nil {
		return nil, nil
	}
	input := &schrun.RunListInput{Has: &schrun.RunListInputHas{}}
	if in != nil {
		copyValue := *in
		if copyValue.Has == nil {
			copyValue.Has = &schrun.RunListInputHas{}
		}
		input = &copyValue
	}
	out := &schrun.RunListOutput{}
	if _, err := s.dao.Operate(ctx,
		datly.WithURI(schrun.RunListPathURI),
		datly.WithInput(input),
		datly.WithOutput(out),
	); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (s *datlyStore) ListForRunDue(ctx context.Context) ([]*schedulepkg.ScheduleView, error) {
	if s == nil || s.dao == nil {
		return nil, nil
	}
	out := &schedulepkg.ScheduleOutput{}
	if _, err := s.dao.Operate(ctx,
		datly.WithURI(schedulepkg.SchedulePathListRunDueURI),
		datly.WithInput(&schedulepkg.ScheduleRunDueListInput{}),
		datly.WithOutput(out),
	); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (s *datlyStore) PatchSchedule(ctx context.Context, schedule *schedwrite.Schedule) error {
	if s == nil || s.dao == nil || schedule == nil {
		return nil
	}
	in := &schedwrite.Input{Schedules: []*schedwrite.Schedule{schedule}}
	out := &schedwrite.Output{}
	if _, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPatch, schedwrite.PathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	); err != nil {
		return err
	}
	if len(out.Violations) > 0 {
		return errors.New(out.Violations[0].Message)
	}
	return nil
}

func (s *datlyStore) PatchRuns(ctx context.Context, rows []*agrunwrite.MutableRunView) error {
	if s == nil || s.data == nil || len(rows) == 0 {
		return nil
	}
	_, err := s.data.PatchRuns(ctx, rows)
	return err
}

func (s *datlyStore) ListRunsForDue(ctx context.Context, scheduleID string, scheduledFor *time.Time, excludeStatuses []string) ([]*schrun.RunView, error) {
	if s == nil || s.dao == nil || strings.TrimSpace(scheduleID) == "" {
		return nil, nil
	}
	in := &schrun.RunDueInput{
		Id:  strings.TrimSpace(scheduleID),
		Has: &schrun.RunDueInputHas{Id: true},
	}
	if scheduledFor != nil && !scheduledFor.IsZero() {
		in.ScheduledFor = scheduledFor.UTC()
		in.Has.ScheduledFor = true
	}
	if len(excludeStatuses) > 0 {
		in.ExcludeStatuses = append([]string(nil), excludeStatuses...)
		in.Has.ExcludeStatuses = true
	}
	out := &schrun.RunOutput{}
	uri := strings.ReplaceAll(schrun.RunPathRunDueURI, "{id}", strings.TrimSpace(scheduleID))
	if _, err := s.dao.Operate(ctx, datly.WithURI(uri), datly.WithInput(in), datly.WithOutput(out)); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (s *datlyStore) TryClaimSchedule(ctx context.Context, scheduleID, leaseOwner string, leaseUntil time.Time) (bool, error) {
	out := &schlease.ClaimLeaseOutput{}
	in := &schlease.ClaimLeaseInput{
		ScheduleID: strings.TrimSpace(scheduleID),
		LeaseOwner: strings.TrimSpace(leaseOwner),
		LeaseUntil: leaseUntil.UTC(),
		Now:        time.Now().UTC(),
		Has:        &schlease.ClaimLeaseInputHas{ScheduleID: true, LeaseOwner: true, LeaseUntil: true, Now: true},
	}
	if _, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPost, schlease.ClaimLeasePathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	); err != nil {
		return false, err
	}
	return out.Claimed, nil
}

func (s *datlyStore) ReleaseScheduleLease(ctx context.Context, scheduleID, leaseOwner string) (bool, error) {
	out := &schlease.ReleaseLeaseOutput{}
	in := &schlease.ReleaseLeaseInput{
		ScheduleID: strings.TrimSpace(scheduleID),
		LeaseOwner: strings.TrimSpace(leaseOwner),
		Has:        &schlease.ReleaseLeaseInputHas{ScheduleID: true, LeaseOwner: true},
	}
	if _, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPost, schlease.ReleaseLeasePathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	); err != nil {
		return false, err
	}
	return out.Released, nil
}

func (s *datlyStore) TryClaimRun(ctx context.Context, runID, leaseOwner string, leaseUntil time.Time) (bool, error) {
	out := &runlease.ClaimLeaseOutput{}
	in := &runlease.ClaimLeaseInput{
		RunID:      strings.TrimSpace(runID),
		LeaseOwner: strings.TrimSpace(leaseOwner),
		LeaseUntil: leaseUntil.UTC(),
		Now:        time.Now().UTC(),
		Has:        &runlease.ClaimLeaseInputHas{RunID: true, LeaseOwner: true, LeaseUntil: true, Now: true},
	}
	if _, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPost, runlease.ClaimLeasePathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	); err != nil {
		return false, err
	}
	return out.Claimed, nil
}

func (s *datlyStore) ReleaseRunLease(ctx context.Context, runID, leaseOwner string) (bool, error) {
	out := &runlease.ReleaseLeaseOutput{}
	in := &runlease.ReleaseLeaseInput{
		RunID:      strings.TrimSpace(runID),
		LeaseOwner: strings.TrimSpace(leaseOwner),
		Has:        &runlease.ReleaseLeaseInputHas{RunID: true, LeaseOwner: true},
	}
	if _, err := s.dao.Operate(ctx,
		datly.WithPath(contract.NewPath(http.MethodPost, runlease.ReleaseLeasePathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	); err != nil {
		return false, err
	}
	return out.Released, nil
}
