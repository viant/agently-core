package scheduledmaintenance

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	legacyread "github.com/viant/agently-core/internal/datly/legacyrun/read"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	schedread "github.com/viant/agently-core/internal/datly/schedule/read"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	lease "github.com/viant/agently-core/internal/store/maintenancelease"
	dexec "github.com/viant/datly/exec"
	custom "github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/x"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

const DryRun = "dry_run"
const DeleteMode = "delete"

type Input struct {
	RunID          string      `parameter:"RunID,kind=body,in=runId"`
	InactiveBefore time.Time   `parameter:"InactiveBefore,kind=body,in=inactiveBefore"`
	Mode           string      `parameter:"Mode,kind=body,in=mode"`
	Lease          lease.Lease `parameter:"Lease,kind=body,in=lease"`
}
type Output struct {
	RunID, ScheduleID, Source, Mode, Reason string
	Eligible, Deleted                       bool
	ConversationCount                       int
	LastActivity                            time.Time
}
type Component struct {
	Contract xdatly.Component[Input, Output] `component:"ScheduledRunMaintenance,path=/v1/internal/agently/scheduled-run/maintenance,method=POST,handler=NewMaintain,internal=true"`
}
type Maintain struct{}

func NewMaintain() handler.Contract[Input, Output] { return &Maintain{} }
func Exports() (*x.Registry, error) {
	registry := x.NewRegistry()
	for _, typ := range []reflect.Type{reflect.TypeFor[Input](), reflect.TypeFor[Output](), reflect.TypeFor[lease.Lease]()} {
		registry.Register(x.NewType(typ))
	}
	factory, err := x.NewFunction(reflect.TypeFor[Component]().PkgPath(), "NewMaintain", custom.Factory(NewMaintain))
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
	Schema  tree.TableInspector        `bind:"kind=conversationtreeSchema,in=inspector,required"`
}

type selectedRun struct {
	ID, ScheduleID, ConversationID, Source string
	ActivityAt                             time.Time
}

