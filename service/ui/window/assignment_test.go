package window

import (
	"bytes"
	"context"
	"encoding/json"
	lookupProto "github.com/viant/agently-core/protocol/lookup/overlay"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/agently-core/workspace"
	forgeTypes "github.com/viant/forge/backend/types"
)

func TestEnrichedWindowMissingDatasourceWarnsWithoutWideningAssignment(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeCollectionWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "report.yaml"), `
windowKey: report
resources:
  dataSources: [collection_details]
view:
  content: {id: report, kind: dashboard.reportBuilder}
`)
		cleanup := SetWorkspaceWindowEnricher(func(_ context.Context, window *forgeTypes.Window) error {
			window.View.Content.Dashboard = &forgeTypes.Dashboard{ReportBuilder: map[string]interface{}{
				"sources": []interface{}{map[string]interface{}{"dataSourceRef": "revision_details"}},
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
		if !strings.Contains(output.String(), `references datasource "revision_details" that is not attached`) {
			t.Fatalf("enriched references were not checked: %s", output.String())
		}
		if _, leaked := got.DataSource["revision_details"]; leaked {
			t.Fatal("validation must not attach unassigned datasources")
		}
	})
}

// writeCollectionWorkspace seeds local datasource, dialog and model dependencies.
func writeCollectionWorkspace(t *testing.T, root string) {
	t.Helper()
	ds := func(domain, id, extra string) {
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDataSource, domain, id+".yaml"), "id: "+id+"\ncardinality: collection\nbackend: {kind: inline, rows: []}\n"+extra)
	}
	ds("collection", "collection_details", "")
	ds("collection", "collection_entries", "")
	ds("collection", "collection_members", "")
	ds("entry", "entry_draft_read", "resourceModelRef: entryDraft\n")
	ds("entry", "entry_draft_write", "")
	ds("entry", "entry_stage_read", "")
	ds("order", "revision_lookup", "")
	ds("order", "revision_details", "")
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeModel, "entry.yaml"), `
schemas:
  entryDraft:
    type: object
    properties:
      name: {type: string}
      stages: {type: array, items: {$ref: entryStage}}
  entryStage:
    type: object
    properties: {startDate: {type: string}}
resourceModels:
  entryDraft:
    schemaRef: entryDraft
    read: {dataSourceRef: entry_draft_read}
    write: {dataSourceRef: entry_draft_write, inputPath: Data, mode: full}
    fields: {name: {write: Name}}
`)
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeModel, "revision.yaml"), `
schemas:
  revisionDraft: {type: object, properties: {name: {type: string}}}
resourceModels:
  revisionDraft: {schemaRef: revisionDraft, read: {dataSourceRef: revision_details}}
`)
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "collectionEntryForm.yaml"), `
id: collectionEntryForm
title: Create Entry
dataSourceRef: entry_draft_read
content:
  id: entryForm
  dataSourceRef: entry_draft_read
  items:
    - id: name
      type: text
      on:
        - event: onClick
          handler: window.openDialog
          args: [entryStageEditor]
`)
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "entryStageEditor.yaml"), `
id: entryStageEditor
title: Flight
dataSourceRef: entry_stage_read
content: {id: flightForm, dataSourceRef: entry_stage_read}
`)
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "revisionDraft.yaml"), `
id: revisionDraft
title: Order
dataSourceRef: revision_details
content: {id: orderForm, dataSourceRef: revision_details}
`)
	mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "chooseRevision.yaml"), `
id: chooseRevision
title: Select Revision
dataSourceRef: revision_lookup
content: {id: revisionTable, dataSourceRef: revision_lookup}
`)
}

func collectionAssetNames() (dataSources, dialogs, models []string) {
	return []string{"collection_details", "collection_entries", "collection_members", "entry_draft_read", "entry_draft_write", "entry_stage_read", "revision_lookup", "revision_details"},
		[]string{"collectionEntryForm", "entryStageEditor", "revisionDraft", "chooseRevision"},
		[]string{"entryDraft", "revisionDraft"}
}

