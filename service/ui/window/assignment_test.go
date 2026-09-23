package window

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/agently-core/workspace"
	forgeTypes "github.com/viant/forge/backend/types"
)

func TestEnrichedWindowMissingDatasourceWarnsWithoutWideningAssignment(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeStewardLikeWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "report.yaml"), `
windowKey: report
resources:
  dataSources: [advertiser_properties]
view:
  content: {id: report, kind: dashboard.reportBuilder}
`)
		cleanup := SetWorkspaceWindowEnricher(func(_ context.Context, window *forgeTypes.Window) error {
			window.View.Content.Dashboard = &forgeTypes.Dashboard{ReportBuilder: map[string]interface{}{
				"sources": []interface{}{map[string]interface{}{"dataSourceRef": "order_properties"}},
			}}
			return nil
		})
		defer cleanup()
		var output bytes.Buffer
		previous := log.Writer()
		log.SetOutput(&output)
		defer log.SetOutput(previous)
		got, err := LoadWorkspaceWindow(context.Background(), "report", nil)
		if err != nil {
			t.Fatalf("missing enriched datasource must not halt window: %v", err)
		}
		if !strings.Contains(output.String(), `references datasource "order_properties" that is not attached`) {
			t.Fatalf("enriched references were not checked: %s", output.String())
		}
		if _, leaked := got.DataSource["order_properties"]; leaked {
			t.Fatal("validation must not attach unassigned datasources")
		}
	})
}

// writeStewardLikeWorkspace seeds a workspace shaped like the Steward
// deployment: advertiser/campaign/order assets that must never leak into an
// unrelated window.
func writeStewardLikeWorkspace(t *testing.T, root string) {
	t.Helper()
	ds := func(domain, id, extra string) {
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDataSource, domain, id+".yaml"), "id: "+id+"\ncardinality: collection\nbackend: {kind: inline, rows: []}\n"+extra)
	}
	ds("advertiser", "advertiser_properties", "")
	ds("advertiser", "advertiser_campaigns", "")
	ds("advertiser", "advertiser_user_pools", "")
	ds("campaign", "draft_campaign_create", "resourceModelRef: campaignDraft\n")
	ds("campaign", "campaign_create_patch", "")
	ds("campaign", "campaign_flight_draft", "")
	ds("order", "order_lookup", "")
	ds("order", "order_properties", "")
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeModel, "campaign.yaml"), `
schemas:
  campaignDraft:
    type: object
    properties:
      name: {type: string}
      flights: {type: array, items: {$ref: campaignFlight}}
  campaignFlight:
    type: object
    properties: {startDate: {type: string}}
resourceModels:
  campaignDraft:
    schemaRef: campaignDraft
    read: {dataSourceRef: draft_campaign_create}
    write: {dataSourceRef: campaign_create_patch, inputPath: Data, mode: full}
    fields: {name: {write: Name}}
`)
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeModel, "order.yaml"), `
schemas:
  orderDraft: {type: object, properties: {name: {type: string}}}
resourceModels:
  orderDraft: {schemaRef: orderDraft, read: {dataSourceRef: order_properties}}
`)
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "advertiserCampaignCreate.yaml"), `
id: advertiserCampaignCreate
title: Create Campaign
dataSourceRef: draft_campaign_create
content:
  id: campaignCreateForm
  dataSourceRef: draft_campaign_create
  items:
    - id: name
      type: text
      on:
        - event: onClick
          handler: window.openDialog
          args: [campaignFlightDraft]
`)
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "campaignFlightDraft.yaml"), `
id: campaignFlightDraft
title: Flight
dataSourceRef: campaign_flight_draft
content: {id: flightForm, dataSourceRef: campaign_flight_draft}
`)
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "orderDraft.yaml"), `
id: orderDraft
title: Order
dataSourceRef: order_properties
content: {id: orderForm, dataSourceRef: order_properties}
`)
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "adOrderPicker.yaml"), `
id: adOrderPicker
title: Select Ad Order
dataSourceRef: order_lookup
content: {id: adOrderTable, dataSourceRef: order_lookup}
`)
}

