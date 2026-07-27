package agent

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/viant/agently-core/internal/logx"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	agenttool "github.com/viant/agently-core/service/agent/tool"
	intakesvc "github.com/viant/agently-core/service/intake"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
)

const directActionToolResultAssistantText = "$toolResult"

func validateDirectAction(action *intakesvc.DirectActionContext) error {
	if action == nil {
		return fmt.Errorf("direct action is nil")
	}
	toolName := strings.TrimSpace(action.ToolName)
	if toolName == "" {
		return fmt.Errorf("direct action toolName is required")
	}
	if strings.TrimSpace(action.AssistantText) == "" {
		return fmt.Errorf("direct action assistantText is required")
	}
	if action.Input == nil {
		return fmt.Errorf("direct action input is required")
	}
	switch strings.ToLower(strings.TrimSpace(mcpname.Display(toolName))) {
	case "ui/view/open":
		if strings.TrimSpace(stringValue(action.Input["id"])) == "" {
			items, ok := action.Input["items"].([]interface{})
			if !ok || len(items) == 0 {
				return fmt.Errorf("ui/view:open direct action input.id or input.items is required")
			}
		}
	}
	return nil
}

func clearDirectActionInContext(ctx map[string]any) {
	tc := intakesvc.FromContext(ctx)
	if tc == nil {
		return
	}
	tc.DirectAction = intakesvc.DirectActionContext{}
}

func directActionSelectionFromIntake(cfg *agentmdl.Intake) agenttool.Selection {
	if cfg == nil {
		return agenttool.Selection{}
	}
	selection := agenttool.Selection{
		Bundles: append([]string(nil), cfg.Tool.Bundles...),
	}
	for _, item := range cfg.Tool.Items {
		if item == nil {
			continue
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = strings.TrimSpace(item.Definition.Name)
		}
		if name == "" {
			continue
		}
		selection.Tools = append(selection.Tools, name)
	}
	return selection
}

func (s *Service) directActionAllowedToolNames(ctx context.Context, cfg *agentmdl.Intake) (map[string]struct{}, error) {
	control := directActionSelectionFromIntake(cfg)
	if len(control.Tools) == 0 && len(control.Bundles) == 0 {
		return nil, nil
	}
	defs, err := s.resolveStructuredToolDefinitions(ctx, control)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(mcpname.Canonical(def.Name))
		if name == "" {
			continue
		}
		allowed[strings.ToLower(name)] = struct{}{}
	}
	return allowed, nil
}

func (s *Service) authorizeDirectAction(ctx context.Context, input *QueryInput, action *intakesvc.DirectActionContext) error {
	if input == nil || input.Agent == nil {
		return fmt.Errorf("direct action requires an agent context")
	}
	allowed, err := s.directActionAllowedToolNames(ctx, &input.Agent.Intake)
	if err != nil {
		return err
	}
	toolName := strings.ToLower(strings.TrimSpace(mcpname.Canonical(action.ToolName)))
	if toolName == "" {
		return fmt.Errorf("direct action toolName is required")
	}
	if len(allowed) == 0 {
		return fmt.Errorf("direct action tool %q is not allowed by intake.tool policy", strings.TrimSpace(action.ToolName))
	}
	if _, ok := allowed[toolName]; !ok {
		return fmt.Errorf("direct action tool %q is not allowed by intake.tool policy", strings.TrimSpace(action.ToolName))
	}
	return nil
}

func (s *Service) maybeRunDirectAction(ctx context.Context, input *QueryInput, output *QueryOutput) (bool, error) {
	action := directActionFromContext(input.Context)
	if action == nil {
		return false, nil
	}
	if err := validateDirectAction(action); err != nil {
		logx.Warnf("conversation", "agent.Query directAction ignored convo=%q turn_id=%q reason=%v", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), err)
		clearDirectActionInContext(input.Context)
		return false, nil
	}
	if err := s.authorizeDirectAction(ctx, input, action); err != nil {
		logx.Warnf("conversation", "agent.Query directAction unauthorized convo=%q turn_id=%q tool=%q reason=%v", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), strings.TrimSpace(action.ToolName), err)
		clearDirectActionInContext(input.Context)
		return false, nil
	}
	toolName := strings.TrimSpace(action.ToolName)
	logx.Infof("conversation", "agent.Query directAction start convo=%q turn_id=%q tool=%q", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), toolName)
	toolCall, _, err := toolexec.ExecuteToolStep(ctx, s.registry, toolexec.StepInfo{
		Name:       toolName,
		Args:       action.Input,
		ResponseID: "intake_direct_action",
	}, s.conversation)
	if err != nil {
		return true, err
	}
	s.annotateDirectActionExecution(input, action, &toolCall.Result)
	text := directActionAssistantText(action, toolCall.Result)
	output.TurnID = input.MessageID
	output.MessageID = input.MessageID
	output.Content = text
	if err := s.publishDirectActionAssistantMessage(ctx, input, text); err != nil {
		return true, err
	}
	logx.Infof("conversation", "agent.Query directAction ok convo=%q turn_id=%q tool=%q", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), toolName)
	return true, nil
}

func directActionAssistantText(action *intakesvc.DirectActionContext, result string) string {
	if action == nil {
		return ""
	}
	configured := strings.TrimSpace(action.AssistantText)
	if configured != directActionToolResultAssistantText {
		return configured
	}
	if strings.EqualFold(strings.TrimSpace(mcpname.Display(action.ToolName)), "steward/diagnostic") {
		if formatted := formatDiagnosticToolResult(result); formatted != "" {
			return formatted
		}
	}
	return strings.TrimSpace(result)
}

func formatDiagnosticToolResult(result string) string {
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result)), &payload); err != nil {
		return ""
	}
	if report := formatDiagnosticReport(payload); report != "" {
		return report
	}
	return formatDiagnosticMarkdown(payload)
}

