package conversationmaintenance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	legacyread "github.com/viant/agently-core/internal/datly/legacyrun/read"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	"github.com/viant/agently-core/internal/store/maintenancediag"
	lease "github.com/viant/agently-core/internal/store/maintenancelease"
	shared "github.com/viant/agently-core/internal/store/scheduledmaintenance"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	custom "github.com/viant/datly/runtime/handler/custom"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

const Interactive = "interactive"
const Scheduled = "scheduled"
const Fallback = "scheduled_fallback"

type Input struct {
	RootID         string      `parameter:"RootID,kind=body,in=rootId"`
	Kind           string      `parameter:"Kind,kind=body,in=kind"`
	InactiveBefore time.Time   `parameter:"InactiveBefore,kind=body,in=inactiveBefore"`
	Mode           string      `parameter:"Mode,kind=body,in=mode"`
	Lease          lease.Lease `parameter:"Lease,kind=body,in=lease"`
}
type Output struct {
	RootID, Kind, Mode, Reason string
	Eligible, Deleted          bool
	ConversationCount          int
	LastActivity               time.Time
}
type Component struct {
	Contract xdatly.Component[Input, Output] `component:"ConversationMaintenance,path=/v1/internal/agently/conversation/maintenance,method=POST,handler=NewMaintain,internal=true"`
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
	Invoker dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
	Starter handler.TransactionStarter `bind:"kind=transactionStarter,required"`
	Schema  tree.TableInspector        `bind:"kind=conversationtreeSchema,in=inspector,required"`
}

