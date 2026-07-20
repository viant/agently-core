package sdk

import (
	"encoding/json"
	"strings"
)

const renderedContentSchemaVersion = "1"

// NormalizeRenderedContent turns recognized Forge fences into an ordered,
// platform-neutral transcript contract. It deliberately retains malformed and
// unfinished fences as markdown so clients never invent a renderable surface.
func NormalizeRenderedContent(content string) *RenderedContent {
	return normalizeRenderedContent(content, true)
}

// normalizeRenderedContent distinguishes a completed assistant message from
// an intermediate streaming snapshot. Intermediate snapshots are renderable,
// but cannot become implicitly save/export eligible before a terminal event.
func normalizeRenderedContent(content string, complete bool) *RenderedContent {
	parts := scanRenderedContent(content)
	var output []*RenderedContentPart
	var diagnostics []*RenderedContentWarning
	hasForge := false
	for _, part := range parts {
		if part.kind != "fence" {
			appendRenderedText(&output, part.raw)
			continue
		}
		if !part.closed || (part.language != "forge-data" && part.language != "forge-ui" && part.language != "forge-report") {
			appendRenderedText(&output, part.raw)
			continue
		}
		switch part.language {
		case "forge-data":
			if len(part.body) > maxInlineReportDataBytes+maxInlineEnvelopeOverhead {
				appendRenderedText(&output, part.raw)
				diagnostics = append(diagnostics, &RenderedContentWarning{Code: "REPORT_DATA_LIMIT_EXCEEDED", Message: "A forge-data transaction exceeds the 5 MB static-data limit.", Fence: "forge-data", Path: "$", SuggestedFix: "Reduce or split the static datasource payload."})
				continue
			}
			var value struct {
				Version   int             `json:"version"`
				Scope     string          `json:"scope"`
				ReportRef string          `json:"reportRef"`
				Sequence  int             `json:"sequence"`
				ID        string          `json:"id"`
				Format    string          `json:"format"`
				Mode      string          `json:"mode"`
				Data      json.RawMessage `json:"data"`
			}
			if json.Unmarshal([]byte(part.body), &value) != nil || strings.TrimSpace(value.ID) == "" {
				attributes := renderedFenceAttributes(part.header)
				id := strings.TrimSpace(attributes["id"])
				format := strings.ToLower(strings.TrimSpace(attributes["format"]))
				payload := json.RawMessage(part.body)
				if format == "csv" {
					payload, _ = json.Marshal(part.body)
				}
				if id != "" && (format == "csv" || json.Valid(payload)) {
					hasForge = true
					output = append(output, &RenderedContentPart{Kind: "forgeData", Source: part.raw, Data: &RenderedData{
						ID: id, Format: format, Mode: strings.TrimSpace(attributes["mode"]), Payload: payload,
					}})
					continue
				}
				appendRenderedText(&output, part.raw)
				diagnostics = append(diagnostics, &RenderedContentWarning{Code: "invalid_forge_data", Message: "Forge data fence is not a valid named JSON payload.", Fence: "forge-data", Path: "$", SuggestedFix: "Provide a valid named forge-data JSON envelope."})
				continue
			}
			if value.Version != 2 && strings.TrimSpace(value.ReportRef) != "" {
				appendRenderedText(&output, part.raw)
				diagnostics = append(diagnostics, &RenderedContentWarning{
					Code: "REPORT_DATA_VERSION_REQUIRED", Message: "Progressive forge-data transactions require version 2.",
					Fence: "forge-data", Path: "version", SuggestedFix: "Set version to 2 when scope, reportRef, and sequence associate data with a progressive report.",
				})
				continue
			}
			hasForge = true
			output = append(output, &RenderedContentPart{Kind: "forgeData", Source: part.raw, Payload: append(json.RawMessage(nil), []byte(part.body)...), Data: &RenderedData{
				Version: value.Version, Scope: strings.TrimSpace(value.Scope), ReportRef: strings.TrimSpace(value.ReportRef),
				Sequence: value.Sequence, ID: strings.TrimSpace(value.ID), Format: strings.TrimSpace(value.Format),
				Mode: strings.TrimSpace(value.Mode), Payload: value.Data,
			}})
		case "forge-ui":
			var value json.RawMessage
			if json.Unmarshal([]byte(part.body), &value) != nil || len(value) == 0 {
				appendRenderedText(&output, part.raw)
				diagnostics = append(diagnostics, &RenderedContentWarning{Code: "invalid_forge_ui", Message: "Forge UI fence is not valid JSON.", Fence: "forge-ui", Path: "$", SuggestedFix: "Provide a valid forge-ui JSON document."})
				continue
			}
			hasForge = true
			output = append(output, &RenderedContentPart{Kind: "forgeUI", Language: part.language, Source: part.raw, Payload: value})
		case "forge-report":
			if len(part.body) > maxInlineReportSourceBytes {
				appendRenderedText(&output, part.raw)
				diagnostics = append(diagnostics, &RenderedContentWarning{Code: "REPORT_SOURCE_LIMIT_EXCEEDED", Message: "A forge-report transaction exceeds the 5 MB source limit.", Fence: "forge-report", Path: "$", SuggestedFix: "Reduce report metadata or split blocks across bounded transactions."})
				continue
			}
			var value RenderedReport
			if json.Unmarshal([]byte(part.body), &value) != nil || strings.TrimSpace(value.ID) == "" {
				appendRenderedText(&output, part.raw)
				diagnostics = append(diagnostics, &RenderedContentWarning{Code: "invalid_forge_report", Message: "Forge report fence is not a valid named JSON transaction.", Fence: "forge-report", Path: "$", SuggestedFix: "Provide a valid forge-report envelope with an id, sequence, and mode."})
				continue
			}
			value.Scope = strings.TrimSpace(value.Scope)
			value.ID = strings.TrimSpace(value.ID)
			value.Mode = strings.ToLower(strings.TrimSpace(value.Mode))
			value.Grammar = strings.ToLower(strings.TrimSpace(value.Grammar))
			value.Payload = append(json.RawMessage(nil), []byte(part.body)...)
			hasForge = true
			output = append(output, &RenderedContentPart{Kind: "forgeReport", Language: part.language, Source: part.raw, Payload: value.Payload, Report: &value})
		}
	}
	if !hasForge && len(diagnostics) == 0 {
		return nil
	}
	reports, reportDiagnostics := assembleRenderedReports(output, complete)
	diagnostics = append(diagnostics, reportDiagnostics...)
	return &RenderedContent{SchemaVersion: renderedContentSchemaVersion, Parts: output, Reports: reports, Diagnostics: diagnostics}
}