func formatDiagnosticMarkdown(payload map[string]interface{}) string {
	explanation := normalizeInterfaceMap(payload["explanation"])
	scope := normalizeInterfaceMap(payload["scope"])
	coverage := normalizeInterfaceMap(payload["coverage"])
	if len(explanation) == 0 {
		return ""
	}

	classification := strings.TrimSpace(stringValue(explanation["primaryBlockerClass"]))
	confidence := diagnosticUserLabel(stringValue(explanation["confidence"]))
	diagnosis := diagnosticUserText(stringValue(explanation["diagnosis"]))
	nextValidation := diagnosticUserText(stringValue(explanation["nextValidation"]))
	title := diagnosticResultTitle(scope)
	classLabel := diagnosticClassLabel(classification)

	var builder strings.Builder
	builder.WriteString("# ")
	builder.WriteString(title)
	if classLabel != "" {
		builder.WriteString("\n\n**Conclusion:** ")
		builder.WriteString(classLabel)
		if confidence != "" {
			builder.WriteString(" (")
			builder.WriteString(confidence)
			builder.WriteString(" confidence)")
		}
	}
	if diagnosis != "" {
		builder.WriteString("\n\n")
		builder.WriteString(diagnosis)
	}
	if facts := diagnosticSupportingFactRows(explanation); len(facts) > 0 {
		builder.WriteString("\n\n## Supporting evidence")
		for _, fact := range facts {
			path := stringValue(fact["path"])
			value := diagnosticScalarString(fact["value"])
			source := stringValue(fact["source"])
			builder.WriteString("\n- ")
			if source != "" {
				builder.WriteString(source)
				builder.WriteString(": ")
			}
			if path != "" {
				builder.WriteString(path)
				if value != "" {
					builder.WriteString(" = ")
				}
			}
			builder.WriteString(value)
		}
	}
	if len(coverage) > 0 {
		builder.WriteString("\n\n## Coverage")
		if level := diagnosticCoverageLabel(stringValue(coverage["level"])); level != "" {
			builder.WriteString("\n- Level: ")
			builder.WriteString(level)
		}
		if missing := diagnosticStringList(coverage["missingEvidence"]); len(missing) > 0 {
			builder.WriteString("\n- Missing evidence: ")
			builder.WriteString(strings.Join(diagnosticSurfaceLabels(missing), ", "))
		}
		if skipped := diagnosticStringList(coverage["skippedSurfaces"]); len(skipped) > 0 {
			builder.WriteString("\n- Skipped surfaces: ")
			builder.WriteString(strings.Join(diagnosticSurfaceLabels(skipped), ", "))
		}
		if deeper, _ := coverage["deeperProofRequired"].(bool); deeper {
			builder.WriteString("\n- Deeper proof required")
		}
	}
	if nextValidation != "" {
		builder.WriteString("\n\n## Next validation\n")
		builder.WriteString(nextValidation)
	}
	return strings.TrimSpace(builder.String())
}

