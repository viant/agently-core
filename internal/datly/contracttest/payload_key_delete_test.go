package tests

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	payloaddelete "github.com/viant/agently-core/internal/datly/payload/delete"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	rh "github.com/viant/datly/runtime/handler"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
	dtag "github.com/viant/datly/tag"
	xhandler "github.com/viant/xdatly/handler"
)

func TestPayloadKeyDeleteSQLite(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	db, _ := payloadFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file)))))
	payloadKeyDeleteSuite(t, db)
}

// This opt-in test never creates schema or uses a normal application database.
func TestPayloadKeyDeleteMySQL(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_CLEANUP_BENCHMARK_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_CLEANUP_BENCHMARK_DSN is not set")
	}
	cfg, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	require.Equal(t, "tcp", cfg.Net)
	require.Equal(t, "127.0.0.1:3308", cfg.Addr, "use the dedicated local benchmark MySQL")
	require.True(t, strings.HasPrefix(cfg.DBName, "cleanup_bench_verify_"))
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	payloadKeyDeleteSuite(t, db)
}

func payloadKeyDeleteSuite(t *testing.T, db *sql.DB) {
	for _, mode := range []string{"row", "bulk"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(payloaddelete.ModeEnvironment, mode)
			payloadKeyDeleteModeSuite(t, db)
		})
	}
}

