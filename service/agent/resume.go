package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/auth/mcpauth"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/protocol/tool"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolasyncconfig "github.com/viant/agently-core/protocol/tool/asyncconfig"
	toolobservation "github.com/viant/agently-core/protocol/tool/service/observation"
	runtimeprojection "github.com/viant/agently-core/runtime/projection"
	runtimerecovery "github.com/viant/agently-core/runtime/recovery"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/shared/asyncwait"
	toolapproval "github.com/viant/agently-core/service/shared/toolapproval"
	"github.com/viant/agently-core/service/shared/toolexec"
)

// planLoopPhase selects where a restored run re-enters the ReAct loop.
type planLoopPhase int

const (
	// planLoopPhaseModel restarts the model phase of the given iteration.
	planLoopPhaseModel planLoopPhase = iota
	// planLoopPhaseTools resumes a completed model attempt at its tool phase.
	planLoopPhaseTools
)

// planLoopStart is the restored loop position. The zero value is the ordinary
// entry used by Query.
type planLoopStart struct {
	Iteration int
	Phase     planLoopPhase
	// Plan is the durable tool plan of the completed model attempt (tools phase).
	Plan *execution.Plan
	// Completed holds durable terminal results keyed by op ID (tools phase).
	Completed map[string]llm.ToolCall
	// MessageID/Content identify the completed assistant message (tools phase).
	MessageID string
	Content   string
}

func (p planLoopStart) firstIteration() int {
	if p.Iteration < 1 {
		return 1
	}
	return p.Iteration
}

// resumeTurnRequest carries only the persisted identity needed to restart the
// loop for an already-admitted, freshly claimed root run.
type resumeTurnRequest struct {
	ConversationID string
	TurnID         string
	LeaseOwner     string
	AgentID        string
	UserID         string
	Iteration      int
}

var (
	errRunLeaseLost                 = errors.New("run lease lost")
	errRecoveryAmbiguousToolOutcome = errors.New("recovery blocked: tool call outcome unknown")
	errRecoveryTurnNotResumable     = errors.New("recovery: turn is not resumable")
)