func formatDiagnosticReport(payload map[string]interface{}) string {
	explanation := normalizeInterfaceMap(payload["explanation"])
	scope := normalizeInterfaceMap(payload["scope"])
	coverage := normalizeInterfaceMap(payload["coverage"])
	if len(explanation) == 0 || len(scope) == 0 {
		return ""
	}

	classification := strings.TrimSpace(stringValue(explanation["primaryBlockerClass"]))
	confidence := diagnosticUserLabel(stringValue(explanation["confidence"]))
	diagnosis := diagnosticUserText(stringValue(explanation["diagnosis"]))
	nextValidation := diagnosticUserText(stringValue(explanation["nextValidation"]))
	if diagnosis == "" {
		diagnosis = "Diagnostic did not return a narrative explanation."
	}
	entityTitle, reportScope := diagnosticReportIdentity(scope)
	reportID := "steward_diagnostic_report"
	classLabel := diagnosticClassLabel(classification)
	if classLabel == "" {
		classLabel = "No supported classification"
	}

	deliverySummary := diagnosticDatasetRows(payload, "delivery_summary")
	deliverySummary = diagnosticUserFacingRows(deliverySummary)
	deliveryPacing := diagnosticDatasetRows(payload, "delivery_pacing")
	restrictions := diagnosticDatasetRows(payload, "restriction_soft_ineligibilities")
	restrictions = diagnosticUserFacingRestrictionRows(restrictions)
	supplyPaths := diagnosticDatasetRows(payload, "supply_path_evidence")
	supplyPaths = diagnosticUserFacingRows(supplyPaths)
	setupChanges := diagnosticDatasetRows(payload, "setup_changes")
	setupChanges = diagnosticUserFacingRows(setupChanges)
	baseline := diagnosticDatasetRows(payload, "baseline")
	baseline = diagnosticUserFacingRows(baseline)
	supportingFacts := diagnosticSupportingFactRows(explanation)
	coverageEvidence := diagnosticCoverageRows(coverage)
	deliveryPacing = diagnosticUserFacingRows(deliveryPacing)

	overview := map[string]interface{}{
		"entity":              entityTitle,
		"primaryDiagnosis":    classLabel,
		"confidence":          confidence,
		"diagnosis":           diagnosis,
		"coverageLevel":       diagnosticCoverageLabel(stringValue(coverage["level"])),
		"deeperProofRequired": coverage["deeperProofRequired"],
		"from":                strings.TrimSpace(stringValue(scope["from"])),
		"to":                  strings.TrimSpace(stringValue(scope["to"])),
	}
	if len(deliverySummary) > 0 {
		for key, value := range deliverySummary[0] {
			overview[key] = value
		}
	}

	primaryRead := fmt.Sprintf("**%s.** %s", classLabel, diagnosis)
	if confidence != "" {
		primaryRead += fmt.Sprintf("\n\nConfidence: **%s**.", confidence)
	}
	if nextValidation != "" {
		primaryRead += "\n\n**Next validation:** " + nextValidation
	}
	deliveryWindow := diagnosticWindowLabel(scope)
	deliveryNote := fmt.Sprintf(
		"The headline spend is the **aggregate for %s**. The pacing table below is explicitly a **latest entity snapshot**; its spend is not the report-window total.",
		deliveryWindow,
	)
	validationRead := primaryRead
	if len(coverageEvidence) > 0 {
		validationRead += "\n\nCoverage gaps and intentionally skipped optional surfaces are listed below; they are not silently replaced with another diagnosis."
	}

	sections := []map[string]interface{}{
		{"id": "report_tabs", "kind": "tabGroupBlock", "title": "Diagnostic report sections", "sectionIds": []string{"overview_section", "delivery_section", "evidence_section", "changes_section", "validation_section"}, "defaultSectionId": "overview_section"},
		{"id": "overview_section", "kind": "sectionBlock", "title": "Overview", "navigationLabel": "Overview"},
		{"id": "delivery_section", "kind": "sectionBlock", "title": "Delivery", "navigationLabel": "Delivery"},
		{"id": "evidence_section", "kind": "sectionBlock", "title": "Restricting factors", "navigationLabel": "Restricting factors"},
		{"id": "changes_section", "kind": "sectionBlock", "title": "Recent changes", "navigationLabel": "Recent changes"},
		{"id": "validation_section", "kind": "sectionBlock", "title": "Corrective validation", "navigationLabel": "Corrective validation"},
	}

	blocks := []map[string]interface{}{
		{"id": "primary_diagnosis", "kind": "kpiBlock", "title": "Primary diagnosis", "datasetRef": "diagnostic_overview", "valueField": "primaryDiagnosis", "valueLabel": "Conclusion", "tone": diagnosticTone(classification, confidence)},
		{"id": "confidence", "kind": "kpiBlock", "title": "Confidence", "datasetRef": "diagnostic_overview", "valueField": "confidence", "valueLabel": "Confidence", "tone": diagnosticConfidenceTone(confidence)},
	}
	if _, ok := overview["totalSpend"]; ok {
		blocks = append(blocks, map[string]interface{}{"id": "window_spend", "kind": "kpiBlock", "title": "Report-window spend", "description": deliveryWindow, "datasetRef": "diagnostic_overview", "valueField": "totalSpend", "valueLabel": "Aggregate spend", "valueFormat": "currency", "tone": "neutral"})
	}
	if _, ok := overview["flightSpendShortfall"]; ok {
		blocks = append(blocks, map[string]interface{}{"id": "flight_shortfall", "kind": "kpiBlock", "title": "Latest flight shortfall", "datasetRef": "diagnostic_overview", "valueField": "flightSpendShortfall", "valueLabel": "Latest snapshot", "valueFormat": "currency", "tone": "warning"})
	}
	blocks = append(blocks,
		map[string]interface{}{"id": "diagnostic_posture", "kind": "badgesBlock", "title": "Diagnostic posture", "datasetRef": "diagnostic_overview", "items": []map[string]interface{}{
			{"id": "coverage", "valueField": "coverageLevel", "label": "Coverage", "tone": "info"},
			{"id": "proof", "valueField": "deeperProofRequired", "label": "Deeper proof required", "tone": "warning"},
		}},
		map[string]interface{}{"id": "primary_read", "kind": "markdownBlock", "title": "Primary read", "markdown": primaryRead},
		map[string]interface{}{"id": "delivery_window_definition", "kind": "markdownBlock", "title": "Metric windows", "markdown": deliveryNote},
	)
	if len(deliverySummary) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"id": "delivery_summary_table", "kind": "tableBlock", "title": "Report-window delivery aggregate — " + deliveryWindow, "datasetRef": "delivery_summary",
			"columns": []map[string]interface{}{
				{"key": "bids", "label": "Bids", "format": "compact"},
				{"key": "impressions", "label": "Impressions", "format": "compact"},
				{"key": "postBidOutcomeRate", "label": "Post-bid impression rate", "format": "percentFraction"},
				{"key": "creativeTestRecommendation", "label": "Creative validation"},
				{"key": "totalSpend", "label": "Window spend", "format": "currency"},
				{"key": "dailySpendShortfall", "label": "Latest daily shortfall", "format": "currency"},
				{"key": "flightSpendShortfall", "label": "Latest flight shortfall", "format": "currency"},
			},
		})
	}
	if len(deliveryPacing) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"id": "delivery_snapshot_table", "kind": "tableBlock", "title": "Latest entity pacing snapshot — not window totals", "datasetRef": "delivery_pacing",
			"columns": []map[string]interface{}{
				{"key": "entityKind", "label": "Entity"},
				{"key": "entityId", "label": "ID", "format": "number"},
				{"key": "dailyPacingStatus", "label": "Daily pacing"},
				{"key": "flightPacingStatus", "label": "Flight pacing"},
				{"key": "dailySpendShortfall", "label": "Snapshot daily shortfall", "format": "currency"},
				{"key": "flightSpendShortfall", "label": "Snapshot flight shortfall", "format": "currency"},
				{"key": "bids", "label": "Snapshot bids", "format": "compact"},
				{"key": "impressions", "label": "Snapshot impressions", "format": "compact"},
				{"key": "totalSpend", "label": "Snapshot spend", "format": "currency"},
			},
		})
	}
	if len(supportingFacts) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"id": "supporting_facts_table", "kind": "tableBlock", "title": "Primary supporting facts", "datasetRef": "supporting_facts",
			"columns": []map[string]interface{}{
				{"key": "path", "label": "Evidence"},
				{"key": "value", "label": "Finding"},
				{"key": "source", "label": "Evidence source"},
			},
		})
	}
	if len(restrictions) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"id": "restriction_table", "kind": "tableBlock", "title": "Delivery-gap materiality by factor", "datasetRef": "restriction_evidence",
			"columns": []map[string]interface{}{
				{"key": "factor", "label": "Delivery factor"},
				{"key": "observedFilterEvents", "label": "Observed filter events", "format": "compact"},
				{"key": "estimatedAffectedOpportunities", "label": "Estimated affected opportunities", "format": "compact"},
				{"key": "pacingShortfall", "label": "Pacing shortfall", "format": "currency"},
				{"key": "approximateOpportunitiesNeeded", "label": "Approx. opportunities needed", "format": "compact"},
				{"key": "countAssessmentLabel", "label": "Count-based assessment"},
				{"key": "sourceLabel", "label": "Evidence source"},
			},
		})
	}
	if len(supplyPaths) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"id": "supply_path_table", "kind": "tableBlock", "title": "Supply-path evidence", "datasetRef": "supply_path_evidence",
			"description": "Seller ID identifies the observed supply-path participant; it is not causal proof by itself.",
			"columns": []map[string]interface{}{
				{"key": "sellerId", "label": "Seller ID"},
				{"key": "sellerDomain", "label": "Seller domain"},
				{"key": "sellerDomainPath", "label": "Seller path"},
				{"key": "publisherId", "label": "Publisher ID", "format": "number"},
				{"key": "dealId", "label": "Deal ID", "format": "number"},
				{"key": "siteId", "label": "Site ID", "format": "number"},
				{"key": "hopCount", "label": "Hops", "format": "number"},
				{"key": "pathComplete", "label": "Path complete"},
				{"key": "bids", "label": "Bids", "format": "compact"},
				{"key": "impressions", "label": "Impressions", "format": "compact"},
				{"key": "spend", "label": "Spend", "format": "currency"},
				{"key": "winRate", "label": "Win rate", "format": "percentFraction"},
				{"key": "ecpm", "label": "eCPM", "format": "currency"},
			},
		})
	}
	if len(setupChanges) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"id": "setup_changes_table", "kind": "tableBlock", "title": "Recent setup-change evidence", "datasetRef": "setup_changes",
			"columns": []map[string]interface{}{
				{"key": "analysisDate", "label": "Analysis date"},
				{"key": "userChangeFlag", "label": "User change"},
				{"key": "deploymentFlag", "label": "Deployment change"},
				{"key": "source", "label": "Source"},
			},
		})
	}
	if len(baseline) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"id": "baseline_table", "kind": "tableBlock", "title": "Budget and request baseline", "datasetRef": "baseline",
			"columns": []map[string]interface{}{
				{"key": "analysisDate", "label": "Analysis date"},
				{"key": "tdBudgetStatus", "label": "Today budget status"},
				{"key": "ydBudgetStatus", "label": "Yesterday budget status"},
				{"key": "tdDealReqs", "label": "Today deal requests", "format": "compact"},
				{"key": "ydDealReqs", "label": "Yesterday deal requests", "format": "compact"},
			},
		})
	}
	blocks = append(blocks, map[string]interface{}{"id": "corrective_read", "kind": "markdownBlock", "title": "Corrective validation", "markdown": validationRead})
	if len(coverageEvidence) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"id": "coverage_table", "kind": "tableBlock", "title": "Evidence coverage", "datasetRef": "coverage_evidence",
			"columns": []map[string]interface{}{{"key": "status", "label": "Status"}, {"key": "surface", "label": "Evidence surface"}},
		})
	}
	groupedBlocks := map[string][]map[string]interface{}{
		"overview_section":   {},
		"delivery_section":   {},
		"evidence_section":   {},
		"changes_section":    {},
		"validation_section": {},
	}
	for _, block := range blocks {
		switch block["id"] {
		case "primary_diagnosis", "confidence", "window_spend", "flight_shortfall", "diagnostic_posture", "primary_read":
			groupedBlocks["overview_section"] = append(groupedBlocks["overview_section"], block)
		case "delivery_window_definition", "delivery_summary_table", "delivery_snapshot_table":
			groupedBlocks["delivery_section"] = append(groupedBlocks["delivery_section"], block)
		case "supporting_facts_table", "restriction_table", "supply_path_table":
			groupedBlocks["evidence_section"] = append(groupedBlocks["evidence_section"], block)
		case "setup_changes_table", "baseline_table":
			groupedBlocks["changes_section"] = append(groupedBlocks["changes_section"], block)
		case "corrective_read", "coverage_table":
			groupedBlocks["validation_section"] = append(groupedBlocks["validation_section"], block)
		}
	}
	reportBlocks := []map[string]interface{}{sections[0]}
	for _, section := range sections[1:] {
		reportBlocks = append(reportBlocks, section)
		reportBlocks = append(reportBlocks, groupedBlocks[strings.TrimSpace(stringValue(section["id"]))]...)
	}

	var builder strings.Builder
	sequence := 1
	appendForgeFence(&builder, "forge-report", map[string]interface{}{
		"version": 1, "scope": reportScope, "id": reportID, "sequence": sequence, "mode": "start", "grammar": "report-document-v1",
		"title": entityTitle + " troubleshooting", "subtitle": "Diagnostic-final causal assessment",
	})
	datasets := []struct {
		id   string
		rows []map[string]interface{}
	}{
		{id: "diagnostic_overview", rows: []map[string]interface{}{overview}},
		{id: "delivery_summary", rows: deliverySummary},
		{id: "delivery_pacing", rows: deliveryPacing},
		{id: "supporting_facts", rows: supportingFacts},
		{id: "restriction_evidence", rows: restrictions},
		{id: "supply_path_evidence", rows: supplyPaths},
		{id: "setup_changes", rows: setupChanges},
		{id: "baseline", rows: baseline},
		{id: "coverage_evidence", rows: coverageEvidence},
	}
	for _, dataset := range datasets {
		if len(dataset.rows) == 0 {
			continue
		}
		sequence++
		appendForgeFence(&builder, "forge-data", map[string]interface{}{
			"version": 2, "scope": reportScope, "id": dataset.id, "reportRef": reportID, "sequence": sequence,
			"format": "json", "mode": "replace", "data": dataset.rows,
		})
	}
	sequence++
	appendForgeFence(&builder, "forge-report", map[string]interface{}{
		"version": 1, "scope": reportScope, "id": reportID, "sequence": sequence, "mode": "append", "blocks": reportBlocks,
	})
	layoutItems := make([]map[string]interface{}, 0, len(reportBlocks))
	for _, block := range reportBlocks {
		item := map[string]interface{}{"blockId": block["id"]}
		switch block["id"] {
		case "primary_diagnosis", "confidence", "window_spend", "flight_shortfall":
			item["size"] = "quarter"
		case "diagnostic_posture", "primary_read":
			item["size"] = "half"
		}
		layoutItems = append(layoutItems, item)
	}
	sequence++
	appendForgeFence(&builder, "forge-report", map[string]interface{}{
		"version": 1, "scope": reportScope, "id": reportID, "sequence": sequence, "mode": "patch",
		"layout": map[string]interface{}{"type": "grid", "columns": 12, "items": layoutItems},
	})
	sequence++
	appendForgeFence(&builder, "forge-report", map[string]interface{}{
		"version": 1, "scope": reportScope, "id": reportID, "sequence": sequence, "mode": "commit",
	})
	return strings.TrimSpace(builder.String())
}

