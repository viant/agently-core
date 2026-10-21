package conversationtree

import (
	"context"
	_ "embed"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	eventread "github.com/viant/agently-core/internal/datly/agui/cleanup/event/read"
	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	msgread "github.com/viant/agently-core/internal/datly/message/read"
	"github.com/viant/agently-core/internal/store/conversation"
	"github.com/viant/agently-core/internal/store/maintenancebatch"
	lease "github.com/viant/agently-core/internal/store/maintenancelease"
	"github.com/viant/agently-core/internal/store/maintenancetx"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	"github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/datly/spec"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

type captureKey struct{}

// PlanCapture is an in-process, per-attempt capability; never an API input.
// Only large payload plans leave the original managed transaction read-only.
type PlanCapture struct{ plan *DeletePlan }

func WithMaintenancePlanCapture(ctx context.Context) (context.Context, *PlanCapture) {
	capture := &PlanCapture{}
	return context.WithValue(ctx, captureKey{}, capture), capture
}
func (c *PlanCapture) Reset()        { c.plan = nil }
func (c *PlanCapture) HasPlan() bool { return c != nil && c.plan != nil }
func HasCapturedMaintenancePlan(ctx context.Context) bool {
	capture, _ := ctx.Value(captureKey{}).(*PlanCapture)
	return capture.HasPlan()
}
func CaptureMaintenancePlan(ctx context.Context, plan *DeletePlan) bool {
	capture, _ := ctx.Value(captureKey{}).(*PlanCapture)
	if capture == nil || !maintenancetx.Enabled(ctx) || plan == nil || len(plan.AGUIEvents)+len(plan.PayloadIDs) <= maintenancebatch.Size {
		return false
	}
	capture.plan = plan
	return true
}

// MaintenanceProgress contains confirmed explicit DML only. Partial never
// means that the whole tree was deleted; a subsequent pass qualifies anew.
type MaintenanceProgress struct {
	Partial                   bool
	CommittedBatches          int
	AffectedRows, DeletedRows int64
	ConversationCount         int
}

func (p *MaintenanceProgress) Add(summary maintenancetx.Summary) {
	p.AffectedRows += summary.AffectedRows
	p.DeletedRows += summary.DeletedRows
	p.ConversationCount += int(summary.DeletedByTable["conversation"])
	if summary.AffectedRows > 0 {
		p.CommittedBatches++
	}
}

type unitKind uint8

const (
	eventUnit unitKind = iota
	metadataUnit
	payloadUnit
)

type maintenanceUnit struct {
	plan     *DeletePlan
	kind     unitKind
	events   []*eventread.Event
	payloads []string
}
type unitKey struct{}

// The unit is supplied through private context, not JSON. Its public-shaped
// input carries only the matching standalone lease receipt.
type MaintenanceUnitInput struct {
	Lease lease.Lease `parameter:"Lease,kind=body,in=lease"`
}
type MaintenanceUnitOutput struct{}
type MaintenanceUnitComponent struct {
	Contract xdatly.Component[MaintenanceUnitInput, MaintenanceUnitOutput] `component:"ConversationMaintenanceUnit,path=/v1/internal/agently/conversation/maintenance-unit,method=POST,handler=NewMaintenanceUnit,internal=true" componentCallPolicy:"imperative"`
}
type MaintenanceUnit struct{}

func NewMaintenanceUnit() handler.Contract[MaintenanceUnitInput, MaintenanceUnitOutput] {
	return &MaintenanceUnit{}
}
func (MaintenanceUnitComponent) DatlyHandler(name string) func() (rh.TypedHandler, error) {
	if name != "NewMaintenanceUnit" {
		return nil
	}
	return custom.Factory(NewMaintenanceUnit)
}

type unitDependencies struct {
	Invoker  dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
	Starter  handler.TransactionStarter `bind:"kind=transactionStarter,required"`
	Reporter dexec.MutationReporter     `bind:"kind=mutationReporter,required"`
	SQL      rh.TransactionSQLProvider  `bind:"kind=transactionSQL,required"`
}

func (*MaintenanceUnit) Exec(ctx context.Context, session handler.Session, input *MaintenanceUnitInput, output *MaintenanceUnitOutput) error {
	unit, ok := ctx.Value(unitKey{}).(*maintenanceUnit)
	if !ok || unit == nil || unit.plan == nil || !maintenancetx.Enabled(ctx) || input == nil || output == nil {
		return fmt.Errorf("cleanup unit requires a private maintenance plan")
	}
	if _, err := lease.CheckedAt(ctx, input.Lease); err != nil {
		return err
	}
	if session == nil || session.Binder() == nil {
		return fmt.Errorf("cleanup unit session is unavailable")
	}
	deps := unitDependencies{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil {
		return fmt.Errorf("cleanup unit capabilities are unavailable")
	}
	maintenancetx.Observe(ctx, deps.Reporter)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	m := &Mutator{Invoker: deps.Invoker, OwnerID: func(context.Context) string { return "" }}
	switch unit.kind {
	case eventUnit:
		return deleteMaintenanceEvents(ctx, deps.SQL, unit.events)
	case metadataUnit:
		// A concurrent cleanup can finish this entire tree between units. Do not
		// feed absent identities to strict protocol/report writers in that case.
		present := len(unit.plan.ConversationIDs) == 0
		d := NewSystemDiscoverer(deps.Invoker, planSchema(unit.plan.Tables))
		err := eachDeleteBatch(unit.plan.ConversationIDs, func(ids []string) error {
			query := &convread.ConversationInput{}
			query.SetIds(ids)
			rows, err := d.graphRows(ctx, query, []string{"id"})
			present = present || len(rows) > 0
			return err
		})
		if err != nil {
			return err
		}
		if !present {
			return nil
		}
		if err := validateMaintenanceUnitReferences(ctx, deps.Invoker, unit.plan); err != nil {
			return err
		}
		// Keep every discovery edge, cyclic detach and dependent metadata delete
		// together. Payload-only commits never remove these restart anchors.
		core := *unit.plan
		core.AGUIEvents, core.PayloadIDs = nil, nil
		return m.Apply(ctx, &core, InvestigationDelete)
	case payloadUnit:
		return (&conversation.PayloadStore{Invoker: deps.Invoker}).DeleteUnreferencedTrusted(ctx, unit.payloads...)
	default:
		return fmt.Errorf("invalid cleanup unit")
	}
}

type planSchema map[string]bool

func (s planSchema) HasTable(_ context.Context, connector, table string) (bool, error) {
	if connector != "agently" {
		return false, fmt.Errorf("unexpected cleanup connector")
	}
	present, known := s[table]
	if !known {
		return false, fmt.Errorf("cleanup plan has no schema evidence for %s", table)
	}
	return present, nil
}

func validateMaintenanceUnitReferences(ctx context.Context, invoker dexec.ComponentInvoker, plan *DeletePlan) error {
	d := NewSystemDiscoverer(invoker, planSchema(plan.Tables))
	d.QualifyRootRetention()
	if err := d.ValidateInboundLinks(ctx, plan.Graph); err != nil {
		return err
	}
	// Payload commits widen the time since planning. Do not remove the only
	// parent/message edge to a child which was not in that plan. These compact
	// boundary reads do not rediscover or requalify the retained tree.
	linked := []string{}
	if err := eachDeleteBatch(plan.ConversationIDs, func(ids []string) error {
		childrenQuery := &convread.ConversationInput{}
		childrenQuery.SetParentIds(ids)
		children, err := d.graphRows(ctx, childrenQuery, []string{"id"})
		if err != nil {
			return err
		}
		for _, row := range children {
			if row != nil && plan.Graph.Nodes[row.Id] == nil {
				return ErrGraphReferenced
			}
		}
		messageQuery := &msgread.MessagesInput{}
		messageQuery.SetConversationIds(ids)
		messages, err := d.messageRows(ctx, messageQuery, deleteSelectors("id", "linked_conversation_id"))
		if err != nil {
			return err
		}
		for _, row := range messages {
			if row != nil && row.LinkedConversationId != nil && plan.Graph.Nodes[*row.LinkedConversationId] == nil {
				linked = append(linked, *row.LinkedConversationId)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := eachDeleteBatch(linked, func(ids []string) error {
		query := &convread.ConversationInput{}
		query.SetIds(ids)
		rows, err := d.graphRows(ctx, query, []string{"id"})
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			return ErrGraphReferenced
		}
		return nil
	}); err != nil {
		return err
	}
	if plan.Tables["schedule"] {
		ids, err := d.ValidateScheduleReferences(ctx, plan.Graph, time.Now().UTC())
		if err != nil {
			return err
		}
		if !subsetOf(ids, plan.ScheduleIDs) {
			return ErrGraphReferenced
		}
	}
	refs, err := d.ValidateExportReferences(ctx, plan.Graph)
	if err != nil {
		return err
	}
	if !subsetOf(refs.ReportRunIDs, plan.ReportRunIDs) || !subsetOf(refs.JobIDs, plan.ReportJobIDs) || !subsetOf(refs.ArtifactIDs, plan.ReportArtifactIDs) {
		return ErrGraphReferenced
	}
	return nil
}
func subsetOf(ids, planned []string) bool {
	allowed := treeIDSet(planned)
	for _, id := range ids {
		if !allowed[id] {
			return false
		}
	}
	return true
}

var maintenanceUnitTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[MaintenanceUnitComponent]().PkgPath(), Name: "ConversationMaintenanceUnit"},
	Route:     spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/conversation/maintenance-unit"},
}

// Apply owns no transaction itself. Each Invoke owns only the current unit's
// rollback/retry; committed predecessors and unknown commit outcomes never replay.
func (c *PlanCapture) Apply(ctx context.Context, invoker dexec.ComponentInvoker, ownership lease.Lease) (progress MaintenanceProgress, retErr error) {
	if !c.HasPlan() || invoker == nil {
		return progress, fmt.Errorf("cleanup plan is unavailable")
	}
	defer func() { progress.Partial = retErr != nil && progress.AffectedRows > 0 }()
	var err error
	ctx, err = PinGraphReader(ctx)
	if err != nil {
		return progress, err
	}
	units, err := maintenanceUnits(c.plan)
	if err != nil {
		return progress, err
	}
	for _, unit := range units {
		unitCtx := context.WithValue(ctx, unitKey{}, unit)
		value, effects, err := maintenancetx.Invoke(unitCtx, invoker, func(scope context.Context) (context.Context, error) {
			scope, err := (&lease.Store{Invoker: invoker}).Preflight(scope, ownership)
			if err != nil {
				return nil, err
			}
			driver, _ := invoker.(maintenancetx.DriverInspector)
			return maintenancetx.Prepare(scope, driver)
		}, dexec.ComponentRequest{Target: maintenanceUnitTarget, Input: &MaintenanceUnitInput{Lease: ownership}})
		// Count a known commit before checking an unexpected output: it must not
		// disappear from the result or tempt a caller to replay that unit.
		if effects != nil {
			summary, summaryErr := effects.CommitSummary()
			if summaryErr != nil {
				return progress, summaryErr
			}
			progress.Add(summary)
		}
		if err != nil {
			return progress, err
		}
		if out, ok := value.(*MaintenanceUnitOutput); !ok || out == nil {
			return progress, fmt.Errorf("cleanup unit returned %T", value)
		}
	}
	return progress, nil
}

func maintenanceUnits(plan *DeletePlan) ([]*maintenanceUnit, error) {
	if plan == nil || plan.Graph == nil || plan.Tables == nil {
		return nil, fmt.Errorf("cleanup plan is incomplete")
	}
	events := append([]*eventread.Event(nil), plan.AGUIEvents...)
	for _, event := range events {
		if event == nil || aguiCleanupString(event.EventKey) == "" || aguiCleanupString(event.RunKey) == "" || plan.Graph.Nodes[aguiCleanupString(event.ThreadId)] == nil {
			return nil, fmt.Errorf("cleanup event scope mismatch")
		}
	}
	sort.Slice(events, func(i, j int) bool { return *events[i].EventKey < *events[j].EventKey })
	units := []*maintenanceUnit{}
	for start := 0; start < len(events); start += maintenancebatch.Size {
		end := start + maintenancebatch.Size
		if end > len(events) {
			end = len(events)
		}
		units = append(units, &maintenanceUnit{plan: plan, kind: eventUnit, events: events[start:end]})
	}
	units = append(units, &maintenanceUnit{plan: plan, kind: metadataUnit})
	payloads := normalizeIDs(plan.PayloadIDs)
	for start := 0; start < len(payloads); start += maintenancebatch.Size {
		end := start + maintenancebatch.Size
		if end > len(payloads) {
			end = len(payloads)
		}
		units = append(units, &maintenanceUnit{plan: plan, kind: payloadUnit, payloads: payloads[start:end]})
	}
	return units, nil
}

//go:embed sql/maintenance_events.sql
var maintenanceEventsSQL string

func deleteMaintenanceEvents(ctx context.Context, provider rh.TransactionSQLProvider, events []*eventread.Event) error {
	if len(events) > maintenancebatch.Size {
		return fmt.Errorf("cleanup event unit exceeds SQL batch bound")
	}
	ids, runs, threads := []string{}, []string{}, []string{}
	for _, row := range events {
		if row == nil || row.EventKey == nil || row.RunKey == nil || row.ThreadId == nil {
			return fmt.Errorf("cleanup event identity unavailable")
		}
		ids, runs, threads = append(ids, *row.EventKey), append(runs, *row.RunKey), append(threads, *row.ThreadId)
	}
	runs, threads = normalizeIDs(runs), normalizeIDs(threads)
	if len(ids) == 0 {
		return nil
	}
	if provider == nil {
		return fmt.Errorf("cleanup event SQL capability unavailable")
	}
	txSQL, err := provider.Connector(ctx, "agently")
	if err != nil {
		return err
	}
	query := maintenanceEventsSQL
	args := []any{}
	for _, group := range []struct {
		token string
		ids   []string
	}{{"/*IDS*/", ids}, {"/*RUNS*/", runs}, {"/*THREADS*/", threads}} {
		query = strings.Replace(query, group.token, strings.TrimSuffix(strings.Repeat("?,", len(group.ids)), ","), 1)
		for _, id := range group.ids {
			args = append(args, id)
		}
	}
	// This unit has no buffered mutations: a real transaction-owned bulk DELETE
	// needs no writer anchor or flush. Missing/re-scoped events are safe no-ops;
	// manual deletion continues to use the original strict protocol writers.
	result, err := txSQL.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	maintenancetx.RecordImmediate(ctx, "call_payload", affected)
	return nil
}