// resumeTurn is the unexported restart-recovery backdoor into the existing
// ReAct loop. It is called only by the watchdog after a successful conditional
// claim of the same root run. It bypasses Query, intake, routing, new-turn
// admission and startTurn; it does not bypass binding, the reactor, tool
// execution, retry policies, heartbeat, ownership checks or finalization.
func (s *Service) resumeTurn(ctx context.Context, req resumeTurnRequest) error {
	if s == nil || s.conversation == nil {
		return fmt.Errorf("resumeTurn: conversation client not configured")
	}
	conversationID := strings.TrimSpace(req.ConversationID)
	turnID := strings.TrimSpace(req.TurnID)
	if conversationID == "" || turnID == "" || strings.TrimSpace(req.LeaseOwner) == "" {
		return fmt.Errorf("resumeTurn: conversation, turn and lease owner are required")
	}
	conv, err := s.conversation.GetConversation(ctx, conversationID,
		apiconv.WithIncludeTranscript(true), apiconv.WithIncludeModelCall(true), apiconv.WithIncludeToolCall(true))
	if err != nil {
		return fmt.Errorf("resumeTurn: load conversation: %w", err)
	}
	if conv == nil || conv.HasConversationParent() || conv.ScheduleId != nil {
		return errRecoveryTurnNotResumable
	}
	var turnRow *apiconv.Turn
	for _, candidate := range conv.GetTranscript() {
		if candidate != nil && strings.TrimSpace(candidate.Id) == turnID {
			turnRow = candidate
			break
		}
	}
	if turnRow == nil || isTerminalArtifactStatus(turnRow.Status) || strings.EqualFold(strings.TrimSpace(turnRow.Status), "queued") {
		return errRecoveryTurnNotResumable
	}
	agentID := resolveResumeAgentID(req.AgentID, turnRow.AgentIdUsed, conv)
	if agentID == "" {
		return fmt.Errorf("resumeTurn: agent id is unknown for turn %s", turnID)
	}
	input := &QueryInput{
		AgentID:                agentID,
		ConversationID:         conversationID,
		MessageID:              turnID,
		UserId:                 strings.TrimSpace(req.UserID),
		Query:                  resumeTurnQuery(turnRow),
		SkipInitialUserMessage: true,
	}
	input.DisplayQuery = input.Query
	if err := s.ensureAgent(ctx, input); err != nil {
		return fmt.Errorf("resumeTurn: %w", err)
	}
	if input.EmbeddingModel == "" && s.defaults != nil {
		input.EmbeddingModel = s.defaults.Embedder
	}
	turn := runtimerequestctx.TurnMeta{Assistant: input.Agent.ID, ConversationID: conversationID, TurnID: turnID}
	ctx = runtimerequestctx.WithConversationID(ctx, conversationID)
	ctx = runtimerequestctx.WithTurnMeta(ctx, turn)
	ctx = runtimerequestctx.WithRunMeta(ctx, runtimerequestctx.RunMeta{RunID: turnID})
	ctx = runtimerecovery.WithMode(ctx, runtimerecovery.ModeResume)
	var cancel func()
	ctx, cancel = s.registerTurnCancel(ctx, turn)
	defer cancel()
	var leaseCancel context.CancelFunc
	ctx, leaseCancel = context.WithCancel(ctx)
	defer leaseCancel()
	ctx = withRunLease(ctx, s.newRunLease(req.LeaseOwner, leaseCancel))
	ctx = s.resumeExecutionContext(ctx, input)

	stopHeartbeat := s.startRunHeartbeat(ctx, turn)
	finalize := func(status string, runErr error) error {
		return stopRunHeartbeatThen(stopHeartbeat, func() error {
			return s.finalizeTurn(ctx, turn, status, runErr)
		})
	}
	waiting, err := s.turnAwaitingUserAction(ctx, turn)
	if err != nil {
		return finalize("failed", err)
	}
	if waiting {
		// Approval/user input is pending: preserve the wait, no loop work.
		return finalize("waiting_for_user", nil)
	}
	start, err := s.recoveryStart(ctx, turnRow, turnID, req.Iteration)
	if err != nil {
		return finalize("failed", err)
	}
	logx.Infof("conversation", "agent.resumeTurn convo=%q turn_id=%q iter=%d phase=%d planned=%d completed=%d", conversationID, turnID, start.firstIteration(), start.Phase, planStepCount(start.Plan), len(start.Completed))
	output := &QueryOutput{ConversationID: conversationID, TurnID: turnID}
	status, runErr := s.runPlanAndStatusFrom(ctx, input, output, start)
	if lease := runLeaseFromContext(ctx); lease != nil && !lease.Active() {
		stopHeartbeat()
		return errRunLeaseLost
	}
	return finalize(status, runErr)
}

func resumeTurnQuery(turn *apiconv.Turn) string {
	if turn == nil {
		return ""
	}
	for i := len(turn.Message) - 1; i >= 0; i-- {
		message := turn.Message[i]
		if message == nil || !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		if message.RawContent != nil {
			if value := strings.TrimSpace(*message.RawContent); value != "" {
				return value
			}
		}
		if message.Content != nil {
			if value := strings.TrimSpace(*message.Content); value != "" {
				return value
			}
		}
	}
	return ""
}

func resolveResumeAgentID(runAgentID string, turnAgentID *string, conv *apiconv.Conversation) string {
	if turnAgentID != nil {
		if value := strings.TrimSpace(*turnAgentID); value != "" && !isAutoAgentRef(value) && !isInternalHelperAgentID(value) {
			return value
		}
	}
	if value := strings.TrimSpace(runAgentID); value != "" && !isAutoAgentRef(value) && !isInternalHelperAgentID(value) {
		return value
	}
	if value := lastTurnAgentIDUsed(conv); value != "" {
		return value
	}
	if conv != nil && conv.AgentId != nil {
		return strings.TrimSpace(*conv.AgentId)
	}
	return ""
}