func appendForgeFence(builder *strings.Builder, language string, payload map[string]interface{}) {
	if builder == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if builder.Len() > 0 {
		builder.WriteByte('\n')
	}
	builder.WriteString("```")
	builder.WriteString(language)
	builder.WriteByte('\n')
	builder.Write(data)
	builder.WriteString("\n```")
}

func diagnosticReportIdentity(scope map[string]interface{}) (string, string) {
	if ids := diagnosticStringList(scope["audienceIds"]); len(ids) > 0 {
		return "Line " + strings.Join(ids, ", "), "line_" + diagnosticIdentifier(strings.Join(ids, "_"))
	}
	if ids := diagnosticStringList(scope["adOrderIds"]); len(ids) > 0 {
		return "Ad order " + strings.Join(ids, ", "), "order_" + diagnosticIdentifier(strings.Join(ids, "_"))
	}
	return "Steward diagnostic", "steward_diagnostic"
}

func diagnosticIdentifier(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func diagnosticClassLabel(classification string) string {
	switch strings.ToUpper(strings.TrimSpace(classification)) {
	case "DEEPER_PROOF_REQUIRED":
		return "No single delivery constraint is proven"
	case "DELIVERY_PROOF_REQUIRED":
		return "More delivery data is needed"
	case "ORDER_VISIBILITY_REQUIRED":
		return "Order activity is not yet visible"
	case "SETUP_NOT_READY":
		return "Campaign setup is incomplete"
	case "FRAUD_FILTER_PRESSURE":
		return "Inventory quality filters are limiting eligible opportunities"
	case "BID_FLOOR_PRESSURE":
		return "A bid-price gap is visible, but its cause needs validation"
	case "RECENT_BID_SUPPRESSION_PRESSURE", "RECENT_BID_SUPPRESSION_SIGNAL":
		return "Recent-contact safeguards are limiting repeat opportunities"
	case "BID_MULTIPLIER_PRESSURE":
		return "Bid adjustments are reducing competitiveness"
	case "NEGATIVE_BID_PRESSURE":
		return "Bid adjustments are suppressing bids"
	case "FREQUENCY_CAP_PRESSURE":
		return "Frequency limits are constraining additional reach"
	case "ML_TIMEOUT_PRESSURE":
		return "Optimization decisions are timing out"
	case "KPI_GOAL_OPTIMIZATION_PRESSURE":
		return "Optimization goals are limiting eligible opportunities"
	case "CREATIVE_PROTOCOL_WRAPPER_PRESSURE":
		return "Creative compatibility is limiting delivery"
	case "BID_TO_IMPRESSION_DROPOFF":
		return "Bids are not becoming delivered impressions"
	case "DSP_SHAPER_REJECTION_PRESSURE":
		return "Platform traffic controls are limiting eligible requests"
	case "RESTRICTIVE_TARGETING_SIGNAL":
		return "Targeting settings are narrowing available inventory"
	case "DEAL_RESTRICTION_PRESSURE":
		return "Deal eligibility is limiting available inventory"
	case "SITE_SUPPLY_RESTRICTION_PRESSURE":
		return "Eligible site inventory is too narrow"
	case "EFFECTIVE_BID_COMPETITIVENESS_SIGNAL":
		return "Bid competitiveness may be limiting wins"
	case "":
		return ""
	}
	text := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(classification), "_", " "))
	text = strings.NewReplacer(
		" pressure", " constraint",
		" signal", " indicator",
		" required", " needs validation",
		"dsp ", "platform ",
		"ml ", "optimization ",
	).Replace(text)
	return diagnosticSentenceLabel(text)
}

