package executil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	plan "github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/debugtrace"
	queueread "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	queuew "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/memory"
)

const (
	SystemDocumentTag    = "system_doc"
	SystemDocumentMode   = "system_document"
	ResourceDocumentTag  = "resource_doc"
	finalizeWriteTimeout = 15 * time.Second
)

var errToolQueued = errors.New("tool execution queued for approval")

// StepInfo carries the tool step data needed for execution.
type StepInfo struct {
	ID   string
	Name string
	Args map[string]interface{}
	// ResponseID is the assistant response.id that requested this tool call
	ResponseID string
}

// ExecuteToolStep runs a tool via the registry, records transcript, and updates traces.
// Returns normalized plan.ToolCall, span and any combined error.
func ExecuteToolStep(ctx context.Context, reg tool.Registry, step StepInfo, conv apiconv.Client) (out plan.ToolCall, span plan.CallSpan, retErr error) {
	span = plan.CallSpan{StartedAt: time.Now()}
	errs := make([]error, 0, 6)
	if strings.TrimSpace(step.ID) == "" {
		step.ID = "tool-" + uuid.NewString()
	}

	turn, ok := memory.TurnMetaFromContext(ctx)
	if !ok {
		retErr = fmt.Errorf("turn meta not found")
		return
	}
	argsJSON := ""
	if debugConvEnabled() && len(step.Args) > 0 {
		if b, jErr := json.Marshal(step.Args); jErr == nil {
			argsJSON = string(b)
		} else {
			argsJSON = fmt.Sprintf("{\"marshal_error\":%q}", jErr.Error())
		}
	}
	debugConvf("tool execute start convo=%q turn=%q op_id=%q tool=%q args_len=%d args_head=%q args_tail=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), len(argsJSON), headString(argsJSON, 512), tailString(argsJSON, 512))
	if debugtrace.Enabled() {
		debugtrace.Write("executil", "tool_execute_start", map[string]any{
			"conversationID": strings.TrimSpace(turn.ConversationID),
			"turnID":         strings.TrimSpace(turn.TurnID),
			"toolCallID":     strings.TrimSpace(step.ID),
			"toolName":       strings.TrimSpace(step.Name),
			"responseID":     strings.TrimSpace(step.ResponseID),
			"turnTrace":      strings.TrimSpace(memory.TurnTrace(turn.TurnID)),
			"args":           step.Args,
		})
	}
	toolMsgID := ""
	toolCallStarted := false
	toolCallClosed := false
	forcedStatus := ""
	// Ensure started tool calls never remain non-terminal on abort/early exits.
	// Conversation terminal status is owned by turn finalization.
	defer func() {
		if !toolCallStarted || strings.TrimSpace(toolMsgID) == "" {
			return
		}
		status := strings.TrimSpace(forcedStatus)
		if status == "" {
			if errors.Is(retErr, context.Canceled) || errors.Is(retErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				status = "canceled"
			} else {
				status = "failed"
			}
		}
		errMsg := ""
		if status == "failed" {
			if retErr != nil {
				errMsg = retErr.Error()
			} else if cerr := ctx.Err(); cerr != nil {
				errMsg = cerr.Error()
			} else {
				errMsg = "forced close on abort"
			}
		}
		finCtx, cancelFin := detachedFinalizeCtx(ctx)
		defer cancelFin()
		if toolCallClosed && strings.EqualFold(status, "completed") && strings.TrimSpace(errMsg) == "" {
			return
		}
		warnConvf("tool force close convo=%q turn=%q op_id=%q tool=%q status=%q ret_err=%q parent_ctx_err=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(status), strings.TrimSpace(errMsg), strings.TrimSpace(formatContextErr(ctx)))
		if !toolCallClosed {
			_ = completeToolCall(finCtx, conv, toolMsgID, step.ID, step.Name, status, time.Now(), "", errMsg)
		}
	}()

	// 1) Create tool message (parent derived from ModelMessageIDFromContext)
	toolMsgID, err := createToolMessage(ctx, conv, turn, span.StartedAt, step.Name)
	if err != nil {
		retErr = err
		return
	}
	ctx = memory.WithToolMessageID(ctx, toolMsgID)

	// 2) Initialize tool call (running) with LLM op id
	if err := initToolCall(ctx, conv, toolMsgID, step.ID, turn, step.Name, span.StartedAt, step.ResponseID); err != nil {
		retErr = err
		return
	}
	toolCallStarted = true

	// 3) Persist request payload
	if len(step.Args) > 0 {
		if _, pErr := persistRequestPayload(ctx, conv, toolMsgID, step.Args); pErr != nil {
			errs = append(errs, fmt.Errorf("persist request payload: %w", pErr))
		}
	}

	// 4) Execute tool with a bounded context so one stuck call won't hang the run
	// Apply per-tool timeout when available (scoped registry exposes TimeoutResolver directly).
	registryTimeout := time.Duration(0)
	if tr, ok := reg.(tool.TimeoutResolver); ok {
		if d, ok2 := tr.ToolTimeout(step.Name); ok2 && d > 0 {
			registryTimeout = d
			ctx = WithToolTimeout(ctx, d)
		}
	}
	wrapperTimeout, wrapperTimeoutOK := toolTimeoutFromContext(ctx)
	argsTimeoutMs, hasArgsTimeout := timeoutMsFromArgs(step.Args)
	ctxDeadline, ctxRemaining := formatContextDeadline(ctx)
	if hasArgsTimeout {
		debugConvf("tool execute context convo=%q turn=%q op_id=%q tool=%q parent_deadline=%q parent_remaining=%q registry_timeout=%q wrapper_timeout=%q args_timeout_ms=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), ctxDeadline, ctxRemaining, registryTimeout.String(), wrapperTimeout.String(), argsTimeoutMs)
	} else if wrapperTimeoutOK {
		debugConvf("tool execute context convo=%q turn=%q op_id=%q tool=%q parent_deadline=%q parent_remaining=%q registry_timeout=%q wrapper_timeout=%q args_timeout_ms=none", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), ctxDeadline, ctxRemaining, registryTimeout.String(), wrapperTimeout.String())
	} else {
		debugConvf("tool execute context convo=%q turn=%q op_id=%q tool=%q parent_deadline=%q parent_remaining=%q registry_timeout=%q wrapper_timeout=default args_timeout_ms=none", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), ctxDeadline, ctxRemaining, registryTimeout.String())
	}
	var toolResult string
	var execErr error
	out, toolResult, execErr = executeToolWithRetry(ctx, reg, step, conv)
	// Optionally wrap overflow with YAML helper when native continuation is not supported.
	if wrapped := maybeWrapOverflow(ctx, reg, step.Name, toolResult, toolMsgID); wrapped != "" {
		toolResult = wrapped
		out.Result = wrapped
	}
	if execErr != nil && strings.TrimSpace(toolResult) == "" {
		// Provide the error text as response payload so the LLM can reason over it.
		// Use a clear cancellation message instead of the raw context error string.
		if errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
			toolResult = "tool execution was cancelled"
		} else {
			toolResult = execErr.Error()
		}
		out.Result = toolResult
	}
	if execErr != nil {
		errs = append(errs, fmt.Errorf("execute tool: %w", execErr))
		cause := classifyTimeoutCause(ctx, nil, execErr)
		warnConvf("tool execute error convo=%q turn=%q op_id=%q tool=%q cause=%q err=%q parent_ctx_err=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(cause), strings.TrimSpace(execErr.Error()), strings.TrimSpace(formatContextErr(ctx)))
	} else if looksLikeAuthElicitation(toolResult) {
		warnConvf("tool execute auth challenge convo=%q turn=%q op_id=%q tool=%q exec_err=nil result_len=%d result_head=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), len(toolResult), headString(toolResult, 256))
	}
	span.SetEnd(time.Now())

	// Debug trace: log tool call result to /tmp/agently-debug.log
	{
		errStr := ""
		if execErr != nil {
			errStr = execErr.Error()
		}
		status, _ := resolveToolStatus(execErr, ctx, toolResult)
		debugtrace.LogToolCall(step.Name, step.ID, status, len(toolResult), toolResult, errStr)
	}

	// Notify feed system about tool completion (for SSE feed events).
	if notifier := feedNotifierFromContext(ctx); notifier != nil {
		notifier.NotifyToolCompleted(ctx, step.Name, toolResult)
	}

	// 5) Persist side effects + response payload.
	// When the parent context is already cancelled (e.g. user cancelled the turn),
	// fall back to a detached context so the tool result is still persisted for
	// the conversation history and the LLM can reason over it in the next turn.
	persistCtx := ctx
	if ctx.Err() != nil {
		var cancelPersist func()
		persistCtx, cancelPersist = detachedFinalizeCtx(ctx)
		defer cancelPersist()
	}
	if strings.TrimSpace(toolResult) != "" {
		if err := persistDocumentsIfNeeded(persistCtx, reg, conv, turn, step.Name, toolResult); err != nil {
			errs = append(errs, fmt.Errorf("emit system content: %w", err))
		}
		if err := persistToolImageAttachmentIfNeeded(persistCtx, conv, turn, toolMsgID, step.Name, toolResult); err != nil {
			errs = append(errs, fmt.Errorf("persist tool attachments: %w", err))
		}
		if redacted, ok := redactToolResultIfNeeded(step.Name, toolResult); ok {
			toolResult = redacted
			out.Result = redacted
		}
	}

	respID, respErr := persistResponsePayload(persistCtx, conv, toolResult)
	if respErr != nil {
		errs = append(errs, fmt.Errorf("persist response payload: %w", respErr))
	}

	// 6) Mirror the tool result into the tool message body so transcript/history
	// readers can consume the result without depending on payload joins.
	if uErr := updateToolMessageContent(persistCtx, conv, toolMsgID, toolResult); uErr != nil {
		errs = append(errs, fmt.Errorf("update tool message: %w", uErr))
	}

	// 7) Finish tool call. Conversation terminal status is finalized at turn level.
	status, errMsg := resolveToolStatus(execErr, ctx, toolResult)
	forcedStatus = status
	// Use detached + bounded context for terminal writes.
	finCtx, cancelFin := detachedFinalizeCtx(ctx)
	defer cancelFin()
	if cErr := completeToolCall(finCtx, conv, toolMsgID, step.ID, step.Name, status, span.EndedAt, respID, errMsg); cErr != nil {
		errs = append(errs, fmt.Errorf("complete tool call: %w", cErr))
	} else {
		toolCallClosed = true
	}

	if len(errs) > 0 {
		retErr = errors.Join(errs...)
	}

	if retErr != nil && len(errs) > 0 {
		errorConvf("tool execute done convo=%q turn=%q op_id=%q tool=%q status=%q result_len=%d err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(status), len(toolResult), retErr)
	} else {
		infoConvf("tool execute done convo=%q turn=%q op_id=%q tool=%q status=%q result_len=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(status), len(toolResult))
	}
	if debugtrace.Enabled() {
		debugtrace.Write("executil", "tool_execute_done", map[string]any{
			"conversationID": strings.TrimSpace(turn.ConversationID),
			"turnID":         strings.TrimSpace(turn.TurnID),
			"toolCallID":     strings.TrimSpace(step.ID),
			"toolName":       strings.TrimSpace(step.Name),
			"responseID":     strings.TrimSpace(step.ResponseID),
			"status":         strings.TrimSpace(status),
			"resultLen":      len(toolResult),
			"error":          errString(retErr),
		})
	}

	return
}

// SynthesizeToolStep persists a tool call using a precomputed result without
// invoking the actual tool. It mirrors ExecuteToolStep's persistence flow
// (messages, request/response payloads, status), setting status to "completed".
func SynthesizeToolStep(ctx context.Context, conv apiconv.Client, step StepInfo, toolResult string) error {
	if strings.TrimSpace(step.ID) == "" {
		step.ID = "tool-" + uuid.NewString()
	}
	turn, ok := memory.TurnMetaFromContext(ctx)
	if !ok {
		return fmt.Errorf("turn meta not found")
	}
	argsJSON := ""
	if debugConvEnabled() && len(step.Args) > 0 {
		if b, jErr := json.Marshal(step.Args); jErr == nil {
			argsJSON = string(b)
		} else {
			argsJSON = fmt.Sprintf("{\"marshal_error\":%q}", jErr.Error())
		}
	}
	debugConvf("tool synth start convo=%q turn=%q op_id=%q tool=%q args_len=%d args_head=%q args_tail=%q result_len=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), len(argsJSON), headString(argsJSON, 512), tailString(argsJSON, 512), len(toolResult))
	if debugtrace.Enabled() {
		debugtrace.Write("executil", "tool_synth_start", map[string]any{
			"conversationID": strings.TrimSpace(turn.ConversationID),
			"turnID":         strings.TrimSpace(turn.TurnID),
			"toolCallID":     strings.TrimSpace(step.ID),
			"toolName":       strings.TrimSpace(step.Name),
			"responseID":     strings.TrimSpace(step.ResponseID),
			"turnTrace":      strings.TrimSpace(memory.TurnTrace(turn.TurnID)),
			"args":           step.Args,
			"resultLen":      len(toolResult),
		})
	}
	startedAt := time.Now()
	toolMsgID, err := createToolMessage(ctx, conv, turn, startedAt, step.Name)
	if err != nil {
		return err
	}
	ctx = memory.WithToolMessageID(ctx, toolMsgID)
	if err := initToolCall(ctx, conv, toolMsgID, step.ID, turn, step.Name, startedAt, step.ResponseID); err != nil {
		return err
	}
	if len(step.Args) > 0 {
		if _, pErr := persistRequestPayload(ctx, conv, toolMsgID, step.Args); pErr != nil {
			return fmt.Errorf("persist request payload: %w", pErr)
		}
	}
	// Persist provided result
	if redacted, ok := redactToolResultIfNeeded(step.Name, toolResult); ok {
		toolResult = redacted
	}
	respID, respErr := persistResponsePayload(ctx, conv, toolResult)
	if respErr != nil {
		return fmt.Errorf("persist response payload: %w", respErr)
	}
	if uErr := updateToolMessageContent(ctx, conv, toolMsgID, toolResult); uErr != nil {
		return fmt.Errorf("update tool message: %w", uErr)
	}
	// Complete tool call
	status := "completed"
	completedAt := time.Now()
	finCtx, cancelFin := detachedFinalizeCtx(ctx)
	defer cancelFin()
	if cErr := completeToolCall(finCtx, conv, toolMsgID, step.ID, step.Name, status, completedAt, respID, ""); cErr != nil {
		return fmt.Errorf("complete tool call: %w", cErr)
	}
	debugConvf("tool synth done convo=%q turn=%q op_id=%q tool=%q status=%q result_len=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(status), len(toolResult))
	if debugtrace.Enabled() {
		debugtrace.Write("executil", "tool_synth_done", map[string]any{
			"conversationID": strings.TrimSpace(turn.ConversationID),
			"turnID":         strings.TrimSpace(turn.TurnID),
			"toolCallID":     strings.TrimSpace(step.ID),
			"toolName":       strings.TrimSpace(step.Name),
			"responseID":     strings.TrimSpace(step.ResponseID),
			"status":         strings.TrimSpace(status),
			"resultLen":      len(toolResult),
		})
	}
	return nil
}

// executeTool runs the tool and returns the normalized ToolCall, raw result and error.
func executeTool(ctx context.Context, reg tool.Registry, step StepInfo, conv apiconv.Client) (plan.ToolCall, string, error) {
	applyContextWorkdir(step.Name, step.Args, ctx)
	if err := tool.ValidateExecution(ctx, tool.FromContext(ctx), step.Name, step.Args); err != nil {
		out := plan.ToolCall{ID: step.ID, Name: step.Name, Arguments: step.Args, Error: err.Error()}
		return out, "", err
	}
	if cfg, ok := tool.ApprovalQueueFor(ctx, step.Name); ok && cfg != nil {
		if cfg.IsQueue() {
			msg, err := enqueueToolApproval(ctx, conv, step)
			out := plan.ToolCall{ID: step.ID, Name: step.Name, Arguments: step.Args, Result: msg}
			if err != nil {
				out.Error = err.Error()
				return out, "", err
			}
			return out, msg, errToolQueued
		}
		if cfg.IsPrompt() {
			action, err := promptToolApproval(ctx, step, cfg)
			if err != nil {
				out := plan.ToolCall{ID: step.ID, Name: step.Name, Arguments: step.Args, Error: err.Error()}
				return out, "", err
			}
			switch strings.ToLower(strings.TrimSpace(action)) {
			case "accept":
				// approved — fall through to normal execution below
			case "cancel":
				msg := "tool execution was not approved by user"
				out := plan.ToolCall{ID: step.ID, Name: step.Name, Arguments: step.Args, Result: msg}
				return out, msg, errToolPromptDeclined
			default:
				msg := "tool execution was not approved by user"
				out := plan.ToolCall{ID: step.ID, Name: step.Name, Arguments: step.Args, Result: msg}
				return out, msg, errToolPromptDeclined
			}
		}
	}
	toolResult, err := reg.Execute(ctx, step.Name, step.Args)
	out := plan.ToolCall{ID: step.ID, Name: step.Name, Arguments: step.Args, Result: toolResult}
	if err != nil {
		out.Error = err.Error()
	}
	return out, toolResult, err
}

// promptToolApproval creates an inline elicitation for prompt-mode approval and
// blocks until the user accepts, declines, or cancels.
func promptToolApproval(ctx context.Context, step StepInfo, cfg *plan.ApprovalConfig) (string, error) {
	turn, ok := memory.TurnMetaFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("turn meta not found for prompt approval")
	}
	elicitor := approvalElicitorFromContext(ctx)
	if elicitor == nil {
		return "", fmt.Errorf("prompt approval not configured: no elicitor in context")
	}
	action, payload, err := elicitor.ElicitToolApproval(ctx, &turn, step.Name, cfg, step.Args)
	if err != nil {
		return action, err
	}
	if strings.EqualFold(strings.TrimSpace(action), "accept") {
		view := BuildApprovalView(step.Name, step.Args, cfg)
		if edits := extractApprovalEditedFields(payload); len(edits) > 0 {
			if err := ApplyApprovalEdits(step.Args, view.Editors, edits); err != nil {
				return "", err
			}
		}
	}
	return action, nil
}

func applyContextWorkdir(toolName string, args map[string]interface{}, ctx context.Context) {
	if len(args) == 0 {
		return
	}
	if hasExplicitWorkdir(args) {
		return
	}
	workdir, ok := WorkdirFromContext(ctx)
	if !ok {
		return
	}
	switch strings.TrimSpace(toolName) {
	case "system_exec-execute", "system/exec:execute", "system_patch-apply", "system/patch:apply":
		args["workdir"] = workdir
	}
}

func hasExplicitWorkdir(args map[string]interface{}) bool {
	if len(args) == 0 {
		return false
	}
	raw, ok := args["workdir"]
	if !ok || raw == nil {
		return false
	}
	switch actual := raw.(type) {
	case string:
		return strings.TrimSpace(actual) != ""
	default:
		return true
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// resolveToolStatus determines the terminal status for a tool call based on execution error and parent context.
// Returns one of: "completed", "failed", "canceled" and an optional error message.
func resolveToolStatus(execErr error, parentCtx context.Context, toolResult string) (string, string) {
	status := "completed"
	var errMsg string
	if execErr != nil {
		if errors.Is(execErr, errToolQueued) {
			return "queued", ""
		}
		if errors.Is(execErr, errToolPromptDeclined) {
			return "rejected", ""
		}
		if errors.Is(execErr, errToolPromptCanceled) {
			return "rejected", ""
		}
		// Treat context cancellation and deadline as cancellations, not failures
		if errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) || parentCtx.Err() == context.Canceled {
			status = "canceled"
		} else {
			status = "failed"
			errMsg = execErr.Error()
		}
	} else if parentCtx.Err() == context.Canceled {
		status = "canceled"
	} else if looksLikeAuthElicitation(toolResult) {
		status = "waiting_for_user"
	}
	return status, errMsg
}

func detachedFinalizeCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), finalizeWriteTimeout)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), finalizeWriteTimeout)
}

func looksLikeAuthElicitation(result string) bool {
	text := strings.ToLower(strings.TrimSpace(result))
	if text == "" {
		return false
	}
	return strings.Contains(text, "mcp server requires authentication") ||
		strings.Contains(text, "please sign in to continue") ||
		(strings.Contains(text, "\"type\":\"elicitation\"") && strings.Contains(text, "\"mode\":\"url\""))
}

type toolApprovalQueueWriter interface {
	PatchToolApprovalQueue(ctx context.Context, queue *queuew.ToolApprovalQueue) error
}

type toolApprovalQueueReader interface {
	ListToolApprovalQueues(ctx context.Context, in *queueread.QueueRowsInput) ([]*queueread.QueueRowView, error)
}

func enqueueToolApproval(ctx context.Context, conv apiconv.Client, step StepInfo) (string, error) {
	turn, ok := memory.TurnMetaFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("turn meta not found")
	}
	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if userID == "" {
		userID = "anonymous"
	}
	arguments, err := json.Marshal(step.Args)
	if err != nil {
		return "", fmt.Errorf("marshal tool arguments: %w", err)
	}
	writer, ok := conv.(toolApprovalQueueWriter)
	if !ok || writer == nil {
		return "", fmt.Errorf("approval queue writer not configured")
	}
	queueToolName := displayQueueToolName(step.Name)
	if reader, ok := conv.(toolApprovalQueueReader); ok && reader != nil {
		in := &queueread.QueueRowsInput{
			UserId:         userID,
			ConversationId: turn.ConversationID,
			TurnId:         turn.TurnID,
			QueueStatus:    "pending",
			Has: &queueread.QueueRowsInputHas{
				UserId:         true,
				ConversationId: true,
				TurnId:         true,
				QueueStatus:    true,
			},
		}
		if rows, err := reader.ListToolApprovalQueues(ctx, in); err == nil && len(rows) > 0 {
			want := strings.ToLower(strings.TrimSpace(mcpname.Canonical(step.Name)))
			for _, row := range rows {
				if row == nil {
					continue
				}
				got := strings.ToLower(strings.TrimSpace(mcpname.Canonical(row.ToolName)))
				if got == want {
					return "queued for user approval", nil
				}
			}
		}
	}
	cfg, _ := tool.ApprovalQueueFor(ctx, step.Name)
	view := BuildApprovalView(step.Name, step.Args, cfg)
	metadata, _ := json.Marshal(map[string]interface{}{
		"opId":       step.ID,
		"responseId": step.ResponseID,
		"turnId":     turn.TurnID,
		"approval":   view,
	})

	rec := &queuew.ToolApprovalQueue{Has: &queuew.ToolApprovalQueueHas{}}
	rec.SetId(uuid.NewString())
	rec.SetUserId(userID)
	rec.SetToolName(queueToolName)
	if view.Title != "" {
		rec.SetTitle(view.Title)
	}
	rec.SetArguments(arguments)
	rec.SetStatus("pending")
	rec.SetConversationId(turn.ConversationID)
	rec.SetTurnId(turn.TurnID)
	rec.SetMessageId(turn.ParentMessageID)
	rec.SetMetadata(metadata)
	if err := writer.PatchToolApprovalQueue(ctx, rec); err != nil {
		return "", fmt.Errorf("enqueue tool approval: %w", err)
	}
	return "queued for user approval", nil
}

func displayQueueToolName(name string) string {
	return mcpname.Display(name)
}