// resumeExecutionContext mirrors the execution-scoped context Query installs
// after admission (tool policy, projection/approval/async state, elicitor,
// workdir and tool timeout) so resumed tools behave exactly as normal ones.
func (s *Service) resumeExecutionContext(ctx context.Context, input *QueryInput) context.Context {
	if pol := s.resolveToolPolicy(input); pol != nil {
		ctx = tool.WithPolicy(ctx, pol)
	}
	ctx = runtimeprojection.WithState(ctx)
	ctx = toolapprovalqueue.WithState(ctx)
	ctx = toolasyncconfig.WithState(ctx)
	ctx = asyncwait.WithState(ctx)
	if s.elicitation != nil {
		ctx = toolapproval.WithElicitor(ctx, &agentToolApprovalElicitor{elicService: s.elicitation})
		ctx = mcpauth.WithBlocker(ctx, &mcpAuthBlocker{elicitation: s.elicitation})
	}
	if s.asyncManager != nil {
		ctx = toolexec.WithAsyncManager(ctx, s.asyncManager)
	}
	ctx = toolexec.WithAsyncConversation(ctx, s.conversation)
	ctx = toolexec.WithWorkdir(ctx, ensureResolvedWorkdir(input))
	ctx = toolobservation.WithState(ctx)
	if s.defaults != nil && s.defaults.ToolCallTimeoutSec > 0 {
		ctx = toolexec.WithToolTimeout(ctx, time.Duration(s.defaults.ToolCallTimeoutSec)*time.Second)
	}
	return ctx
}

// recoveryStart derives the resume boundary from the durable model/tool calls
// of the root turn. Only an incomplete persisted record is minimally patched;
// completed calls are left for the binder to consume.
func (s *Service) recoveryStart(ctx context.Context, turnRow *apiconv.Turn, runID string, runIteration int) (planLoopStart, error) {
	iteration := runIteration
	if iteration < 1 {
		iteration = 1
	}
	var modelMsg *apiconv.Message
	for _, raw := range turnRow.Message {
		if raw == nil || raw.ModelCall == nil {
			continue
		}
		msg := (*apiconv.Message)(raw)
		if msg.ModelCall.RunId != nil && strings.TrimSpace(*msg.ModelCall.RunId) != "" && strings.TrimSpace(*msg.ModelCall.RunId) != runID {
			continue
		}
		if modelMsg == nil || modelCallIteration(msg) > modelCallIteration(modelMsg) ||
			(modelCallIteration(msg) == modelCallIteration(modelMsg) && !msg.CreatedAt.Before(modelMsg.CreatedAt)) {
			modelMsg = msg
		}
	}
	if modelMsg == nil {
		return planLoopStart{Iteration: iteration, Phase: planLoopPhaseModel}, nil
	}
	if it := modelCallIteration(modelMsg); it > 0 {
		iteration = it
	}
	toolRows := indexTurnToolCalls(turnRow)
	completed := strings.EqualFold(strings.TrimSpace(modelMsg.ModelCall.Status), "completed") && modelMsg.ModelCall.CompletedAt != nil
	if !completed {
		// Unfinished model attempt is discarded as a whole.
		if !isTerminalArtifactStatus(modelMsg.ModelCall.Status) {
			upd := apiconv.NewModelCall()
			upd.SetMessageID(modelMsg.Id)
			upd.SetStatus("canceled")
			upd.SetErrorMessage("interrupted by restart")
			upd.SetCompletedAt(time.Now())
			if err := s.conversation.PatchModelCall(ctx, upd); err != nil {
				return planLoopStart{}, fmt.Errorf("mark interrupted model call: %w", err)
			}
		}
		msgPatch := apiconv.NewMessage()
		msgPatch.SetId(modelMsg.Id)
		msgPatch.SetConversationID(modelMsg.ConversationId)
		msgPatch.SetInterim(1)
		if err := s.conversation.PatchMessage(ctx, msgPatch); err != nil {
			return planLoopStart{}, fmt.Errorf("hide interrupted model message: %w", err)
		}
		// A persisted started tool-call row must still be inspected: its
		// external outcome may be unknown even though its model attempt is hidden.
		for _, row := range toolRows {
			if row.iteration == iteration && row.running() {
				return planLoopStart{}, errRecoveryAmbiguousToolOutcome
			}
		}
		return planLoopStart{Iteration: iteration, Phase: planLoopPhaseModel}, nil
	}
	resp, err := s.modelCallResponse(ctx, modelMsg)
	if err != nil {
		return planLoopStart{}, err
	}
	start := planLoopStart{
		Iteration: iteration,
		Phase:     planLoopPhaseTools,
		Plan:      s.orchestrator.PlanFromResponse(resp),
		Completed: map[string]llm.ToolCall{},
		MessageID: strings.TrimSpace(modelMsg.Id),
		Content:   strings.TrimSpace(modelMsg.GetContent()),
	}
	for _, step := range start.Plan.Steps {
		row, ok := toolRows[strings.TrimSpace(step.ID)]
		if !ok {
			continue // planned but never started: execute only this operation
		}
		switch {
		case isTerminalArtifactStatus(row.status):
		case row.hasDurableResult():
			// Result is durable but the terminal status projection is not: repair
			// only the missing status, never invoke the tool again.
			upd := apiconv.NewToolCall()
			upd.SetMessageID(row.messageID)
			upd.SetOpID(row.opID)
			upd.SetStatus("completed")
			upd.SetCompletedAt(time.Now())
			if err := s.conversation.PatchToolCall(ctx, upd); err != nil {
				return planLoopStart{}, fmt.Errorf("repair tool call %s: %w", row.opID, err)
			}
		default:
			return planLoopStart{}, errRecoveryAmbiguousToolOutcome
		}
		start.Completed[strings.TrimSpace(step.ID)] = llm.ToolCall{ID: step.ID, Name: step.Name, Arguments: step.Args, Result: row.result}
	}
	return start, nil
}