func (*Maintain) Exec(ctx context.Context, session handler.Session, input *Input, output *Output) error {
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("scheduled maintenance invocation is incomplete")
	}
	if input.Mode == "" {
		input.Mode = DryRun
	}
	input.RunID = strings.TrimSpace(input.RunID)
	input.InactiveBefore = input.InactiveBefore.UTC()
	if input.RunID == "" || input.InactiveBefore.IsZero() || (input.Mode != DryRun && input.Mode != DeleteMode) {
		return fmt.Errorf("invalid scheduled maintenance request")
	}
	deps := dependencies{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil || deps.Schema == nil {
		return fmt.Errorf("scheduled maintenance capabilities are unavailable")
	}
	ctx = dexec.WithTransactionIsolation(ctx, dexec.IsolationSerializable)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	deleting := input.Mode == DeleteMode
	if deleting {
		if _, err := (&lease.Store{Invoker: deps.Invoker}).Fence(ctx, input.Lease); err != nil {
			return err
		}
	}
	output.RunID = input.RunID
	output.Mode = input.Mode
	if err := evaluate(ctx, deps, input, output, deleting); err != nil {
		return err
	}
	if deleting && !output.Deleted {
		snapshot := *output
		return &skipped{result: &snapshot}
	}
	return nil
}
func evaluate(ctx context.Context, deps dependencies, input *Input, result *Output, deleting bool) error {
	selected, err := selectRun(ctx, deps, input.RunID, deleting)
	if err != nil {
		return err
	}
	if selected == nil {
		result.Reason = "not_found"
		return nil
	}
	result.Source = selected.Source
	result.ScheduleID = selected.ScheduleID
	result.LastActivity = selected.ActivityAt
	schedule, err := selectSchedule(ctx, deps, selected.ScheduleID, deleting)
	if err != nil {
		return err
	}
	if schedule == nil {
		result.Reason = "schedule_missing"
		return nil
	}
	if schedule.Internal {
		result.Reason = "internal_schedule"
		return nil
	}
	query := &convread.ConversationInput{}
	query.SetScheduleRunId(selected.ID)
	roots, err := conversationRoots(ctx, deps.Invoker, []string{selected.ConversationID}, query)
	if err != nil {
		return err
	}
	d := tree.NewSystemDiscoverer(deps.Invoker, deps.Schema)
	d.LockDetachRows = deleting
	graph, err := d.Discover(ctx, roots...)
	if err != nil {
		if errors.Is(err, tree.ErrNotFound) {
			result.Reason = "not_found"
			return nil
		}
		if errors.Is(err, tree.ErrTooLarge) {
			result.Reason = "graph_too_large"
			return nil
		}
		return err
	}
	result.ConversationCount = len(graph.Nodes)
	if deleting {
		if err = d.LockConversationGraph(ctx, graph); err != nil {
			return err
		}
		graph, err = d.Discover(ctx, roots...)
		if err != nil {
			return err
		}
		if err = d.LockConversationGraph(ctx, graph); err != nil {
			return err
		}
		result.ConversationCount = len(graph.Nodes)
	}
	// Structural containment precedes preparation's additional internal-wakeup
	// dependencies, matching the legacy order without historical owner checks.
	evidence, err := d.CollectRunEvidence(ctx, graph)
	if err != nil {
		return err
	}
	if err = validateScope(ctx, deps, graph, evidence, selected); err != nil {
		if errors.Is(err, tree.ErrGraphReferenced) {
			result.Reason = "graph_referenced"
			return nil
		}
		return err
	}
	if len(graph.Nodes) > 0 {
		activity, known, err := graphActivity(ctx, deps, graph)
		if err != nil {
			return err
		}
		if !known {
			result.Reason = "activity_unknown"
			return nil
		}
		result.LastActivity = later(result.LastActivity, activity)
	}
	current, legacy := []string{}, []string{}
	if selected.Source == "schedule_run" {
		legacy = []string{selected.ID}
	} else {
		current = []string{selected.ID}
	}
	now := time.Now().UTC()
	plan, err := d.CollectDeletePlan(ctx, graph, now, current, legacy)
	if err != nil {
		reason, mapped := prepareReason(err)
		if mapped {
			result.Reason = reason
			return nil
		}
		return err
	}
	if deleting {
		if err = d.LockDeletePlanRuns(ctx, plan); err != nil {
			return err
		}
	}
	if err = d.RefreshDeletePlanRunEvidence(ctx, plan); err != nil {
		return err
	}
	if deleting {
		if err = d.LockDeletePlanRuns(ctx, plan); err != nil {
			return err
		}
	}
	related, known, err := relatedActivity(ctx, deps, plan)
	if err != nil {
		return err
	}
	if known {
		result.LastActivity = later(result.LastActivity, related)
	}
	if result.LastActivity.After(input.InactiveBefore) {
		result.Reason = "recent_activity"
		return nil
	}
	if err = (&tree.RunEvidence{Current: plan.Runs, Legacy: plan.LegacyRuns}).Validate(time.Now().UTC()); err != nil {
		if errors.Is(err, tree.ErrConversationActive) {
			result.Reason = "live_run"
			return nil
		}
		return err
	}
	for _, job := range plan.ReportJobs {
		if job != nil {
			switch strings.ToLower(strings.TrimSpace(job.Status)) {
			case "queued", "running":
				result.Reason = "active_report_export"
				return nil
			}
		}
	}
	result.Eligible = true
	if !deleting {
		result.Reason = "eligible"
		return nil
	}
	if err = (&tree.Mutator{Invoker: deps.Invoker, OwnerID: d.OwnerID}).Apply(ctx, plan, tree.InvestigationDelete); err != nil {
		return err
	}
	result.Deleted = true
	result.Reason = "deleted"
	return nil
}

