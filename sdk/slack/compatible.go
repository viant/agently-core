package slack

import (
	"encoding/json"
	"regexp"
)

// Frozen CLI extraction baseline. Deliberately independent of the CLI package:
// changing CLI presentation must not silently change Slack presentation. This
// small extractor is not a transaction assembler: native modes use the SDK's
// NormalizeRenderedContent instead. Keep its regex, raw identity, start/commit
// matching, commit order and first-markdown semantics covered by golden tests.
var compatibleReportFence = regexp.MustCompile("(?s)```forge-report\\s*\\n(.*?)\\n```")

type reportText struct{ title, body string }

func compatibleSummaries(content string) ([]reportText, bool) {
	pending := map[string]reportText{}
	missingSummary := map[string]bool{}
	var result []reportText
	malformed := false
	for _, fence := range compatibleReportFence.FindAllStringSubmatch(content, -1) {
		var report struct {
			Scope, ID, Mode, Title string
			Blocks                 []struct{ Kind, Markdown string }
		}
		if json.Unmarshal([]byte(fence[1]), &report) != nil {
			malformed = true
			continue
		}
		key := report.Scope + "\x00" + report.ID
		switch report.Mode {
		case "start":
			delete(pending, key)
			missingSummary[key] = true
			for _, block := range report.Blocks {
				if block.Kind == "markdownBlock" && block.Markdown != "" {
					pending[key] = reportText{report.Title, boundedSummary(block.Markdown)}
					delete(missingSummary, key)
					break
				}
			}
		case "commit":
			if missingSummary[key] {
				malformed = true
				delete(missingSummary, key)
			}
			if summary, ok := pending[key]; ok {
				result = append(result, summary)
				delete(pending, key)
			}
		}
	}
	return result, malformed || len(pending) > 0 || len(missingSummary) > 0
}
