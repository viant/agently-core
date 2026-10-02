package scheduler

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/sqlitewrite"
	schedulerlease "github.com/viant/agently-core/internal/store/schedulerlease"
	runmodel "github.com/viant/agently-core/model/run"
	schedulemodel "github.com/viant/agently-core/model/schedule"
	scheduledrunmodel "github.com/viant/agently-core/model/scheduledrun"

	dexec "github.com/viant/datly/exec"
)

// Store provides persisted scheduler reads/writes backed by Datly components.
type Store interface {
	Get(ctx context.Context, id string) (*schedulemodel.ScheduleView, error)
	List(ctx context.Context) ([]*schedulemodel.ScheduleView, error)
	ListRuns(ctx context.Context, in *scheduledrunmodel.RunListInput, page, size int) (*RunListPage, error)
	ListForRunDue(ctx context.Context) ([]*schedulemodel.ScheduleView, error)
	DeleteScheduledRun(ctx context.Context, id string) error
	DeleteSchedule(ctx context.Context, id string) error
	PatchSchedule(ctx context.Context, schedule *schedulemodel.Schedule) error
	PatchRuns(ctx context.Context, rows []*runmodel.MutableRunView) error
	ListRunsForDue(ctx context.Context, scheduleID string, scheduledFor *time.Time, excludeStatuses []string) ([]*scheduledrunmodel.RunView, error)
	TryClaimSchedule(ctx context.Context, scheduleID, leaseOwner string, leaseUntil time.Time) (bool, error)
	ReleaseScheduleLease(ctx context.Context, scheduleID, leaseOwner string) (bool, error)
	TryClaimRun(ctx context.Context, runID, leaseOwner string, leaseUntil time.Time) (bool, error)
	ReleaseRunLease(ctx context.Context, runID, leaseOwner string) (bool, error)
}

type datlyStore struct {
	writeGate string
	data      data.Service
	native    dexec.ComponentInvoker
}

type RunListPage struct {
	Rows       []*scheduledrunmodel.RunView
	PageCount  int
	TotalCount int
}

func NewDatlyStore(ctx context.Context, invoker dexec.ComponentInvoker, dataSvc data.Service) (Store, error) {
	if invoker == nil || (reflect.ValueOf(invoker).Kind() == reflect.Pointer && reflect.ValueOf(invoker).IsNil()) {
		return nil, errors.New("scheduler store requires a native component invoker")
	}
	if dataSvc == nil {
		return nil, errors.New("scheduler store requires a data service")
	}
	store := &datlyStore{native: invoker, data: dataSvc}
	if metadata, ok := invoker.(interface {
		ConfiguredDriver(context.Context, string) (string, error)
		ConnectionIdentity(context.Context, string) (string, error)
	}); ok {
		driver, err := metadata.ConfiguredDriver(ctx, "agently")
		if err != nil {
			return nil, err
		}
		identity, err := metadata.ConnectionIdentity(ctx, "agently")
		if err != nil {
			return nil, err
		}
		store.writeGate = sqlitewrite.KeyForConnector(driver, identity, "agently")
	}
	return store, nil
}

func (s *datlyStore) Get(ctx context.Context, id string) (*schedulemodel.ScheduleView, error) {
	rows, err := s.listSchedulesNative(ctx, id, false)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}
func (s *datlyStore) List(ctx context.Context) ([]*schedulemodel.ScheduleView, error) {
	return s.listSchedulesNative(ctx, "", false)
}
func (s *datlyStore) ListRuns(ctx context.Context, in *scheduledrunmodel.RunListInput, page, size int) (*RunListPage, error) {
	return s.listRunsNative(ctx, in, page, size)
}

func cloneRunInputs(ctx context.Context, in *scheduledrunmodel.RunListInput, page, size int) (*scheduledrunmodel.RunListInput, *scheduledrunmodel.RunTotalInput) {
	listInput := &scheduledrunmodel.RunListInput{Has: &scheduledrunmodel.RunListInputHas{}}
	totalInput := &scheduledrunmodel.RunTotalInput{Has: &scheduledrunmodel.RunTotalInputHas{}}
	if in != nil && in.Has != nil && in.Has.Since {
		listInput.Since = in.Since
		listInput.Has.Since = true
		totalInput.Since = in.Since
		totalInput.Has.Since = true
	}
	effectiveUserID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if in != nil {
		if incomingUserID := strings.TrimSpace(in.EffectiveUserID); incomingUserID != "" {
			effectiveUserID = incomingUserID
		}
	}
	listInput.EffectiveUserID = effectiveUserID
	listInput.Has.EffectiveUserID = true
	listInput.Limit = size
	listInput.Has.Limit = true
	listInput.Offset = (page - 1) * size
	listInput.Has.Offset = true
	totalInput.EffectiveUserID = effectiveUserID
	totalInput.Has.EffectiveUserID = true
	if in != nil {
		if scheduleID := strings.TrimSpace(in.ScheduleId); scheduleID != "" {
			listInput.ScheduleId = scheduleID
			listInput.Has.ScheduleId = true
			totalInput.ScheduleId = scheduleID
			totalInput.Has.ScheduleId = true
		}
		if status := strings.TrimSpace(in.RunStatus); status != "" {
			status = containsPattern(status)
			listInput.RunStatus = status
			listInput.Has.RunStatus = true
			totalInput.RunStatus = status
			totalInput.Has.RunStatus = true
		}
		if conversationID := strings.TrimSpace(in.ConversationId); conversationID != "" {
			conversationID = containsPattern(conversationID)
			listInput.ConversationId = conversationID
			listInput.Has.ConversationId = true
			totalInput.ConversationId = conversationID
			totalInput.Has.ConversationId = true
		}
		if errorMessage := strings.TrimSpace(in.ErrorMessage); errorMessage != "" {
			errorMessage = containsPattern(errorMessage)
			listInput.ErrorMessage = errorMessage
			listInput.Has.ErrorMessage = true
			totalInput.ErrorMessage = errorMessage
			totalInput.Has.ErrorMessage = true
		}
	}
	return listInput, totalInput
}

