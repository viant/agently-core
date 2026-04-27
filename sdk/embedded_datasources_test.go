package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpschema "github.com/viant/mcp-protocol/schema"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
	"github.com/viant/agently-core/sdk/api"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/service/elicitation/refiner"
	oversvc "github.com/viant/agently-core/service/lookup/overlay"
	"github.com/viant/forge/backend/types"
)

// fakeExecutor returns a fixed result so the test doesn't need a real MCP.
type fakeExecutor struct{}

func (fakeExecutor) Execute(_ context.Context, _ string, _ map[string]interface{}) (string, error) {
	return `{"results":[{"id":1,"name":"Acme"}]}`, nil
}

// Ensure SetDatasourceStack wires the backendClient so it satisfies both
// optional interfaces AND installs the elicitation refiner hook.
func TestBackendClient_SetDatasourceStack_WiresEndToEnd(t *testing.T) {
	// Build the full datasource stack.
	dsStore := dssvc.NewMemoryStore()
	ds := &dsproto.DataSource{
		ID: "advertiser",
		Backend: &dsproto.Backend{
			Kind: dsproto.BackendMCPTool, Service: "platform", Method: "advertiser_search",
		},
	}
	ds.DataSource = types.DataSource{Selectors: &types.Selectors{Data: "results"}}
	dsStore.Put(ds)
	dsService := dssvc.New(dssvc.Options{Store: dsStore, Executor: fakeExecutor{}})

	// Build the overlay stack with one schema-bound binding.
	overlayStore := oversvc.NewMemoryStore()
	overlayStore.Replace([]*loproto.Overlay{{
		ID:       "ov1",
		Priority: 10,
		Target:   loproto.Target{Kind: "elicitation"},
		Mode:     loproto.ModePartial,
		Bindings: []loproto.Binding{{
			Match: loproto.Match{FieldName: "advertiser_id", Type: "integer"},
			Lookup: loproto.Lookup{
				DataSource:   "advertiser",
				DialogId:     "advertiserPicker",
				QueryInput:   "q",
				ResolveInput: "id",
				Outputs: []loproto.Parameter{
					{Location: "id", Name: "advertiser_id"},
				},
				Display: "${name}",
			},
		}},
	}})
	overlayService := oversvc.New(overlayStore)

	// Clean slate — remove any overlay hook installed by a previous test.
	refiner.SetOverlayHook(nil)

	// Satisfy newBackend's non-nil constraints with a zero-value agent and
	// conversation client stand-in. We never call methods that use them in
	// this test.
	bc := &backendClient{}

	// Wire the stack.
	bc.SetDatasourceStack(dsService, overlayService)

	// 1) Backend interface (now always carrying the three methods) is
	//    satisfied by the embedded client — no optional sub-interfaces.
	var _ Backend = bc

	// 2) FetchDatasource returns projected rows.
	fetchOut, err := bc.FetchDatasource(context.Background(), &api.FetchDatasourceInput{
		ID: "advertiser", Inputs: map[string]interface{}{"q": "x"},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(fetchOut.Rows) != 1 || fetchOut.Rows[0]["name"] != "Acme" {
		t.Fatalf("projection failed: %+v", fetchOut.Rows)
	}

	// 3) Registry returns nothing here (no Named bindings) but must not error.
	regOut, err := bc.ListLookupRegistry(context.Background(), &api.ListLookupRegistryInput{Context: "template:any"})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	_ = regOut

	// 4) The refiner hook is installed — calling refiner.Refine on a schema
	// with advertiser_id should gain x-ui-widget=lookup + x-ui-lookup.
	props := map[string]interface{}{
		"advertiser_id": map[string]interface{}{"type": "integer"},
	}
	rs := &mcpschema.ElicitRequestParamsRequestedSchema{Properties: props}
	refiner.Refine(rs)
	after, _ := rs.Properties["advertiser_id"].(map[string]interface{})
	if after["x-ui-widget"] != "lookup" {
		t.Fatalf("overlay hook did not attach x-ui-widget: %+v", after)
	}
	att, _ := after["x-ui-lookup"].(map[string]interface{})
	if att["dataSource"] != "advertiser" {
		t.Fatalf("attachment missing dataSource: %+v", att)
	}
	if att["queryInput"] != "q" || att["resolveInput"] != "id" {
		t.Fatalf("attachment missing query/resolve inputs: %+v", att)
	}

	// 5) HTTP dispatch reaches the same backend (handler picks up interface).
	body := `{"inputs":{"q":"x"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/advertiser/fetch", strings.NewReader(body))
	req.SetPathValue("id", "advertiser")
	w := httptest.NewRecorder()
	handleFetchDatasource(bc)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var parsed api.FetchDatasourceOutput
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Rows) != 1 || parsed.Rows[0]["name"] != "Acme" {
		t.Fatalf("HTTP projection mismatch: %+v", parsed.Rows)
	}

	// Cleanup: remove the hook so other tests are unaffected.
	refiner.SetOverlayHook(nil)
}

// Passing nil services to SetDatasourceStack reverts handlers to 501 and
// removes the refiner hook.
func TestBackendClient_SetDatasourceStack_NilRevertsToUnconfigured(t *testing.T) {
	bc := &backendClient{}
	bc.SetDatasourceStack(nil, nil)

	// Refiner hook removed — a schema should NOT gain x-ui-widget=lookup.
	props := map[string]interface{}{
		"advertiser_id": map[string]interface{}{"type": "integer"},
	}
	rs := &mcpschema.ElicitRequestParamsRequestedSchema{Properties: props}
	refiner.Refine(rs)
	if got := props["advertiser_id"].(map[string]interface{})["x-ui-widget"]; got == "lookup" {
		t.Fatalf("hook still installed after nil reset: %v", got)
	}

	// Fetch — should return a clear error.
	_, err := bc.FetchDatasource(context.Background(), &api.FetchDatasourceInput{ID: "x"})
	if err == nil {
		t.Fatalf("want error when stack not configured")
	}
}