func diagnosticSentenceLabel(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", " "))
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func diagnosticUserLabel(value string) string {
	return diagnosticSentenceLabel(strings.ToLower(strings.TrimSpace(value)))
}

func diagnosticUserText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.NewReplacer(
		"offer.bid.floor", "inventory price floors",
		"Estimated optimization rejections", "Estimated affected opportunities",
		"estimated optimization rejections", "estimated affected opportunities",
		"Optimization rejections", "Affected opportunities",
		"optimization rejections", "affected opportunities",
		"Optimization rejection evidence", "Delivery-filtering evidence",
		"optimization rejection evidence", "delivery-filtering evidence",
		"Optimization rejection", "Delivery filtering",
		"optimization rejection", "delivery filtering",
		"estimated rejection count", "estimated affected-opportunity count",
		"estimated rejection volume", "estimated affected-opportunity volume",
		"rejection volume", "affected-opportunity volume",
		"pacing-control", "automated budget or delivery control",
		"pacing shortfall", "delivery gap",
		"late-stage bid competitiveness", "bid competitiveness",
		"performance depth", "deeper delivery analysis",
		"supply-path", "inventory-path",
		"bid-allocation", "bid allocation",
		"causal blocker", "delivery constraint",
		"blocker", "constraint",
	).Replace(value)
}