func payloadKeyDeleteModeSuite(t *testing.T, db *sql.DB) {
	ctx := context.Background()
	for _, size := range []int{1024, 64 * 1024, 1024 * 1024} {
		t.Run(fmt.Sprintf("key_only_%d_bytes", size), func(t *testing.T) {
			id := seedKeyPayload(t, db, size)
			var mu sync.Mutex
			type observation struct {
				query string
				rows  int
				err   error
			}
			var queries []observation
			rt := payloadKeyRuntime(t, db, nil, druntime.WithObservability(druntime.ObservabilityConfig{
				ReadingData: func(view string, _ time.Duration, query string, rows int, _ []any, err error) {
					if !strings.EqualFold(view, "CurrentWriter") {
						return
					}
					mu.Lock()
					defer mu.Unlock()
					queries = append(queries, observation{query, rows, err})
				},
			}))
			input := payloadKeyInput(id)
			_, err := rt.InvokeComponent(ctx, payloadKeyRequest(input, true))
			require.NoError(t, err)
			require.Len(t, input.CurrentWriter, 1)
			require.Equal(t, id, input.CurrentWriter[0].Id)
			require.Equal(t, 1, reflect.TypeFor[payloaddelete.CurrentWriterView]().NumField(), "previous state must contain only the key")
			mu.Lock()
			observed := append([]observation(nil), queries...)
			mu.Unlock()
			require.Len(t, observed, 1)
			require.NoError(t, observed[0].err)
			require.Equal(t, 1, observed[0].rows)
			require.NotContains(t, strings.ToLower(observed[0].query), "inline_body")
			require.Contains(t, strings.ToLower(observed[0].query), "select c.id")
			assertKeyPayloadCount(t, db, id, 0)
			t.Logf("%d-byte payload: CurrentWriter selected id only", size)
		})
	}
	t.Run("missing_and_repeated_delete", func(t *testing.T) {
		id := seedKeyPayload(t, db, 1024)
		rt := payloadKeyRuntime(t, db, nil)
		for _, key := range []string{id + "-missing", id, id} {
			_, err := rt.InvokeComponent(ctx, payloadKeyRequest(payloadKeyInput(key), true))
			require.NoError(t, err)
		}
		assertKeyPayloadCount(t, db, id, 0)
	})
	for _, count := range []int{399, 400} {
		t.Run(fmt.Sprintf("batch_%d_key_only", count), func(t *testing.T) {
			ids := seedKeyPayloadBatch(t, db, count)
			var mu sync.Mutex
			var queries []string
			rt := payloadKeyRuntime(t, db, nil, druntime.WithObservability(druntime.ObservabilityConfig{
				ReadingData: func(view string, _ time.Duration, query string, rows int, _ []any, err error) {
					if strings.EqualFold(view, "CurrentWriter") {
						mu.Lock()
						defer mu.Unlock()
						require.NoError(t, err)
						require.Equal(t, count, rows, "the key read must not be paginated")
						queries = append(queries, query)
					}
				},
			}))
			input := payloadKeyInput(ids...)
			_, err := rt.InvokeComponent(ctx, payloadKeyRequest(input, true))
			require.NoError(t, err)
			require.Len(t, input.CurrentWriter, count)
			mu.Lock()
			observed := append([]string(nil), queries...)
			mu.Unlock()
			require.Len(t, observed, 1, "one key-only pre-read for the whole batch")
			require.NotContains(t, strings.ToLower(observed[0]), "inline_body")
			for _, id := range ids {
				assertKeyPayloadCount(t, db, id, 0)
			}
		})
	}
	t.Run("mixed_missing_and_existing_batch", func(t *testing.T) {
		ids := seedKeyPayloadBatch(t, db, 3)
		rt := payloadKeyRuntime(t, db, nil)
		requested := []string{ids[0] + "-missing", ids[0], ids[1], ids[1] + "-missing", ids[2]}
		input := payloadKeyInput(requested...)
		_, err := rt.InvokeComponent(ctx, payloadKeyRequest(input, true))
		require.NoError(t, err)
		require.Len(t, input.Payloads, 3)
		for _, id := range ids {
			assertKeyPayloadCount(t, db, id, 0)
		}
		// All keys missing, including the repeated delete, is a no-op.
		input = payloadKeyInput(requested...)
		_, err = rt.InvokeComponent(ctx, payloadKeyRequest(input, true))
		require.NoError(t, err)
		require.Empty(t, input.Payloads)
	})
	t.Run("late_reference_in_mixed_batch_rolls_back", func(t *testing.T) {
		ids := seedKeyPayloadBatch(t, db, 3)
		seedKeyReference(t, db, ids[1], "message", "attachment_payload_id")
		_, err := db.Exec("UPDATE message SET attachment_payload_id=NULL WHERE id=?", ids[1])
		require.NoError(t, err)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		var mu sync.Mutex
		var injectErr error
		injected := false
		rt := payloadKeyRuntime(t, db, tx, druntime.WithObservability(druntime.ObservabilityConfig{
			ReadingData: func(view string, _ time.Duration, _ string, _ int, _ []any, _ error) {
				if strings.EqualFold(view, "CurrentWriter") {
					mu.Lock()
					defer mu.Unlock()
					_, injectErr = tx.Exec("UPDATE message SET attachment_payload_id=? WHERE id=?", ids[1], ids[1])
					injected = true
				}
			},
		}))
		_, err = rt.InvokeComponent(ctx, payloadKeyRequest(payloadKeyInput(ids...), true))
		var conflict *xhandler.Conflict
		require.ErrorAs(t, err, &conflict, "one guarded row must abort the whole batch")
		mu.Lock()
		require.True(t, injected)
		require.NoError(t, injectErr)
		mu.Unlock()
		_ = tx.Rollback()
		for _, id := range ids {
			assertKeyPayloadCount(t, db, id, 1)
		}
		var reference sql.NullString
		require.NoError(t, db.QueryRow("SELECT attachment_payload_id FROM message WHERE id=?", ids[1]).Scan(&reference))
		require.False(t, reference.Valid)
	})
	t.Run("reject_invalid_contract", func(t *testing.T) {
		id := seedKeyPayload(t, db, 1024)
		rt := payloadKeyRuntime(t, db, nil)
		for _, name := range []string{"absent_access", "false_access", "empty", "duplicate", "nil_row", "unmarked_id", "unmarked_delete", "false_delete", "blank_id", "invalid_last", "oversized_401", "oversized_500"} {
			t.Run(name, func(t *testing.T) {
				input := payloadKeyInput(id)
				request := payloadKeyRequest(input, true)
				switch name {
				case "absent_access":
					request.Providers = nil
				case "false_access":
					request = payloadKeyRequest(input, false)
				case "empty":
					input.SetPayloads(nil)
				case "duplicate":
					input.SetPayloads(append(input.Payloads, input.Payloads[0]))
				case "nil_row":
					input.SetPayloads([]*payloaddelete.PayloadDelete{nil})
				case "unmarked_id":
					input.Payloads[0].Has.Id = false
				case "unmarked_delete":
					input.Payloads[0].Has.ShouldDelete = false
				case "false_delete":
					input.Payloads[0].SetShouldDelete(false)
				case "blank_id":
					input.Payloads[0].SetId(" ")
				case "invalid_last":
					input.SetPayloads(append(input.Payloads, nil))
				case "oversized_401", "oversized_500":
					count := 401
					if name == "oversized_500" {
						count = 500
					}
					ids := []string{id}
					for i := 1; i < count; i++ {
						ids = append(ids, fmt.Sprintf("%s-missing-%d", id, i))
					}
					input = payloadKeyInput(ids...)
					request = payloadKeyRequest(input, true)
				}
				_, err := rt.InvokeComponent(ctx, request)
				require.Error(t, err)
				assertKeyPayloadCount(t, db, id, 1)
			})
		}
		request := httptest.NewRequest("PATCH", payloadKeyRequest(payloadKeyInput(id), true).Target.Route.Path, strings.NewReader(fmt.Sprintf(`{"deleteUnreferenced":true,"data":[{"id":%q,"shouldDelete":true}]}`, id)))
		request.Header.Set("Content-Type", "application/json")
		scope, err := requestprovider.New(request)
		require.NoError(t, err)
		defer scope.Close()
		_, err = rt.ExecuteRoute(ctx, "PATCH", request.URL.Path, scope)
		require.Error(t, err, "the internal component must not become a public deletion route")
		assertKeyPayloadCount(t, db, id, 1)
	})
	for _, ref := range []struct{ table, column string }{
		{"message", "attachment_payload_id"}, {"message", "elicitation_payload_id"},
		{"model_call", "request_payload_id"}, {"model_call", "response_payload_id"},
		{"model_call", "provider_request_payload_id"}, {"model_call", "provider_response_payload_id"}, {"model_call", "stream_payload_id"},
		{"tool_call", "request_payload_id"}, {"tool_call", "response_payload_id"}, {"generated_file", "payload_id"},
	} {
		t.Run("guard_"+ref.table+"_"+ref.column, func(t *testing.T) {
			id := seedKeyPayload(t, db, 1024)
			clear := seedKeyReference(t, db, id, ref.table, ref.column)
			rt := payloadKeyRuntime(t, db, nil)
			// Bypass the preliminary reference reader: the DELETE itself must guard
			// every reference, not merely rely on the store's earlier snapshot.
			_, err := rt.InvokeComponent(ctx, payloadKeyRequest(payloadKeyInput(id), true))
			var conflict *xhandler.Conflict
			require.ErrorAs(t, err, &conflict)
			assertKeyPayloadCount(t, db, id, 1)
			clear()
			_, err = rt.InvokeComponent(ctx, payloadKeyRequest(payloadKeyInput(id), true))
			require.NoError(t, err)
			assertKeyPayloadCount(t, db, id, 0)
		})
	}
	t.Run("caller_transaction_owns_completion", func(t *testing.T) {
		id := seedKeyPayload(t, db, 1024)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		rt := payloadKeyRuntime(t, db, tx)
		_, err = rt.InvokeComponent(ctx, payloadKeyRequest(payloadKeyInput(id), true))
		require.NoError(t, err)
		// Already absent must also be a no-op inside a caller transaction,
		// without aborting it or taking ownership of commit/rollback.
		_, err = rt.InvokeComponent(ctx, payloadKeyRequest(payloadKeyInput(id), true))
		require.NoError(t, err)
		var count int
		require.NoError(t, tx.QueryRow("SELECT COUNT(*) FROM call_payload WHERE id=?", id).Scan(&count))
		require.Zero(t, count)
		require.NoError(t, tx.Rollback())
		assertKeyPayloadCount(t, db, id, 1)
	})
	t.Run("reference_after_current_read_rolls_back", func(t *testing.T) {
		id := seedKeyPayload(t, db, 1024)
		clear := seedKeyReference(t, db, id, "message", "attachment_payload_id")
		_, err := db.Exec("UPDATE message SET attachment_payload_id=NULL WHERE id=?", id)
		require.NoError(t, err)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = tx.Exec("UPDATE call_payload SET digest='before-delete' WHERE id=?", id)
		require.NoError(t, err)
		var injectedMu sync.Mutex
		injected := false
		var injectErr error
		rt := payloadKeyRuntime(t, db, tx, druntime.WithObservability(druntime.ObservabilityConfig{
			ReadingData: func(view string, _ time.Duration, _ string, _ int, _ []any, _ error) {
				if strings.EqualFold(view, "CurrentWriter") {
					// Deterministic late reference within the caller transaction; SQLite
					// cannot concurrently upgrade a second writer while this one holds it.
					injectedMu.Lock()
					defer injectedMu.Unlock()
					_, injectErr = tx.Exec("UPDATE message SET attachment_payload_id=? WHERE id=?", id, id)
					injected = true
				}
			},
		}))
		_, err = rt.InvokeComponent(ctx, payloadKeyRequest(payloadKeyInput(id), true))
		var conflict *xhandler.Conflict
		require.ErrorAs(t, err, &conflict)
		injectedMu.Lock()
		didInject, injectionError := injected, injectErr
		injectedMu.Unlock()
		require.True(t, didInject)
		require.NoError(t, injectionError)
		_ = tx.Rollback() // runtime may already have aborted the caller transaction
		assertKeyPayloadCount(t, db, id, 1)
		var digest, reference sql.NullString
		require.NoError(t, db.QueryRow("SELECT digest FROM call_payload WHERE id=?", id).Scan(&digest))
		require.False(t, digest.Valid, "earlier caller mutation must roll back too")
		require.NoError(t, db.QueryRow("SELECT attachment_payload_id FROM message WHERE id=?", id).Scan(&reference))
		require.False(t, reference.Valid)
		clear()
	})
	for _, cancelSecond := range []bool{false, true} {
		t.Run(fmt.Sprintf("later_batch_rollback_cancel_%t", cancelSecond), func(t *testing.T) {
			ids := seedKeyPayloadBatch(t, db, 401)
			if !cancelSecond {
				seedKeyReference(t, db, ids[400], "message", "attachment_payload_id")
			}
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer tx.Rollback()
			rt := payloadKeyRuntime(t, db, tx)
			_, err = rt.InvokeComponent(ctx, payloadKeyRequest(payloadKeyInput(ids[:400]...), true))
			require.NoError(t, err)
			var count int
			require.NoError(t, tx.QueryRow("SELECT COUNT(*) FROM call_payload WHERE id=?", ids[0]).Scan(&count))
			require.Zero(t, count, "first chunk executed but must not commit")
			secondCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if cancelSecond {
				cancel()
			}
			_, err = rt.InvokeComponent(secondCtx, payloadKeyRequest(payloadKeyInput(ids[400]), true))
			require.Error(t, err)
			if !cancelSecond {
				var conflict *xhandler.Conflict
				require.ErrorAs(t, err, &conflict)
			}
			_ = tx.Rollback()
			for _, id := range ids {
				assertKeyPayloadCount(t, db, id, 1)
			}
		})
	}
}