func stewardAssetNames() (dataSources, dialogs, models []string) {
	return []string{"advertiser_properties", "advertiser_campaigns", "advertiser_user_pools", "draft_campaign_create", "campaign_create_patch", "campaign_flight_draft", "order_lookup", "order_properties"},
		[]string{"advertiserCampaignCreate", "campaignFlightDraft", "orderDraft", "adOrderPicker"},
		[]string{"campaignDraft", "orderDraft"}
}

func TestMergeWorkspaceForgeAssetsKeepsAgentWindowFreeOfStewardAssets(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeStewardLikeWorkspace(t, root)
		// The Agent window is an application built-in: it owns its datasources,
		// declares no workspace resources, and must not receive any of the
		// Steward assets the workspace happens to contain.
		agent := &forgeTypes.Window{
			WindowKey: "agent",
			DataSource: map[string]forgeTypes.DataSource{
				"agents":     {Cardinality: "collection"},
				"agentTools": {Cardinality: "collection"},
			},
			View: forgeTypes.View{Content: &forgeTypes.Container{ID: "agent", Binding: forgeTypes.Binding{DataSourceRef: "agents"}}},
		}
		agent.SetCode([]byte("({ open: (context) => context?.handlers?.window?.openDialog?.({execution: {args: ['agentLov', {awaitResult: false}]}}) })"))

		if err := MergeWorkspaceForgeAssets(context.Background(), agent, nil); err != nil {
			t.Fatalf("merge built-in agent window: %v", err)
		}
		if len(agent.DataSource) != 2 {
			t.Fatalf("agent window must keep only its own datasources, got %v", sortedDataSourceKeys(agent))
		}
		dataSources, dialogs, models := stewardAssetNames()
		for _, id := range dataSources {
			if _, leaked := agent.DataSource[id]; leaked {
				t.Fatalf("Steward datasource %s leaked into the agent window", id)
			}
		}
		if len(agent.Dialogs) != 0 {
			t.Fatalf("Steward dialogs leaked into the agent window: %v", dialogIDs(agent))
		}
		for _, name := range models {
			if _, leaked := agent.ResourceModels[name]; leaked {
				t.Fatalf("Steward resource model %s leaked into the agent window", name)
			}
			if _, leaked := agent.Schemas[name]; leaked {
				t.Fatalf("Steward schema %s leaked into the agent window", name)
			}
		}
		_ = dialogs
	})
}

func TestLoadWorkspaceWindowAttachesAssignedAssetsAndTransitiveDependencies(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeStewardLikeWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "advertiser.yaml"), `
windowKey: advertiser
namespace: Advertiser
resources:
  dataSources: [advertiser_properties]
  dialogs: [advertiserCampaignCreate]
view:
  content:
    id: advertiser
    dataSourceRef: advertiser_properties
`)
		got, err := LoadWorkspaceWindow(context.Background(), "advertiser", nil)
		if err != nil {
			t.Fatalf("load advertiser window: %v", err)
		}
		// Assigned directly.
		for _, id := range []string{"advertiser_properties"} {
			if _, ok := got.DataSource[id]; !ok {
				t.Fatalf("assigned datasource %s missing: %v", id, sortedDataSourceKeys(got))
			}
		}
		// Transitive: dialog -> its datasource -> resource model -> schemas ($ref) -> write datasource,
		// and dialog -> nested window.openDialog args -> dialog -> its datasource.
		for _, id := range []string{"draft_campaign_create", "campaign_create_patch", "campaign_flight_draft"} {
			if _, ok := got.DataSource[id]; !ok {
				t.Fatalf("transitive datasource %s missing: %v", id, sortedDataSourceKeys(got))
			}
		}
		if !containsDialog(got, "advertiserCampaignCreate") || !containsDialog(got, "campaignFlightDraft") {
			t.Fatalf("assigned and nested dialogs must be attached, got %v", dialogIDs(got))
		}
		if _, ok := got.ResourceModels["campaignDraft"]; !ok {
			t.Fatalf("transitive resource model missing: %v", got.ResourceModels)
		}
		for _, name := range []string{"campaignDraft", "campaignFlight"} {
			if _, ok := got.Schemas[name]; !ok {
				t.Fatalf("transitive schema %s missing: %v", name, got.Schemas)
			}
		}
		// Not assigned and not reachable: order assets stay out.
		for _, id := range []string{"advertiser_campaigns", "advertiser_user_pools", "order_lookup", "order_properties"} {
			if _, leaked := got.DataSource[id]; leaked {
				t.Fatalf("unassigned datasource %s leaked into advertiser window", id)
			}
		}
		if containsDialog(got, "orderDraft") || containsDialog(got, "adOrderPicker") {
			t.Fatalf("unassigned dialogs leaked: %v", dialogIDs(got))
		}
		if _, leaked := got.ResourceModels["orderDraft"]; leaked {
			t.Fatal("unassigned order model leaked into advertiser window")
		}
	})
}