func hydrateRenderedContent(state *ConversationState) {
	if state == nil {
		return
	}
	for _, turn := range state.Turns {
		if turn == nil {
			continue
		}
		for _, message := range turn.Messages {
			if message != nil && strings.EqualFold(message.Role, "assistant") {
				message.RenderedContent = NormalizeRenderedContent(message.Content)
			}
		}
		if turn.Assistant != nil {
			assistantMessages := append([]*AssistantMessageState{turn.Assistant.Narration, turn.Assistant.Final}, turn.Assistant.Messages...)
			for _, message := range assistantMessages {
				if message != nil {
					message.RenderedContent = NormalizeRenderedContent(message.Content)
				}
			}
		}
		if turn.Execution != nil {
			for _, page := range turn.Execution.Pages {
				if page != nil {
					page.RenderedContent = NormalizeRenderedContent(page.Content)
				}
			}
		}
	}
}

type renderedFencePart struct {
	kind, raw, language, header, body string
	closed                            bool
}

func scanRenderedContent(content string) []renderedFencePart {
	if content == "" {
		return nil
	}
	const marker = "```"
	var out []renderedFencePart
	for cursor := 0; cursor < len(content); {
		open := strings.Index(content[cursor:], marker)
		if open < 0 {
			out = append(out, renderedFencePart{kind: "text", raw: content[cursor:]})
			break
		}
		open += cursor
		if open > cursor {
			out = append(out, renderedFencePart{kind: "text", raw: content[cursor:open]})
		}
		languageStart := open + len(marker)
		languageEnd := languageStart
		for languageEnd < len(content) && renderedLanguageByte(content[languageEnd]) {
			languageEnd++
		}
		header, bodyStart, ok := renderedFenceHeaderAndBodyStart(content, languageEnd)
		if !ok {
			out = append(out, renderedFencePart{kind: "text", raw: marker})
			cursor = languageStart
			continue
		}
		language := strings.ToLower(content[languageStart:languageEnd])
		close := renderedFenceClose(content, bodyStart, language)
		if close < 0 {
			out = append(out, renderedFencePart{kind: "fence", raw: content[open:], language: language, header: header, body: content[bodyStart:]})
			break
		}
		out = append(out, renderedFencePart{kind: "fence", raw: content[open : close+len(marker)], language: language, header: header, body: content[bodyStart:close], closed: true})
		cursor = close + len(marker)
	}
	return out
}