func (*Maintain) Exec(ctx context.Context, session handler.Session, input *Input, output *Output) (retErr error) {
	ctx, trace := maintenancediag.Begin(ctx, "conversationmaintenance")
	defer func() { trace.Finish(retErr) }()
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("conversation maintenance invocation is incomplete")
	}
	if input.RootID == "" || input.InactiveBefore.IsZero() || (input.Kind != Interactive && input.Kind != Scheduled && input.Kind != Fallback) || (input.Mode != shared.DryRun && input.Mode != shared.DeleteMode) {
		return fmt.Errorf("invalid conversation maintenance request")
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
		return fmt.Errorf("conversation maintenance capabilities are unavailable")
	}
	deps.Invoker = maintenancediag.Wrap(ctx, deps.Invoker)
	ctx = dexec.WithTransactionIsolation(ctx, dexec.IsolationSerializable)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	deleting := input.Mode == shared.DeleteMode
	if deleting {
		if _, err := (&lease.Store{Invoker: deps.Invoker}).Fence(ctx, input.Lease); err != nil {
			return err
		}
	}
	output.RootID = input.RootID
	output.Kind = input.Kind
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
	query := &convread.ConversationInput{}
	query.SetId(strings.TrimSpace(input.RootID))
	rows, err := shared.ReadConversations(ctx, deps.Invoker, query, []string{"id", "conversation_parent_id", "conversation_parent_turn_id"}, false)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		result.Reason = "not_found"
		return nil
	}
	if len(rows) != 1 || rows[0] == nil {
		return fmt.Errorf("conversation maintenance root identity is ambiguous")
	}
	if value(rows[0].ConversationParentId) != "" || value(rows[0].ConversationParentTurnId) != "" {
		result.Reason = "not_root"
		return nil
	}
	d := tree.NewSystemDiscoverer(deps.Invoker, deps.Schema)
	d.LockDetachRows = deleting
	graph, err := d.Discover(ctx, input.RootID)
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
	if deleting {
		if err = d.LockConversationGraph(ctx, graph); err != nil {
			return err
		}
		graph, err = d.Discover(ctx, input.RootID)
		if err != nil {
			return err
		}
		if err = d.LockConversationGraph(ctx, graph); err != nil {
			return err
		}
	}
	result.ConversationCount = len(graph.Nodes)
	runIDs, err := d.CollectInitialRunIDs(ctx, graph)
	if err != nil {
		return err
	}
	legacyIDs, err := legacyGraphRows(ctx, deps, graph)
	if err != nil {
		return err
	}
	actual, err := graphKind(ctx, deps, graph, runIDs, len(legacyIDs) > 0)
	if err != nil {
		return err
	}
	expected := input.Kind
	if expected == Fallback {
		expected = Scheduled
	}
	if actual != expected {
		result.Reason = "kind_mismatch"
		return nil
	}
	activity, known, err := shared.GraphActivity(ctx, deps.Invoker, graph)
	if err != nil {
		return err
	}
	result.LastActivity = activity
	if !known {
		result.Reason = "activity_unknown"
		return nil
	}
	if activity.After(input.InactiveBefore) {
		result.Reason = "recent_activity"
		return nil
	}
	if input.Kind == Fallback && (len(runIDs) > 0 || len(legacyIDs) > 0) {
		result.Reason = "run_present"
		return nil
	}
	now := time.Now().UTC()
	plan, err := d.CollectDeletePlan(ctx, graph, now, nil, nil)
	if err != nil {
		reason, mapped := shared.PrepareReason(err)
		if mapped {
			result.Reason = reason
			return nil
		}
		return err
	}
	// CollectDeletePlan already refreshed run evidence. No intervening lock
	// or state change justifies repeating the same readers here.
	if input.Kind == Fallback {
		runIDs, err = d.CollectInitialRunIDs(ctx, graph)
		if err != nil {
			return err
		}
		legacyIDs, err = legacyGraphRows(ctx, deps, graph)
		if err != nil {
			return err
		}
		if len(runIDs) > 0 || len(legacyIDs) > 0 {
			result.Reason = "run_present"
			return nil
		}
	}
	if deleting {
		if err = d.LockDeletePlanRuns(ctx, plan); err != nil {
			return err
		}
	}
	if err = (&tree.RunEvidence{Current: plan.Runs, Legacy: plan.LegacyRuns}).Validate(now); err != nil {
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
func graphKind(ctx context.Context, deps dependencies, graph *tree.Graph, runIDs []string, legacyPresent bool) (string, error) {
	if legacyPresent {
		return Scheduled, nil
	}
	ids := []string{}
	for id, node := range graph.Nodes {
		ids = append(ids, id)
		if node != nil && strings.TrimSpace(node.ScheduleRunID) != "" {
			return Scheduled, nil
		}
	}
	query := &convread.ConversationInput{}
	query.SetIds(ids)
	rows, err := shared.ReadConversations(ctx, deps.Invoker, query, []string{"id", "scheduled", "schedule_id", "schedule_run_id", "schedule_kind"}, false)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row != nil {
			scheduled := 0
			if row.Scheduled != nil {
				scheduled = *row.Scheduled
			}
			if scheduled != 0 || value(row.ScheduleId) != "" || value(row.ScheduleRunId) != "" || value(row.ScheduleKind) != "" {
				return Scheduled, nil
			}
		}
	}
	if len(runIDs) > 0 {
		query := &runread.RunRowsInput{}
		query.SetIds(runIDs)
		rows, err := shared.ReadCurrentRuns(ctx, deps.Invoker, query, []string{"id", "conversation_kind"}, false)
		if err != nil {
			return "", err
		}
		for _, row := range rows {
			if row != nil && strings.EqualFold(strings.TrimSpace(row.ConversationKind), Scheduled) {
				return Scheduled, nil
			}
		}
	}
	return Interactive, nil
}
func legacyGraphRows(ctx context.Context, deps dependencies, graph *tree.Graph) ([]string, error) {
	present, err := deps.Schema.HasTable(ctx, "agently", "schedule_run")
	if err != nil || !present {
		return nil, err
	}
	ids := []string{}
	markers := []string{}
	for id, node := range graph.Nodes {
		ids = append(ids, id)
		if node != nil && strings.TrimSpace(node.ScheduleRunID) != "" {
			markers = append(markers, node.ScheduleRunID)
		}
	}
	queries := []*legacyread.Input{}
	if len(ids) > 0 {
		query := &legacyread.Input{}
		query.SetConversationIDs(ids)
		queries = append(queries, query)
	}
	if len(markers) > 0 {
		query := &legacyread.Input{}
		query.SetIDs(markers)
		queries = append(queries, query)
	}
	seen := map[string]bool{}
	result := []string{}
	for _, query := range queries {
		rows, err := shared.ReadLegacyRuns(ctx, deps.Invoker, query, []string{"id"}, false)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row != nil && !seen[row.Id] {
				seen[row.Id] = true
				result = append(result, row.Id)
			}
		}
	}
	return result, nil
}
func value(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

type skipped struct{ result *Output }

func (*skipped) Error() string { return "conversation maintenance skipped" }
