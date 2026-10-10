package tests

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	payloadwrite "github.com/viant/agently-core/internal/datly/payload/write"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

// TestPayloadDeleteCurrentWriterIgnoresCallerProjection characterizes the pinned
// Datly runtime, not the desired cleanup optimization. Independent input views
// intentionally do not inherit output selectors. A caller's id-only selector
// therefore does NOT prevent CurrentWriter from reading inline_body. This probe
// documents why cleanup uses payload/delete instead of this full writer. The
// companion TestPayloadKeyDelete tests assert its lean SQL and previous state.
func TestPayloadDeleteCurrentWriterIgnoresCallerProjection(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	for _, size := range []int{1024, 64 * 1024, 1024 * 1024} {
		for _, projectID := range []bool{false, true} {
			t.Run(fmt.Sprintf("bytes=%d/id_selector=%t", size, projectID), func(t *testing.T) {
				db, _ := payloadFixture(t, project)
				body := bytes.Repeat([]byte("x"), size)
				_, err := db.Exec("UPDATE call_payload SET inline_body=?, size_bytes=? WHERE id='p1'", body, size)
				must(t, err)

				// Capture the executed SELECT locally in the test only. Do not enable
				// SQL/argument logging in application cleanup to measure this behavior.
				type observation struct {
					query string
					rows  int
					err   error
				}
				var mu sync.Mutex
				var reads []observation
				rt, _, _ := payloadRuntime(t, db, druntime.WithObservability(druntime.ObservabilityConfig{
					ReadingData: func(view string, _ time.Duration, query string, rows int, _ []any, err error) {
						if !strings.EqualFold(view, "CurrentWriter") {
							return
						}
						mu.Lock()
						defer mu.Unlock()
						reads = append(reads, observation{query: query, rows: rows, err: err})
					},
				}))
				t.Cleanup(func() { must(t, rt.Shutdown(context.Background())) })
				providers := []locator.Provider{provider.Named("payloadaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
					if name == "deleteUnreferenced" {
						return true, true, nil
					}
					return nil, false, nil
				})}
				if projectID {
					providers = append(providers, queryselectors.Provider(state.Selectors{&state.NamedSelector{
						Name: "CurrentWriter", Selector: state.Selector{Fields: []string{"id"}},
					}}))
				}
				row := &payloadwrite.Payload{}
				row.SetId("p1")
				row.SetShouldDelete(true)
				input := &payloadwrite.Input{}
				input.SetPayloads([]*payloadwrite.Payload{row})
				_, err = rt.InvokeComponent(context.Background(), dexec.ComponentRequest{
					Target: dexec.ComponentTarget{
						Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[payloadwrite.WriterComponent]().PkgPath(), Name: "writer"},
						Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/payload"},
					},
					Input: input, Providers: providers,
				})
				must(t, err)
				if !input.DeleteUnreferenced {
					t.Fatal("test did not use the guarded cleanup delete path")
				}
				if len(input.CurrentWriter) != 1 || input.CurrentWriter[0] == nil {
					t.Fatalf("bound CurrentWriter has %d rows, want one non-nil row", len(input.CurrentWriter))
				}
				previous := input.CurrentWriter[0]
				if previous.Id != "p1" || previous.InlineBody == nil || !bytes.Equal(*previous.InlineBody, body) {
					t.Fatalf("CurrentWriter no longer loads the full %d-byte previous body; re-evaluate delete-only projection", size)
				}
				mu.Lock()
				observed := append([]observation(nil), reads...)
				mu.Unlock()
				if len(observed) != 1 || observed[0].err != nil || observed[0].rows != 1 {
					t.Fatalf("expected one successful CurrentWriter query returning one row, got %+v", observed)
				}
				if !strings.Contains(strings.ToLower(observed[0].query), "inline_body") {
					t.Fatalf("actual CurrentWriter SQL no longer selects inline_body: %s", observed[0].query)
				}
				var remaining int
				must(t, db.QueryRow("SELECT COUNT(*) FROM call_payload WHERE id='p1'").Scan(&remaining))
				if remaining != 0 {
					t.Fatal("unreferenced payload was not deleted")
				}
				t.Logf("id-only selector=%t: CurrentWriter returned %d body bytes before guarded deletion", projectID, len(*previous.InlineBody))
			})
		}
	}
}
