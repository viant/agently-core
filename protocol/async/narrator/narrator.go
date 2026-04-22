package narrator

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	asynccfg "github.com/viant/agently-core/protocol/async"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

type LLMInput struct {
	OperationID string
	UserAsk     string
	Intent      string
	Summary     string
	Message     string
	Status      string
	Tool        string
}

type LLMRunner func(context.Context, LLMInput) (string, error)

type llmRunnerKey struct{}

func WithLLMRunner(ctx context.Context, runner LLMRunner) context.Context {
	if ctx == nil || runner == nil {
		return ctx
	}
	return context.WithValue(ctx, llmRunnerKey{}, runner)
}

func llmRunnerFromContext(ctx context.Context) LLMRunner {
	if ctx == nil {
		return nil
	}
	runner, _ := ctx.Value(llmRunnerKey{}).(LLMRunner)
	return runner
}

func StartPreamble(ctx context.Context, cfg *asynccfg.Config, rec *asynccfg.OperationRecord) (string, error) {
	if rec == nil {
		return "", nil
	}
	userAsk := runtimerequestctx.UserAskFromContext(ctx)
	mode := narrationMode(cfg)
	switch mode {
	case "none":
		return "", nil
	case "keydata":
		text := renderKeyData(rec.KeyData, rec.Message)
		if text == "" {
			return fallback(userAsk, rec.OperationIntent, rec.OperationSummary, rec.Message, rec.Status, rec.ToolName), nil
		}
		return text, nil
	case "template":
		return renderTemplate(cfg, userAsk, rec.OperationIntent, rec.OperationSummary, rec.Message, rec.Status, rec.ToolName), nil
	case "llm":
		runner := llmRunnerFromContext(ctx)
		if runner == nil {
			return "", fmt.Errorf("async narrator llm mode not configured")
		}
		text, err := runner(ctx, LLMInput{
			OperationID: rec.ID,
			UserAsk:     userAsk,
			Intent:      rec.OperationIntent,
			Summary:     rec.OperationSummary,
			Message:     rec.Message,
			Status:      rec.Status,
			Tool:        rec.ToolName,
		})
		if err != nil {
			return "", err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return fallback(userAsk, rec.OperationIntent, rec.OperationSummary, rec.Message, rec.Status, rec.ToolName), nil
		}
		return text, nil
	default:
		return fallback(userAsk, rec.OperationIntent, rec.OperationSummary, rec.Message, rec.Status, rec.ToolName), nil
	}
}

func UpdatePreamble(ctx context.Context, cfg *asynccfg.Config, ev asynccfg.ChangeEvent) (string, error) {
	userAsk := runtimerequestctx.UserAskFromContext(ctx)
	mode := narrationMode(cfg)
	switch mode {
	case "none":
		return "", nil
	case "keydata":
		text := renderKeyData(ev.KeyData, ev.Message)
		if text == "" {
			return fallback(userAsk, ev.Intent, ev.Summary, ev.Message, ev.Status, ev.ToolName), nil
		}
		return text, nil
	case "template":
		return renderTemplate(cfg, userAsk, ev.Intent, ev.Summary, ev.Message, ev.Status, ev.ToolName), nil
	case "llm":
		runner := llmRunnerFromContext(ctx)
		if runner == nil {
			return "", fmt.Errorf("async narrator llm mode not configured")
		}
		text, err := runner(ctx, LLMInput{
			OperationID: ev.OperationID,
			UserAsk:     userAsk,
			Intent:      ev.Intent,
			Summary:     ev.Summary,
			Message:     ev.Message,
			Status:      ev.Status,
			Tool:        ev.ToolName,
		})
		if err != nil {
			return "", err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return fallback(userAsk, ev.Intent, ev.Summary, ev.Message, ev.Status, ev.ToolName), nil
		}
		return text, nil
	default:
		return fallback(userAsk, ev.Intent, ev.Summary, ev.Message, ev.Status, ev.ToolName), nil
	}
}

func narrationMode(cfg *asynccfg.Config) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(cfg.Narration))
}

