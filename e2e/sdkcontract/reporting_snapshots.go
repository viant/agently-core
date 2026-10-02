package sdkcontract

import "encoding/json"

// ReportingSnapshots supplies deterministic valid saved report models for SDK acceptance.
func ReportingSnapshots() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"reportSpec":  json.RawMessage(`{"version":1,"kind":"reportSpec","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"title":"Demo Report","parameters":{"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},"layoutIntent":{"kind":"single","resultPanePosition":"left","blockOrder":["primaryTable"]},"refinements":[],"calculatedFields":[],"datasets":[{"id":"primary","dataSourceRef":"demo","request":{}}],"blocks":[{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[]}]}`),
		"reportFill":  json.RawMessage(`{"version":1,"kind":"reportFill","specVersion":1,"specHash":"spec-1","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"parameters":{"viewMode":"table","groupBy":"","pageSize":25,"orderField":"","orderDir":"asc"},"refinements":[],"calculatedFields":[],"datasets":[{"id":"primary","dataSourceRef":"demo","request":{"limit":25,"offset":0},"provenance":{"requestHash":"request-1","rowCount":1,"truncated":false,"hasMore":false,"diagnostics":[]},"rows":[{"channel":"Display"}]}],"blocks":[{"id":"primaryTable","kind":"tableBlock","datasetRef":"primary","columns":[],"content":{"columns":[],"rowCount":1,"resolvedRows":[]}}],"diagnostics":[]}`),
		"reportPrint": json.RawMessage(`{"version":1,"kind":"reportPrint","specVersion":1,"specHash":"spec-1","fillVersion":1,"fillHash":"fill-1","source":{"kind":"dashboard.reportBuilder","containerId":"demo","stateKey":"demo","dataSourceRef":"demo"},"title":"Demo Report","pageGeometry":{"width":612,"height":792,"marginTop":48,"marginRight":48,"marginBottom":48,"marginLeft":48,"headerHeight":24,"footerHeight":24},"pages":[{"number":1,"elements":[{"id":"body-1","kind":"text","box":{"x":48,"y":96,"width":200,"height":18}}],"headerElements":[],"footerElements":[]}],"bookmarks":[{"id":"section-1","title":"Section 1","pageNumber":1}],"diagnostics":[]}`),
	}
}