// Edge rows contain 1 MiB each; neither a batched key read nor a guarded delete
// should materialize them. Every seed and cleanup is confined to this unique tag.
func seedKeyPayloadBatch(t *testing.T, db *sql.DB, count int) []string {
	t.Helper()
	tag := fmt.Sprintf("key-delete-batch-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, err := db.Exec("DELETE FROM call_payload WHERE id LIKE ?", tag+"-%")
		require.NoError(t, err)
	})
	tx, err := db.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	stmt, err := tx.Prepare("INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,compression) VALUES(?,'attachment','text/plain',?,'inline',?,'none')")
	require.NoError(t, err)
	defer stmt.Close()
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("%s-%03d", tag, i)
		size := 1024
		if i == 0 || i == count-1 {
			size = 1024 * 1024
		}
		_, err := stmt.Exec(ids[i], size, bytes.Repeat([]byte("x"), size))
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	return ids
}

func seedKeyPayload(t *testing.T, db *sql.DB, size int) string {
	t.Helper()
	id := fmt.Sprintf("key-delete-test-%d", time.Now().UnixNano())
	_, err := db.Exec("INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,compression) VALUES(?,'attachment','text/plain',?,'inline',?,'none')", id, size, bytes.Repeat([]byte("x"), size))
	require.NoError(t, err)
	t.Cleanup(func() { _, err := db.Exec("DELETE FROM call_payload WHERE id=?", id); require.NoError(t, err) })
	return id
}

