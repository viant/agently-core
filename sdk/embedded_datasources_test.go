package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpschema "github.com/viant/mcp-protocol/schema"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
	"github.com/viant/agently-core/sdk/api"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/service/elicitation/refiner"
	oversvc "github.com/viant/agently-core/service/lookup/overlay"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
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
		ID: "account",
		Backend: &dsproto.Backend{
			Kind: dsproto.BackendMCPTool, Service: "platform", Method: "account_search",
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
			Match: loproto.Match{FieldName: "account_id", Type: "integer"},
			Lookup: loproto.Lookup{
				DataSource:   "account",
				DialogId:     "accountPicker",
				QueryInput:   "q",
				ResolveInput: "id",
				Outputs: []loproto.Parameter{
					{Location: "id", Name: "account_id"},
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
		ID: "account", Inputs: map[string]interface{}{"q": "x"},
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
	// with account_id should gain x-ui-widget=lookup + x-ui-lookup.
	props := map[string]interface{}{
		"account_id": map[string]interface{}{"type": "integer"},
	}
	rs := &mcpschema.ElicitRequestParamsRequestedSchema{Properties: props}
	refiner.Refine(rs)
	after, _ := rs.Properties["account_id"].(map[string]interface{})
	if after["x-ui-widget"] != "lookup" {
		t.Fatalf("overlay hook did not attach x-ui-widget: %+v", after)
	}
	att, _ := after["x-ui-lookup"].(map[string]interface{})
	if att["dataSource"] != "account" {
		t.Fatalf("attachment missing dataSource: %+v", att)
	}
	if att["queryInput"] != "q" || att["resolveInput"] != "id" {
		t.Fatalf("attachment missing query/resolve inputs: %+v", att)
	}

	// 5) HTTP dispatch reaches the same backend (handler picks up interface).
	body := `{"inputs":{"q":"x"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/account/fetch", strings.NewReader(body))
	req.SetPathValue("id", "account")
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
		"account_id": map[string]interface{}{"type": "integer"},
	}
	rs := &mcpschema.ElicitRequestParamsRequestedSchema{Properties: props}
	refiner.Refine(rs)
	if got := props["account_id"].(map[string]interface{})["x-ui-widget"]; got == "lookup" {
		t.Fatalf("hook still installed after nil reset: %v", got)
	}

	// Fetch — should return a clear error.
	_, err := bc.FetchDatasource(context.Background(), &api.FetchDatasourceInput{ID: "x"})
	if err == nil {
		t.Fatalf("want error when stack not configured")
	}
}

func TestBackendClient_LookupRegistryReloadsForgeLookupsFromWorkspaceStore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := fsstore.New(root)

	lookupRepo := filepath.Join(root, "extension/forge/lookups", "order_lookup.yaml")
	if err := osWriteFile(lookupRepo, []byte(`
id: order_lookup
priority: 10
bindings:
  - lookup:
      dataSource: order_lookup
      dialogId: adOrderPicker
      outputs:
        - location: adOrderId
          name: order_id
      display: "${adOrderName}"
    named:
      name: order
      title: Order list
      queryInput: AdOrderName
      resolveInput: AdOrderId
      required: true
      store: "${adOrderId}"
      display: "${adOrderName}"
      modelForm: "${id}"
`)); err != nil {
		t.Fatalf("write initial lookup: %v", err)
	}

	bc := &backendClient{
		store:           store,
		datasourceStore: dssvc.NewMemoryStore(),
		overlayStore:    oversvc.NewMemoryStore(),
	}
	bc.datasourceSvc = dssvc.New(dssvc.Options{Store: bc.datasourceStore, Executor: fakeExecutor{}})
	bc.overlaySvc = oversvc.New(bc.overlayStore)

	reg1, err := bc.ListLookupRegistry(ctx, &api.ListLookupRegistryInput{Context: "conversation:any"})
	if err != nil {
		t.Fatalf("registry 1: %v", err)
	}
	if len(reg1.Entries) != 1 || reg1.Entries[0].Name != "order" {
		t.Fatalf("unexpected initial registry: %+v", reg1.Entries)
	}

	creativeLookup := filepath.Join(root, "extension/forge/lookups", "creative_lookup.yaml")
	if err := osWriteFile(creativeLookup, []byte(`
id: creative_lookup
priority: 10
bindings:
  - lookup:
      dataSource: creative_lookup
      dialogId: creativePicker
      outputs:
        - location: creativeId
          name: creative_id
      display: "${creativeName}"
    named:
      name: creative
      title: Creative list
      queryInput: CreativeName
      resolveInput: CreativeId
      store: "${creativeId}"
      display: "${creativeName}"
      modelForm: "${id}"
`)); err != nil {
		t.Fatalf("write creative lookup: %v", err)
	}

	reg2, err := bc.ListLookupRegistry(ctx, &api.ListLookupRegistryInput{Context: "conversation:any"})
	if err != nil {
		t.Fatalf("registry 2: %v", err)
	}
	if len(reg2.Entries) != 2 {
		t.Fatalf("want 2 entries after reload, got %+v", reg2.Entries)
	}
	var foundCreative bool
	for _, entry := range reg2.Entries {
		if entry.Name == "creative" && entry.DialogId == "creativePicker" && entry.DataSource == "creative_lookup" {
			foundCreative = true
		}
	}
	if !foundCreative {
		t.Fatalf("creative lookup missing after live reload: %+v", reg2.Entries)
	}
}

func TestBackendClient_FetchDatasourceReloadsForgeDatasourcesFromWorkspaceStore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := fsstore.New(root)

	dsPath := filepath.Join(root, "extension/forge/datasources", "creative_lookup.yaml")
	if err := osWriteFile(dsPath, []byte(`
id: creative_lookup
title: Creative Lookup
cardinality: collection
backend:
  kind: inline
  rows:
    - creativeId: 24845598
      creativeName: Test Creative
      advertiserName: Acme
`)); err != nil {
		t.Fatalf("write datasource: %v", err)
	}

	bc := &backendClient{
		store:           store,
		datasourceStore: dssvc.NewMemoryStore(),
		overlayStore:    oversvc.NewMemoryStore(),
	}
	bc.datasourceSvc = dssvc.New(dssvc.Options{Store: bc.datasourceStore, Executor: fakeExecutor{}})
	bc.overlaySvc = oversvc.New(bc.overlayStore)

	out, err := bc.FetchDatasource(ctx, &api.FetchDatasourceInput{ID: "creative_lookup"})
	if err != nil {
		t.Fatalf("fetch datasource: %v", err)
	}
	if len(out.Rows) != 1 || out.Rows[0]["creativeName"] != "Test Creative" {
		t.Fatalf("unexpected datasource rows: %+v", out.Rows)
	}
}

func osWriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
