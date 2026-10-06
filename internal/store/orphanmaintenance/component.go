package orphanmaintenance

import (
	"context"
	"fmt"
	"time"

	read "github.com/viant/agently-core/internal/datly/orphanmaintenance/read"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	"github.com/viant/agently-core/internal/store/maintenancediag"
	maintenance "github.com/viant/agently-core/internal/store/maintenancelease"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	custom "github.com/viant/datly/runtime/handler/custom"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

type ApplyComponent struct {
	Contract xdatly.Component[ApplyInput, ApplyOutput] `component:"OrphanMaintenanceApply,path=/v1/internal/agently/orphan-maintenance/apply,method=POST,handler=NewApply,internal=true"`
}
type ApplyInput struct {
	RuleID    string            `parameter:"RuleID,kind=body,in=ruleId"`
	RecordID  string            `parameter:"RecordID,kind=body,in=recordId"`
	OlderThan time.Time         `parameter:"OlderThan,kind=body,in=olderThan"`
	Lease     maintenance.Lease `parameter:"Lease,kind=body,in=lease"`
}
type ApplyOutput struct {
	Result *Result `json:"result"`
}
type Apply struct{}

func NewApply() handler.Contract[ApplyInput, ApplyOutput] { return &Apply{} }

// DatlyHandler binds the holder's declared handler to its typed implementation.
// Linked package discovery calls this provider without a host registration list.
func (ApplyComponent) DatlyHandler(name string) func() (rh.TypedHandler, error) {
	if name != "NewApply" {
		return nil
	}
	return custom.Factory(NewApply)
}
func (*Apply) Exec(ctx context.Context, session handler.Session, input *ApplyInput, output *ApplyOutput) (retErr error) {
	ctx, trace := maintenancediag.Begin(ctx, "orphanmaintenance")
	defer func() { trace.Finish(retErr) }()
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("orphan maintenance invocation is incomplete")
	}
	request, err := normalizeRequest(Request{RuleID: input.RuleID, RecordID: input.RecordID, OlderThan: input.OlderThan, Lease: input.Lease})
	if err != nil {
		return err
	}
	deps := struct {
		Invoker  dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
		Starter  handler.TransactionStarter `bind:"kind=transactionStarter,required"`
		Reporter dexec.MutationReporter     `bind:"kind=mutationReporter,required"`
		Driver   tree.DriverInspector       `bind:"kind=orphanmaintenance,in=driver,required"`
	}{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil || deps.Reporter == nil || deps.Driver == nil {
		return fmt.Errorf("orphan maintenance capabilities are unavailable")
	}
	mysql, err := mysqlContract(ctx, deps.Driver)
	if err != nil {
		return err
	}
	rule, exists := FindRule(mysql, request.RuleID)
	if !exists {
		return fmt.Errorf("%w: unknown orphan rule %q", ErrInvalidRequest, request.RuleID)
	}
	keys, err := KeyValues(rule, request.RecordID)
	if err != nil {
		return err
	}
	deps.Invoker = maintenancediag.Wrap(ctx, deps.Invoker)
	ctx = dexec.WithTransactionIsolation(ctx, dexec.IsolationSerializable)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	if _, err := (&maintenance.Store{Invoker: deps.Invoker}).Fence(ctx, request.Lease); err != nil {
		return err
	}
	result := &Result{RuleID: rule.ID, RecordID: request.RecordID, Action: rule.Action, Reason: NoLongerEligible}
	output.Result = result
	snapshot, found, err := lockRecord(ctx, deps.Invoker, rule, keys)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	query := &read.ReportInput{}
	query.SetOlderThan(request.OlderThan)
	query.SetRuleID(rule.ID)
	query.SetRecordID(snapshot.RecordID)
	query.SetLimit(1)
	query.SetHasCursor(false)
	query.SetAfterPriority(0)
	query.SetAfterRule("")
	query.SetAfterRecord("")
	rows, err := readReport(ctx, deps.Invoker, mysql, query)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	if len(rows) != 1 || rows[0] == nil || rows[0].RuleId != rule.ID || rows[0].RecordId != snapshot.RecordID {
		return fmt.Errorf("orphan recheck returned inconsistent identity")
	}
	result.Eligible = true
	if rule.Action == ReportOnly {
		result.Reason = ReportedOnly
		return nil
	}
	before := len(deps.Reporter.MutationReport().Results)
	if err := mutate(ctx, deps.Invoker, rule, snapshot); err != nil {
		return err
	}
	report := deps.Reporter.MutationReport()
	if len(report.Results) != before+1 {
		return fmt.Errorf("orphan mutation produced %d execution results", len(report.Results)-before)
	}
	evidence := report.Results[before]
	if evidence.Error != nil {
		return evidence.Error
	}
	operation := "delete"
	if rule.Action == SafeDetach {
		operation = "update"
	}
	if evidence.Table != rule.Table || evidence.Operation != operation || evidence.Records != 1 || evidence.Affected != 1 {
		return fmt.Errorf("orphan maintenance rule=%q record=%q affected %d rows, expected 1", rule.ID, request.RecordID, evidence.Affected)
	}
	result.Mutated = true
	if rule.Action == SafeDelete {
		result.Deleted = true
		result.Reason = Deleted
	} else {
		result.Detached = true
		result.Reason = Detached
	}
	return nil
}
