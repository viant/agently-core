package reporting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// The backend intentionally validates the shared canonical artifact shell here.
// It does not attempt to port Forge's full JSON schema into Go in one slice.

type reportSourceIdentity struct {
	Kind          string `json:"kind"`
	ContainerID   string `json:"containerId"`
	StateKey      string `json:"stateKey"`
	DataSourceRef string `json:"dataSourceRef"`
}

type reportSpecIdentity struct {
	Version int                  `json:"version"`
	Source  reportSourceIdentity `json:"source"`
}

type reportFillIdentity struct {
	Version     int                  `json:"version"`
	SpecVersion int                  `json:"specVersion"`
	Source      reportSourceIdentity `json:"source"`
}

type reportPrintIdentity struct {
	Version     int                  `json:"version"`
	SpecVersion int                  `json:"specVersion"`
	FillVersion int                  `json:"fillVersion"`
	Source      reportSourceIdentity `json:"source"`
}

func normalizeSubmitExportRequest(request *SubmitExportRequest) (*SubmitExportRequest, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting export: request is required")
	}
	if request.ReportExportRequest == nil {
		return cloneSubmitExportRequest(request), nil
	}
	envelope := request.ReportExportRequest
	if strings.TrimSpace(envelope.Kind) != "" && strings.TrimSpace(envelope.Kind) != "reportExportRequest" {
		return nil, fmt.Errorf("reporting export: invalid reportExportRequest kind %q", strings.TrimSpace(envelope.Kind))
	}
	if envelope.Version < 1 {
		return nil, fmt.Errorf("reporting export: reportExportRequest version must be at least 1")
	}
	scope, err := mapReportExportSourceScope(envelope.Source.From)
	if err != nil {
		return nil, err
	}
	if scope == ExportScopeSavedPayload && strings.TrimSpace(envelope.Source.PayloadID) == "" {
		return nil, fmt.Errorf("reporting export: reportExportRequest source.payloadId is required for savedPayload exports")
	}
	artifactRef := strings.TrimSpace(envelope.Source.ArtifactRef)
	if artifactRef == "" {
		return nil, fmt.Errorf("reporting export: reportExportRequest source.artifactRef is required")
	}
	if strings.TrimSpace(string(envelope.Target.Format)) == "" {
		return nil, fmt.Errorf("reporting export: reportExportRequest target.format is required")
	}
	conversationID, workspaceID := extractExportContextIDs(envelope.Metadata)
	return &SubmitExportRequest{
		ArtifactRef:    strings.TrimSpace(envelope.Source.ArtifactRef),
		Format:         envelope.Target.Format,
		Scope:          scope,
		ConversationID: conversationID,
		WorkspaceID:    workspaceID,
		ReportSpec:     cloneJSON(envelope.ReportSpec),
		ReportFill:     cloneJSON(envelope.ReportFill),
		ReportPrint:    cloneJSON(envelope.ReportPrint),
		Metadata:       cloneJSON(envelope.Metadata),
	}, nil
}

func mapReportExportSourceScope(from string) (ExportScope, error) {
	switch strings.TrimSpace(strings.ToLower(from)) {
	case "draft":
		return ExportScopeDraft, nil
	case "preset":
		return ExportScopeDraft, nil
	case "savedpayload":
		return ExportScopeSavedPayload, nil
	case "savedview":
		return ExportScopeSavedView, nil
	case "publishedsnapshot":
		return ExportScopePublishedSnapshot, nil
	default:
		return "", fmt.Errorf("reporting export: unsupported reportExportRequest source.from %q", strings.TrimSpace(from))
	}
}