func TestMergeWorkspaceForgeAssetsKeepsAgentWindowFreeOfUnassignedAssets(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeCollectionWorkspace(t, root)
		// The Agent window is an application built-in: it owns its datasources,
		// declares no workspace resources, and must not receive any of the
		// workspace assets the workspace happens to contain.
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
		dataSources, dialogs, models := collectionAssetNames()
		for _, id := range dataSources {
			if _, leaked := agent.DataSource[id]; leaked {
				t.Fatalf("collection workspace datasource %s leaked into the agent window", id)
			}
		}
		if len(agent.Dialogs) != 0 {
			t.Fatalf("collection workspace dialogs leaked into the agent window: %v", dialogIDs(agent))
		}
		for _, name := range models {
			if _, leaked := agent.ResourceModels[name]; leaked {
				t.Fatalf("collection workspace resource model %s leaked into the agent window", name)
			}
			if _, leaked := agent.Schemas[name]; leaked {
				t.Fatalf("collection workspace schema %s leaked into the agent window", name)
			}
		}
		_ = dialogs
	})
}

func TestLoadWorkspaceWindowAttachesAssignedAssetsAndTransitiveDependencies(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeCollectionWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "collection.yaml"), `
windowKey: collection
namespace: Collection
resources:
  dataSources: [collection_details]
  dialogs: [collectionEntryForm]
view:
  content:
    id: collection
    dataSourceRef: collection_details
`)
		got, err := LoadWorkspaceWindow(context.Background(), "collection", nil)
		if err != nil {
			t.Fatalf("load collection window: %v", err)
		}
		// Assigned directly.
		for _, id := range []string{"collection_details"} {
			if _, ok := got.DataSource[id]; !ok {
				t.Fatalf("assigned datasource %s missing: %v", id, sortedDataSourceKeys(got))
			}
		}
		// Transitive: dialog -> its datasource -> resource model -> schemas ($ref) -> write datasource,
		// and dialog -> nested window.openDialog args -> dialog -> its datasource.
		for _, id := range []string{"entry_draft_read", "entry_draft_write", "entry_stage_read"} {
			if _, ok := got.DataSource[id]; !ok {
				t.Fatalf("transitive datasource %s missing: %v", id, sortedDataSourceKeys(got))
			}
		}
		if !containsDialog(got, "collectionEntryForm") || !containsDialog(got, "entryStageEditor") {
			t.Fatalf("assigned and nested dialogs must be attached, got %v", dialogIDs(got))
		}
		if _, ok := got.ResourceModels["entryDraft"]; !ok {
			t.Fatalf("transitive resource model missing: %v", got.ResourceModels)
		}
		for _, name := range []string{"entryDraft", "entryStage"} {
			if _, ok := got.Schemas[name]; !ok {
				t.Fatalf("transitive schema %s missing: %v", name, got.Schemas)
			}
		}
		// Not assigned and not reachable: order assets stay out.
		for _, id := range []string{"collection_entries", "collection_members", "revision_lookup", "revision_details"} {
			if _, leaked := got.DataSource[id]; leaked {
				t.Fatalf("unassigned datasource %s leaked into collection window", id)
			}
		}
		if containsDialog(got, "revisionDraft") || containsDialog(got, "chooseRevision") {
			t.Fatalf("unassigned dialogs leaked: %v", dialogIDs(got))
		}
		if _, leaked := got.ResourceModels["revisionDraft"]; leaked {
			t.Fatal("unassigned order model leaked into collection window")
		}
	})
}