func (s *Service) modelCallResponse(ctx context.Context, msg *apiconv.Message) (*llm.GenerateResponse, error) {
	body := ""
	if p := msg.ModelCall.ModelCallResponsePayload; p != nil && p.InlineBody != nil {
		body = decodePayloadInlineBody(*p.InlineBody, p.Compression)
	}
	if body == "" && msg.ModelCall.ResponsePayloadId != nil && strings.TrimSpace(*msg.ModelCall.ResponsePayloadId) != "" {
		payload, err := s.conversation.GetPayload(ctx, strings.TrimSpace(*msg.ModelCall.ResponsePayloadId))
		if err != nil {
			return nil, fmt.Errorf("load model response payload: %w", err)
		}
		if payload != nil && payload.InlineBody != nil {
			body = decodePayloadInlineBody(string(*payload.InlineBody), payload.Compression)
		}
	}
	resp := &llm.GenerateResponse{}
	if strings.TrimSpace(body) == "" {
		return resp, nil
	}
	if err := json.Unmarshal([]byte(body), resp); err != nil {
		return nil, fmt.Errorf("decode model response payload: %w", err)
	}
	return resp, nil
}

type recoveredToolRow struct {
	messageID string
	opID      string
	status    string
	iteration int
	result    string
	payloadID string
}

func (r recoveredToolRow) running() bool { return !isTerminalArtifactStatus(r.status) }

func (r recoveredToolRow) hasDurableResult() bool {
	return strings.TrimSpace(r.payloadID) != "" || strings.TrimSpace(r.result) != ""
}

func indexTurnToolCalls(turnRow *apiconv.Turn) map[string]recoveredToolRow {
	rows := map[string]recoveredToolRow{}
	add := func(messageID, opID, status string, iteration *int, payloadID *string, content string) {
		opID = strings.TrimSpace(opID)
		if opID == "" {
			return
		}
		row := recoveredToolRow{messageID: messageID, opID: opID, status: status, result: strings.TrimSpace(content)}
		if iteration != nil {
			row.iteration = *iteration
		}
		if payloadID != nil {
			row.payloadID = strings.TrimSpace(*payloadID)
		}
		rows[opID] = row
	}
	for _, msg := range turnRow.Message {
		if msg == nil {
			continue
		}
		if tc := msg.MessageToolCall; tc != nil {
			add(msg.Id, tc.OpId, tc.Status, tc.Iteration, tc.ResponsePayloadId, (*apiconv.Message)(msg).GetContent())
		}
		for _, tm := range msg.ToolMessage {
			if tm == nil || tm.ToolCall == nil {
				continue
			}
			add(tm.Id, tm.ToolCall.OpId, tm.ToolCall.Status, tm.ToolCall.Iteration, tm.ToolCall.ResponsePayloadId, valueOrEmpty(tm.Content))
		}
	}
	return rows
}

func modelCallIteration(msg *apiconv.Message) int {
	if msg == nil || msg.ModelCall == nil || msg.ModelCall.Iteration == nil {
		return 0
	}
	return *msg.ModelCall.Iteration
}

func planStepCount(p *execution.Plan) int {
	if p == nil {
		return 0
	}
	return len(p.Steps)
}
