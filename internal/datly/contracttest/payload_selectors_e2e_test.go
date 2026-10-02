package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
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

func TestPayloadSelectorProxyLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
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
		{"field selection retains declared IDs", input{selector: state.Selector{Fields: []string{"id"}, OrderBy: "id"}}, expect{ids: []string{"p-malformed", "p1"}}},
		{"raw reader uses explicit legacy name mapping", input{raw: true, selector: state.Selector{OrderBy: "id DESC", Limit: 1}}, expect{ids: []string{"p1"}}},
		{"trusted bound criteria", input{selector: state.Selector{Criteria: "size_bytes >= ?", Placeholders: []any{15}, OrderBy: "id"}}, expect{ids: []string{"p-malformed"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, path := payloadFixture(t, project)
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
			request := struct {
				Component, DBPath string
				Raw               bool
				Filters           map[string]any
				Selectors         state.Selectors
			}{"payload", path, tc.input.raw, map[string]any{"tenantID": "tenant-a"}, selectors}
			data, err := json.Marshal(request)
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(data)
			oldData, err := process.CombinedOutput()
			if err != nil {
				t.Fatalf("legacy selectors: %v\n%s", err, oldData)
			}
			var old struct{ Rows []json.RawMessage }
			must(t, json.Unmarshal(oldData, &old))
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
			if !reflect.DeepEqual(ids(old.Rows), tc.expect.ids) || !reflect.DeepEqual(ids(actual), tc.expect.ids) {
				t.Fatalf("legacy=%v v1=%v expected=%v", ids(old.Rows), ids(actual), tc.expect.ids)
			}
			oldRows, newRows := normalizeRowsInOrder(t, old.Rows), normalizeRowsInOrder(t, actual)
			if len(tc.input.selector.Fields) == 0 {
				if !reflect.DeepEqual(oldRows, newRows) {
					t.Fatalf("selected row parity\nlegacy=%s\nv1=%s", pretty(oldRows), pretty(newRows))
				}
			} else {
				// Legacy's fixed payload SQL ignores projection and overfetches.
				// Native v1 intentionally scans only the declared requested columns.
				for index, row := range newRows {
					for _, field := range tc.input.selector.Fields {
						if !reflect.DeepEqual(row[field], oldRows[index][field]) {
							t.Fatalf("projected field %s differs", field)
						}
					}
					if row["inlinebody"] != nil || row["tenantid"] != nil {
						t.Fatalf("v1 overfetched unselected columns: %s", pretty(row))
					}
				}
			}
			// Reusing the provider must keep invocation-local selector changes isolated.
			if _, err = rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: route}}, Input: componentInput, Providers: []locator.Provider{proxy}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
