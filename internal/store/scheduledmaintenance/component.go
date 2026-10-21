package scheduledmaintenance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	legacyread "github.com/viant/agently-core/internal/datly/legacyrun/read"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	schedread "github.com/viant/agently-core/internal/datly/schedule/read"
	turnread "github.com/viant/agently-core/internal/datly/turn/read"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	"github.com/viant/agently-core/internal/store/maintenancediag"
	lease "github.com/viant/agently-core/internal/store/maintenancelease"
	"github.com/viant/agently-core/internal/store/maintenancetx"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	custom "github.com/viant/datly/runtime/handler/custom"
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

// The Store checks the lease before invoking this graph-mutation transaction.
// Imperative composition preserves the same parent-owned transaction.
type Component struct {
	Contract xdatly.Component[Input, Output] `component:"ScheduledRunMaintenance,path=/v1/internal/agently/scheduled-run/maintenance,method=POST,handler=NewMaintain,internal=true" componentCallPolicy:"imperative"`
}
type Maintain struct{}

func NewMaintain() handler.Contract[Input, Output] { return &Maintain{} }

// DatlyHandler binds the holder's declared handler to its typed implementation.
// Linked package discovery calls this provider without a host registration list.
func (Component) DatlyHandler(name string) func() (rh.TypedHandler, error) {
	if name != "NewMaintain" {
		return nil
	}
	return custom.Factory(NewMaintain)
}

type dependencies struct {
	Invoker  dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
	Starter  handler.TransactionStarter `bind:"kind=transactionStarter,required"`
	Schema   tree.TableInspector        `bind:"kind=conversationtreeSchema,in=inspector,required"`
	Reporter dexec.MutationReporter     `bind:"kind=mutationReporter,required"`
}

type selectedRun struct {
	ID, ScheduleID, ConversationID, Source string
	ActivityAt                             time.Time
}