func selectRun(ctx context.Context, deps dependencies, id string, lock bool) (*selectedRun, error) {
	fields := []string{"id", "schedule_id", "conversation_id", "activity_raw"}
	query := &runread.RunRowsInput{}
	query.SetId(id)
	current, err := readRows[runread.RunRowsOutput](ctx, deps.Invoker, runTarget, query, providers("runaccess", "rows", false, fields), lock)
	if err != nil {
		return nil, err
	}
	if len(current.Data) > 1 {
		return nil, fmt.Errorf("scheduled maintenance current identity returned multiple rows")
	}
	if len(current.Data) == 1 && current.Data[0] != nil {
		row := current.Data[0]
		activity, ok := parseActivity(deref(row.ActivityRaw))
		if !ok {
			return nil, fmt.Errorf("scheduled run %q has invalid activity time %q", id, deref(row.ActivityRaw))
		}
		return &selectedRun{ID: strings.TrimSpace(row.Id), ScheduleID: deref(row.ScheduleId), ConversationID: deref(row.ConversationId), Source: "run", ActivityAt: activity}, nil
	}
	present, err := deps.Schema.HasTable(ctx, "agently", "schedule_run")
	if err != nil || !present {
		return nil, err
	}
	oldQuery := &legacyread.Input{}
	oldQuery.SetID(id)
	legacy, err := readRows[legacyread.Output](ctx, deps.Invoker, legacyTarget, oldQuery, providers("schedulerunaccess", "rows", false, fields), lock)
	if err != nil {
		return nil, err
	}
	if len(legacy.Data) > 1 {
		return nil, fmt.Errorf("scheduled maintenance legacy identity returned multiple rows")
	}
	if len(legacy.Data) == 0 || legacy.Data[0] == nil {
		return nil, nil
	}
	row := legacy.Data[0]
	activity, ok := parseActivity(deref(row.ActivityRaw))
	if !ok {
		return nil, fmt.Errorf("scheduled run %q has invalid activity time %q", id, deref(row.ActivityRaw))
	}
	return &selectedRun{ID: strings.TrimSpace(row.Id), ScheduleID: strings.TrimSpace(row.ScheduleId), ConversationID: deref(row.ConversationId), Source: "schedule_run", ActivityAt: activity}, nil
}
func selectSchedule(ctx context.Context, deps dependencies, id string, lock bool) (*schedread.ScheduleView, error) {
	if strings.TrimSpace(id) == "" {
		return nil, nil
	}
	query := &schedread.ScheduleInput{}
	query.SetId(id)
	out, err := readRows[schedread.ScheduleOutput](ctx, deps.Invoker, scheduleTarget, query, providers("scheduleaccess", "rows", false, []string{"id", "internal"}), lock)
	if err != nil {
		return nil, err
	}
	if len(out.Data) > 1 {
		return nil, fmt.Errorf("scheduled maintenance schedule identity returned multiple rows")
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	return out.Data[0], nil
}
func graphActivity(ctx context.Context, deps dependencies, graph *tree.Graph) (time.Time, bool, error) {
	ids := make([]string, 0, len(graph.Nodes))
	for id := range graph.Nodes {
		ids = append(ids, id)
	}
	query := &convread.ConversationInput{}
	query.SetIds(ids)
	rows, err := readRows[convread.ConversationOutput](ctx, deps.Invoker, conversationTarget, query, providers("conversationaccess", "rows", false, []string{"id", "activity_raw"}), false)
	if err != nil {
		return time.Time{}, false, err
	}
	seen := 0
	var latest time.Time
	for _, row := range rows.Data {
		if row == nil {
			continue
		}
		value, known := parseActivity(deref(row.ActivityRaw))
		if !known {
			return time.Time{}, false, nil
		}
		seen++
		latest = later(latest, value)
	}
	return latest, seen == len(ids) && seen > 0, nil
}
func relatedActivity(ctx context.Context, deps dependencies, plan *tree.DeletePlan) (time.Time, bool, error) {
	var latest time.Time
	known := false
	fields := []string{"id", "activity_raw"}
	if len(plan.RunIDs) > 0 {
		query := &runread.RunRowsInput{}
		query.SetIds(plan.RunIDs)
		out, err := readRows[runread.RunRowsOutput](ctx, deps.Invoker, runTarget, query, providers("runaccess", "rows", false, fields), false)
		if err != nil {
			return time.Time{}, false, err
		}
		for _, row := range out.Data {
			if row != nil {
				value, ok := parseActivity(deref(row.ActivityRaw))
				if !ok {
					return time.Time{}, false, nil
				}
				known = true
				latest = later(latest, value)
			}
		}
	}
	if plan.Tables["schedule_run"] && len(plan.ScheduleRunIDs) > 0 {
		query := &legacyread.Input{}
		query.SetIDs(plan.ScheduleRunIDs)
		out, err := readRows[legacyread.Output](ctx, deps.Invoker, legacyTarget, query, providers("schedulerunaccess", "rows", false, fields), false)
		if err != nil {
			return time.Time{}, false, err
		}
		for _, row := range out.Data {
			if row != nil {
				value, ok := parseActivity(deref(row.ActivityRaw))
				if !ok {
					return time.Time{}, false, nil
				}
				known = true
				latest = later(latest, value)
			}
		}
	}
	return latest, known, nil
}
func validateScope(ctx context.Context, deps dependencies, graph *tree.Graph, evidence *tree.RunEvidence, selected *selectedRun) error {
	for _, node := range graph.Nodes {
		if node != nil && strings.TrimSpace(node.ScheduleRunID) != "" && strings.TrimSpace(node.ScheduleRunID) != selected.ID {
			return tree.ErrGraphReferenced
		}
	}
	if len(graph.Nodes) > 0 {
		query := &convread.ConversationInput{}
		ids := make([]string, 0, len(graph.Nodes))
		for id := range graph.Nodes {
			ids = append(ids, id)
		}
		query.SetIds(ids)
		out, err := readRows[convread.ConversationOutput](ctx, deps.Invoker, conversationTarget, query, providers("conversationaccess", "rows", false, []string{"id", "schedule_id"}), false)
		if err != nil {
			return err
		}
		for _, row := range out.Data {
			if row != nil && deref(row.ScheduleId) != "" && deref(row.ScheduleId) != selected.ScheduleID {
				return tree.ErrGraphReferenced
			}
		}
	}
	for _, row := range evidence.Current {
		if row != nil && deref(row.ScheduleId) != "" && (strings.TrimSpace(row.Id) != selected.ID || deref(row.ScheduleId) != selected.ScheduleID) {
			return tree.ErrGraphReferenced
		}
	}
	for _, row := range evidence.Legacy {
		if row != nil && (strings.TrimSpace(row.Id) != selected.ID || strings.TrimSpace(row.ScheduleId) != selected.ScheduleID) {
			return tree.ErrGraphReferenced
		}
	}
	return nil
}
func prepareReason(err error) (string, bool) {
	switch {
	case errors.Is(err, tree.ErrPermissionDenied):
		return "related_owner_mismatch", true
	case errors.Is(err, tree.ErrGraphReferenced):
		return "graph_referenced", true
	case errors.Is(err, tree.ErrScheduleReferenced):
		return "schedule_referenced", true
	case errors.Is(err, tree.ErrConversationActive):
		return "live_schedule", true
	}
	return "", false
}

// A skipped delete is a private failure outcome so the managed runtime rolls
// back the fence alongside the graph. The facade returns its stable decision.
type skipped struct{ result *Output }

func (*skipped) Error() string { return "scheduled maintenance skipped" }

func conversationRoots(ctx context.Context, invoker dexec.ComponentInvoker, ids []string, query *convread.ConversationInput) ([]string, error) {
	fields := []string{"id", "conversation_parent_id", "created_at_raw"}
	byID := map[string]*convread.ConversationView{}
	queries := []*convread.ConversationInput{query}
	selected := []string{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			selected = append(selected, id)
		}
	}
	if len(selected) > 0 {
		byIds := &convread.ConversationInput{}
		byIds.SetIds(selected)
		queries = append(queries, byIds)
	}
	for _, input := range queries {
		if input == nil {
			continue
		}
		rows, err := ReadConversations(ctx, invoker, input, fields, false)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row != nil {
				byID[row.Id] = row
			}
		}
	}
	roots := []*convread.ConversationView{}
	for _, row := range byID {
		if byID[deref(row.ConversationParentId)] == nil {
			roots = append(roots, row)
		}
	}
	sort.SliceStable(roots, func(i, j int) bool {
		left, _ := parseActivity(deref(roots[i].CreatedAtRaw))
		right, _ := parseActivity(deref(roots[j].CreatedAtRaw))
		if left.Equal(right) {
			return roots[i].Id < roots[j].Id
		}
		return left.Before(right)
	})
	result := make([]string, 0, len(roots))
	for _, row := range roots {
		result = append(result, row.Id)
	}
	return result, nil
}