func renderTemplate(cfg *asynccfg.Config, userAsk, intent, summary, message, status, tool string) string {
	if cfg == nil {
		return fallback(userAsk, intent, summary, message, status, tool)
	}
	tpl := strings.TrimSpace(cfg.NarrationTemplate)
	if tpl == "" {
		return fallback(userAsk, intent, summary, message, status, tool)
	}
	repl := strings.NewReplacer(
		"{{user_ask}}", strings.TrimSpace(userAsk),
		"{{intent}}", strings.TrimSpace(intent),
		"{{summary}}", strings.TrimSpace(summary),
		"{{message}}", strings.TrimSpace(message),
		"{{status}}", strings.TrimSpace(status),
		"{{tool}}", strings.TrimSpace(tool),
	)
	return strings.TrimSpace(strings.Join(strings.Fields(repl.Replace(tpl)), " "))
}

func fallback(userAsk, intent, summary, message, status, tool string) string {
	if text := strings.TrimSpace(userAsk); text != "" {
		if msg := strings.TrimSpace(message); msg != "" {
			return text + ": " + msg
		}
		return text
	}
	if text := strings.TrimSpace(intent); text != "" {
		if msg := strings.TrimSpace(message); msg != "" {
			return text + ": " + msg
		}
		return text
	}
	if text := strings.TrimSpace(summary); text != "" {
		if msg := strings.TrimSpace(message); msg != "" {
			return text + ": " + msg
		}
		return text
	}
	if msg := strings.TrimSpace(message); msg != "" {
		return msg
	}
	if t := strings.TrimSpace(tool); t != "" && strings.TrimSpace(status) != "" {
		return t + ": " + strings.TrimSpace(status)
	}
	if t := strings.TrimSpace(tool); t != "" {
		return t
	}
	return ""
}

var (
	htmlCommentPattern = regexp.MustCompile(`<!--[\s\S]*?-->`)
	dataSourcePattern  = regexp.MustCompile(`DATA:([A-Za-z0-9_-]+)`)
)

func renderKeyData(raw []byte, message string) string {
	if text := summarizeRichText(string(raw)); text != "" {
		return text
	}
	return summarizeRichText(message)
}

func summarizeRichText(text string) string {
	source := strings.TrimSpace(text)
	if source == "" {
		return ""
	}
	source = htmlCommentPattern.ReplaceAllString(source, "\n")
	source = strings.ReplaceAll(source, "\r\n", "\n")
	source = strings.ReplaceAll(source, "\r", "\n")
	if idx := strings.Index(source, "```"); idx >= 0 {
		source = source[:idx]
	}
	lines := strings.Split(source, "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(parts) > 0 {
				break
			}
			continue
		}
		parts = append(parts, line)
	}
	joined := strings.TrimSpace(strings.Join(parts, " "))
	joined = strings.Join(strings.Fields(joined), " ")
	if joined != "" && !looksLikeOpaqueData(joined) {
		return truncateSentence(joined, 220)
	}
	matches := dataSourcePattern.FindAllStringSubmatch(text, 3)
	if len(matches) > 0 {
		names := make([]string, 0, len(matches))
		for _, match := range matches {
			name := strings.TrimSpace(match[1])
			if name == "" {
				continue
			}
			names = append(names, humanizeKey(name))
		}
		if len(names) > 0 {
			return "Reviewing " + strings.Join(names, ", ") + "."
		}
	}
	return ""
}

func looksLikeOpaqueData(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return true
	}
	alpha := 0
	for _, r := range trimmed {
		if unicode.IsLetter(r) {
			alpha++
		}
	}
	return alpha == 0
}

func truncateSentence(text string, max int) string {
	if max <= 0 || len([]rune(text)) <= max {
		return text
	}
	runes := []rune(text)
	cut := string(runes[:max])
	lastBoundary := strings.LastIndexAny(cut, ".!?")
	if lastBoundary >= 0 && lastBoundary >= max/2 {
		return strings.TrimSpace(cut[:lastBoundary+1])
	}
	lastSpace := strings.LastIndex(cut, " ")
	if lastSpace > 0 {
		return strings.TrimSpace(cut[:lastSpace]) + "…"
	}
	return strings.TrimSpace(cut) + "…"
}

func humanizeKey(text string) string {
	text = strings.ReplaceAll(strings.TrimSpace(text), "_", " ")
	text = strings.ReplaceAll(text, "-", " ")
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	runes := []rune(strings.ToLower(text))
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
