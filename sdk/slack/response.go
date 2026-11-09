// Package slack provides deterministic presentation of completed Agently
// responses. Transport, authentication, persistence and delivery remain with
// the caller. Presentation never modifies the source conversation.
package slack

import (
	"fmt"
	"strings"

	"github.com/viant/agently-core/sdk"
)

// ResponseMode selects how a completed canonical response is presented.
type ResponseMode string

const (
	ResponseRaw               ResponseMode = "raw"
	ResponseCLICompatible     ResponseMode = "cli_compatible"
	ResponseSummary           ResponseMode = "summary"
	ResponseNormalizedSummary ResponseMode = "normalized_summary"
	ResponseReportText        ResponseMode = "report_text"
)

// Validate accepts the zero value as raw for existing callers/configurations.
func (m ResponseMode) Validate() error {
	switch m {
	case "", ResponseRaw, ResponseCLICompatible, ResponseSummary, ResponseNormalizedSummary, ResponseReportText:
		return nil
	default:
		return fmt.Errorf("response mode must be raw, cli_compatible, summary, normalized_summary or report_text")
	}
}

type ResponseInput struct {
	// Content is the exact turn's canonical final. StandaloneMessages must be
	// selected with sdk.SelectStandaloneAssistantMessages from that same turn.
	Content            string
	StandaloneMessages []*sdk.TurnMessageState
	ConversationURL    string
	Mode               ResponseMode
}

type ResponseOutput struct {
	Text string
	// Diagnostics contains fixed codes only, suitable for logging. It never
	// includes report IDs, authored fields, source payloads or native messages.
	Diagnostics []string
}

const (
	interactiveNotice = "Some content could not be presented here. View the full response in Agently."
	additionalNotice  = "Additional interactive content is available in Agently."
	summaryTruncated  = "\n[Summary truncated; see transcript for the full report.]"
)

// PresentResponse presents the supplied public standalone messages followed by
// the canonical final. It performs no I/O and never modifies canonical state.
// A supplied conversation link is first in every mode, including raw. Each
// message is projected separately: report transactions never cross messages.
// Standalone messages with a nonempty ID use the first occurrence of that ID;
// messages without IDs and the final remain independent.
// Unknown modes return an error; presentation limitations return fixed codes.
func PresentResponse(input ResponseInput) (ResponseOutput, error) {
	if err := input.Mode.Validate(); err != nil {
		return ResponseOutput{}, err
	}
	var output ResponseOutput
	var sections []string
	if link := strings.TrimSpace(input.ConversationURL); link != "" {
		sections = append(sections, "[View full response in Agently]("+link+")")
	}
	contents := make([]string, 0, len(input.StandaloneMessages)+1)
	seen := map[string]bool{}
	for _, message := range input.StandaloneMessages {
		if message == nil {
			continue
		}
		if message.MessageID != "" {
			if seen[message.MessageID] {
				continue
			}
			seen[message.MessageID] = true
		}
		contents = append(contents, message.Content)
	}
	contents = append(contents, input.Content)
	limit := presentationComplete
	for _, content := range contents {
		if input.Mode == "" || input.Mode == ResponseRaw {
			if content != "" {
				sections = append(sections, content)
			}
			continue
		}
		text, messageLimit, warning := presentMessage(content, input.Mode)
		if text != "" {
			sections = append(sections, text)
		}
		limit = max(limit, messageLimit)
		if warning && len(output.Diagnostics) == 0 {
			output.Diagnostics = append(output.Diagnostics, "native_rendering_warning")
		}
	}
	if limit != presentationComplete {
		output.Diagnostics = append(output.Diagnostics, "interactive_content_not_shown")
		notice := additionalNotice
		if limit == presentationIncomplete {
			notice = interactiveNotice
		}
		sections = append(sections, notice)
	}
	output.Text = strings.Join(sections, "\n\n")
	return output, nil
}

// A presentation limitation is not validation. Parsing, transaction assembly and
// producer diagnostics remain owned by the native projector and normalizer.
type presentationLimit int

const (
	presentationComplete presentationLimit = iota
	presentationOmitted
	presentationIncomplete
)

func presentMessage(content string, mode ResponseMode) (string, presentationLimit, bool) {
	placeholder := ""
	if mode == ResponseCLICompatible {
		placeholder = "[Interactive content]"
	}
	prose, rich := sdk.ProjectTextContent(content, placeholder)
	sections := []string{strings.TrimSpace(prose)}
	if !rich {
		return joinText(sections), presentationComplete, false
	}
	var reports []reportText
	rendered := sdk.NormalizeRenderedContent(content)
	limit := presentationComplete
	warning := false
	if rendered == nil {
		limit = presentationIncomplete
	} else {
		if len(rendered.Diagnostics) > 0 {
			limit, warning = presentationIncomplete, true
		}
		for _, part := range rendered.Parts {
			if part == nil {
				continue
			}
			// Native normalization retains malformed/open fences as markdown.
			// The native projector hides those rather than leaking them as prose.
			if _, hidden := sdk.ProjectTextContent(part.Text, ""); hidden {
				limit = presentationIncomplete
			}
			if part.Kind == "forgeUI" || part.Kind == "forgeData" {
				limit = max(limit, presentationOmitted)
			}
			if part.Data != nil && part.Data.Version != 2 {
				limit = presentationIncomplete
			}
		}
	}
	switch mode {
	case ResponseCLICompatible, ResponseSummary:
		var pending bool
		reports, pending = compatibleSummaries(content)
		if pending {
			limit = presentationIncomplete
		}
		// Use native assembled metadata to describe omissions, while preserving
		// the frozen start/commit extraction above unchanged.
		if rendered != nil {
			for _, report := range rendered.Reports {
				if report == nil || report.Status != "committed" {
					limit = presentationIncomplete
					continue
				}
				_, reportLimit := presentReport(report, ResponseNormalizedSummary)
				limit = max(limit, reportLimit)
			}
		}
	case ResponseNormalizedSummary, ResponseReportText:
		if rendered != nil {
			for _, report := range rendered.Reports {
				if report == nil || report.Status != "committed" {
					limit = presentationIncomplete
					continue
				}
				text, reportLimit := presentReport(report, mode)
				limit = max(limit, reportLimit)
				if text.body != "" {
					reports = append(reports, text)
				}
			}
		}
	}
	for _, report := range reports {
		var hiddenTitle, hiddenBody bool
		report.title, hiddenTitle = sdk.ProjectTextContent(report.title, placeholder)
		report.body, hiddenBody = sdk.ProjectTextContent(report.body, placeholder)
		if hiddenTitle || hiddenBody {
			limit = presentationIncomplete
		}
		title := strings.TrimSpace(report.title)
		if title == "" {
			title = "Report"
		}
		heading := "### " + title
		if mode == ResponseCLICompatible {
			heading = "[report] " + report.title
		}
		sections = append(sections, heading+"\n"+report.body)
	}
	// A valid UI/data-only response is an intentional omission. A report with
	// no usable summary is an incomplete presentation, even beside another report.
	if len(reports) == 0 && limit == presentationComplete {
		limit = presentationIncomplete
	}
	return joinText(sections), limit, warning
}

func joinText(sections []string) string {
	var nonempty []string
	for _, section := range sections {
		if strings.TrimSpace(section) != "" {
			nonempty = append(nonempty, section)
		}
	}
	return strings.Join(nonempty, "\n\n")
}

func boundedSummary(text string) string {
	runes := []rune(text)
	if len(runes) > 8192 {
		return string(runes[:8192]) + summaryTruncated
	}
	return text
}
