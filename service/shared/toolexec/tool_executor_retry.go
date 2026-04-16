package toolexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	plan "github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/protocol/tool"
)

const defaultToolCallAttempts = 2

var maxRetryDuration = 5 * time.Second

// executeToolWithRetry runs a tool and retries once on transient context cancellations.
func executeToolWithRetry(ctx context.Context, reg tool.Registry, step StepInfo, conv apiconv.Client) (plan.ToolCall, string, error) {
	attempts := defaultToolCallAttempts
	if attempts < 1 {
		attempts = 1
	}
	var out plan.ToolCall
	var result string
	var execErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx, cancel := toolExecContext(ctx)
		parentDeadline, parentRemaining := formatContextDeadline(ctx)
		attemptDeadline, attemptRemaining := formatContextDeadline(attemptCtx)
		if argsTimeoutMs, ok := timeoutMsFromArgs(step.Args); ok {
			logx.DebugCtxf(ctx, "conversation", "tool attempt start tool=%q op_id=%q attempt=%d/%d args_timeout_ms=%d parent_deadline=%q parent_remaining=%q attempt_deadline=%q attempt_remaining=%q", strings.TrimSpace(step.Name), strings.TrimSpace(step.ID), attempt, attempts, argsTimeoutMs, parentDeadline, parentRemaining, attemptDeadline, attemptRemaining)
		} else {
			logx.DebugCtxf(ctx, "conversation", "tool attempt start tool=%q op_id=%q attempt=%d/%d args_timeout_ms=none parent_deadline=%q parent_remaining=%q attempt_deadline=%q attempt_remaining=%q", strings.TrimSpace(step.Name), strings.TrimSpace(step.ID), attempt, attempts, parentDeadline, parentRemaining, attemptDeadline, attemptRemaining)
		}
		started := time.Now()
		out, result, execErr = executeTool(attemptCtx, reg, step, conv)
		attemptCtxErr := attemptCtx.Err()
		cancel()
		elapsed := time.Since(started)
		if execErr != nil {
			cause := classifyTimeoutCause(ctx, attemptCtxErr, execErr)
			logx.WarnCtxf(ctx, "conversation", "tool attempt end tool=%q op_id=%q attempt=%d/%d elapsed=%s cause=%q err=%q attempt_ctx_err=%q parent_ctx_err=%q", strings.TrimSpace(step.Name), strings.TrimSpace(step.ID), attempt, attempts, elapsed.String(), strings.TrimSpace(cause), strings.TrimSpace(execErr.Error()), strings.TrimSpace(errorString(attemptCtxErr)), strings.TrimSpace(formatContextErr(ctx)))
			debugMCPAuthFailure(ctx, step, "", execErr)
		} else {
			logx.DebugCtxf(ctx, "conversation", "tool attempt end tool=%q op_id=%q attempt=%d/%d elapsed=%s status=ok", strings.TrimSpace(step.Name), strings.TrimSpace(step.ID), attempt, attempts, elapsed.String())
			debugMCPAuthFailure(ctx, step, result, nil)
		}
		if execErr == nil || !shouldRetryToolCall(ctx, execErr, elapsed, attempt, attempts) {
			break
		}
		logx.DebugCtxf(ctx, "executil", "tool %s attempt %d/%d failed after %s with %v; retrying", step.Name, attempt, attempts, elapsed, execErr)
	}
	return out, result, execErr
}

func debugMCPAuthFailure(ctx context.Context, step StepInfo, result string, execErr error) {
	if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_MCP_AUTH")) == "" {
		return
	}
	server := toolServer(strings.TrimSpace(step.Name))
	msg := ""
	switch {
	case execErr != nil:
		msg = strings.TrimSpace(execErr.Error())
	case strings.EqualFold(strings.TrimSpace(result), "Unauthorized"):
		msg = strings.TrimSpace(result)
	}
	lower := strings.ToLower(msg)
	if msg == "" || (!strings.Contains(lower, "unauthorized") && !strings.Contains(lower, "forbidden")) {
		return
	}
	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	provider := strings.TrimSpace(authctx.Provider(ctx))
	tb := authctx.TokensFromContext(ctx)
	accessFP := "none"
	idFP := "none"
	if tb != nil {
		accessFP = tokenFingerprint(tb.AccessToken)
		idFP = tokenFingerprint(tb.IDToken)
	}
	fmt.Fprintf(os.Stderr, "[mcp-auth] stage=tool_unauthorized server=%s tool=%s op_id=%s user=%q provider=%q access_sha=%s id_sha=%s err=%q\n",
		strings.TrimSpace(server), strings.TrimSpace(step.Name), strings.TrimSpace(step.ID), userID, provider, accessFP, idFP, msg)
}

func tokenFingerprint(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}

func toolServer(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if idx := strings.IndexAny(name, "/:"); idx > 0 {
		return strings.TrimSpace(name[:idx])
	}
	return ""
}

func shouldRetryToolCall(ctx context.Context, execErr error, elapsed time.Duration, attempt, maxAttempts int) bool {
	if execErr == nil {
		return false
	}
	if attempt >= maxAttempts {
		return false
	}
	if maxRetryDuration > 0 && elapsed > maxRetryDuration {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	return isContextCancellationError(execErr)
}

func isContextCancellationError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context canceled") || strings.Contains(msg, "context deadline")
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