// ReadConversations forwards a bounded request/projection to the one canonical
// conversation reader; maintenance policies retain control over classification.
func ReadConversations(ctx context.Context, invoker dexec.ComponentInvoker, input *convread.ConversationInput, fields []string, lock bool) ([]*convread.ConversationView, error) {
	out, err := readRows[convread.ConversationOutput](ctx, invoker, conversationTarget, input, providers("conversationaccess", "rows", false, fields), lock)
	if err != nil {
		return nil, err
	}
	return out.Data, nil
}
func ReadCurrentRuns(ctx context.Context, invoker dexec.ComponentInvoker, input *runread.RunRowsInput, fields []string, lock bool) ([]*runread.RunRowsView, error) {
	out, err := readRows[runread.RunRowsOutput](ctx, invoker, runTarget, input, providers("runaccess", "rows", false, fields), lock)
	if err != nil {
		return nil, err
	}
	return out.Data, nil
}
func ReadLegacyRuns(ctx context.Context, invoker dexec.ComponentInvoker, input *legacyread.Input, fields []string, lock bool) ([]*legacyread.LegacyRun, error) {
	out, err := readRows[legacyread.Output](ctx, invoker, legacyTarget, input, providers("schedulerunaccess", "rows", false, fields), lock)
	if err != nil {
		return nil, err
	}
	return out.Data, nil
}
func GraphActivity(ctx context.Context, invoker dexec.ComponentInvoker, graph *tree.Graph) (time.Time, bool, error) {
	return graphActivity(ctx, dependencies{Invoker: invoker}, graph)
}
func PrepareReason(err error) (string, bool) { return prepareReason(err) }
