package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	linkread "github.com/viant/agently-core/internal/datly/oauth/linkstate/read"
	replacement "github.com/viant/agently-core/internal/datly/oauth/linkstate/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
)

// This verifies the atomic replacement primitive. Create/adopt/retry composition
// is a separate outstanding gate and this package is deliberately not linked.
func TestLinkStateAtomicReplacement(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc  string
		input bool
	}
	for _, tc := range []useCase{{"expired row changes state hash under guarded flow identity", false}, {"consumed row changes state hash and clears consumed timestamp", true}} {
		t.Run(tc.desc, func(t *testing.T) {
			fixture := func() (*sql.DB, string) {
				db, path := goalFixture(t, project)
				consumed := "NULL"
				expiry := "2026-01-01 00:00:00"
				if tc.input {
					consumed = "'2026-01-01 00:00:00'"
					expiry = "2027-01-01 00:00:00"
				}
				_, err := db.Exec("INSERT INTO oauth_link_state(state_hash,flow_hash,user_id,session_hash,provider,expires_at,consumed_at,created_at) VALUES('old','flow','u1','session1','ap','" + expiry + "'," + consumed + ",'2026-01-01 00:00:00')")
				must(t, err)
				return db, path
			}
			db, _ := fixture()
			resources := resource.New()
			must(t, resources.Register(linkread.ReaderDatlyResourceNamespace, linkread.ReaderDatlyResources))
			must(t, resources.Register(replacement.WriteDatlyResourceNamespace, replacement.WriteDatlyResources))
			ra := payloadArtifact(t, resources, reflect.TypeFor[linkread.ReaderComponent](), reflect.TypeFor[linkread.LinkStateInput](), reflect.TypeFor[linkread.LinkStateOutput]())
			wa := payloadArtifact(t, resources, reflect.TypeFor[replacement.WriteComponent](), reflect.TypeFor[replacement.Input](), reflect.TypeFor[replacement.Output]())
			reader, err := ra.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
			must(t, err)
			views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
			must(t, err)
			handler, err := writer.New(wa.Component, reflect.TypeFor[replacement.Input](), reflect.TypeFor[replacement.Output](), "patch")
			must(t, err)
			rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: ra.Component, Input: ra.Input, Output: ra.Output, OutputType: reflect.TypeFor[linkread.LinkStateOutput](), Reader: reader, Providers: []locator.Provider{ordinaryAccess("linkstateaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return true, true, nil })}}, {Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[replacement.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db}}}, druntime.WithResources(resources))
			must(t, err)
			request := httptest.NewRequest("PATCH", "/v1/internal/agently/user/oauth/linkstate/write?mode=replace&observedState=old&now=2026-01-02%2000:00:00", strings.NewReader(`{"data":[{"stateHash":"new","flowHash":"flow","userId":"u2","sessionHash":"session2","provider":"bp","expiresAt":"2027-01-01T00:00:00Z","consumedAt":null,"createdAt":"2026-01-02T00:00:00Z"}]}`))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/internal/agently/user/oauth/linkstate/write", scope)
			must(t, err)
			input := &linkread.LinkStateInput{}
			input.SetFlowHash("flow")
			input.SetPending(true)
			result, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: ra.Component.Key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/user/oauth/linkstate/current"}}, Input: input})
			must(t, err)
			data, err := json.Marshal(result.(*linkread.LinkStateOutput).Data)
			must(t, err)
			var current []json.RawMessage
			must(t, json.Unmarshal(data, &current))
			newRows := normalizeRowsInOrder(t, current)
			if len(newRows) != 1 || newRows[0]["statehash"] != "new" || newRows[0]["consumedat"] != nil {
				t.Fatalf("replacement=%s", pretty(newRows))
			}
		})
	}
}