func seedKeyReference(t *testing.T, db *sql.DB, id, table, column string) func() {
	t.Helper()
	clear := func() {
		for _, statement := range []string{
			"DELETE FROM generated_file WHERE conversation_id=?", "DELETE FROM model_call WHERE message_id=?",
			"DELETE FROM tool_call WHERE message_id=?", "DELETE FROM message WHERE id=?", "DELETE FROM conversation WHERE id=?",
		} {
			_, err := db.Exec(statement, id)
			require.NoError(t, err)
		}
	}
	t.Cleanup(clear)
	for _, statement := range []string{
		"INSERT INTO conversation(id) VALUES(?)",
		"INSERT INTO message(id,conversation_id,role,type) VALUES(?,?,'assistant','text')",
	} {
		args := []any{id}
		if strings.Contains(statement, "message") {
			args = append(args, id)
		}
		_, err := db.Exec(statement, args...)
		require.NoError(t, err)
	}
	var query string
	switch table {
	case "message":
		query = "UPDATE message SET " + column + "=? WHERE id=?"
	case "model_call":
		query = "INSERT INTO model_call(" + column + ",message_id,provider,model,model_kind,status) VALUES(?,?,'test','test','chat','completed')"
	case "tool_call":
		query = "INSERT INTO tool_call(" + column + ",message_id,op_id,tool_name,tool_kind,status) VALUES(?,?,'op','test','general','completed')"
	case "generated_file":
		query = "INSERT INTO generated_file(payload_id,conversation_id,id,provider,mode,copy_mode) VALUES(?,?,?,'test','inline','eager')"
	}
	args := []any{id, id}
	if table == "generated_file" {
		args = append(args, id)
	}
	_, err := db.Exec(query, args...)
	require.NoError(t, err)
	return clear
}