func (*Maintain) Exec(ctx context.Context, session handler.Session, input *Input, output *Output) (retErr error) {
	ctx, trace := maintenancediag.Begin(ctx, "scheduledmaintenance")
	defer func() { trace.Finish(retErr) }()
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
	deleting := input.Mode == DeleteMode
	if deleting {
		if _, err := lease.CheckedAt(ctx, input.Lease); err != nil {
			return err
		}
	}
	var err error
	ctx, err = tree.PinGraphReader(ctx)
	if err != nil {
		return err
	}
	deps := dependencies{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil || deps.Schema == nil {
		return fmt.Errorf("scheduled maintenance capabilities are unavailable")
	}
	deps.Invoker = maintenancediag.Wrap(ctx, deps.Invoker)
	maintenancetx.Observe(ctx, deps.Reporter)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
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
	selected, err := selectRun(ctx, deps, input.RunID, false)
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
	schedule, err := selectSchedule(ctx, deps, selected.ScheduleID, false)
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
	d := tree.NewSystemDiscoverer(deps.Invoker, deps.Schema)
	rootRows, err := conversationRoots(ctx, d.Invoker, []string{selected.ConversationID}, query)
	if err != nil {
		if errors.Is(err, tree.ErrGraphReferenced) {
			result.Reason = "graph_referenced"
			return nil
		}
		return err
	}
	roots := make([]string, 0, len(rootRows))
	var rootActivity time.Time
	for _, row := range rootRows {
		if deref(row.ConversationParentId) != "" || deref(row.ConversationParentTurnId) != "" {
			result.Reason = "not_root"
			return nil
		}
		activity, known := parseActivity(deref(row.ActivityRaw))
		if !known {
			result.Reason = "activity_unknown"
			return nil
		}
		rootActivity = later(rootActivity, activity)
		roots = append(roots, row.Id)
	}
	if len(roots) > 0 {
		result.LastActivity = rootActivity
		d.QualifyRootRetention()
	}
	if result.LastActivity.After(input.InactiveBefore) {
		result.Reason = "recent_activity"
		return nil
	}
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
	evidence := &tree.RunEvidence{Current: plan.Runs, Legacy: plan.LegacyRuns}
	classification := deps
	classification.Invoker = d.Invoker
	if err = validateScope(ctx, classification, graph, evidence, selected, plan.ScheduleIDs); err != nil {
		if errors.Is(err, tree.ErrGraphReferenced) {
			result.Reason = "graph_referenced"
			return nil
		}
		return err
	}
	// A run with no surviving conversation has no qualified root. Preserve
	// its own retention and lease/heartbeat protection.
	if len(roots) == 0 {
		if err = evidence.Validate(now); err != nil {
			if errors.Is(err, tree.ErrConversationActive) {
				result.Reason = "live_run"
				return nil
			}
			return err
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
	current, err := ReadCurrentRuns(ctx, deps.Invoker, query, fields, lock)
	if err != nil {
		return nil, err
	}
	if len(current) > 1 {
		return nil, fmt.Errorf("scheduled maintenance current identity returned multiple rows")
	}
	if len(current) == 1 && current[0] != nil {
		row := current[0]
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
func validateScope(ctx context.Context, deps dependencies, graph *tree.Graph, evidence *tree.RunEvidence, selected *selectedRun, internalSchedules []string) error {
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
		out, err := ReadConversations(ctx, deps.Invoker, query, nil, false)
		if err != nil {
			return err
		}
		for _, row := range out {
			if row != nil && deref(row.ScheduleId) != "" && deref(row.ScheduleId) != selected.ScheduleID {
				return tree.ErrGraphReferenced
			}
		}
	}
	internal := map[string]bool{}
	for _, id := range internalSchedules {
		internal[id] = true
	}
	for _, row := range evidence.Current {
		if row != nil && !internal[deref(row.ScheduleId)] && deref(row.ScheduleId) != "" && (strings.TrimSpace(row.Id) != selected.ID || deref(row.ScheduleId) != selected.ScheduleID) {
			return tree.ErrGraphReferenced
		}
	}
	for _, row := range evidence.Legacy {
		if row != nil && !internal[strings.TrimSpace(row.ScheduleId)] && (strings.TrimSpace(row.Id) != selected.ID || strings.TrimSpace(row.ScheduleId) != selected.ScheduleID) {
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
// back graph mutations. The facade returns its stable decision.
type skipped struct{ result *Output }

func (*skipped) Error() string { return "scheduled maintenance skipped" }

func conversationRoots(ctx context.Context, invoker dexec.ComponentInvoker, ids []string, query *convread.ConversationInput) ([]*convread.ConversationView, error) {
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
	conversationIDs := make([]string, 0, len(byID))
	for id := range byID {
		conversationIDs = append(conversationIDs, id)
	}
	insideTurns := map[string]bool{}
	if len(conversationIDs) > 0 {
		turnQuery := &turnread.TurnRowsInput{}
		turnQuery.SetConversationIDs(conversationIDs)
		turns, err := tree.ReadCleanupTurns(ctx, invoker, turnQuery, false)
		if err != nil {
			return nil, err
		}
		for _, row := range turns {
			if row != nil {
				insideTurns[row.Id] = true
			}
		}
	}
	for _, row := range byID {
		if byID[deref(row.ConversationParentId)] == nil && !insideTurns[deref(row.ConversationParentTurnId)] {
			roots = append(roots, row)
		}
	}
	// A closed parent cycle is not a run-only execution. Without a root we
	// cannot qualify its age, and must not delete the run out from under it.
	if len(byID) > 0 && len(roots) == 0 {
		return nil, tree.ErrGraphReferenced
	}
	sort.SliceStable(roots, func(i, j int) bool {
		left, _ := parseActivity(deref(roots[i].CreatedAtRaw))
		right, _ := parseActivity(deref(roots[j].CreatedAtRaw))
		if left.Equal(right) {
			return roots[i].Id < roots[j].Id
		}
		return left.Before(right)
	})
	return roots, nil
}

// Classification reads use host-only compact metadata; candidate pagination
// and public conversation/run readers keep their existing contracts.
func ReadConversations(ctx context.Context, invoker dexec.ComponentInvoker, input *convread.ConversationInput, _ []string, lock bool) ([]*convread.ConversationView, error) {
	return tree.ReadCleanupConversations(ctx, invoker, input, lock)
}
func ReadCurrentRuns(ctx context.Context, invoker dexec.ComponentInvoker, input *runread.RunRowsInput, _ []string, lock bool) ([]*runread.RunRowsView, error) {
	return tree.ReadCleanupRuns(ctx, invoker, input, lock)
}
func ReadLegacyRuns(ctx context.Context, invoker dexec.ComponentInvoker, input *legacyread.Input, fields []string, lock bool) ([]*legacyread.LegacyRun, error) {
	out, err := readRows[legacyread.Output](ctx, invoker, legacyTarget, input, providers("schedulerunaccess", "rows", false, fields), lock)
	if err != nil {
		return nil, err
	}
	return out.Data, nil
}
func PrepareReason(err error) (string, bool) { return prepareReason(err) }