func TestLoadWorkspaceWindowFailsWhenYAMLReferencesUnassignedWorkspaceAssets(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeStewardLikeWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "advertiser.yaml"), `
windowKey: advertiser
resources:
  dataSources: [advertiser_properties]
view:
  content:
    id: advertiser
    dataSourceRef: advertiser_properties
    containers:
      - id: campaigns
        dataSourceRef: advertiser_campaigns
        table:
          columns:
            - id: name
              link: {kind: dialog, dialogId: advertiserCampaignCreate}
      - id: picker
        items:
          - id: adOrder
            lookup: {dialogId: adOrderPicker, dataSource: order_lookup}
`)
		_, err := LoadWorkspaceWindow(context.Background(), "advertiser", nil)
		if err == nil {
			t.Fatal("expected assignment validation error")
		}
		for _, want := range []string{"resources.dialogs is missing: adOrderPicker, advertiserCampaignCreate"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not mention %q", err.Error(), want)
			}
		}
	})
}

func TestLoadWorkspaceWindowValidatesActionCodeDialogAndDataSourceLiterals(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeStewardLikeWorkspace(t, root)
		code := `({
  openFlight: ({context}) => context?.handlers?.window?.openDialog?.({
    context,
    execution: {args: ['campaignFlightDraft', {awaitResult: false}]},
  }),
  closeArchive: ({context}) => context?.handlers?.window?.closeDialog?.({dialogId: "orderDraft", context}),
  refresh: ({context}) => context?.Context?.('advertiser_user_pools')?.handlers?.dataSource?.fetchCollection?.(),
  // openDialog?.({execution: {args: ['adOrderPicker']}}) commented out code is ignored
  pick: ({context}) => {
    const dialogByKind = {create: 'advertiserCampaignCreate'};
    const dialogId = dialogByKind[context?.kind];
    return context?.handlers?.window?.openDialog?.({dialogId, context});
  },
})`
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "advertiser.js"), code)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "advertiser.yaml"), `
windowKey: advertiser
resources:
  dataSources: [advertiser_properties]
view:
  content: {id: advertiser, dataSourceRef: advertiser_properties}
`)
		_, err := LoadWorkspaceWindow(context.Background(), "advertiser", nil)
		if err == nil {
			t.Fatal("expected action code references to require assignment")
		}
		if !strings.Contains(err.Error(), "resources.dialogs is missing: advertiserCampaignCreate, campaignFlightDraft, orderDraft") {
			t.Fatalf("unexpected dialog validation error: %v", err)
		}
		if strings.Contains(err.Error(), "resources.dataSources") {
			t.Fatalf("missing datasource must be a warning, not an error: %v", err)
		}
		if strings.Contains(err.Error(), "adOrderPicker") {
			t.Fatalf("commented out code must not create references: %v", err)
		}

		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "advertiser.yaml"), `
windowKey: advertiser
resources:
  dataSources: [advertiser_properties, advertiser_user_pools]
  dialogs: [advertiserCampaignCreate, campaignFlightDraft, orderDraft]
view:
  content: {id: advertiser, dataSourceRef: advertiser_properties}
`)
		got, err := LoadWorkspaceWindow(context.Background(), "advertiser", nil)
		if err != nil {
			t.Fatalf("load window with complete assignment: %v", err)
		}
		if !containsDialog(got, "orderDraft") || containsDialog(got, "adOrderPicker") {
			t.Fatalf("unexpected dialogs: %v", dialogIDs(got))
		}
	})
}