func validateSubmitExportRequest(request *SubmitExportRequest) error {
	if request == nil {
		return fmt.Errorf("reporting export: request is required")
	}
	if strings.TrimSpace(request.ArtifactRef) == "" {
		return fmt.Errorf("reporting export: artifactRef is required")
	}
	switch request.Format {
	case ExportFormatPDF, ExportFormatCSV, ExportFormatXLSX:
	default:
		return fmt.Errorf("reporting export: unsupported format %q", strings.TrimSpace(string(request.Format)))
	}
	switch request.Scope {
	case "", ExportScopeDraft, ExportScopeSavedPayload, ExportScopeSavedView, ExportScopePublishedSnapshot:
	default:
		return fmt.Errorf("reporting export: unsupported scope %q", strings.TrimSpace(string(request.Scope)))
	}

	specPresent := len(bytes.TrimSpace(request.ReportSpec)) != 0
	fillPresent := len(bytes.TrimSpace(request.ReportFill)) != 0
	printPresent := len(bytes.TrimSpace(request.ReportPrint)) != 0

	switch request.Format {
	case ExportFormatPDF:
		if !printPresent {
			return fmt.Errorf("reporting export: reportPrint is required for pdf export")
		}
	case ExportFormatCSV, ExportFormatXLSX:
		if !fillPresent {
			return fmt.Errorf("reporting export: reportFill is required for tabular export")
		}
	}

	if specPresent {
		if err := validateReportSpecDocument(request.ReportSpec); err != nil {
			return fmt.Errorf("reporting export: %w", err)
		}
	}
	if fillPresent {
		if err := validateReportFillDocument(request.ReportFill); err != nil {
			return fmt.Errorf("reporting export: %w", err)
		}
	}
	if printPresent {
		if err := validateReportPrintDocument(request.ReportPrint); err != nil {
			return fmt.Errorf("reporting export: %w", err)
		}
	}

	return validateExportArtifactChain(request, specPresent, fillPresent, printPresent)
}

func extractExportContextIDs(metadata json.RawMessage) (string, string) {
	if len(bytes.TrimSpace(metadata)) == 0 {
		return "", ""
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return "", ""
	}
	conversationID := strings.TrimSpace(stringValue(payload["conversationId"]))
	workspaceID := strings.TrimSpace(stringValue(payload["workspaceId"]))
	return conversationID, workspaceID
}

func stringValue(value any) string {
	switch actual := value.(type) {
	case string:
		return actual
	default:
		return ""
	}
}

func validateExportArtifactChain(request *SubmitExportRequest, specPresent, fillPresent, printPresent bool) error {
	var (
		specIdentity  *reportSpecIdentity
		fillIdentity  *reportFillIdentity
		printIdentity *reportPrintIdentity
		err           error
	)
	if specPresent {
		specIdentity, err = decodeReportSpecIdentity(request.ReportSpec)
		if err != nil {
			return fmt.Errorf("reporting export: %w", err)
		}
	}
	if fillPresent {
		fillIdentity, err = decodeReportFillIdentity(request.ReportFill)
		if err != nil {
			return fmt.Errorf("reporting export: %w", err)
		}
	}
	if printPresent {
		printIdentity, err = decodeReportPrintIdentity(request.ReportPrint)
		if err != nil {
			return fmt.Errorf("reporting export: %w", err)
		}
	}
	if specIdentity != nil && fillIdentity != nil {
		if fillIdentity.SpecVersion != specIdentity.Version {
			return fmt.Errorf(
				"reporting export: reportFill specVersion %d does not match reportSpec version %d",
				fillIdentity.SpecVersion,
				specIdentity.Version,
			)
		}
		if !sameReportSource(specIdentity.Source, fillIdentity.Source) {
			return fmt.Errorf("reporting export: reportFill source does not match reportSpec source")
		}
	}
	if specIdentity != nil && printIdentity != nil {
		if printIdentity.SpecVersion != specIdentity.Version {
			return fmt.Errorf(
				"reporting export: reportPrint specVersion %d does not match reportSpec version %d",
				printIdentity.SpecVersion,
				specIdentity.Version,
			)
		}
		if !sameReportSource(specIdentity.Source, printIdentity.Source) {
			return fmt.Errorf("reporting export: reportPrint source does not match reportSpec source")
		}
	}
	if fillIdentity != nil && printIdentity != nil {
		if printIdentity.FillVersion != fillIdentity.Version {
			return fmt.Errorf(
				"reporting export: reportPrint fillVersion %d does not match reportFill version %d",
				printIdentity.FillVersion,
				fillIdentity.Version,
			)
		}
		if printIdentity.SpecVersion != fillIdentity.SpecVersion {
			return fmt.Errorf(
				"reporting export: reportPrint specVersion %d does not match reportFill specVersion %d",
				printIdentity.SpecVersion,
				fillIdentity.SpecVersion,
			)
		}
		if !sameReportSource(fillIdentity.Source, printIdentity.Source) {
			return fmt.Errorf("reporting export: reportPrint source does not match reportFill source")
		}
	}
	return nil
}