func assertKeyPayloadCount(t *testing.T, db *sql.DB, id string, expected int) {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM call_payload WHERE id=?", id).Scan(&count))
	require.Equal(t, expected, count)
}

func payloadKeyInput(ids ...string) *payloaddelete.Input {
	rows := make([]*payloaddelete.PayloadDelete, 0, len(ids))
	for _, id := range ids {
		row := &payloaddelete.PayloadDelete{}
		row.SetId(id)
		row.SetShouldDelete(true)
		rows = append(rows, row)
	}
	input := &payloaddelete.Input{}
	input.SetPayloads(rows)
	return input
}

func payloadKeyRequest(input *payloaddelete.Input, allowed bool) dexec.ComponentRequest {
	request := dexec.ComponentRequest{
		Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[payloaddelete.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/payload/delete"}},
		Input:  input,
		Providers: []locator.Provider{provider.Named("payloadaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			return allowed, name == "deleteUnreferenced", nil
		})},
	}
	if os.Getenv(payloaddelete.ModeEnvironment) == "bulk" {
		request.Target.Component.Name = "PayloadBulkDelete"
		request.Target.Route.Path = "/v1/internal/agently/payload/delete-bulk"
	}
	return request
}

func payloadKeyRuntime(t *testing.T, db *sql.DB, tx *sql.Tx, options ...druntime.Option) *druntime.Runtime {
	t.Helper()
	typ := reflect.TypeFor[payloaddelete.WriterComponent]()
	if os.Getenv(payloaddelete.ModeEnvironment) == "bulk" {
		typ = reflect.TypeFor[payloaddelete.BulkDeleteComponent]()
	}
	field, _ := typ.FieldByName("Contract")
	metadata, _, err := dtag.ParseComponent(field.Tag)
	require.NoError(t, err)
	source := &bootstrap.RouteSource{PackagePath: typ.PkgPath(), HolderType: typ.Name(), FieldName: field.Name, Tag: metadata, InputType: "Input", OutputType: "Output"}
	component, err := source.Resolve(reflect.TypeFor[payloaddelete.Input](), reflect.TypeFor[payloaddelete.Output]())
	require.NoError(t, err)
	require.True(t, component.Routes[0].Internal)
	resources := resource.New()
	require.NoError(t, resources.Register(payloaddelete.WriterDatlyResourceNamespace, payloaddelete.WriterDatlyResources))
	var handler rh.TypedHandler
	if typ == reflect.TypeFor[payloaddelete.BulkDeleteComponent]() {
		handler = &payloaddelete.BulkDelete{}
	} else {
		handler, err = writer.New(component, reflect.TypeFor[payloaddelete.Input](), reflect.TypeFor[payloaddelete.Output](), "patch")
		require.NoError(t, err)
	}
	artifact, err := bootstrap.BuildArtifact(bootstrap.ArtifactInput{Component: component, InputType: reflect.TypeFor[payloaddelete.Input](), OutputType: reflect.TypeFor[payloaddelete.Output](), Handler: handler, HandlerOwnedOutput: true, Resources: resources})
	require.NoError(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: artifact.ViewDependencies, Input: artifact.Input, SQL: &dsql.SQLComponent{DB: db, Tx: tx}})
	require.NoError(t, err)
	connectors := &dsql.SQLComponent{DB: db, Tx: tx}
	require.NoError(t, connectors.RegisterConnector("agently", db))
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[payloaddelete.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db, Tx: tx}, Capabilities: rh.InvocationCapabilities{Connector: connectors}}}, append([]druntime.Option{druntime.WithResources(resources)}, options...)...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rt.Shutdown(context.Background())) })
	return rt
}
