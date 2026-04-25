package narrator

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	asynccfg "github.com/viant/agently-core/protocol/async"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// ErrNarratorTimeout indicates the LLM runner exceeded the per-call
// budget. Callers typically treat it as a silent skip.
var ErrNarratorTimeout = errors.New("narrator llm runner timed out")

// effectiveLLMTimeout is the currently configured bound applied to LLM
// narrator invocations when no ctx-scoped override is present. Starts
// at zero (meaning "no timeout"); bootstrap MUST set it via
// SetLLMTimeout using the workspace `default.async.narrator.llmTimeout`
// baseline. This package holds no defaults of its own — the
// authoritative default lives in
// `workspace/config.DefaultsWithFallback`.
var effectiveLLMTimeout time.Duration

// SetLLMTimeout replaces the package-level LLM timeout used by
// runLLMRunner when no ctx-scoped override is present. Typically called
// once at application bootstrap from the workspace-config applier.
// Passing a non-positive value means "no timeout" (runner ctx used
// as-is).
func SetLLMTimeout(d time.Duration) {
	if d <= 0 {
		effectiveLLMTimeout = 0
		return
	}
	effectiveLLMTimeout = d
}

type llmTimeoutKey struct{}

// WithLLMTimeout attaches a per-call timeout override to ctx. Used by
// request-scoped code paths that need a different bound than the
// package-level SetLLMTimeout value (e.g. admin flows, tests). A
// non-positive duration is ignored.
func WithLLMTimeout(ctx context.Context, d time.Duration) context.Context {
	if ctx == nil || d <= 0 {
		return ctx
	}
	return context.WithValue(ctx, llmTimeoutKey{}, d)
}

func llmTimeoutFromContext(ctx context.Context) time.Duration {
	if ctx == nil {
		return effectiveLLMTimeout
	}
	if v, ok := ctx.Value(llmTimeoutKey{}).(time.Duration); ok && v > 0 {
		return v
	}
	return effectiveLLMTimeout
}

// runLLMRunner invokes the configured LLM runner with a bounded
// context. On timeout, returns ("", ErrNarratorTimeout). On any other
// runner error, returns ("", err). Otherwise returns the trimmed text
// (which may be empty, causing the caller to fall through to the
// deterministic fallback ladder).
func runLLMRunner(ctx context.Context, runner LLMRunner, in LLMInput) (string, error) {
	if runner == nil {
		return "", fmt.Errorf("async narrator llm mode not configured")
	}
	runCtx := ctx
	if bound := llmTimeoutFromContext(ctx); bound > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, bound)
		defer cancel()
	}
	text, err := runner(runCtx, in)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return "", ErrNarratorTimeout
		}
		return "", err
	}
	// Even when the runner returns nil error, a cancelled runCtx means
	// the bound tripped after the runner returned. Normalize to timeout
	// so callers can treat it uniformly.
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return "", ErrNarratorTimeout
	}
	return strings.TrimSpace(text), nil
}

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

func StartNarration(ctx context.Context, cfg *asynccfg.Config, rec *asynccfg.OperationRecord) (string, error) {
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
		text, err := runLLMRunner(ctx, llmRunnerFromContext(ctx), LLMInput{
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
		if text == "" {
			return fallback(userAsk, rec.OperationIntent, rec.OperationSummary, rec.Message, rec.Status, rec.ToolName), nil
		}
		return text, nil
	default:
		return fallback(userAsk, rec.OperationIntent, rec.OperationSummary, rec.Message, rec.Status, rec.ToolName), nil
	}
}

func UpdateNarration(ctx context.Context, cfg *asynccfg.Config, ev asynccfg.ChangeEvent) (string, error) {
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
		text, err := runLLMRunner(ctx, llmRunnerFromContext(ctx), LLMInput{
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
