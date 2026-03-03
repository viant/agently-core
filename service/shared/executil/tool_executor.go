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
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	queueread "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	queuew "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/memory"
)

const (
	SystemDocumentTag   = "system_doc"
	SystemDocumentMode  = "system_document"
	ResourceDocumentTag = "resource_doc"
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
func ExecuteToolStep(ctx context.Context, reg tool.Registry, step StepInfo, conv apiconv.Client) (plan.ToolCall, plan.CallSpan, error) {
	span := plan.CallSpan{StartedAt: time.Now()}
	errs := make([]error, 0, 6)
	if strings.TrimSpace(step.ID) == "" {
		step.ID = "tool-" + uuid.NewString()
	}

	turn, ok := memory.TurnMetaFromContext(ctx)
	if !ok {
		return plan.ToolCall{}, span, fmt.Errorf("turn meta not found")
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

	// 1) Create tool message
	toolMsgID, err := createToolMessage(ctx, conv, turn, span.StartedAt)
	if err != nil {
		return plan.ToolCall{}, span, err
	}

	// 2) Initialize tool call (running) with LLM op id
	if err := initToolCall(ctx, conv, toolMsgID, step.ID, turn, step.Name, span.StartedAt, step.ResponseID); err != nil {
		return plan.ToolCall{}, span, err
	}

	// 3) Persist request payload
	if len(step.Args) > 0 {
		if _, pErr := persistRequestPayload(ctx, conv, toolMsgID, step.Args); pErr != nil {
			errs = append(errs, fmt.Errorf("persist request payload: %w", pErr))
		}
	}

	// 4) Execute tool with a bounded context so one stuck call won't hang the run
	// Apply per-tool timeout when available (scoped registry exposes TimeoutResolver directly).
	if tr, ok := reg.(tool.TimeoutResolver); ok {
		if d, ok2 := tr.ToolTimeout(step.Name); ok2 && d > 0 {
			ctx = WithToolTimeout(ctx, d)
		}
	}
	out, toolResult, execErr := executeToolWithRetry(ctx, reg, step, conv)
	// Optionally wrap overflow with YAML helper when native continuation is not supported.
	if wrapped := maybeWrapOverflow(ctx, reg, step.Name, toolResult, toolMsgID); wrapped != "" {
		toolResult = wrapped
		out.Result = wrapped
	}
	if execErr != nil && strings.TrimSpace(toolResult) == "" {
		// Provide the error text as response payload so the LLM can reason over it.
		toolResult = execErr.Error()
		out.Result = toolResult
	}
	if execErr != nil {
		errs = append(errs, fmt.Errorf("execute tool: %w", execErr))
	}
	span.SetEnd(time.Now())

	// 5) Persist side effects + response payload (use background when canceled to avoid DB write cancellation)
	persistCtx := ctx
	if ctx.Err() == context.Canceled {
		persistCtx = context.Background()
	}

	if strings.TrimSpace(toolResult) != "" {
		docCtx := ctx
		if ctx.Err() != nil {
			docCtx = context.Background()
		}
		if err := persistDocumentsIfNeeded(docCtx, reg, conv, turn, step.Name, toolResult); err != nil {
			errs = append(errs, fmt.Errorf("emit system content: %w", err))
		}
		if err := persistToolImageAttachmentIfNeeded(docCtx, conv, turn, toolMsgID, step.Name, toolResult); err != nil {
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

	// 6) Update tool message with result content - why duplication of content gere
	//if uErr := updateToolMessageContent(persistCtx, conv, toolMsgID, toolResult); uErr != nil {
	//	errs = append(errs, fmt.Errorf("update tool message: %w", uErr))
	//}

	// 7) Finish tool call (record error message when present)
	status, errMsg := resolveToolStatus(execErr, ctx)
	// Use background for final write when terminated to avoid canceled ctx
	finCtx := ctx
	if status == "canceled" {
		finCtx = context.Background()
	}
	if cErr := completeToolCall(finCtx, conv, toolMsgID, step.ID, status, span.EndedAt, respID, errMsg); cErr != nil {
		errs = append(errs, fmt.Errorf("complete tool call: %w", cErr))
	}
	patchErr := conv.PatchConversations(ctx, convw.NewConversationStatus(turn.ConversationID, status))
	if patchErr != nil {
		errs = append(errs, fmt.Errorf("patch conversations call: %w", patchErr))
	}

	var retErr error
	if len(errs) > 0 {
		retErr = errors.Join(errs...)
	}

	if retErr != nil && len(errs) > 0 {
		errorConvf("tool execute done convo=%q turn=%q op_id=%q tool=%q status=%q result_len=%d err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(status), len(toolResult), retErr)
	} else {
		infoConvf("tool execute done convo=%q turn=%q op_id=%q tool=%q status=%q result_len=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(status), len(toolResult))
	}

	return out, span, retErr
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
	startedAt := time.Now()
	toolMsgID, err := createToolMessage(ctx, conv, turn, startedAt)
	if err != nil {
		return err
	}
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
	// Complete tool call
	status := "completed"
	completedAt := time.Now()
	if cErr := completeToolCall(ctx, conv, toolMsgID, step.ID, status, completedAt, respID, ""); cErr != nil {
		return fmt.Errorf("complete tool call: %w", cErr)
	}
	_ = conv.PatchConversations(ctx, convw.NewConversationStatus(turn.ConversationID, status))
	debugConvf("tool synth done convo=%q turn=%q op_id=%q tool=%q status=%q result_len=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(step.ID), strings.TrimSpace(step.Name), strings.TrimSpace(status), len(toolResult))
	return nil
}

// executeTool runs the tool and returns the normalized ToolCall, raw result and error.
func executeTool(ctx context.Context, reg tool.Registry, step StepInfo, conv apiconv.Client) (plan.ToolCall, string, error) {
	if err := tool.ValidateExecution(ctx, tool.FromContext(ctx), step.Name, step.Args); err != nil {
		out := plan.ToolCall{ID: step.ID, Name: step.Name, Arguments: step.Args, Error: err.Error()}
		return out, "", err
	}
	if tool.RequiresApprovalQueue(ctx, step.Name) {
		msg, err := enqueueToolApproval(ctx, conv, step)
		out := plan.ToolCall{ID: step.ID, Name: step.Name, Arguments: step.Args, Result: msg}
		if err != nil {
			out.Error = err.Error()
			return out, "", err
		}
		return out, msg, errToolQueued
	}
	toolResult, err := reg.Execute(ctx, step.Name, step.Args)
	out := plan.ToolCall{ID: step.ID, Name: step.Name, Arguments: step.Args, Result: toolResult}
	if err != nil {
		out.Error = err.Error()
	}
	return out, toolResult, err
}

// resolveToolStatus determines the terminal status for a tool call based on execution error and parent context.
// Returns one of: "completed", "failed", "canceled" and an optional error message.
func resolveToolStatus(execErr error, parentCtx context.Context) (string, string) {
	status := "completed"
	var errMsg string
	if execErr != nil {
		if errors.Is(execErr, errToolQueued) {
			return "queued", ""
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
	}
	return status, errMsg
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
	metadata, _ := json.Marshal(map[string]interface{}{
		"opId":       step.ID,
		"responseId": step.ResponseID,
		"turnId":     turn.TurnID,
	})
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
	rec := &queuew.ToolApprovalQueue{Has: &queuew.ToolApprovalQueueHas{}}
	rec.SetId(uuid.NewString())
	rec.SetUserId(userID)
	rec.SetToolName(queueToolName)
	title := strings.TrimSpace(step.Name)
	if cfg, ok := tool.ApprovalQueueFor(ctx, step.Name); ok && cfg != nil {
		if k := strings.TrimSpace(cfg.TitleSelector); k != "" {
			if v, has := step.Args[k]; has && v != nil {
				if vv := strings.TrimSpace(fmt.Sprintf("%v", v)); vv != "" {
					title = vv
				}
			}
		}
	}
	if title != "" {
		rec.SetTitle(title)
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
	canon := strings.TrimSpace(mcpname.Canonical(name))
	if canon == "" {
		return strings.TrimSpace(name)
	}
	n := mcpname.Name(canon)
	service := strings.TrimSpace(n.Service())
	method := strings.TrimSpace(n.Method())
	if service != "" && method != "" {
		return service + "/" + method
	}
	return canon
}
