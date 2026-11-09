package slack

import (
	"encoding/json"
	"strings"

	"github.com/viant/agently-core/sdk"
)

// Read only authored text, not arbitrary string values in datasets, chart
// options, expressions or browser configuration. Source is already assembled
// by the native SDK; this code does not validate or apply transactions.
type authoredReport struct {
	Title, Subtitle, Description string
	Blocks                       []map[string]json.RawMessage
}

func presentReport(report *sdk.RenderedReportAssembly, mode ResponseMode) (reportText, presentationLimit) {
	var source authoredReport
	if json.Unmarshal(report.Source, &source) != nil {
		return reportText{}, presentationIncomplete
	}
	if mode == ResponseNormalizedSummary {
		for _, block := range source.Blocks {
			if textField(block, "kind") == "markdownBlock" {
				if text := textField(block, "markdown"); text != "" {
					limit := presentationComplete
					if len(source.Blocks) > 1 || source.Subtitle != "" || source.Description != "" || len([]rune(text)) > 8192 {
						limit = presentationOmitted
					}
					return reportText{source.Title, boundedSummary(text)}, limit
				}
			}
		}
		return reportText{}, presentationIncomplete
	}
	if report.Grammar != "report-document-v1" {
		return reportText{}, presentationIncomplete
	}
	sections := []string{source.Subtitle, source.Description}
	limit := presentationComplete
	// Native canonical Source has a flat block list. Referenced children appear
	// there once; following blockIds/childBlockIds/sectionIds would duplicate them.
	for _, block := range source.Blocks {
		var fields []string
		switch textField(block, "kind") {
		case "markdownBlock":
			fields = []string{"markdown"}
		case "infoPanelBlock":
			fields = []string{"eyebrow", "description", "body"}
		case "calloutBlock":
			fields = []string{"description", "body"}
		case "sectionBlock":
			fields = []string{"subtitle", "description"}
		case "compositeBlock", "tabGroupBlock":
			fields = []string{"description"}
		case "chartBlock", "kpiBlock", "tableBlock":
			// Browser-only data presentation is intentionally omitted.
			limit = max(limit, presentationOmitted)
			continue
		default:
			limit = presentationIncomplete
			continue
		}
		if title := textField(block, "title"); title != "" {
			sections = append(sections, "#### "+title)
		}
		for _, field := range fields {
			sections = append(sections, textField(block, field))
		}
	}
	body := joinText(sections)
	if strings.TrimSpace(body) == "" {
		limit = presentationIncomplete
	}
	return reportText{source.Title, body}, limit
}

func textField(block map[string]json.RawMessage, key string) string {
	var text string
	_ = json.Unmarshal(block[key], &text)
	return text
}
