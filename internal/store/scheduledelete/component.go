package scheduledelete

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	legacyread "github.com/viant/agently-core/internal/datly/legacyrun/read"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	schedwrite "github.com/viant/agently-core/internal/datly/schedule/write"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	dexec "github.com/viant/datly/exec"
	custom "github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/datly/spec"
	"github.com/viant/x"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

var ErrScheduleNotFound = errors.New("schedule not found")
var ErrScheduledRunNotFound = errors.New("scheduled run not found")

type Component struct {
	Contract xdatly.Component[Input, Output] `component:"ScheduleCascadeDelete,path=/v1/internal/agently/scheduler/delete,method=POST,handler=NewDelete,internal=true"`
}
type Input struct {
	ID      string `parameter:"ID,kind=body,in=id,required=true"`
	RunOnly bool   `parameter:"RunOnly,kind=body,in=runOnly"`
}
type Output struct {
	ID string `json:"id"`
}
type Delete struct{}

func NewDelete() handler.Contract[Input, Output] { return &Delete{} }
func Exports() (*x.Registry, error) {
	registry := x.NewRegistry()
	for _, typ := range []reflect.Type{reflect.TypeFor[Input](), reflect.TypeFor[Output]()} {
		registry.Register(x.NewType(typ))
	}
	factory, err := x.NewFunction(reflect.TypeFor[Component]().PkgPath(), "NewDelete", custom.Factory(NewDelete))
	if err != nil {
		return nil, err
	}
	if err = registry.RegisterFunctions(factory); err != nil {
		return nil, err
	}
	return registry, nil
}

type dependencies struct {
	Invoker dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
	Starter handler.TransactionStarter `bind:"kind=transactionStarter,required"`
	Owner   *string                    `bind:"kind=visibility,in=subject,required"`
	Schema  tree.TableInspector        `bind:"kind=conversationtreeSchema,in=inspector,required"`
}

func (*Delete) Exec(ctx context.Context, session handler.Session, input *Input, output *Output) error {
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("schedule deletion invocation is incomplete")
	}
	deps := dependencies{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil || deps.Owner == nil || deps.Schema == nil {
		return fmt.Errorf("schedule deletion capabilities are unavailable")
	}
	id := strings.TrimSpace(input.ID)
	if id == "" {
		if input.RunOnly {
			return fmt.Errorf("%w: empty id", ErrScheduledRunNotFound)
		}
		return nil
	}
	ctx = dexec.WithTransactionIsolation(ctx, dexec.IsolationSerializable)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	owner := strings.TrimSpace(*deps.Owner)
	now := time.Now().UTC()
	var err error
	if input.RunOnly {
		err = deleteRun(ctx, deps, id, owner, now)
	} else {
		err = deleteSchedule(ctx, deps, id, owner, now)
	}
	if err != nil {
		return err
	}
	output.ID = id
	return nil
}

func deleteSchedule(ctx context.Context, deps dependencies, id, owner string, now time.Time) error {
	schedule, err := readSchedule(ctx, deps.Invoker, id, owner)
	if err != nil {
		return err
	}
	if stored := deref(schedule.CreatedByUserId); stored != "" && (owner == "" || stored != owner) {
		return tree.ErrPermissionDenied
	}
	if scheduleLeaseActive(schedule.LeaseUntilRaw, now) {
		return tree.ErrConversationActive
	}
	input := &runread.RunRowsInput{}
	input.SetScheduleId(id)
	runs, err := readRuns(ctx, deps.Invoker, input, owner, false)
	if err != nil {
		return err
	}
	legacyAvailable, err := legacyPresent(ctx, deps.Schema)
	if err != nil {
		return err
	}
	legacyIDs, runIDs, conversationIDs := []string{}, []string{}, []string{}
	for _, run := range runs {
		if run != nil {
			runIDs = append(runIDs, run.Id)
			conversationIDs = append(conversationIDs, deref(run.ConversationId))
		}
	}
	if legacyAvailable {
		input := &legacyread.Input{}
		input.SetScheduleID(id)
		runs, err := readLegacy(ctx, deps.Invoker, input, owner, false)
		if err != nil {
			return err
		}
		for _, run := range runs {
			if run != nil {
				legacyIDs = append(legacyIDs, run.Id)
				conversationIDs = append(conversationIDs, deref(run.ConversationId))
			}
		}
	}
	query := &convread.ConversationInput{}
	query.SetScheduleId(id)
	roots, err := ConversationRoots(ctx, deps.Invoker, owner, conversationIDs, query)
	if err != nil {
		return err
	}
	if err := invokeTree(ctx, deps.Invoker, roots, runIDs, legacyIDs, now); err != nil {
		return err
	}
	record := &schedwrite.Schedule{}
	record.SetId(id)
	record.SetShouldDelete(true)
	mutation := &schedwrite.Input{}
	mutation.SetSchedules([]*schedwrite.Schedule{record})
	value, err := deps.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: scheduleWriterTarget, Input: mutation, Providers: trustedProviders("scheduleaccess", owner)})
	if err != nil {
		return err
	}
	if output, ok := value.(*schedwrite.Output); !ok || output == nil {
		return fmt.Errorf("schedule writer returned %T", value)
	}
	return nil
}

var scheduleWriterTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[schedwrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/scheduler/"}}

type selectedRun struct {
	id, scheduleID, conversationID, effectiveUserID string
	legacy                                          bool
}

func locateRun(ctx context.Context, deps dependencies, id, owner string, legacyAvailable, lock bool, legacyOnly *bool) (*selectedRun, error) {
	if legacyOnly == nil || !*legacyOnly {
		input := &runread.RunRowsInput{}
		input.SetId(id)
		rows, err := readRuns(ctx, deps.Invoker, input, owner, lock)
		if err != nil {
			return nil, err
		}
		if len(rows) > 1 {
			return nil, fmt.Errorf("run identity returned %d rows", len(rows))
		}
		if len(rows) == 1 && rows[0] != nil && deref(rows[0].ScheduleId) != "" {
			r := rows[0]
			return &selectedRun{id: r.Id, scheduleID: deref(r.ScheduleId), conversationID: deref(r.ConversationId), effectiveUserID: deref(r.EffectiveUserId)}, nil
		}
	}
	if legacyAvailable && (legacyOnly == nil || *legacyOnly) {
		input := &legacyread.Input{}
		input.SetID(id)
		rows, err := readLegacy(ctx, deps.Invoker, input, owner, lock)
		if err != nil {
			return nil, err
		}
		if len(rows) > 1 {
			return nil, fmt.Errorf("legacy run identity returned %d rows", len(rows))
		}
		if len(rows) == 1 && rows[0] != nil && strings.TrimSpace(rows[0].ScheduleId) != "" {
			r := rows[0]
			return &selectedRun{id: r.Id, scheduleID: strings.TrimSpace(r.ScheduleId), conversationID: deref(r.ConversationId), legacy: true}, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrScheduledRunNotFound, id)
}
func deleteRun(ctx context.Context, deps dependencies, id, owner string, now time.Time) error {
	available, err := legacyPresent(ctx, deps.Schema)
	if err != nil {
		return err
	}
	located, err := locateRun(ctx, deps, id, owner, available, false, nil)
	if err != nil {
		return err
	}
	schedule, err := readSchedule(ctx, deps.Invoker, located.scheduleID, owner)
	if errors.Is(err, ErrScheduleNotFound) {
		return fmt.Errorf("%w: schedule=%s", ErrScheduledRunNotFound, located.scheduleID)
	}
	if err != nil {
		return err
	}
	if schedule.Internal {
		return fmt.Errorf("%w: %s", ErrScheduledRunNotFound, id)
	}
	run, err := locateRun(ctx, deps, id, owner, available, true, &located.legacy)
	if err != nil {
		return err
	}
	if run.scheduleID != schedule.Id {
		return fmt.Errorf("%w: %s", ErrScheduledRunNotFound, id)
	}
	stored := deref(schedule.CreatedByUserId)
	if stored == "" {
		stored = run.effectiveUserID
	}
	if owner == "" || stored == "" || owner != stored {
		return tree.ErrPermissionDenied
	}
	query := &convread.ConversationInput{}
	query.SetScheduleRunId(id)
	roots, err := ConversationRoots(ctx, deps.Invoker, owner, []string{run.conversationID}, query)
	if err != nil {
		return err
	}
	currentIDs, legacyIDs := []string{}, []string{}
	if run.legacy {
		legacyIDs = append(legacyIDs, id)
	} else {
		currentIDs = append(currentIDs, id)
	}
	return invokeTree(ctx, deps.Invoker, roots, currentIDs, legacyIDs, now)
}
