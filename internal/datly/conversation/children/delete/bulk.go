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
	Contract xdatly.Component[Input, Output] `component:"writer,path=/v1/internal/agently/conversation/children/delete,method=PATCH,connector=agently,handler=NewDelete,internal=true"`
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
		SQL     rh.TransactionSQLProvider `bind:"kind=transactionSQL,required"`
		Flusher xhandler.Flusher          `bind:"kind=flusher,required"`
	}{}
	if err := invocation.Binder.Bind(ctx, &deps); err != nil {
		return nil, err
	}
	if deps.SQL == nil || deps.Flusher == nil {
		return nil, fmt.Errorf("conversation child deletion capabilities unavailable")
	}
	// Previously queued detaches/removals must be visible in the SAME transaction.
	if err := deps.Flusher.Flush(ctx, ""); err != nil {
		return nil, err
	}
	tx, err := deps.SQL.Connector(ctx, "agently")
	if err != nil {
		return nil, err
	}
	args := make([]any, len(input.IDs))
	for i, id := range input.IDs {
		args[i] = id
	}
	query := strings.Replace(queries[input.Table], "/*IDS*/", strings.TrimSuffix(strings.Repeat("?,", len(args)), ","), 1)
	done := maintenancediag.Phase(ctx, "children_bulk_delete")
	defer func() {
		done(retErr, fmt.Sprintf("table=%s keys=%d affected=%d delete_statements=1", input.Table, len(args), output.Affected))
	}()
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	output.Affected, err = result.RowsAffected()
	if err != nil {
		return nil, err
	}
	// As with canonical onDeleteNotFound=ignore, absent keys are a safe no-op.
	// No BEGIN/COMMIT, fallback or retry here: any error rolls back the parent.
	return output, nil
}