func containsPattern(value string) string {
	return "%" + strings.TrimSpace(value) + "%"
}

func computePageCount(totalCount, size int) int {
	if totalCount <= 0 || size <= 0 {
		return 1
	}
	return int(math.Max(1, math.Ceil(float64(totalCount)/float64(size))))
}

func (s *datlyStore) ListForRunDue(ctx context.Context) ([]*schedulemodel.ScheduleView, error) {
	return s.listSchedulesNative(ctx, "", true)
}
func (s *datlyStore) PatchSchedule(ctx context.Context, schedule *schedulemodel.Schedule) error {
	if s == nil || schedule == nil {
		return nil
	}
	if schedule.Internal == nil {
		schedule.SetInternal(false)
	}
	_, err := sqlitewrite.Do(ctx, s.writeGate, func() (struct{}, error) { return struct{}{}, s.patchScheduleNative(ctx, schedule) })
	return err
}
func (s *datlyStore) DeleteSchedule(ctx context.Context, id string) error {
	if s == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	return s.data.DeleteScheduleCascade(ctx, id)
}

func (s *datlyStore) DeleteScheduledRun(ctx context.Context, id string) error {
	if s == nil || s.data == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	return s.data.DeleteScheduledRun(ctx, id)
}

func (s *datlyStore) PatchRuns(ctx context.Context, rows []*runmodel.MutableRunView) error {
	if s == nil || s.data == nil || len(rows) == 0 {
		return nil
	}
	_, err := s.data.PatchRuns(ctx, rows)
	return err
}

func (s *datlyStore) ListRunsForDue(ctx context.Context, scheduleID string, scheduledFor *time.Time, excludeStatuses []string) ([]*scheduledrunmodel.RunView, error) {
	if s == nil || strings.TrimSpace(scheduleID) == "" {
		return nil, nil
	}
	return s.listRunsForDueNative(ctx, scheduleID, scheduledFor, excludeStatuses)
}

func (s *datlyStore) leaseStore() (*schedulerlease.Store, error) {
	if s == nil || s.native == nil {
		return nil, errors.New("native scheduler lease runtime is required")
	}
	return &schedulerlease.Store{Invoker: s.native}, nil
}

func (s *datlyStore) TryClaimSchedule(ctx context.Context, scheduleID, leaseOwner string, leaseUntil time.Time) (bool, error) {
	store, err := s.leaseStore()
	if err != nil {
		return false, err
	}
	return sqlitewrite.Do(ctx, s.writeGate, func() (bool, error) {
		return store.TryClaimSchedule(native.WithAccess(ctx, native.Access{Internal: true, Mode: "rows"}), scheduleID, leaseOwner, leaseUntil)
	})
}

func (s *datlyStore) ReleaseScheduleLease(ctx context.Context, scheduleID, leaseOwner string) (bool, error) {
	store, err := s.leaseStore()
	if err != nil {
		return false, err
	}
	return sqlitewrite.Do(ctx, s.writeGate, func() (bool, error) {
		return store.ReleaseSchedule(native.WithAccess(ctx, native.Access{Internal: true, Mode: "rows"}), scheduleID, leaseOwner)
	})
}

func (s *datlyStore) TryClaimRun(ctx context.Context, runID, leaseOwner string, leaseUntil time.Time) (bool, error) {
	store, err := s.leaseStore()
	if err != nil {
		return false, err
	}
	return sqlitewrite.Do(ctx, s.writeGate, func() (bool, error) {
		return store.TryClaimRun(native.WithAccess(ctx, native.Access{Internal: true, Mode: "rows"}), runID, leaseOwner, leaseUntil)
	})
}

func (s *datlyStore) ReleaseRunLease(ctx context.Context, runID, leaseOwner string) (bool, error) {
	store, err := s.leaseStore()
	if err != nil {
		return false, err
	}
	return sqlitewrite.Do(ctx, s.writeGate, func() (bool, error) {
		return store.ReleaseRun(native.WithAccess(ctx, native.Access{Internal: true, Mode: "rows"}), runID, leaseOwner)
	})
}
