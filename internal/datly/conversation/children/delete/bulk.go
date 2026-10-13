// Package delete provides key-only bulk deletion for a parent's locked graph.
// It is deliberately separate from the public message/call/file writers.
package delete

import (
	"context"
	"embed"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/viant/agently-core/internal/store/maintenancebatch"
	"github.com/viant/agently-core/internal/store/maintenancediag"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	"github.com/viant/xdatly"
	xhandler "github.com/viant/xdatly/handler"
)

const Path = "/v1/internal/agently/conversation/children/delete"

type Input struct {
	Trusted bool     `parameter:"Trusted,kind=conversationchildrenaccess,in=internal,dataType=bool,required=true"`
	Table   string   `parameter:"Table,kind=body,in=table"`
	IDs     []string `parameter:"IDs,kind=body,in=ids"`
}
type Output struct{ Affected int64 }
type Component struct {
	Contract xdatly.Component[Input, Output] `component:"writer,path=/v1/internal/agently/conversation/children/delete,method=PATCH,connector=agently,handler=NewDelete,internal=true" protectedFlushTables:"[\"message\",\"model_call\",\"tool_call\",\"generated_file\"]"`
}

var DeleteDatly = new(Component)
var _reachable = reflect.TypeFor[Component]()

//go:embed sql/*.sql
var resources embed.FS

func (Component) EmbedFS() *embed.FS     { return &resources }
func (Component) EmbedNamespace() string { return "conversation_children_delete" }
func (Component) DatlyHandler(name string) func() (rh.TypedHandler, error) {
	if name != "NewDelete" {
		return nil
	}
	return func() (rh.TypedHandler, error) { return &Delete{}, nil }
}

// Fixed templates are an allowlist, never interpolated caller-provided SQL.
var queries = func() map[string]string {
	result := map[string]string{}
	for _, table := range []string{"message", "model_call", "tool_call", "generated_file"} {
		data, err := resources.ReadFile("sql/" + table + ".sql")
		if err != nil {
			panic(err)
		}
		result[table] = string(data)
	}
	return result
}()

func (input *Input) Init(context.Context) error {
	if input == nil || !input.Trusted {
		return fmt.Errorf("trusted locked conversation graph is required")
	}
	if _, ok := queries[input.Table]; !ok {
		return fmt.Errorf("unsupported conversation child table %q", input.Table)
	}
	// Bound even raw input (including duplicate keys), not just the final SQL.
	if len(input.IDs) > maintenancebatch.Size {
		return fmt.Errorf("conversation child deletion exceeds %d keys", maintenancebatch.Size)
	}
	seen := map[string]bool{}
	keys := make([]string, 0, len(input.IDs))
	for _, id := range input.IDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("conversation child identity is empty")
		}
		if !seen[id] {
			keys = append(keys, id)
			seen[id] = true
		}
	}
	sort.Strings(keys)
	input.IDs = keys
	return nil
}

type Delete struct{}

func (*Delete) InputType() reflect.Type             { return reflect.TypeFor[Input]() }
func (*Delete) OutputType() reflect.Type            { return reflect.TypeFor[Output]() }
func (*Delete) RequiresPreBindingTransaction() bool { return true }

func (*Delete) Execute(ctx context.Context, invocation rh.Invocation) (_ any, retErr error) {
	input, ok := invocation.Input.(*Input)
	if !ok || input == nil || invocation.Binder == nil {
		return nil, fmt.Errorf("conversation child deletion invocation is incomplete")
	}
	// Validate here too: a direct typed invocation must not bypass the boundary.
	if err := input.Init(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	output := &Output{}
	if len(input.IDs) == 0 {
		return output, nil
	}
	deps := struct {
		SQL      rh.TransactionSQLProvider `bind:"kind=transactionSQL,required"`
		DML      xhandler.DML              `bind:"kind=dml,required"`
		Flusher  xhandler.Flusher          `bind:"kind=flusher,required"`
		Reporter dexec.MutationReporter    `bind:"kind=mutationReporter,required"`
	}{}
	if err := invocation.Binder.Bind(ctx, &deps); err != nil {
		return nil, err
	}
	if deps.SQL == nil || deps.DML == nil || deps.Flusher == nil || deps.Reporter == nil {
		return nil, fmt.Errorf("conversation child deletion capabilities unavailable")
	}
	deleteStatements := 1
	if len(input.IDs) > 1 {
		deleteStatements++
	}
	done := maintenancediag.Phase(ctx, "children_bulk_delete")
	defer func() {
		done(retErr, fmt.Sprintf("table=%s keys=%d affected=%d delete_statements=%d", input.Table, len(input.IDs), output.Affected, deleteStatements))
	}()
	// The first real deletion anchors a protected prefix, so earlier queued
	// detaches/removals become visible in the same transaction before bulk SQL.
	before := len(deps.Reporter.MutationReport().Results)
	anchor := input.IDs[0]
	var key any = &struct {
		ID string `sqlx:"id,primaryKey"`
	}{ID: anchor}
	if input.Table == "model_call" || input.Table == "tool_call" {
		key = &struct {
			MessageID string `sqlx:"message_id,primaryKey"`
		}{MessageID: anchor}
	}
	if err := deps.DML.Delete(input.Table, key); err != nil {
		return nil, err
	}
	if err := deps.Flusher.Flush(ctx, input.Table); err != nil {
		return nil, err
	}
	results := deps.Reporter.MutationReport().Results
	if len(results) <= before {
		return nil, fmt.Errorf("conversation child deletion anchor was not reported")
	}
	output.Affected = results[len(results)-1].Affected
	if len(input.IDs) == 1 {
		return output, nil
	}
	tx, err := deps.SQL.Connector(ctx, "agently")
	if err != nil {
		return nil, err
	}
	args := make([]any, len(input.IDs)-1)
	for i, id := range input.IDs[1:] {
		args[i] = id
	}
	query := strings.Replace(queries[input.Table], "/*IDS*/", strings.TrimSuffix(strings.Repeat("?,", len(args)), ","), 1)
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	output.Affected += affected
	// As with canonical onDeleteNotFound=ignore, absent keys are a safe no-op.
	// No BEGIN/COMMIT, fallback or retry here: any error rolls back the parent.
	return output, nil
}