func validateReportSpecDocument(document json.RawMessage) error {
	root, err := decodeCanonicalDocument(document, "reportSpec")
	if err != nil {
		return err
	}
	return validateReportSpecRoot(root)
}

func validateReportFillDocument(document json.RawMessage) error {
	root, err := decodeCanonicalDocument(document, "reportFill")
	if err != nil {
		return err
	}
	return validateReportFillRoot(root)
}

func validateReportPrintDocument(document json.RawMessage) error {
	root, err := decodeCanonicalDocument(document, "reportPrint")
	if err != nil {
		return err
	}
	return validateReportPrintRoot(root)
}

func decodeCanonicalDocument(document json.RawMessage, artifactKind string) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(document)) == 0 {
		return nil, fmt.Errorf("invalid %s: document is required", artifactKind)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(document, &root); err != nil {
		return nil, fmt.Errorf("invalid %s document: %w", artifactKind, err)
	}
	return root, nil
}

func validateReportFillRoot(root map[string]json.RawMessage) error {
	if err := requireCanonicalFields(root, "reportFill", []string{
		"version",
		"kind",
		"specVersion",
		"specHash",
		"source",
		"parameters",
		"refinements",
		"calculatedFields",
		"datasets",
		"blocks",
		"diagnostics",
	}); err != nil {
		return err
	}
	var kind string
	if err := requireJSONStringForKind(root["kind"], "reportFill", "kind", false, &kind); err != nil {
		return err
	}
	if strings.TrimSpace(kind) != "reportFill" {
		return fmt.Errorf("invalid reportFill: kind must be reportFill")
	}
	if _, err := requireIntegerAtLeast(root["version"], "reportFill", "version", 1); err != nil {
		return err
	}
	if _, err := requireIntegerAtLeast(root["specVersion"], "reportFill", "specVersion", 1); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["specHash"], "reportFill", "specHash", false, new(string)); err != nil {
		return err
	}
	if err := validateCanonicalSource(root["source"], "reportFill"); err != nil {
		return err
	}
	if err := validateCanonicalParameters(root["parameters"], "reportFill"); err != nil {
		return err
	}
	if err := requireJSONArrayForKind(root["refinements"], "reportFill", "refinements", true); err != nil {
		return err
	}
	if err := requireJSONArrayForKind(root["calculatedFields"], "reportFill", "calculatedFields", true); err != nil {
		return err
	}
	if err := validateReportFillDatasets(root["datasets"]); err != nil {
		return err
	}
	if err := validateReportFillBlocks(root["blocks"]); err != nil {
		return err
	}
	if err := validateDiagnosticArray(root["diagnostics"], "reportFill", "diagnostics"); err != nil {
		return err
	}
	return nil
}

func validateReportPrintRoot(root map[string]json.RawMessage) error {
	if err := requireCanonicalFields(root, "reportPrint", []string{
		"version",
		"kind",
		"specVersion",
		"specHash",
		"fillVersion",
		"fillHash",
		"source",
		"title",
		"pageGeometry",
		"pages",
		"bookmarks",
		"diagnostics",
	}); err != nil {
		return err
	}
	var kind string
	if err := requireJSONStringForKind(root["kind"], "reportPrint", "kind", false, &kind); err != nil {
		return err
	}
	if strings.TrimSpace(kind) != "reportPrint" {
		return fmt.Errorf("invalid reportPrint: kind must be reportPrint")
	}
	if _, err := requireIntegerAtLeast(root["version"], "reportPrint", "version", 1); err != nil {
		return err
	}
	if _, err := requireIntegerAtLeast(root["specVersion"], "reportPrint", "specVersion", 1); err != nil {
		return err
	}
	if _, err := requireIntegerAtLeast(root["fillVersion"], "reportPrint", "fillVersion", 1); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["specHash"], "reportPrint", "specHash", false, new(string)); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["fillHash"], "reportPrint", "fillHash", false, new(string)); err != nil {
		return err
	}
	if err := validateCanonicalSource(root["source"], "reportPrint"); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["title"], "reportPrint", "title", false, new(string)); err != nil {
		return err
	}
	if err := validateReportPrintPageGeometry(root["pageGeometry"]); err != nil {
		return err
	}
	if err := validateReportPrintPages(root["pages"]); err != nil {
		return err
	}
	if err := validateReportPrintBookmarks(root["bookmarks"]); err != nil {
		return err
	}
	if err := validateDiagnosticArray(root["diagnostics"], "reportPrint", "diagnostics"); err != nil {
		return err
	}
	return nil
}