func diagnosticEvidenceLabel(path string) string {
	path = strings.TrimSpace(path)
	switch {
	case strings.HasSuffix(path, "baseline.previousOrder.spend"):
		return "Previous-day order spend"
	case strings.HasSuffix(path, "baseline.currentOrder.spend"):
		return "Current-day order spend"
	case strings.HasSuffix(path, "campaignContext.siblingSpendGain"):
		return "Sibling-order spend gained"
	case strings.HasSuffix(path, "campaignContext.siblingSpendLoss"):
		return "Sibling-order spend lost"
	case strings.HasSuffix(path, "campaignContext.pacing.hasReallocationSignal"):
		return "Campaign pacing reallocation detected"
	case strings.Contains(path, "optimizationRejections") && strings.HasSuffix(path, ".feature"):
		return "Leading factor reducing delivery opportunities"
	case strings.Contains(path, "estimatedOptimizationRejections"):
		return "Estimated opportunities affected by the leading factor"
	case strings.HasSuffix(path, "approxMissingBids"):
		return "Estimated additional opportunities needed to close the delivery gap"
	case strings.HasSuffix(path, "missingBidCoveragePct"):
		return "Share of the delivery gap explained by this factor"
	case strings.HasSuffix(path, "deliveredBidSharePct"):
		return "Affected opportunities compared with submitted bids"
	case strings.HasSuffix(path, "postBidOutcomeRatePct"):
		return "Share of submitted bids that became delivered impressions"
	case strings.HasSuffix(path, "creativeIds"):
		return "Creatives available for validation"
	case strings.Contains(path, "dailySpendShortfall"):
		return "Latest daily delivery gap"
	case strings.Contains(path, "flightSpendShortfall"):
		return "Latest flight delivery gap"
	}
	segment := path
	if index := strings.LastIndex(segment, "."); index >= 0 {
		segment = segment[index+1:]
	}
	segment = strings.NewReplacer(
		"Pct", " percent",
		"Id", " ID",
		"Reqs", " requests",
	).Replace(segment)
	var builder strings.Builder
	for index, char := range segment {
		if index > 0 && char >= 'A' && char <= 'Z' {
			builder.WriteByte(' ')
		}
		builder.WriteRune(char)
	}
	return diagnosticSentenceLabel(strings.ToLower(builder.String()))
}

func diagnosticFeatureLabel(feature string) string {
	feature = strings.ToLower(strings.TrimSpace(feature))
	switch {
	case feature == "ad.pmp.deal.id":
		return "Deal eligibility requirements"
	case feature == "external.pmp.deal":
		return "Private marketplace deal availability"
	case strings.HasPrefix(feature, "sitelet"):
		return "Eligible site inventory"
	case feature == "bid.recentbid":
		return "Recent-contact safeguards"
	case feature == "offer.bid.floor":
		return "Bid competitiveness against inventory price floors"
	case feature == "ml.fraud.filter":
		return "Inventory quality and fraud safeguards"
	case strings.Contains(feature, "frequency.cap"):
		return "Audience frequency limits"
	case feature == "bid.multiplier":
		return "Bid adjustment settings"
	case feature == "offer.bid.negative":
		return "Negative bid adjustments"
	case strings.Contains(feature, ".timeout"):
		return "Optimization decision timing"
	case feature == "media.api.protocol.wrapper":
		return "Creative compatibility"
	case strings.Contains(feature, "brand.safety"):
		return "Brand-safety requirements"
	case strings.Contains(feature, "viewability"):
		return "Viewability requirements"
	case strings.HasPrefix(feature, "site.lists"):
		return "Site-list eligibility"
	case strings.HasPrefix(feature, "profile"):
		return "Combined targeting requirements"
	}
	if feature == "" {
		return "Additional delivery factor"
	}
	return "Additional delivery factor"
}

func diagnosticEvidenceRoleLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "primary":
		return "Primary contributor"
	case "secondary":
		return "Secondary contributor"
	case "late_stage":
		return "Downstream indicator"
	case "supporting":
		return "Supporting evidence"
	default:
		return "Additional evidence"
	}
}

func diagnosticSourceLabel(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "addiagnostic", "baseline":
		return "Campaign setup and pacing"
	case "adtargetingprofile":
		return "Targeting and optimization settings"
	case "forecasting":
		return "Reach and inventory forecast"
	case "metaorder":
		return "Order configuration"
	case "metricsadcube":
		return "Delivery performance"
	case "shaperrejection":
		return "Platform traffic controls"
	case "signalperformance":
		return "Targeting signal performance"
	case "sitemetricsadcube":
		return "Site-level delivery"
	case "sitemetricsadcubeshaperoverlap":
		return "Site delivery and traffic-control comparison"
	case "globalsupplyperformance":
		return "Market-wide supply comparison"
	case "supplyoptimizationperformance":
		return "Inventory-path optimization"
	case "":
		return "Diagnostic evidence"
	default:
		return "Diagnostic evidence"
	}
}

func diagnosticCoverageLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "complete", "full":
		return "Complete evidence"
	case "partial":
		return "Partial evidence"
	case "no_data":
		return "Some requested data had no matching activity"
	case "missing":
		return "Some evidence was unavailable"
	case "":
		return ""
	default:
		return "Available evidence"
	}
}

func diagnosticSurfaceLabels(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, diagnosticSourceLabel(value))
	}
	return result
}