func TestLoadWorkspaceWindowPreservesUnresolvedAssignmentsWithoutBlocking(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeStewardLikeWorkspace(t, root)
		// Assigned ids the workspace does not (yet) provide are declared intent:
		// they are logged as unresolved and never fail the window.
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "advertiser.yaml"), `
windowKey: advertiser
namespace: Advertiser
resources:
  dataSources: [advertiser_properties, advertiser_custom_segment_patch]
  dialogs: [noSuchDialog]
  models: [noSuchRegistry]
  resourceModels: [noSuchModel]
  schemas: [noSuchSchema]
view:
  content: {id: advertiser, dataSourceRef: advertiser_properties}
`)
		got, err := LoadWorkspaceWindow(context.Background(), "advertiser", nil)
		if err != nil {
			t.Fatalf("unresolved assignments must not block the window: %v", err)
		}
		if _, ok := got.DataSource["advertiser_properties"]; !ok || len(got.DataSource) != 1 {
			t.Fatalf("unexpected datasources: %v", sortedDataSourceKeys(got))
		}
		if len(got.Dialogs) != 0 || len(got.Schemas) != 0 || len(got.ResourceModels) != 0 {
			t.Fatalf("unresolved assignments must attach nothing: %v %v %v", dialogIDs(got), got.Schemas, got.ResourceModels)
		}
		// Resource diagnostics stay in backend logs and cannot change executable UI code.
		if got.Actions != nil {
			t.Fatalf("resource diagnostics must not create window actions, got %q", codeOf(got))
		}
	})
}

func TestValidateWindowReferencesIgnoresPlaceholderAndPreservesActionCode(t *testing.T) {
	window := &forgeTypes.Window{
		WindowKey: "chat/new",
		Namespace: "Chat",
		View: forgeTypes.View{Content: &forgeTypes.Container{
			ID: "chat", Binding: forgeTypes.Binding{DataSourceRef: "_"},
		}},
	}
	const originalCode = "(() => ({open: () => true}))();"
	window.SetCode([]byte(originalCode))
	notes, err := validateWindowReferences(window, &workspaceCatalog{})
	if err != nil || len(notes) != 0 {
		t.Fatalf("placeholder must not require a workspace resource: notes=%v err=%v", notes, err)
	}
	if got := codeOf(window); got != originalCode {
		t.Fatalf("backend reference checks must preserve action code, got %q", got)
	}
	window.View.Content.Binding.DataSourceRef = "unavailable_source"
	notes, err = validateWindowReferences(window, &workspaceCatalog{})
	if err != nil || len(notes) != 1 {
		t.Fatalf("unknown resources should warn without rejecting the window: notes=%v err=%v", notes, err)
	}
	if got := codeOf(window); got != originalCode {
		t.Fatalf("warning must not change action code, got %q", got)
	}
	catalog := newWorkspaceCatalog()
	catalog.dataSources["unavailable_source"] = forgeTypes.DataSource{}
	notes, err = validateWindowReferences(window, catalog)
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "not attached") {
		t.Fatalf("known but unattached datasource must warn without failing: notes=%v err=%v", notes, err)
	}
}

func codeOf(window *forgeTypes.Window) string {
	if window == nil || window.Actions == nil {
		return ""
	}
	return window.Actions.Code
}

