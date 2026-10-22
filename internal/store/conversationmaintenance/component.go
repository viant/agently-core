package conversationmaintenance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	"github.com/viant/agently-core/internal/datly/dbtime"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	"github.com/viant/agently-core/internal/store/maintenancediag"
	lease "github.com/viant/agently-core/internal/store/maintenancelease"
	"github.com/viant/agently-core/internal/store/maintenancetx"
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
	Partial                    bool
	CommittedBatches           int
	AffectedRows               int64
}

// The Store checks the lease before invoking this graph-mutation transaction.
// Imperative composition preserves the same parent-owned transaction.
type Component struct {
	Contract xdatly.Component[Input, Output] `component:"ConversationMaintenance,path=/v1/internal/agently/conversation/maintenance,method=POST,handler=NewMaintain,internal=true" componentCallPolicy:"imperative"`
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
	SQL      rh.TransactionSQLProvider  `bind:"kind=transactionSQL,required"`
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
	deleting := input.Mode == shared.DeleteMode
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
		return fmt.Errorf("conversation maintenance capabilities are unavailable")
	}
	deps.Invoker = maintenancediag.Wrap(ctx, deps.Invoker)
	maintenancetx.Observe(ctx, deps.Reporter)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	output.RootID = input.RootID
	output.Kind = input.Kind
	output.Mode = input.Mode
	if err := evaluate(ctx, deps, input, output, deleting); err != nil {
		return err
	}
	if deleting && !output.Deleted && !tree.HasCapturedMaintenancePlan(ctx) {
		snapshot := *output
		return &skipped{result: &snapshot}
	}
	return nil
}
func evaluate(ctx context.Context, deps dependencies, input *Input, result *Output, deleting bool) error {
	d := tree.NewSystemDiscoverer(deps.Invoker, deps.Schema)
	query := &convread.ConversationInput{}
	query.SetId(strings.TrimSpace(input.RootID))
	rows, err := shared.ReadConversations(ctx, d.Invoker, query, nil, false)
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
	activity, known := dbtime.ParseActivity(value(rows[0].ActivityRaw))
	result.LastActivity = activity
	if !known {
		result.Reason = "activity_unknown"
		return nil
	}
	if activity.After(input.InactiveBefore) {
		result.Reason = "recent_activity"
		return nil
	}
	d.QualifyRootRetention()
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
	result.ConversationCount = len(graph.Nodes)
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
	classification := deps
	classification.Invoker = d.Invoker
	actual, err := graphKind(ctx, classification, graph, plan.Runs, len(plan.LegacyRuns) > 0)
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
	if input.Kind == Fallback {
		if len(plan.Runs) > 0 || len(plan.LegacyRuns) > 0 {
			result.Reason = "run_present"
			return nil
		}
	}
	result.Eligible = true
	if !deleting {
		result.Reason = "eligible"
		return nil
	}
	if tree.CaptureMaintenancePlan(ctx, plan) {
		result.Reason = "planned"
		return nil
	}
	if err = (&tree.Mutator{Invoker: deps.Invoker, OwnerID: d.OwnerID, SQL: deps.SQL}).Apply(ctx, plan, tree.InvestigationDelete); err != nil {
		return err
	}
	result.Deleted = true
	result.Reason = "deleted"
	return nil
}
func graphKind(ctx context.Context, deps dependencies, graph *tree.Graph, runs []*runread.RunRowsView, legacyPresent bool) (string, error) {
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
	if len(runs) > 0 {
		for _, row := range runs {
			if row != nil && strings.EqualFold(strings.TrimSpace(row.ConversationKind), Scheduled) {
				return Scheduled, nil
			}
		}
	}
	return Interactive, nil
}
func value(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

type skipped struct{ result *Output }

func (*skipped) Error() string { return "conversation maintenance skipped" }
