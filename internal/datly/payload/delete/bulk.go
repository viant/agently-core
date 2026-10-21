package delete

import (
	"context"
	"embed"
	"fmt"
	"reflect"
	"strings"

	"github.com/viant/agently-core/internal/store/maintenancediag"
	"github.com/viant/agently-core/internal/store/maintenancetx"
	rh "github.com/viant/datly/runtime/handler"
	"github.com/viant/sqlx"
	xdatly "github.com/viant/xdatly"
	xhandler "github.com/viant/xdatly/handler"
)

// BulkDeleteComponent is authored, not generated. It shares the row writer's
// trusted contract and key-only pre-read; one guarded row anchors the flush.
type BulkDeleteComponent struct {
	Contract xdatly.Component[Input, Output] `component:"PayloadBulkDelete,path=/v1/internal/agently/payload/delete-bulk,method=PATCH,connector=agently,view=writer,handler=NewBulkDelete,internal=true" protectedFlushTables:"[\"call_payload\"]"`
}

var BulkDeleteDatly = new(BulkDeleteComponent)

func (BulkDeleteComponent) EmbedFS() *embed.FS     { return &WriterDatlyResources }
func (BulkDeleteComponent) EmbedNamespace() string { return WriterDatlyResourceNamespace }
func (BulkDeleteComponent) DatlyHandler(name string) func() (rh.TypedHandler, error) {
	if name != "NewBulkDelete" {
		return nil
	}
	return func() (rh.TypedHandler, error) { return &BulkDelete{}, nil }
}

type BulkDelete struct{}

func (*BulkDelete) InputType() reflect.Type             { return reflect.TypeFor[Input]() }
func (*BulkDelete) OutputType() reflect.Type            { return reflect.TypeFor[Output]() }
func (*BulkDelete) RequiresPreBindingTransaction() bool { return true }

//go:embed sql/bulk_delete.sql
var bulkDeleteSQL string

func (*BulkDelete) Execute(ctx context.Context, invocation rh.Invocation) (_ any, retErr error) {
	input, ok := invocation.Input.(*Input)
	if !ok || input == nil || invocation.Binder == nil {
		return nil, fmt.Errorf("bulk payload deletion invocation is incomplete")
	}
	// The shared Input.Init has validated every requested row and filtered keys
	// absent at the pre-read. Repeated deletion with no remaining keys is a no-op.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !input.DeleteUnreferenced {
		return nil, fmt.Errorf("bulk payload deletion requires trusted access")
	}
	output := &Output{}
	if len(input.Payloads) == 0 {
		return output, nil
	}
	deps := struct {
		SQL     rh.TransactionSQLProvider `bind:"kind=transactionSQL,required"`
		DML     xhandler.DML              `bind:"kind=dml,required"`
		Flusher xhandler.Flusher          `bind:"kind=flusher,required"`
	}{}
	if err := invocation.Binder.Bind(ctx, &deps); err != nil {
		return nil, err
	}
	if deps.SQL == nil || deps.DML == nil || deps.Flusher == nil {
		return nil, fmt.Errorf("bulk payload deletion capabilities unavailable")
	}
	guarded, ok := deps.DML.(rh.CriteriaDML)
	if !ok {
		return nil, fmt.Errorf("bulk payload deletion requires guarded DML")
	}
	deleteStatements := 1
	if len(input.Payloads) > 1 {
		deleteStatements++
	}
	done := maintenancediag.Phase(ctx, "payload_bulk_delete")
	var affected int64
	defer func() {
		done(retErr, fmt.Sprintf("mode=bulk delete_statements=%d expected=%d affected=%d", deleteStatements, len(input.Payloads), affected))
	}()
	// Use the same reference predicate as the fixed bulk SQL. This real first
	// deletion anchors a protected prefix that also drains earlier reference
	// removals in this transaction. Cleanup never writes its lease here.
	guardStart := strings.Index(bulkDeleteSQL, "\n  AND ")
	if guardStart < 0 {
		return nil, fmt.Errorf("bulk payload deletion guard is missing")
	}
	guard := strings.TrimSpace(bulkDeleteSQL[guardStart+len("\n  AND "):])
	if err := guarded.DeleteWithCriteria("call_payload", input.Payloads[0], &sqlx.Criteria{Expression: guard}); err != nil {
		return nil, err
	}
	if err := deps.Flusher.Flush(ctx, "call_payload"); err != nil {
		return nil, err
	}
	affected = 1
	if len(input.Payloads) == 1 {
		output.Data = input.Payloads
		return output, nil
	}
	txSQL, err := deps.SQL.Connector(ctx, "agently")
	if err != nil {
		return nil, err
	}
	args := make([]any, len(input.Payloads)-1)
	for i, row := range input.Payloads[1:] {
		args[i] = row.Id
	}
	query := strings.Replace(bulkDeleteSQL, "/*IDS*/", strings.TrimSuffix(strings.Repeat("?,", len(args)), ","), 1)
	result, err := txSQL.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	bulkAffected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	affected += bulkAffected
	maintenancetx.RecordImmediate(ctx, "call_payload", bulkAffected)
	if bulkAffected != int64(len(args)) {
		if maintenancetx.Enabled(ctx) {
			// Guards remain in the SQL: missing or newly referenced rows stay
			// untouched. Counts come from the actual native/SQL evidence, not an
			// invented list of deleted identities for this partially matched batch.
			return output, nil
		}
		// Some rows may already have been removed by this statement. Returning a
		// typed conflict rolls back ALL chunks and graph mutations, not just those
		// rows. Never silently accept a late reference or concurrent disappearance.
		return nil, &xhandler.Conflict{Entity: "call_payload", Field: "id", Reason: "bulk deletion predicate no longer matches every persisted row"}
	}
	output.Data = input.Payloads
	return output, nil
}