func TestLoadWorkspaceWindowFailsWhenYAMLReferencesUnassignedWorkspaceAssets(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeCollectionWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "collection.yaml"), `
windowKey: collection
resources:
  dataSources: [collection_details]
view:
  content:
    id: collection
    dataSourceRef: collection_details
    containers:
      - id: entries
        dataSourceRef: collection_entries
        table:
          columns:
            - id: name
              link: {kind: dialog, dialogId: collectionEntryForm}
      - id: picker
        items:
          - id: revision
            lookup: {dialogId: chooseRevision, dataSource: revision_lookup}
`)
		_, err := LoadWorkspaceWindow(context.Background(), "collection", nil)
		if err == nil {
			t.Fatal("expected assignment validation error")
		}
		for _, want := range []string{"resources.dialogs is missing: chooseRevision, collectionEntryForm"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not mention %q", err.Error(), want)
			}
		}
	})
}

func TestLoadWorkspaceWindowValidatesActionCodeDialogAndDataSourceLiterals(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeCollectionWorkspace(t, root)
		code := `({
  openFlight: ({context}) => context?.handlers?.window?.openDialog?.({
    context,
    execution: {args: ['entryStageEditor', {awaitResult: false}]},
  }),
  closeArchive: ({context}) => context?.handlers?.window?.closeDialog?.({dialogId: "revisionDraft", context}),
  refresh: ({context}) => context?.Context?.('collection_members')?.handlers?.dataSource?.fetchCollection?.(),
  // openDialog?.({execution: {args: ['chooseRevision']}}) commented out code is ignored
  pick: ({context}) => {
    const dialogByKind = {create: 'collectionEntryForm'};
    const dialogId = dialogByKind[context?.kind];
    return context?.handlers?.window?.openDialog?.({dialogId, context});
  },
})`
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "collection.js"), code)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "collection.yaml"), `
windowKey: collection
resources:
  dataSources: [collection_details]
view:
  content: {id: collection, dataSourceRef: collection_details}
`)
		_, err := LoadWorkspaceWindow(context.Background(), "collection", nil)
		if err == nil {
			t.Fatal("expected action code references to require assignment")
		}
		if !strings.Contains(err.Error(), "resources.dialogs is missing: collectionEntryForm, entryStageEditor, revisionDraft") {
			t.Fatalf("unexpected dialog validation error: %v", err)
		}
		if strings.Contains(err.Error(), "resources.dataSources") {
			t.Fatalf("missing datasource must be a warning, not an error: %v", err)
		}
		if strings.Contains(err.Error(), "chooseRevision") {
			t.Fatalf("commented out code must not create references: %v", err)
		}

		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "collection.yaml"), `
windowKey: collection
resources:
  dataSources: [collection_details, collection_members]
  dialogs: [collectionEntryForm, entryStageEditor, revisionDraft]
view:
  content: {id: collection, dataSourceRef: collection_details}
`)
		got, err := LoadWorkspaceWindow(context.Background(), "collection", nil)
		if err != nil {
			t.Fatalf("load window with complete assignment: %v", err)
		}
		if !containsDialog(got, "revisionDraft") || containsDialog(got, "chooseRevision") {
			t.Fatalf("unexpected dialogs: %v", dialogIDs(got))
		}
	})
}

func TestLoadWorkspaceWindowPreservesUnresolvedAssignmentsWithoutBlocking(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeCollectionWorkspace(t, root)
		// Assigned ids the workspace does not (yet) provide are declared intent:
		// they are logged as unresolved and never fail the window.
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "collection.yaml"), `
windowKey: collection
namespace: Collection
resources:
  dataSources: [collection_details, collection_label_write]
  dialogs: [noSuchDialog]
  models: [noSuchRegistry]
  resourceModels: [noSuchModel]
  schemas: [noSuchSchema]
view:
  content: {id: collection, dataSourceRef: collection_details}
`)
		got, err := LoadWorkspaceWindow(context.Background(), "collection", nil)
		if err != nil {
			t.Fatalf("unresolved assignments must not block the window: %v", err)
		}
		if _, ok := got.DataSource["collection_details"]; !ok || len(got.DataSource) != 1 {
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
		writeCollectionWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "entry.yaml"), `
windowKey: entry
resources:
  models: [entry]
view:
  content: {id: entry}
`)
		got, err := LoadWorkspaceWindow(context.Background(), "entry", nil)
		if err != nil {
			t.Fatalf("load entry window: %v", err)
		}
		if _, ok := got.ResourceModels["entryDraft"]; !ok {
			t.Fatalf("registry models missing: %v", got.ResourceModels)
		}
		if _, ok := got.Schemas["entryStage"]; !ok {
			t.Fatalf("registry schemas missing: %v", got.Schemas)
		}
		// Model read/write datasources are transitive dependencies of the registry.
		for _, id := range []string{"entry_draft_read", "entry_draft_write"} {
			if _, ok := got.DataSource[id]; !ok {
				t.Fatalf("model datasource %s missing: %v", id, sortedDataSourceKeys(got))
			}
		}
		if _, leaked := got.ResourceModels["revisionDraft"]; leaked {
			t.Fatal("unassigned order registry leaked")
		}
	})
}