func requireCanonicalFields(root map[string]json.RawMessage, artifactKind string, fields []string) error {
	for _, field := range fields {
		if len(root[field]) == 0 {
			return fmt.Errorf("invalid %s: missing %s", artifactKind, field)
		}
	}
	return nil
}

func validateCanonicalSource(raw json.RawMessage, artifactKind string) error {
	if err := requireJSONObjectForKind(raw, artifactKind, "source"); err != nil {
		return err
	}
	root, err := decodeCanonicalDocument(raw, artifactKind)
	if err != nil {
		return fmt.Errorf("invalid %s: source must be an object", artifactKind)
	}
	if err := requireCanonicalFields(root, artifactKind, []string{
		"kind",
		"containerId",
		"stateKey",
		"dataSourceRef",
	}); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["kind"], artifactKind, "source.kind", false, new(string)); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["containerId"], artifactKind, "source.containerId", false, new(string)); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["stateKey"], artifactKind, "source.stateKey", false, new(string)); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["dataSourceRef"], artifactKind, "source.dataSourceRef", false, new(string)); err != nil {
		return err
	}
	return nil
}

func validateCanonicalParameters(raw json.RawMessage, artifactKind string) error {
	if err := requireJSONObjectForKind(raw, artifactKind, "parameters"); err != nil {
		return err
	}
	root, err := decodeCanonicalDocument(raw, artifactKind)
	if err != nil {
		return fmt.Errorf("invalid %s: parameters must be an object", artifactKind)
	}
	if err := requireCanonicalFields(root, artifactKind, []string{
		"viewMode",
		"groupBy",
		"pageSize",
		"orderField",
		"orderDir",
	}); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["viewMode"], artifactKind, "parameters.viewMode", false, new(string)); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["groupBy"], artifactKind, "parameters.groupBy", true, new(string)); err != nil {
		return err
	}
	if _, err := requireIntegerAtLeast(root["pageSize"], artifactKind, "parameters.pageSize", 1); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["orderField"], artifactKind, "parameters.orderField", true, new(string)); err != nil {
		return err
	}
	var orderDir string
	if err := requireJSONStringForKind(root["orderDir"], artifactKind, "parameters.orderDir", false, &orderDir); err != nil {
		return err
	}
	switch orderDir {
	case "asc", "desc":
	default:
		return fmt.Errorf("invalid %s: parameters.orderDir must be asc or desc", artifactKind)
	}
	return nil
}

func validateCanonicalLayoutIntent(raw json.RawMessage, artifactKind string) error {
	if err := requireJSONObjectForKind(raw, artifactKind, "layoutIntent"); err != nil {
		return err
	}
	root, err := decodeCanonicalDocument(raw, artifactKind)
	if err != nil {
		return fmt.Errorf("invalid %s: layoutIntent must be an object", artifactKind)
	}
	if err := requireCanonicalFields(root, artifactKind, []string{
		"kind",
		"resultPanePosition",
		"blockOrder",
	}); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["kind"], artifactKind, "layoutIntent.kind", false, new(string)); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["resultPanePosition"], artifactKind, "layoutIntent.resultPanePosition", false, new(string)); err != nil {
		return err
	}
	if err := requireJSONArrayForKind(root["blockOrder"], artifactKind, "layoutIntent.blockOrder", true); err != nil {
		return err
	}
	if len(root["items"]) != 0 {
		if err := requireJSONArrayForKind(root["items"], artifactKind, "layoutIntent.items", true); err != nil {
			return err
		}
	}
	return nil
}

func validateDiagnosticArray(raw json.RawMessage, artifactKind string, field string) error {
	var entries []json.RawMessage
	if err := requireJSONArrayForKind(raw, artifactKind, field, true, &entries); err != nil {
		return err
	}
	for index, entry := range entries {
		if err := requireJSONObjectForKind(entry, artifactKind, fmt.Sprintf("%s[%d]", field, index)); err != nil {
			return err
		}
		root, err := decodeCanonicalDocument(entry, artifactKind)
		if err != nil {
			return fmt.Errorf("invalid %s: %s[%d] must be an object", artifactKind, field, index)
		}
		if err := requireCanonicalFields(root, artifactKind, []string{"code", "severity", "message"}); err != nil {
			return err
		}
		if err := requireJSONStringForKind(root["code"], artifactKind, fmt.Sprintf("%s[%d].code", field, index), false, new(string)); err != nil {
			return err
		}
		if err := requireJSONStringForKind(root["severity"], artifactKind, fmt.Sprintf("%s[%d].severity", field, index), false, new(string)); err != nil {
			return err
		}
		if err := requireJSONStringForKind(root["message"], artifactKind, fmt.Sprintf("%s[%d].message", field, index), false, new(string)); err != nil {
			return err
		}
	}
	return nil
}