func diagnosticUserFacingRestrictionRows(rows []map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(rows))
	indexByFactor := map[string]int{}
	for _, row := range rows {
		feature := stringValue(row["feature"])
		row["factor"] = diagnosticFeatureLabel(feature)
		row["sourceLabel"] = diagnosticSourceLabel(stringValue(row["source"]))
		row["observedFilterEvents"] = row["optimizationRejections"]
		row["estimatedAffectedOpportunities"] = row["estimatedOptimizationRejections"]
		row["pacingShortfall"] = row["pacingSpendShortfall"]
		row["approximateOpportunitiesNeeded"] = row["approximateMissingBids"]
		if value, ok := row["countAssessment"]; ok {
			row["countAssessmentLabel"] = diagnosticCountAssessmentLabel(stringValue(value))
		}
		if value, ok := row["classification"]; ok {
			row["impactRole"] = diagnosticEvidenceRoleLabel(stringValue(value))
		}
		delete(row, "feature")
		delete(row, "source")
		delete(row, "classification")
		delete(row, "countAssessment")
		delete(row, "optimizationRejections")
		delete(row, "estimatedOptimizationRejections")
		delete(row, "optimizationRejectionShare")
		delete(row, "featureRatio")
		delete(row, "pacingSpendShortfall")
		delete(row, "observedSpendPerBid")
		delete(row, "approximateMissingBids")
		delete(row, "estimatedGapCoverage")
		factorKey := strings.ToLower(strings.TrimSpace(stringValue(row["factor"])))
		if existingIndex, ok := indexByFactor[factorKey]; ok {
			existing := result[existingIndex]
			if diagnosticRestrictionCount(row) > diagnosticRestrictionCount(existing) {
				result[existingIndex] = row
			}
			continue
		}
		indexByFactor[factorKey] = len(result)
		result = append(result, row)
	}
	return result
}

func diagnosticRestrictionCount(row map[string]interface{}) float64 {
	if row == nil {
		return 0
	}
	if value := diagnosticNumericValue(row["estimatedAffectedOpportunities"]); value > 0 {
		return value
	}
	return diagnosticNumericValue(row["observedFilterEvents"])
}

func diagnosticNumericValue(value interface{}) float64 {
	switch actual := value.(type) {
	case float64:
		return actual
	case float32:
		return float64(actual)
	case int:
		return float64(actual)
	case int64:
		return float64(actual)
	case json.Number:
		result, _ := actual.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(strings.TrimSpace(actual), 64)
		return result
	default:
		return 0
	}
}

func diagnosticCountAssessmentLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "material":
		return "Large enough to materially explain the delivery gap"
	case "below_materiality_threshold":
		return "Too small to explain enough of the delivery gap"
	case "not_comparable":
		return "Not enough count data for a reliable comparison"
	default:
		return "Count comparison unavailable"
	}
}

func diagnosticUserFacingRows(rows []map[string]interface{}) []map[string]interface{} {
	for _, row := range rows {
		if value, ok := row["creativeTestRecommended"]; ok {
			if recommended, _ := value.(bool); recommended {
				row["creativeTestRecommendation"] = "Run Creative Tester"
			} else {
				row["creativeTestRecommendation"] = "Not indicated by current post-bid results"
			}
			delete(row, "creativeTestRecommended")
		}
		if value, ok := row["entityKind"]; ok {
			switch strings.ToLower(strings.TrimSpace(stringValue(value))) {
			case "ad_order":
				row["entityKind"] = "Ad order"
			case "audience", "line":
				row["entityKind"] = "Line"
			}
		}
		for _, key := range []string{"dailyPacingStatus", "flightPacingStatus", "tdBudgetStatus", "ydBudgetStatus"} {
			if value, ok := row[key]; ok {
				row[key] = diagnosticStatusLabel(stringValue(value))
			}
		}
		if value, ok := row["source"]; ok {
			row["source"] = diagnosticSourceLabel(stringValue(value))
		}
	}
	return rows
}

func diagnosticStatusLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "behind":
		return "Behind plan"
	case "on_track":
		return "On track"
	case "ahead":
		return "Ahead of plan"
	case "has_budget":
		return "Budget available"
	case "a_daily_budget":
		return "Daily budget configured"
	case "no_budget":
		return "No budget available"
	}
	return diagnosticSentenceLabel(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", " ")))
}

func diagnosticTone(classification, confidence string) string {
	if strings.EqualFold(strings.TrimSpace(classification), "DEEPER_PROOF_REQUIRED") || strings.EqualFold(strings.TrimSpace(confidence), "low") {
		return "warning"
	}
	return "info"
}

func diagnosticConfidenceTone(confidence string) string {
	switch strings.ToLower(strings.TrimSpace(confidence)) {
	case "high":
		return "success"
	case "low":
		return "warning"
	default:
		return "info"
	}
}

func diagnosticWindowLabel(scope map[string]interface{}) string {
	from := strings.TrimSpace(stringValue(scope["from"]))
	to := strings.TrimSpace(stringValue(scope["to"]))
	switch {
	case from != "" && to != "":
		return from + " through " + to
	case from != "":
		return "from " + from
	case to != "":
		return "through " + to
	default:
		return "the Diagnostic request window"
	}
}

func diagnosticSupportingFactRows(explanation map[string]interface{}) []map[string]interface{} {
	items, _ := explanation["supportingFacts"].([]interface{})
	result := make([]map[string]interface{}, 0, len(items))
	hasCountComparison := false
	for _, item := range items {
		path := strings.TrimSpace(stringValue(normalizeInterfaceMap(item)["path"]))
		if strings.HasSuffix(path, "approxMissingBids") || strings.Contains(path, "estimatedOptimizationRejections") {
			hasCountComparison = true
			break
		}
	}
	for _, item := range items {
		fact := normalizeInterfaceMap(item)
		if len(fact) == 0 {
			continue
		}
		path := strings.TrimSpace(stringValue(fact["path"]))
		if hasCountComparison && (strings.HasSuffix(path, "missingBidCoveragePct") || strings.HasSuffix(path, "deliveredBidSharePct")) {
			continue
		}
		value := fact["value"]
		if strings.HasSuffix(path, ".feature") {
			value = diagnosticFeatureLabel(stringValue(value))
		}
		result = append(result, map[string]interface{}{
			"path":   diagnosticEvidenceLabel(path),
			"value":  value,
			"source": diagnosticSourceLabel(stringValue(fact["source"])),
		})
	}
	return result
}