func TestLoadWorkspaceWindowAssignsModelRegistryByName(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeStewardLikeWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "campaign.yaml"), `
windowKey: campaign
resources:
  models: [campaign]
view:
  content: {id: campaign}
`)
		got, err := LoadWorkspaceWindow(context.Background(), "campaign", nil)
		if err != nil {
			t.Fatalf("load campaign window: %v", err)
		}
		if _, ok := got.ResourceModels["campaignDraft"]; !ok {
			t.Fatalf("registry models missing: %v", got.ResourceModels)
		}
		if _, ok := got.Schemas["campaignFlight"]; !ok {
			t.Fatalf("registry schemas missing: %v", got.Schemas)
		}
		// Model read/write datasources are transitive dependencies of the registry.
		for _, id := range []string{"draft_campaign_create", "campaign_create_patch"} {
			if _, ok := got.DataSource[id]; !ok {
				t.Fatalf("model datasource %s missing: %v", id, sortedDataSourceKeys(got))
			}
		}
		if _, leaked := got.ResourceModels["orderDraft"]; leaked {
			t.Fatal("unassigned order registry leaked")
		}
	})
}

func TestLoadWorkspaceWindowToleratesReferencesOutsideWorkspace(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeStewardLikeWorkspace(t, root)
		// Host-provided datasources and dialogs are unknown to the workspace and
		// must not be reported as missing assignments.
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "chat.yaml"), `
windowKey: chat
dataSource:
  messages: {cardinality: collection}
view:
  content:
    id: chat
    dataSourceRef: messages
    items:
      - id: pick
        lookup: {dialogId: hostProvidedPicker, dataSource: hostProvidedLookup}
`)
		got, err := LoadWorkspaceWindow(context.Background(), "chat", nil)
		if err != nil {
			t.Fatalf("unknown host references must not fail: %v", err)
		}
		if len(got.DataSource) != 1 || len(got.Dialogs) != 0 {
			t.Fatalf("workspace assets leaked: %v %v", sortedDataSourceKeys(got), dialogIDs(got))
		}
	})
}

func TestLoadWorkspaceWindowLocalDeclarationsWinOverWorkspaceAssets(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeStewardLikeWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "campaign.yaml"), `
windowKey: campaign
resources:
  dataSources: [draft_campaign_create]
dataSource:
  draft_campaign_create: {cardinality: object}
schemas:
  campaignDraft: {type: object, properties: {name: {type: string}}}
resourceModels:
  campaignDraft: {schemaRef: campaignDraft}
view:
  content: {id: campaign, dataSourceRef: draft_campaign_create}
`)
		got, err := LoadWorkspaceWindow(context.Background(), "campaign", nil)
		if err != nil {
			t.Fatalf("load campaign window: %v", err)
		}
		if got.DataSource["draft_campaign_create"].Cardinality != "object" {
			t.Fatal("window-owned datasource was overwritten by the workspace asset")
		}
		if _, ok := got.Schemas["campaignDraft"].Properties["flights"]; ok {
			t.Fatal("window-owned schema was overwritten by the workspace registry")
		}
	})
}

func TestCollectCodeReferences(t *testing.T) {
	code := "// openDialog?.({execution: {args: ['commentedOut']}})\n" +
		"/* dialogId: 'blockComment' */\n" +
		"const a = 'single'; const b = \"double\"; const c = `plain`;\n" +
		"const d = `dialog-${kind}`; const e = `${start}T00:00:00Z`;\n" +
		"openDialog?.({execution: {args: ['viaArgs', {awaitResult: false}]}});\n" +
		"closeDialog?.({dialogId: 'viaKey'});\n" +
		"const dialogId = byKind[kind];\n" +
		"openDialog?.({dialogId, context});\n" +
		"openDialog?.({execution: {args: [computedId]}});\n" +
		"const cleaned = String(value).replace(/\"/g, '').replace(/['\"]/g, ''); const ratio = total / count / 2;\n" +
		"const afterRegex = 'survivesRegex';\n"
	refs := collectCodeReferences(code)
	for _, want := range []string{"single", "double", "plain", "viaArgs", "viaKey", "survivesRegex"} {
		if !refs.Literals[want] {
			t.Fatalf("literal %q not collected: %v", want, refs.Literals)
		}
	}
	for _, unwanted := range []string{"commentedOut", "blockComment", "true"} {
		if refs.Literals[unwanted] {
			t.Fatalf("literal %q must be ignored", unwanted)
		}
	}
	joined := strings.Join(refs.DynamicDialogs, "\n")
	for _, want := range []string{"dialog-${kind}", "dialogId = byKind[kind]", "{dialogId,", "args: [computedId"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("dynamic dialog usage %q not reported in %q", want, joined)
		}
	}
	if strings.Contains(joined, "T00:00:00Z") {
		t.Fatalf("non-dialog template literal must not be reported: %q", joined)
	}
}