func validateReportFillDatasets(raw json.RawMessage) error {
	var datasets []json.RawMessage
	if err := requireJSONArrayForKind(raw, "reportFill", "datasets", true, &datasets); err != nil {
		return err
	}
	for index, dataset := range datasets {
		if err := requireJSONObjectForKind(dataset, "reportFill", fmt.Sprintf("datasets[%d]", index)); err != nil {
			return err
		}
		root, err := decodeCanonicalDocument(dataset, "reportFill")
		if err != nil {
			return fmt.Errorf("invalid reportFill: datasets[%d] must be an object", index)
		}
		if err := requireCanonicalFields(root, "reportFill", []string{
			"id",
			"dataSourceRef",
			"request",
			"provenance",
			"rows",
		}); err != nil {
			return err
		}
		if err := requireJSONStringForKind(root["id"], "reportFill", fmt.Sprintf("datasets[%d].id", index), false, new(string)); err != nil {
			return err
		}
		if err := requireJSONStringForKind(root["dataSourceRef"], "reportFill", fmt.Sprintf("datasets[%d].dataSourceRef", index), false, new(string)); err != nil {
			return err
		}
		if err := validateReportFillRequest(root["request"], index); err != nil {
			return err
		}
		if err := validateReportFillProvenance(root["provenance"], index); err != nil {
			return err
		}
		if err := validateJSONArrayObjects(root["rows"], "reportFill", fmt.Sprintf("datasets[%d].rows", index)); err != nil {
			return err
		}
	}
	return nil
}