func TestLoadWorkspaceWindowToleratesReferencesOutsideWorkspace(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeCollectionWorkspace(t, root)
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
		writeCollectionWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "entry.yaml"), `
windowKey: entry
resources:
  dataSources: [entry_draft_read]
dataSource:
  entry_draft_read: {cardinality: object}
schemas:
  entryDraft: {type: object, properties: {name: {type: string}}}
resourceModels:
  entryDraft: {schemaRef: entryDraft}
view:
  content: {id: entry, dataSourceRef: entry_draft_read}
`)
		got, err := LoadWorkspaceWindow(context.Background(), "entry", nil)
		if err != nil {
			t.Fatalf("load entry window: %v", err)
		}
		if got.DataSource["entry_draft_read"].Cardinality != "object" {
			t.Fatal("window-owned datasource was overwritten by the workspace asset")
		}
		if _, ok := got.Schemas["entryDraft"].Properties["stages"]; ok {
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
		writeCollectionWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, "categoryPicker.yaml"), `
id: categoryPicker
title: Category
content:
  id: categoryForm
  items:
    - {id: category, type: select, optionsDataSourceRef: collection_members}
`)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "details.yaml"), `
windowKey: details
resources:
  dataSources: [collection_details]
  dialogs: [categoryPicker]
view:
  content: {id: details, dataSourceRef: collection_details}
`)
		got, err := LoadWorkspaceWindow(context.Background(), "details", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.DataSource["collection_members"]; !ok {
			t.Fatal("assigned dialog lost its options datasource")
		}
		if _, leaked := got.DataSource["revision_details"]; leaked {
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

func TestChatAttachesNamedLookupDialogClosure(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		writeCollectionWorkspace(t, root)
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeLookup, "revision.yaml"), `
id: revision_lookup
bindings:
  - named: {name: revision}
    lookup: {dataSource: revision_lookup, dialogId: chooseRevision}
`)
		chat := &forgeTypes.Window{WindowKey: "chat/new", View: forgeTypes.View{Content: &forgeTypes.Container{Chat: &forgeTypes.Chat{}}}}
		if err := MergeWorkspaceForgeAssets(context.Background(), chat, nil); err != nil {
			t.Fatal(err)
		}
		if len(chat.LookupOverlays) != 1 {
			t.Fatalf("original named overlay missing from compiled owning chat: count=%d", len(chat.LookupOverlays))
		}
		var original lookupProto.Overlay
		if err := json.Unmarshal(chat.LookupOverlays[0], &original); err != nil {
			t.Fatal(err)
		}
		if original.ID != "revision_lookup" || len(original.Bindings) != 1 || original.Bindings[0].Named.Name != "revision" {
			t.Fatal("named lookup declaration changed")
		}
		if _, ok := chat.DataSource["revision_lookup"]; !ok {
			t.Fatal("named lookup datasource missing")
		}
		if len(chat.Dialogs) != 1 || chat.Dialogs[0].Id != "chooseRevision" {
			t.Fatalf("picker missing: %v", dialogIDs(chat))
		}
		if _, leaked := chat.DataSource["revision_details"]; leaked {
			t.Fatal("unrelated order window leaked into chat")
		}
	})
}