func diagnosticCoverageRows(coverage map[string]interface{}) []map[string]interface{} {
	var result []map[string]interface{}
	for _, item := range []struct {
		key    string
		status string
	}{
		{key: "loadedSurfaces", status: "loaded"},
		{key: "noDataEvidence", status: "no_data"},
		{key: "missingEvidence", status: "missing"},
		{key: "skippedSurfaces", status: "skipped"},
	} {
		for _, surface := range diagnosticStringList(coverage[item.key]) {
			result = append(result, map[string]interface{}{"status": diagnosticCoverageStatusLabel(item.status), "surface": diagnosticSourceLabel(surface)})
		}
	}
	return result
}

func diagnosticCoverageStatusLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "loaded":
		return "Included"
	case "no_data":
		return "Checked; no matching activity"
	case "missing":
		return "Unavailable"
	case "skipped":
		return "Not needed for this assessment"
	default:
		return diagnosticUserLabel(value)
	}
}

func diagnosticDatasetRows(payload map[string]interface{}, id string) []map[string]interface{} {
	datasets := normalizeInterfaceMap(payload["factDatasets"])
	dataset := normalizeInterfaceMap(datasets[id])
	if len(dataset) == 0 {
		return nil
	}
	columns := diagnosticOrderedStringList(dataset["columns"])
	if len(columns) == 0 {
		return nil
	}
	if rawRows, ok := dataset["rows"].([]interface{}); ok && len(rawRows) > 0 {
		result := make([]map[string]interface{}, 0, len(rawRows))
		for _, rawRow := range rawRows {
			values, ok := rawRow.([]interface{})
			if !ok {
				continue
			}
			result = append(result, diagnosticRow(columns, values))
		}
		if len(result) > 0 {
			return result
		}
	}
	csvText := strings.TrimSpace(stringValue(dataset["csv"]))
	if csvText == "" {
		return nil
	}
	records, err := csv.NewReader(strings.NewReader(csvText)).ReadAll()
	if err != nil || len(records) < 2 {
		return nil
	}
	header := records[0]
	result := make([]map[string]interface{}, 0, len(records)-1)
	for _, record := range records[1:] {
		values := make([]interface{}, len(record))
		for index, value := range record {
			values[index] = diagnosticCSVScalar(value)
		}
		result = append(result, diagnosticRow(header, values))
	}
	return result
}

func diagnosticOrderedStringList(value interface{}) []string {
	var result []string
	switch actual := value.(type) {
	case []interface{}:
		for _, item := range actual {
			if text := strings.TrimSpace(stringValue(item)); text != "" {
				result = append(result, text)
			}
		}
	case []string:
		for _, item := range actual {
			if text := strings.TrimSpace(item); text != "" {
				result = append(result, text)
			}
		}
	}
	return result
}

func diagnosticRow(columns []string, values []interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(columns))
	for index, column := range columns {
		if index < len(values) {
			result[column] = values[index]
		}
	}
	return result
}

func diagnosticCSVScalar(value string) interface{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		return parsed
	}
	if parsed, err := strconv.ParseFloat(value, 64); err == nil {
		return parsed
	}
	if parsed, err := strconv.ParseBool(value); err == nil {
		return parsed
	}
	return value
}

func diagnosticResultTitle(scope map[string]interface{}) string {
	if ids := diagnosticStringList(scope["audienceIds"]); len(ids) > 0 {
		return "Line " + strings.Join(ids, ", ") + " diagnostic"
	}
	if ids := diagnosticStringList(scope["adOrderIds"]); len(ids) > 0 {
		return "Ad order " + strings.Join(ids, ", ") + " diagnostic"
	}
	return "Steward diagnostic"
}

func diagnosticStringList(value interface{}) []string {
	var result []string
	switch actual := value.(type) {
	case []interface{}:
		for _, item := range actual {
			if text := diagnosticScalarString(item); text != "" {
				result = append(result, text)
			}
		}
	case []string:
		for _, item := range actual {
			if text := strings.TrimSpace(item); text != "" {
				result = append(result, text)
			}
		}
	case nil:
	default:
		if text := diagnosticScalarString(actual); text != "" {
			result = append(result, text)
		}
	}
	sort.Strings(result)
	return result
}

func diagnosticScalarString(value interface{}) string {
	switch actual := value.(type) {
	case float64:
		return strconv.FormatFloat(actual, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(actual), 'f', -1, 32)
	default:
		return strings.TrimSpace(stringValue(actual))
	}
}

func (s *Service) publishDirectActionAssistantMessage(ctx context.Context, input *QueryInput, text string) error {
	return s.publishAssistantMessageWithStatus(ctx, input, text, "completed")
}

func stringValue(v interface{}) string {
	if v == nil {
		return ""
	}
	switch actual := v.(type) {
	case string:
		return actual
	default:
		return fmt.Sprintf("%v", v)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeInterfaceMap(value interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]interface{}); ok {
		return mapped
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	result := map[string]interface{}{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	return result
}

func normalizeToolResult(result string) interface{} {
	result = strings.TrimSpace(result)
	if result == "" {
		return nil
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(result), &decoded); err == nil {
		return decoded
	}
	return nil
}

func (s *Service) annotateDirectActionExecution(input *QueryInput, action *intakesvc.DirectActionContext, result *string) {
	if input == nil || action == nil {
		return
	}
	tc := intakesvc.FromContext(input.Context)
	if tc == nil {
		return
	}
	resultText := ""
	var normalized interface{}
	if result != nil {
		resultText = strings.TrimSpace(*result)
		normalized = normalizeToolResult(resultText)
	}
	tc.DirectActionExecution = intakesvc.DirectActionExecutionContext{
		Executed:   true,
		ToolName:   strings.TrimSpace(action.ToolName),
		Result:     normalized,
		ResultText: resultText,
	}
	input.Context["intake.directActionExecuted"] = true
	input.Context["intake.directActionTool"] = strings.TrimSpace(action.ToolName)
	if normalized != nil {
		input.Context["intake.directActionResult"] = normalized
	}
	if resultText != "" {
		input.Context["intake.directActionResultText"] = resultText
	}
}