func renderedFenceClose(content string, bodyStart int, language string) int {
	const marker = "```"
	for cursor := bodyStart; cursor < len(content); {
		offset := strings.Index(content[cursor:], marker)
		if offset < 0 {
			return -1
		}
		close := cursor + offset
		if language != "forge-data" && language != "forge-ui" && language != "forge-report" ||
			json.Valid([]byte(strings.TrimSpace(content[bodyStart:close]))) ||
			close == bodyStart || content[close-1] == '\n' || content[close-1] == '\r' {
			return close
		}
		cursor = close + len(marker)
	}
	return -1
}

func renderedFenceHeaderAndBodyStart(content string, afterLanguage int) (string, int, bool) {
	if afterLanguage >= len(content) {
		return "", 0, false
	}
	switch content[afterLanguage] {
	case '\n':
		return "", afterLanguage + 1, true
	case '\r':
		if afterLanguage+1 < len(content) && content[afterLanguage+1] == '\n' {
			return "", afterLanguage + 2, true
		}
	case '{', '[':
		return "", afterLanguage, true
	case ' ', '\t':
		newline := afterLanguage
		for newline < len(content) && content[newline] != '\n' && content[newline] != '\r' {
			newline++
		}
		if newline == len(content) {
			return "", 0, false
		}
		if content[newline] == '\n' {
			return strings.TrimSpace(content[afterLanguage:newline]), newline + 1, true
		}
		if newline+1 < len(content) && content[newline+1] == '\n' {
			return strings.TrimSpace(content[afterLanguage:newline]), newline + 2, true
		}
	}
	return "", 0, false
}

func renderedLanguageByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_' || value == '+' || value == '-'
}

func appendRenderedText(parts *[]*RenderedContentPart, value string) {
	if value == "" {
		return
	}
	if len(*parts) > 0 && (*parts)[len(*parts)-1].Kind == "markdown" {
		(*parts)[len(*parts)-1].Text += value
		return
	}
	*parts = append(*parts, &RenderedContentPart{Kind: "markdown", Text: value})
}

func renderedFenceAttributes(header string) map[string]string {
	attributes := map[string]string{}
	for cursor := 0; cursor < len(header); {
		for cursor < len(header) && (header[cursor] == ' ' || header[cursor] == '\t') {
			cursor++
		}
		keyStart := cursor
		for cursor < len(header) && (renderedLanguageByte(header[cursor]) || header[cursor] == '-') {
			cursor++
		}
		if keyStart == cursor || cursor >= len(header) || header[cursor] != '=' {
			return attributes
		}
		key := strings.ToLower(header[keyStart:cursor])
		cursor++
		valueStart := cursor
		if cursor < len(header) && (header[cursor] == '"' || header[cursor] == '\'') {
			quote := header[cursor]
			cursor++
			valueStart = cursor
			for cursor < len(header) && header[cursor] != quote {
				cursor++
			}
			if cursor >= len(header) {
				return attributes
			}
			attributes[key] = header[valueStart:cursor]
			cursor++
			continue
		}
		for cursor < len(header) && header[cursor] != ' ' && header[cursor] != '\t' {
			cursor++
		}
		attributes[key] = header[valueStart:cursor]
	}
	return attributes
}
