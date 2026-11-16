// Package catalogfixture creates an isolated document library for resource
// provider tests. Its windows, fields, dialogs and reader are authored here.
package catalogfixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func New(t testing.TB) string    { t.Helper(); return writeWorkspace(t, false) }
func Lookup(t testing.TB) string { t.Helper(); return writeWorkspace(t, true) }

func writeWorkspace(t testing.TB, lookupOnly bool) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"intents/review_documents.yaml": "id: review_documents\ntoolBundles: [library]\nmessages: [{role: system, text: Review the selected documents.}]\n",
		"tools/bundles/library.yaml":    "id: library\nmatch: [{name: catalog/read}]\n",
		"templates/note.yaml":           "id: note\nname: Note\nformat: json\ninstructions: Summarize the entry.\nschema: {type: object}\n",

		"extension/forge/lookups/document_lookup.yaml": `id: document_lookup
bindings:
  - match: {fieldName: document_key, type: integer}
    lookup:
      dataSource: document_lookup
      dialogId: chooseDocument
      outputs: [{location: entryKey, name: document_key}, {location: title, name: document_title}]
      display: "${title}"
    named:
      name: document
      title: Documents
      queryInput: Title
      resolveInput: Key
      required: true
      store: "${entryKey}"
      display: "${title}"
      modelForm: "${id}"
`,
		"extension/forge/datasources/document_lookup.yaml": `id: document_lookup
title: Library entries
cardinality: collection
uniqueKey: [{field: entryKey}]
selectors: {data: rows}
backend: {kind: mcp_tool, service: library, method: entries.find, pinned: {columns: [entryKey, title]}}
`,
		"extension/forge/dialogs/chooseDocument.yaml": `id: chooseDocument
title: Choose a document
dataSourceRef: document_lookup
selectionMode: single
className: example-picker
properties:
  resolveInput: Key
  quickFilters:
    - {field: Key, placeholder: Key}
    - {field: Title, placeholder: Title}
content:
  id: documentRows
  dataSourceRef: document_lookup
`,
		"extension/forge/windows/documents.yaml": `windowKey: documents
title: Documents
resources:
  dataSources: [document_lookup]
  dialogs: [chooseDocument]
view: {content: {id: library, dataSourceRef: document_lookup}}
`,
		"extension/forge/windows/collections.yaml": `windowKey: collections
title: Collections
resources: {dataSources: [document_lookup]}
view: {content: {id: collections, dataSourceRef: document_lookup}}
`,
		"extension/forge/reporting/builder.yaml": `kind: forge.reporting.builder
id: library
reportBuilder:
  dataSourceRef: document_lookup
  datasets: [{id: primary, dataSourceRef: document_lookup}]
  document:
    title: Library
    blocks: [{id: entries, kind: tableBlock, datasetRef: primary, columns: [{key: title, label: Title}]}]
`,
		"extension/forge/reporting/overview.yaml": `kind: forge.reporting.report
id: overview
namespace: library
name: overview
resourceUri: report://library/overview
label: Library overview
builderRef: library
documentPatch: {title: Library overview}
`,
		"authorization/window-resources.json": `[{"windowKey":"documents","uri":"window://library/documents"},{"windowKey":"collections","uri":"window://library/collections"}]`,
		"authorization/report-resources.json": `[{"resourceUri":"report://library/overview","sourcePath":"extension/forge/reporting/overview.yaml"}]`,
	}
	for name, body := range files {
		if lookupOnly && !(strings.Contains(name, "/lookups/") || strings.Contains(name, "/datasources/") || strings.Contains(name, "/dialogs/")) {
			continue
		}
		file := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