func validateReportFillRequest(raw json.RawMessage, datasetIndex int) error {
	field := fmt.Sprintf("datasets[%d].request", datasetIndex)
	if err := requireJSONObjectForKind(raw, "reportFill", field); err != nil {
		return err
	}
	root, err := decodeCanonicalDocument(raw, "reportFill")
	if err != nil {
		return fmt.Errorf("invalid reportFill: %s must be an object", field)
	}
	var requestKind string
	if kindRaw, ok := root["kind"]; ok {
		if err := requireJSONStringForKind(kindRaw, "reportFill", field+".kind", false, &requestKind); err != nil {
			return err
		}
	}
	if requestKind == "staticCsv" || requestKind == "staticJson" {
		if err := requireCanonicalFields(root, "reportFill", []string{"kind", "format", "rowCount", "columnKeys"}); err != nil {
			return err
		}
		var format string
		if err := requireJSONStringForKind(root["format"], "reportFill", field+".format", false, &format); err != nil {
			return err
		}
		if format != "csv" && format != "json" {
			return fmt.Errorf("invalid reportFill: %s.format must be csv or json", field)
		}
		if _, err := requireIntegerAtLeast(root["rowCount"], "reportFill", field+".rowCount", 0); err != nil {
			return err
		}
		var columnKeys []json.RawMessage
		if err := requireJSONArrayForKind(root["columnKeys"], "reportFill", field+".columnKeys", false, &columnKeys); err != nil {
			return err
		}
		for index, columnKey := range columnKeys {
			if err := requireJSONStringForKind(columnKey, "reportFill", fmt.Sprintf("%s.columnKeys[%d]", field, index), false, new(string)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := requireCanonicalFields(root, "reportFill", []string{"limit", "offset"}); err != nil {
		return err
	}
	if _, err := requireIntegerAtLeast(root["limit"], "reportFill", field+".limit", 1); err != nil {
		return err
	}
	if _, err := requireIntegerAtLeast(root["offset"], "reportFill", field+".offset", 0); err != nil {
		return err
	}
	return nil
}

func validateReportFillProvenance(raw json.RawMessage, datasetIndex int) error {
	field := fmt.Sprintf("datasets[%d].provenance", datasetIndex)
	if err := requireJSONObjectForKind(raw, "reportFill", field); err != nil {
		return err
	}
	root, err := decodeCanonicalDocument(raw, "reportFill")
	if err != nil {
		return fmt.Errorf("invalid reportFill: %s must be an object", field)
	}
	if err := requireCanonicalFields(root, "reportFill", []string{
		"requestHash",
		"rowCount",
		"truncated",
		"hasMore",
		"diagnostics",
	}); err != nil {
		return err
	}
	if err := requireJSONStringForKind(root["requestHash"], "reportFill", field+".requestHash", false, new(string)); err != nil {
		return err
	}
	if _, err := requireIntegerAtLeast(root["rowCount"], "reportFill", field+".rowCount", 0); err != nil {
		return err
	}
	if err := requireBooleanForKind(root["truncated"], "reportFill", field+".truncated"); err != nil {
		return err
	}
	if err := requireBooleanForKind(root["hasMore"], "reportFill", field+".hasMore"); err != nil {
		return err
	}
	if err := validateDiagnosticArray(root["diagnostics"], "reportFill", field+".diagnostics"); err != nil {
		return err
	}
	return nil
}

func validateReportFillBlocks(raw json.RawMessage) error {
	var blocks []json.RawMessage
	if err := requireJSONArrayForKind(raw, "reportFill", "blocks", true, &blocks); err != nil {
		return err
	}
	for index, block := range blocks {
		if err := requireJSONObjectForKind(block, "reportFill", fmt.Sprintf("blocks[%d]", index)); err != nil {
			return err
		}
		root, err := decodeCanonicalDocument(block, "reportFill")
		if err != nil {
			return fmt.Errorf("invalid reportFill: blocks[%d] must be an object", index)
		}
		if err := requireCanonicalFields(root, "reportFill", []string{"id", "kind"}); err != nil {
			return err
		}
		if err := requireJSONStringForKind(root["id"], "reportFill", fmt.Sprintf("blocks[%d].id", index), false, new(string)); err != nil {
			return err
		}
		if err := requireJSONStringForKind(root["kind"], "reportFill", fmt.Sprintf("blocks[%d].kind", index), false, new(string)); err != nil {
			return err
		}
	}
	return nil
}

func validateReportPrintPageGeometry(raw json.RawMessage) error {
	field := "pageGeometry"
	if err := requireJSONObjectForKind(raw, "reportPrint", field); err != nil {
		return err
	}
	root, err := decodeCanonicalDocument(raw, "reportPrint")
	if err != nil {
		return fmt.Errorf("invalid reportPrint: %s must be an object", field)
	}
	if err := requireCanonicalFields(root, "reportPrint", []string{
		"width",
		"height",
		"marginTop",
		"marginRight",
		"marginBottom",
		"marginLeft",
		"headerHeight",
		"footerHeight",
	}); err != nil {
		return err
	}
	if _, err := requireNumberAtLeast(root["width"], "reportPrint", field+".width", 1); err != nil {
		return err
	}
	if _, err := requireNumberAtLeast(root["height"], "reportPrint", field+".height", 1); err != nil {
		return err
	}
	if _, err := requireNumberAtLeast(root["marginTop"], "reportPrint", field+".marginTop", 0); err != nil {
		return err
	}
	if _, err := requireNumberAtLeast(root["marginRight"], "reportPrint", field+".marginRight", 0); err != nil {
		return err
	}
	if _, err := requireNumberAtLeast(root["marginBottom"], "reportPrint", field+".marginBottom", 0); err != nil {
		return err
	}
	if _, err := requireNumberAtLeast(root["marginLeft"], "reportPrint", field+".marginLeft", 0); err != nil {
		return err
	}
	if _, err := requireNumberAtLeast(root["headerHeight"], "reportPrint", field+".headerHeight", 0); err != nil {
		return err
	}
	if _, err := requireNumberAtLeast(root["footerHeight"], "reportPrint", field+".footerHeight", 0); err != nil {
		return err
	}
	return nil
}

func validateReportPrintPages(raw json.RawMessage) error {
	var pages []json.RawMessage
	if err := requireJSONArrayForKind(raw, "reportPrint", "pages", false, &pages); err != nil {
		return err
	}
	for index, page := range pages {
		if err := requireJSONObjectForKind(page, "reportPrint", fmt.Sprintf("pages[%d]", index)); err != nil {
			return err
		}
		root, err := decodeCanonicalDocument(page, "reportPrint")
		if err != nil {
			return fmt.Errorf("invalid reportPrint: pages[%d] must be an object", index)
		}
		if err := requireCanonicalFields(root, "reportPrint", []string{
			"number",
			"elements",
			"headerElements",
			"footerElements",
		}); err != nil {
			return err
		}
		if _, err := requireIntegerAtLeast(root["number"], "reportPrint", fmt.Sprintf("pages[%d].number", index), 1); err != nil {
			return err
		}
		if err := validateReportPrintElementArray(root["elements"], fmt.Sprintf("pages[%d].elements", index)); err != nil {
			return err
		}
		if err := validateReportPrintElementArray(root["headerElements"], fmt.Sprintf("pages[%d].headerElements", index)); err != nil {
			return err
		}
		if err := validateReportPrintElementArray(root["footerElements"], fmt.Sprintf("pages[%d].footerElements", index)); err != nil {
			return err
		}
	}
	return nil
}

func validateReportPrintElementArray(raw json.RawMessage, field string) error {
	var elements []json.RawMessage
	if err := requireJSONArrayForKind(raw, "reportPrint", field, true, &elements); err != nil {
		return err
	}
	for index, element := range elements {
		if err := requireJSONObjectForKind(element, "reportPrint", fmt.Sprintf("%s[%d]", field, index)); err != nil {
			return err
		}
		root, err := decodeCanonicalDocument(element, "reportPrint")
		if err != nil {
			return fmt.Errorf("invalid reportPrint: %s[%d] must be an object", field, index)
		}
		if err := requireCanonicalFields(root, "reportPrint", []string{"id", "kind", "box"}); err != nil {
			return err
		}
		if err := requireJSONStringForKind(root["id"], "reportPrint", fmt.Sprintf("%s[%d].id", field, index), false, new(string)); err != nil {
			return err
		}
		if err := requireJSONStringForKind(root["kind"], "reportPrint", fmt.Sprintf("%s[%d].kind", field, index), false, new(string)); err != nil {
			return err
		}
		if err := validateReportPrintBox(root["box"], fmt.Sprintf("%s[%d].box", field, index)); err != nil {
			return err
		}
	}
	return nil
}

func validateReportPrintBox(raw json.RawMessage, field string) error {
	if err := requireJSONObjectForKind(raw, "reportPrint", field); err != nil {
		return err
	}
	root, err := decodeCanonicalDocument(raw, "reportPrint")
	if err != nil {
		return fmt.Errorf("invalid reportPrint: %s must be an object", field)
	}
	if err := requireCanonicalFields(root, "reportPrint", []string{"x", "y", "width", "height"}); err != nil {
		return err
	}
	if _, err := requireNumberAtLeast(root["x"], "reportPrint", field+".x", 0); err != nil {
		return err
	}
	if _, err := requireNumberAtLeast(root["y"], "reportPrint", field+".y", 0); err != nil {
		return err
	}
	if _, err := requireNumberAtLeast(root["width"], "reportPrint", field+".width", 0); err != nil {
		return err
	}
	if _, err := requireNumberAtLeast(root["height"], "reportPrint", field+".height", 0); err != nil {
		return err
	}
	return nil
}

func validateReportPrintBookmarks(raw json.RawMessage) error {
	var bookmarks []json.RawMessage
	if err := requireJSONArrayForKind(raw, "reportPrint", "bookmarks", true, &bookmarks); err != nil {
		return err
	}
	for index, bookmark := range bookmarks {
		if err := requireJSONObjectForKind(bookmark, "reportPrint", fmt.Sprintf("bookmarks[%d]", index)); err != nil {
			return err
		}
		root, err := decodeCanonicalDocument(bookmark, "reportPrint")
		if err != nil {
			return fmt.Errorf("invalid reportPrint: bookmarks[%d] must be an object", index)
		}
		if err := requireCanonicalFields(root, "reportPrint", []string{"id", "title", "pageNumber"}); err != nil {
			return err
		}
		if err := requireJSONStringForKind(root["id"], "reportPrint", fmt.Sprintf("bookmarks[%d].id", index), false, new(string)); err != nil {
			return err
		}
		if err := requireJSONStringForKind(root["title"], "reportPrint", fmt.Sprintf("bookmarks[%d].title", index), false, new(string)); err != nil {
			return err
		}
		if _, err := requireIntegerAtLeast(root["pageNumber"], "reportPrint", fmt.Sprintf("bookmarks[%d].pageNumber", index), 1); err != nil {
			return err
		}
	}
	return nil
}

func validateJSONArrayObjects(raw json.RawMessage, artifactKind string, field string) error {
	var values []json.RawMessage
	if err := requireJSONArrayForKind(raw, artifactKind, field, true, &values); err != nil {
		return err
	}
	for index, value := range values {
		if err := requireJSONObjectForKind(value, artifactKind, fmt.Sprintf("%s[%d]", field, index)); err != nil {
			return err
		}
	}
	return nil
}

func decodeReportSpecIdentity(document json.RawMessage) (*reportSpecIdentity, error) {
	identity := &reportSpecIdentity{}
	if err := json.Unmarshal(document, identity); err != nil {
		return nil, fmt.Errorf("invalid reportSpec identity: %w", err)
	}
	return identity, nil
}

func decodeReportFillIdentity(document json.RawMessage) (*reportFillIdentity, error) {
	identity := &reportFillIdentity{}
	if err := json.Unmarshal(document, identity); err != nil {
		return nil, fmt.Errorf("invalid reportFill identity: %w", err)
	}
	return identity, nil
}

func decodeReportPrintIdentity(document json.RawMessage) (*reportPrintIdentity, error) {
	identity := &reportPrintIdentity{}
	if err := json.Unmarshal(document, identity); err != nil {
		return nil, fmt.Errorf("invalid reportPrint identity: %w", err)
	}
	return identity, nil
}

func sameReportSource(left, right reportSourceIdentity) bool {
	return strings.TrimSpace(left.Kind) == strings.TrimSpace(right.Kind) &&
		strings.TrimSpace(left.ContainerID) == strings.TrimSpace(right.ContainerID) &&
		strings.TrimSpace(left.StateKey) == strings.TrimSpace(right.StateKey) &&
		strings.TrimSpace(left.DataSourceRef) == strings.TrimSpace(right.DataSourceRef)
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func requireJSONStringForKind(raw json.RawMessage, artifactKind string, field string, allowEmpty bool, target *string) error {
	if isJSONNull(raw) {
		return fmt.Errorf("invalid %s: %s must be a string", artifactKind, field)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("invalid %s: %s must be a string", artifactKind, field)
	}
	if !allowEmpty && strings.TrimSpace(*target) == "" {
		return fmt.Errorf("invalid %s: %s must be a non-empty string", artifactKind, field)
	}
	return nil
}

func requireJSONObjectForKind(raw json.RawMessage, artifactKind string, field string) error {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return fmt.Errorf("invalid %s: %s must be an object", artifactKind, field)
	}
	return nil
}

func requireJSONArrayForKind(raw json.RawMessage, artifactKind string, field string, allowEmpty bool, target ...*[]json.RawMessage) error {
	var value []json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		if allowEmpty {
			return fmt.Errorf("invalid %s: %s must be an array", artifactKind, field)
		}
		return fmt.Errorf("invalid %s: %s must be a non-empty array", artifactKind, field)
	}
	if !allowEmpty && len(value) == 0 {
		return fmt.Errorf("invalid %s: %s must be a non-empty array", artifactKind, field)
	}
	if len(target) > 0 && target[0] != nil {
		*target[0] = value
	}
	return nil
}

func requireIntegerAtLeast(raw json.RawMessage, artifactKind string, field string, minimum int) (int, error) {
	if isJSONNull(raw) {
		return 0, fmt.Errorf("invalid %s: %s must be an integer", artifactKind, field)
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("invalid %s: %s must be an integer", artifactKind, field)
	}
	if value < minimum {
		return 0, fmt.Errorf("invalid %s: %s must be >= %d", artifactKind, field, minimum)
	}
	return value, nil
}

func requireNumberAtLeast(raw json.RawMessage, artifactKind string, field string, minimum float64) (float64, error) {
	if isJSONNull(raw) {
		return 0, fmt.Errorf("invalid %s: %s must be a number", artifactKind, field)
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("invalid %s: %s must be a number", artifactKind, field)
	}
	if value < minimum {
		return 0, fmt.Errorf("invalid %s: %s must be >= %v", artifactKind, field, minimum)
	}
	return value, nil
}

func requireBooleanForKind(raw json.RawMessage, artifactKind string, field string) error {
	if isJSONNull(raw) {
		return fmt.Errorf("invalid %s: %s must be a boolean", artifactKind, field)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("invalid %s: %s must be a boolean", artifactKind, field)
	}
	_ = value
	return nil
}
