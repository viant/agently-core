package narrator

import (
	"context"
	"fmt"
	"strings"

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
