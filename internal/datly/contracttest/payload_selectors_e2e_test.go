package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	rawread "github.com/viant/agently-core/internal/datly/payload/read"
	payloadread "github.com/viant/agently-core/internal/datly/payload/reference"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

func TestPayloadSelectorProxyContracts(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		selector state.Selector
		raw      bool
	}
	type expect struct {
		ids []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"descending order with bound limit", input{selector: state.Selector{OrderBy: "id DESC", Limit: 1}}, expect{ids: []string{"p1"}}},
		{"offset within authorized tenant", input{selector: state.Selector{OrderBy: "id", Limit: 1, Offset: 1}}, expect{ids: []string{"p1"}}},
		{"field selection retains declared IDs", input{selector: state.Selector{Fields: []string{"id"}, OrderBy: "id"}}, expect{ids: []string{"p-criteria", "p1"}}},
		{"raw reader uses explicit legacy name mapping", input{raw: true, selector: state.Selector{OrderBy: "id DESC", Limit: 1}}, expect{ids: []string{"p1"}}},
		{"trusted bound criteria", input{selector: state.Selector{Criteria: "size_bytes >= ?", Placeholders: []any{15}, OrderBy: "id"}}, expect{ids: []string{"p-criteria"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			// Healthy size-15 payload isolates selector/tenant semantics from the
			// explicitly tested corrupt-gzip failure contract.
			db, _ := payloadFixture(t, project)
			rt, binaryKey, rawKey := payloadRuntime(t, db)
			name := "payload"
			key := binaryKey
			route := "/v2/api/agently/payload"
			var componentInput any = &payloadread.Input{TenantID: "tenant-a", Has: &payloadread.InputHas{TenantID: true}}
			if tc.input.raw {
				name = "payload_rows"
				key = rawKey
				route = "/v1/api/agently/payload"
				componentInput = &rawread.PayloadRowsInput{TenantID: "tenant-a", Has: &rawread.PayloadRowsInputHas{TenantID: true}}
			}
			selectors := state.Selectors{&state.NamedSelector{Name: name, Selector: tc.input.selector}}
			// The proxy takes its snapshot before the caller changes the collection.
			proxy := queryselectors.ProviderMapped(selectors, map[string]string{name: "reader"})
			selectors[0].OrderBy = "untrusted-column"
			selectors[0].Limit = 500
			output, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: route}}, Input: componentInput, Providers: []locator.Provider{proxy}})
			must(t, err)
			var rows any
			if tc.input.raw {
				rows = output.(*rawread.PayloadRowsOutput).Data
			} else {
				rows = output.(*payloadread.Output).Data
			}
			encoded, err := json.Marshal(rows)
			must(t, err)
			var actual []json.RawMessage
			must(t, json.Unmarshal(encoded, &actual))
			ids := func(rows []json.RawMessage) []string {
				result := []string{}
				for _, row := range rows {
					var value map[string]json.RawMessage
					must(t, json.Unmarshal(row, &value))
					for name, raw := range value {
						if name == "Id" || name == "id" {
							var id string
							must(t, json.Unmarshal(raw, &id))
							result = append(result, id)
						}
					}
				}
				return result
			}
			if !reflect.DeepEqual(ids(actual), tc.expect.ids) {
				t.Fatalf("selected IDs=%v expected=%v", ids(actual), tc.expect.ids)
			}
			newRows := normalizeRowsInOrder(t, actual)
			for _, row := range newRows {
				if len(tc.input.selector.Fields) > 0 {
					for _, field := range tc.input.selector.Fields {
						if row[field] == nil {
							t.Fatalf("projected field %s missing from row %s", field, row["id"])
						}
					}
					if row["inlinebody"] != nil || row["tenantid"] != nil {
						t.Fatalf("selector overfetched unselected columns: %s", pretty(row))
					}
				} else if row["kind"] != "request" {
					t.Fatalf("row %s kind=%v, want fixture kind request", row["id"], row["kind"])
				}
			}
			// Reusing the provider must keep invocation-local selector changes isolated.
			if _, err = rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: route}}, Input: componentInput, Providers: []locator.Provider{proxy}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