func TestCollectWindowReferencesCoversForgeVocabulary(t *testing.T) {
	window := &forgeTypes.Window{
		Authorization: &forgeTypes.AuthorizationSpec{DataSourceRef: "resource_authorization"},
		Window:        &forgeTypes.WindowSettings{TitleBinding: &forgeTypes.WindowTitleBinding{DataSourceRef: "title_source"}},
		View: forgeTypes.View{Content: &forgeTypes.Container{
			ID:      "root",
			Binding: forgeTypes.Binding{DataSourceRef: "view_source"},
			Items: []forgeTypes.Item{
				{ID: "pick", Lookup: &forgeTypes.Lookup{DialogId: "pickerDialog", DataSource: "lookup_source"}},
				{ID: "open", On: []*forgeTypes.Execute{{Event: "onClick", Handler: "window.openDialog", Arguments: []interface{}{"openedDialog", map[string]interface{}{"awaitResult": false}}}}},
			},
			Table: &forgeTypes.Table{Columns: []forgeTypes.Column{{ID: "name", Link: &forgeTypes.TableLink{Kind: "dialog", DialogId: "linkedDialog"}}}},
		}},
	}
	refs := CollectWindowReferences(window)
	for _, want := range []string{"resource_authorization", "title_source", "view_source", "lookup_source"} {
		if !refs.YAML.DataSources[want] {
			t.Fatalf("datasource reference %q missing: %v", want, refs.YAML.DataSources)
		}
	}
	for _, want := range []string{"pickerDialog", "openedDialog", "linkedDialog"} {
		if !refs.YAML.Dialogs[want] {
			t.Fatalf("dialog reference %q missing: %v", want, refs.YAML.Dialogs)
		}
	}
}

func TestAssignedDialogIncludesOptionsDatasource(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeStewardLikeWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "categoryPicker.yaml"), `
id: categoryPicker
title: Category
content:
  id: categoryForm
  items:
    - {id: category, type: select, optionsDataSourceRef: advertiser_user_pools}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "details.yaml"), `
windowKey: details
resources:
  dataSources: [advertiser_properties]
  dialogs: [categoryPicker]
view:
  content: {id: details, dataSourceRef: advertiser_properties}
`)
		got, err := LoadWorkspaceWindow(context.Background(), "details", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.DataSource["advertiser_user_pools"]; !ok {
			t.Fatal("assigned dialog lost its options datasource")
		}
		if _, leaked := got.DataSource["order_properties"]; leaked {
			t.Fatal("unrelated datasource leaked into window")
		}
	})
}

func containsDialog(window *forgeTypes.Window, id string) bool {
	for _, dialog := range window.Dialogs {
		if dialog.Id == id {
			return true
		}
	}
	return false
}

func dialogIDs(window *forgeTypes.Window) []string {
	result := make([]string, 0, len(window.Dialogs))
	for _, dialog := range window.Dialogs {
		result = append(result, dialog.Id)
	}
	return result
}

func sortedDataSourceKeys(window *forgeTypes.Window) []string {
	keys := map[string]bool{}
	for id := range window.DataSource {
		keys[id] = true
	}
	return sortedKeys(keys)
}
