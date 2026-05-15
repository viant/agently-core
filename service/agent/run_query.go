package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/internal/textutil"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	"github.com/viant/agently-core/internal/debugtrace"
	gfread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
	asynccfg "github.com/viant/agently-core/protocol/async"
	asyncnarrator "github.com/viant/agently-core/protocol/async/narrator"
	bindpkg "github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/tool"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolasyncconfig "github.com/viant/agently-core/protocol/tool/asyncconfig"
	toolobservation "github.com/viant/agently-core/protocol/tool/service/observation"
	runtimeprojection "github.com/viant/agently-core/runtime/projection"
	runtimerecovery "github.com/viant/agently-core/runtime/recovery"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/usage"
	"github.com/viant/agently-core/service/core"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
	elact "github.com/viant/agently-core/service/elicitation/action"
	"github.com/viant/agently-core/service/shared/asyncwait"
	toolapproval "github.com/viant/agently-core/service/shared/toolapproval"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
	skillsvc "github.com/viant/agently-core/service/skill"
)

var queueDrainGuards = &convGuardMap{m: make(map[string]*int32)}

type convGuardMap struct {
	mu sync.Mutex
	m  map[string]*int32
}

func stopRunHeartbeatThen(stop func(), finalize func() error) error {
	if stop != nil {
		stop()
	}
	if finalize == nil {
		return nil
	}
	return finalize()
}

func steeringDirective(checkpoint turnTaskCheckpoint) *bindpkg.Message {
	if !checkpoint.Found {
		return nil
	}
	messageID := strings.TrimSpace(checkpoint.MessageID)
	if messageID == "" {
		return nil
	}
	return &bindpkg.Message{
		ID:        "steer-directive:" + messageID,
		Kind:      bindpkg.MessageKindChatAssistant,
		Role:      string(llm.RoleSystem),
		CreatedAt: checkpoint.CreatedAt,
		Content: strings.TrimSpace(
			fmt.Sprintf(
				"Steering update: the latest user task message in this running turn is a steering follow-up (messageId=%s, createdAt=%s). Treat that user task message as an in-flight steering request, continue the current task instead of restarting, and prioritize addressing that steering update in your next action or response. If you need to send a visible steering acknowledgement or mid-turn progress update while work continues, use the message:add tool with interim unset or false so it emits a visible message row, then continue the current turn.",
				messageID,
				checkpoint.CreatedAt.UTC().Format(time.RFC3339Nano),
			),
		),
	}
}

func asyncStatusSnapshotReason(rec *asynccfg.OperationRecord) string {
	if rec == nil {
		return ""
	}
	switch rec.State {
	case asynccfg.StateCompleted:
		return "success"
	case asynccfg.StateFailed:
		return "failure"
	case asynccfg.StateCanceled:
		return "canceled"
	default:
		return strings.TrimSpace(string(rec.State))
	}
}

func asyncStatusSnapshotPayload(rec *asynccfg.OperationRecord) asynccfg.AggregatedResult {
	if rec == nil {
		return asynccfg.AggregatedResult{}
	}
	item := asynccfg.AggregatedItem{
		OperationID:      strings.TrimSpace(rec.ID),
		ToolName:         strings.TrimSpace(rec.ToolName),
		OperationIntent:  strings.TrimSpace(rec.OperationIntent),
		OperationSummary: strings.TrimSpace(rec.OperationSummary),
		State:            rec.State,
		Reason:           asyncStatusSnapshotReason(rec),
		Detail:           strings.TrimSpace(rec.Message),
	}
	if len(rec.KeyData) > 0 {
		item.Payload = append(json.RawMessage(nil), rec.KeyData...)
	}
	return asynccfg.AggregatedResult{
		Items:          []asynccfg.AggregatedItem{item},
		OpsStillActive: !rec.Terminal(),
	}
}

func cloneToolArgs(args map[string]interface{}) map[string]interface{} {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}

func (s *Service) asyncStatusReplayMessages(ctx context.Context, turn runtimerequestctx.TurnMeta, checkpoint turnTaskCheckpoint) []*bindpkg.Message {
	if s == nil || s.asyncManager == nil {
		return nil
	}
	ops := s.asyncManager.ActiveWaitOps(ctx, turn.ConversationID, turn.TurnID)
	if len(ops) == 0 {
		return nil
	}
	messages := make([]*bindpkg.Message, 0, len(ops))
	for _, rec := range ops {
		if rec == nil {
			continue
		}
		toolCallID := strings.TrimSpace(rec.ToolCallID)
		toolName := strings.TrimSpace(rec.ToolName)
		if toolCallID == "" || toolName == "" {
			logx.WarnCtxf(ctx, "conversation", "agent.runPlan steer status snapshot skipped op_id=%q tool_call_id=%q tool=%q", strings.TrimSpace(rec.ID), toolCallID, toolName)
			continue
		}
		payload := asyncStatusSnapshotPayload(rec)
		data, err := json.Marshal(payload)
		if err != nil {
			logx.WarnCtxf(ctx, "conversation", "agent.runPlan steer status snapshot marshal failed op_id=%q err=%v", strings.TrimSpace(rec.ID), err)
			continue
		}
		messageID := strings.TrimSpace("async-status-snapshot:" + strings.TrimSpace(rec.ID) + ":" + strings.TrimSpace(checkpoint.MessageID))
		messages = append(messages, &bindpkg.Message{
			ID:        messageID,
			Kind:      bindpkg.MessageKindToolResult,
			Role:      string(llm.RoleAssistant),
			CreatedAt: time.Now(),
			ToolOpID:  toolCallID,
			ToolName:  toolName,
			ToolArgs:  cloneToolArgs(rec.StatusArgs),
			Content:   string(data),
		})
	}
	return messages
}

func (s *Service) waitForAsyncChangeOrSteer(ctx context.Context, turn runtimerequestctx.TurnMeta, checkpoint turnTaskCheckpoint) (bool, error) {
	if s == nil || s.asyncManager == nil {
		return false, nil
	}
	signalAfter := s.asyncManager.TurnSignalVersion(turn.ConversationID, turn.TurnID)
	if pending, err := s.hasNewTurnTaskSince(ctx, turn, checkpoint); err != nil || pending {
		return pending, err
	}
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan error, 1)
	go func() {
		results <- s.asyncManager.WaitForChangeSince(waitCtx, turn.ConversationID, turn.TurnID, signalAfter)
	}()

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case err := <-results:
		if err != nil {
			return false, err
		}
		pending, checkErr := s.hasNewTurnTaskSince(ctx, turn, checkpoint)
		if checkErr != nil || pending {
			return pending, checkErr
		}
		return false, nil
	}
}

func (g *convGuardMap) acquire(convID string) bool {
	g.mu.Lock()
	v, ok := g.m[convID]
	if !ok {
		v = new(int32)
		g.m[convID] = v
	}
	g.mu.Unlock()
	return atomic.CompareAndSwapInt32(v, 0, 1)
}

func (g *convGuardMap) release(convID string) {
	g.mu.Lock()
	if v, ok := g.m[convID]; ok {
		atomic.StoreInt32(v, 0)
	}
	g.mu.Unlock()
}

// publishPresetAssistantMessage writes the workspace-intake preset answer (or
// clarification) as the turn's assistant message. The message goes through the
// existing conversation client's PatchMessage path, which both persists to
// the DB and publishes the standard streaming.EventTypeAssistant SSE event
// — same write path normal turns take. This keeps the transcript and the
// SSE stream consistent regardless of whether the assistant content came
// from the workspace-intake LLM call or from a per-agent generate.
//
// Returns nil when the conversation client is not bound (tests), so callers
// always see the preset content on output.Content even when persistence is
// unavailable.
func (s *Service) publishAssistantMessageWithStatus(ctx context.Context, input *QueryInput, text, status string) error {
	if s == nil || s.conversation == nil || input == nil {
		return nil
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	convID := strings.TrimSpace(input.ConversationID)
	turnID := strings.TrimSpace(input.MessageID)
	msg := apiconv.NewMessage()
	msg.SetId(uuid.New().String())
	if convID != "" {
		msg.SetConversationID(convID)
	}
	if turnID != "" {
		msg.SetTurnID(turnID)
	}
	msg.SetRole("assistant")
	msg.SetType("text")
	msg.SetContent(text)
	if s := strings.TrimSpace(status); s != "" {
		msg.SetStatus(s)
	}
	return s.conversation.PatchMessage(runtimerequestctx.WithMessageAddEvent(ctx), msg)
}

func (s *Service) publishPresetAssistantMessage(ctx context.Context, input *QueryInput, text, kind string) error {
	status := ""
	if k := strings.TrimSpace(kind); k != "" {
		status = "intake." + k
	}
	return s.publishAssistantMessageWithStatus(ctx, input, text, status)
}

func (s *Service) Query(ctx context.Context, input *QueryInput, output *QueryOutput) (retErr error) {
	defer func() {
		if output == nil {
			return
		}
		snapshot, ok := runtimeprojection.SnapshotFromContext(ctx)
		if !ok {
			return
		}
		output.Projection = &snapshot
	}()
	queryStarted := time.Now()
	ctx = s.bindAuthFromInputContext(ctx, input)
	ctx = bindEffectiveUserFromInput(ctx, input)
	if input != nil {
		logx.Infof("conversation", "agent.Query serviceStart at=%s convo=%q message_id=%q agent_id=%q user_id=%q", queryStarted.Format(time.RFC3339Nano), strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), strings.TrimSpace(input.AgentID), strings.TrimSpace(input.UserId))
	}
	if input != nil && input.Context != nil {
		if clientID, _ := input.Context["uiClientId"].(string); strings.TrimSpace(clientID) != "" {
			ctx = runtimerequestctx.WithPreferredUIClientID(ctx, strings.TrimSpace(clientID))
		}
	}
	if input != nil && strings.TrimSpace(input.MessageID) == "" {
		input.MessageID = uuid.New().String()
	}
	envStarted := time.Now()
	if err := s.ensureEnvironment(ctx, input); err != nil {
		return err
	}
	logx.Infof("conversation", "agent.Query stage ensureEnvironment convo=%q elapsed=%s", strings.TrimSpace(input.ConversationID), time.Since(envStarted))
	if err := s.repairResumedAsyncStatusRows(ctx, input); err != nil {
		logx.Warnf("conversation", "agent.Query resume async-status repair convo=%q err=%v", strings.TrimSpace(input.ConversationID), err)
	}
	if input == nil || input.Agent == nil {
		return fmt.Errorf("invalid input: agent is required")
	}
	output.ConversationID = input.ConversationID
	output.TurnID = input.MessageID

	// Workspace-intake preset short-circuit ("ONE LLM call for capability
	// turns"). When the classifier produced action=answer or action=clarify,
	// ensureAgent stashes the text under PresetAssistantTextKey. Publish it
	// directly as the assistant response and return — no agent LLM call
	// runs. Total intake-side LLM calls for the turn: exactly 1 (the
	// classifier's).
	if presetText, presetKind := presetAssistantFromContext(input.Context); presetText != "" {
		output.TurnID = input.MessageID
		output.MessageID = input.MessageID
		output.Content = presetText
		logx.Infof("conversation",
			"agent.Query preset short-circuit convo=%q kind=%q text_len=%d",
			strings.TrimSpace(input.ConversationID), presetKind, len(presetText))
		if err := s.publishPresetAssistantMessage(ctx, input, presetText, presetKind); err != nil {
			// Persistence failure must not swallow the answer for the caller
			// (the text is already in output.Content). Log and continue.
			logx.Warnf("conversation",
				"agent.Query preset persist error convo=%q kind=%q err=%v",
				strings.TrimSpace(input.ConversationID), presetKind, err)
		}
		return nil
	}

	if queued, err := s.tryQueueTurn(ctx, input); err != nil {
		return err
	} else if queued {
		output.TurnID = input.MessageID
		output.MessageID = input.MessageID
		output.Content = ""
		return nil
	}
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID:  strings.TrimSpace(input.ConversationID),
		TurnID:          strings.TrimSpace(input.MessageID),
		ParentMessageID: strings.TrimSpace(input.MessageID),
		Assistant:       strings.TrimSpace(input.AgentID),
	})
	logx.Infof("conversation", "agent.Query start convo=%q agent_id=%q user_id=%q query_len=%d query_head=%q query_tail=%q tools_allowed=%d", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.Agent.ID), strings.TrimSpace(input.UserId), len(input.Query), textutil.Head(input.Query, 512), textutil.Tail(input.Query, 512), len(input.ToolsAllowed))
	sysPromptEngine := ""
	sysPromptURI := ""
	instructionEngine := ""
	instructionURI := ""
	if input.Agent.SystemPrompt != nil {
		sysPromptEngine = strings.TrimSpace(input.Agent.SystemPrompt.Engine)
		sysPromptURI = strings.TrimSpace(input.Agent.SystemPrompt.URI)
	}
	if ip := input.Agent.EffectiveInstructionPrompt(); ip != nil {
		instructionEngine = strings.TrimSpace(ip.Engine)
		instructionURI = strings.TrimSpace(ip.URI)
	}
	delegEnabled := false
	delegDepth := 0
	if input.Agent.Delegation != nil {
		delegEnabled = input.Agent.Delegation.Enabled
		delegDepth = input.Agent.Delegation.MaxDepth
	}
	logx.Infof("conversation", "agent.Query config agent_id=%q delegation.enabled=%v delegation.maxDepth=%d systemPrompt.engine=%q systemPrompt.uri=%q instruction.engine=%q instruction.uri=%q", strings.TrimSpace(input.Agent.ID), delegEnabled, delegDepth, sysPromptEngine, sysPromptURI, instructionEngine, instructionURI)

	if s.tokenProvider != nil {
		userID := authctx.EffectiveUserID(ctx)
		if userID != "" {
			provider := authctx.Provider(ctx)
			if provider == "" {
				provider = "oauth"
			}
			ctx, _ = s.tokenProvider.EnsureTokens(ctx, token.Key{Subject: userID, Provider: provider})
		}
	}

	s.captureSecurityContext(ctx, input)
	ctx, _ = withWarnings(ctx)

	ctx, agg := usage.WithAggregator(ctx)
	turn := runtimerequestctx.TurnMeta{
		Assistant:       input.Agent.ID,
		ConversationID:  input.ConversationID,
		TurnID:          input.MessageID,
		ParentMessageID: "",
	}
	ctx = runtimerequestctx.WithTurnMeta(ctx, turn)
	ctx = runtimerequestctx.WithRunMeta(ctx, runtimerequestctx.RunMeta{RunID: turn.TurnID})

	var cancel func()
	ctx, cancel = s.registerTurnCancel(ctx, turn)
	defer cancel()
	var turnStatus string
	var turnRunErr error
	turnFinalized := false
	if pol := s.resolveToolPolicy(input); pol != nil {
		ctx = tool.WithPolicy(ctx, pol)
	}
	ctx = runtimeprojection.WithState(ctx)
	ctx = toolapprovalqueue.WithState(ctx)
	ctx = toolasyncconfig.WithState(ctx)
	ctx = asyncwait.WithState(ctx)
	if s.elicitation != nil {
		ctx = toolapproval.WithElicitor(ctx, &agentToolApprovalElicitor{elicService: s.elicitation})
	}
	if s.asyncManager != nil {
		ctx = toolexec.WithAsyncManager(ctx, s.asyncManager)
	}
	ctx = toolexec.WithAsyncConversation(ctx, s.conversation)

	if d := stuckWarnDuration(); d > 0 {
		warnCtx, warnCancel := context.WithCancel(ctx)
		defer warnCancel()
		go func(convoID, turnID string, dur time.Duration) {
			timer := time.NewTimer(dur)
			defer timer.Stop()
			select {
			case <-warnCtx.Done():
				return
			case <-timer.C:
				logx.Warnf("conversation", "agent.turn stuck warning convo=%q turn_id=%q elapsed=%s", strings.TrimSpace(convoID), strings.TrimSpace(turnID), dur.String())
			}
		}(turn.ConversationID, turn.TurnID, d)
	}
	displayQuery := strings.TrimSpace(input.DisplayQuery)
	if displayQuery == "" {
		displayQuery = strings.TrimSpace(input.Query)
	}
	if ask := resolveUserAsk(input, displayQuery); ask != "" {
		ctx = runtimerequestctx.WithUserAsk(ctx, ask)
	}
	rawUserContent := displayQuery
	userContent := displayQuery

	startTurnMeta := turn
	if !input.SkipInitialUserMessage {
		// Persist the turn row before the first user message so message.turn_id
		// always points at an existing turn. Direct/manual submissions backfill
		// started_by_message_id after the starter message is stored.
		startTurnMeta.ParentMessageID = ""
	}
	if err := s.startTurn(ctx, startTurnMeta, strings.TrimSpace(input.ScheduleId)); err != nil {
		return err
	}
	rawStopRunHeartbeat := func() {}
	if strings.TrimSpace(input.ScheduleId) == "" {
		rawStopRunHeartbeat = s.startRunHeartbeat(ctx, turn)
	}
	var stopRunHeartbeatOnce sync.Once
	stopRunHeartbeat := func() {
		stopRunHeartbeatOnce.Do(rawStopRunHeartbeat)
	}
	defer stopRunHeartbeat()
	if strings.TrimSpace(input.AgentID) != "" {
		upd := apiconv.NewTurn()
		upd.SetId(turn.TurnID)
		upd.SetConversationID(turn.ConversationID)
		upd.SetAgentIDUsed(strings.TrimSpace(input.AgentID))
		_ = s.conversation.PatchTurn(ctx, upd)
	}
	defer func() {
		if turnFinalized {
			return
		}
		finalStatus := strings.TrimSpace(turnStatus)
		finalErr := turnRunErr
		if finalStatus == "" {
			if finalErr == nil {
				finalErr = retErr
			}
			finalStatus = "failed"
			if errors.Is(finalErr, context.Canceled) || errors.Is(finalErr, context.DeadlineExceeded) {
				finalStatus = "canceled"
			} else if s.isTurnCanceled(context.WithoutCancel(ctx), turn.ConversationID, turn.TurnID) {
				finalStatus = "canceled"
			}
		}
		if err := stopRunHeartbeatThen(stopRunHeartbeat, func() error {
			return s.finalizeTurn(ctx, turn, finalStatus, finalErr)
		}); err != nil {
			if retErr == nil {
				retErr = err
			}
			return
		}
		turnFinalized = true
		logx.Infof("conversation", "agent.Query finalize ok convo=%q turn_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(finalStatus))
	}()
	logx.Infof("conversation", "agent.Query startTurn ok convo=%q turn_id=%q parent_message_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(turn.ParentMessageID))

	// Intake sidecar: runs after the turn is active so turn_started is the
	// first execution lifecycle event, but before tool selection so its output
	// still informs bundle/routing decisions.
	s.maybeRunIntakeSidecar(ctx, input)

	if input.SkipInitialUserMessage {
		logx.Infof("conversation", "agent.Query skip addUserMessage convo=%q turn_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	} else {
		if err := s.persistInitialUserMessage(ctx, &turn, input.UserId, userContent, rawUserContent); err != nil {
			return err
		}
		logx.Infof("conversation", "agent.Query addUserMessage ok convo=%q turn_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	}
	ctx = runtimerequestctx.WithTurnMeta(ctx, turn)

	if handled, err := s.maybeRunDirectAction(ctx, input, output); handled {
		if err != nil {
			return err
		}
		turnStatus = "succeeded"
		turnRunErr = nil
		return nil
	}

	if pErr := s.maybeRunPlannerPass(ctx, input); pErr != nil {
		var handled *plannerHandledError
		if errors.As(pErr, &handled) {
			output.TurnID = input.MessageID
			output.MessageID = input.MessageID
			output.Content = handled.content
			turnStatus = "succeeded"
			turnRunErr = nil
			return nil
		}
		return pErr
	}

	toolRouterStarted := time.Now()
	s.maybeAutoSelectToolBundles(ctx, input)
	logx.Infof("conversation", "agent.Query stage toolAutoSelection convo=%q message_id=%q elapsed=%s bundles=%d", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), time.Since(toolRouterStarted), len(input.ToolBundles))

	s.tryMergePromptIntoContext(input)
	workdir := ensureResolvedWorkdir(input)
	ctx = toolexec.WithWorkdir(ctx, workdir)
	ctx = toolobservation.WithState(ctx)
	contextStarted := time.Now()
	if err := s.updatedConversationContext(ctx, input.ConversationID, input); err != nil {
		return err
	}
	logx.Infof("conversation", "agent.Query stage updateConversationContext convo=%q elapsed=%s", strings.TrimSpace(input.ConversationID), time.Since(contextStarted))
	logx.Infof("conversation", "agent.Query prepared convo=%q turn_id=%q message_id=%q", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), strings.TrimSpace(input.MessageID))

	if err := s.processAttachments(ctx, turn, input); err != nil {
		return err
	}
	logx.Infof("conversation", "agent.Query processAttachments ok convo=%q turn_id=%q count=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), len(input.Attachments))

	if s.defaults != nil && s.defaults.ToolCallTimeoutSec > 0 {
		d := time.Duration(s.defaults.ToolCallTimeoutSec) * time.Second
		ctx = toolexec.WithToolTimeout(ctx, d)
	}

	var (
		status string
		err    error
	)
	for {
		runPlanStarted := time.Now()
		checkpoint, ckErr := s.latestTurnTaskCheckpoint(ctx, turn)
		if ckErr != nil {
			logx.Warnf("conversation", "agent.Query steer checkpoint error convo=%q turn_id=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), ckErr)
		}
		status, err = s.runPlanAndStatus(ctx, input, output)

		turnStatus = status
		turnRunErr = err

		logx.Infof("conversation", "agent.Query stage runPlanAndStatus convo=%q turn_id=%q status=%q elapsed=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status), time.Since(runPlanStarted))
		if err != nil {
			logx.Errorf("conversation", "agent.Query runPlan error convo=%q turn_id=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), err)
			break
		}
		logx.Infof("conversation", "agent.Query runPlan ok convo=%q turn_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status))
		if !strings.EqualFold(strings.TrimSpace(status), "succeeded") {
			break
		}
		followUpCheckpoint := effectiveFollowUpCheckpoint(checkpoint, output)
		pending, pErr := s.hasNewTurnTaskSince(ctx, turn, followUpCheckpoint)
		if pErr != nil {
			logx.Warnf("conversation", "agent.Query steer follow-up check error convo=%q turn_id=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), pErr)
			break
		}
		if !pending {
			break
		}
		if latest, latestErr := s.latestTurnTaskCheckpoint(ctx, turn); latestErr == nil && latest.Found {
			output.nextSteerCheckpoint = latest
		}
		logx.Infof("conversation", "agent.Query steer follow-up detected convo=%q turn_id=%q rerunning plan loop", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	}

	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("execution of query function canceled: %w", err)
		}
		return fmt.Errorf("execution of query function failed: %w", err)
	}

	if err := stopRunHeartbeatThen(stopRunHeartbeat, func() error {
		return s.finalizeTurn(ctx, turn, status, err)
	}); err != nil {
		return err
	}
	turnFinalized = true
	logx.Infof("conversation", "agent.Query finalize ok convo=%q turn_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status))
	_ = s.updateDefaultModel(ctx, turn, output)

	fetchStarted := time.Now()
	conv, err := s.fetchConversationWithRetry(ctx, input.ConversationID, apiconv.WithIncludeToolCall(true))
	if err != nil {
		return fmt.Errorf("cannot get conversation: %w", err)
	}
	logx.Infof("conversation", "agent.Query stage fetchConversation convo=%q elapsed=%s", strings.TrimSpace(input.ConversationID), time.Since(fetchStarted))
	if conv == nil {
		return fmt.Errorf("cannot get conversation: not found: %s", strings.TrimSpace(input.ConversationID))
	}
	output.Usage = agg
	if ws := warningsFrom(ctx); len(ws) > 0 {
		output.Warnings = ws
	}
	if err := s.executeChainsAfter(ctx, input, output, turn, conv, status); err != nil {
		return err
	}
	if conv.HasConversationParent() || conv.ScheduleId != nil {
		logx.Infof("conversation", "agent.Query done convo=%q turn_id=%q elapsed=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), time.Since(queryStarted))
		return nil
	}
	err = s.summarizeIfNeeded(ctx, input, conv)
	if err != nil {
		return fmt.Errorf("failed summarizing: %w", err)
	}
	logx.Infof("conversation", "agent.Query done convo=%q turn_id=%q elapsed=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), time.Since(queryStarted))
	return nil
}

func resolveUserAsk(input *QueryInput, displayQuery string) string {
	if input != nil && input.Context != nil {
		if title, ok := input.Context["intake.title"].(string); ok && strings.TrimSpace(title) != "" {
			return strings.TrimSpace(title)
		}
	}
	return strings.TrimSpace(displayQuery)
}

func shouldContinueAfterAsyncChange(planEmpty bool, hasActiveWaitOps bool, changedOpsCount int, terminalContentReady bool) bool {
	if changedOpsCount <= 0 {
		return false
	}
	if !planEmpty || hasActiveWaitOps {
		return true
	}
	return !terminalContentReady
}

func (s *Service) resolveToolPolicy(input *QueryInput) *tool.Policy {
	if len(input.ToolsAllowed) > 0 {
		return &tool.Policy{Mode: tool.ModeAuto, AllowList: input.ToolsAllowed}
	}
	if s == nil || s.defaults == nil {
		return nil
	}
	mode := tool.NormalizeMode(s.defaults.ToolApproval.Mode)
	allowList := append([]string(nil), s.defaults.ToolApproval.AllowList...)
	blockList := append([]string(nil), s.defaults.ToolApproval.BlockList...)
	if (mode == "" || mode == tool.ModeAuto) && len(allowList) == 0 && len(blockList) == 0 {
		return nil
	}
	return &tool.Policy{
		Mode:      mode,
		AllowList: allowList,
		BlockList: blockList,
	}
}

func (s *Service) addAttachment(ctx context.Context, turn runtimerequestctx.TurnMeta, att *bindpkg.Attachment) error {
	pid := uuid.New().String()
	payload := apiconv.NewPayload()
	payload.SetId(pid)
	payload.SetKind("model_request")
	payload.SetMimeType(att.MIMEType())
	payload.SetSizeBytes(len(att.Data))
	payload.SetStorage("inline")
	payload.SetInlineBody(att.Data)
	if strings.TrimSpace(att.URI) != "" {
		payload.SetURI(att.URI)
	}
	if err := s.conversation.PatchPayload(ctx, payload); err != nil {
		return fmt.Errorf("failed to persist attachment payload: %w", err)
	}

	parentMsgID := strings.TrimSpace(turn.ParentMessageID)
	if parentMsgID == "" {
		parentMsgID = strings.TrimSpace(turn.TurnID)
	}

	name := strings.TrimSpace(att.Name)
	if name == "" && strings.TrimSpace(att.URI) != "" {
		name = path.Base(strings.TrimSpace(att.URI))
	}
	if name == "" {
		name = "(attachment)"
	}

	_, err := apiconv.AddMessage(ctx, s.conversation, &turn,
		apiconv.WithRole("user"),
		apiconv.WithType("control"),
		apiconv.WithParentMessageID(parentMsgID),
		apiconv.WithContent(name),
		apiconv.WithAttachmentPayloadID(pid),
	)
	if err != nil {
		return fmt.Errorf("failed to persist attachment message: %w", err)
	}
	return nil
}

func (s *Service) runPlanLoop(ctx context.Context, input *QueryInput, queryOutput *QueryOutput) error {
	iter := 0
	var resolvedModel string
	var loopHistoryMsgs []*bindpkg.Message
	var activeInlineSkillNames []string

	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok {
		return fmt.Errorf("failed to get turn meta")
	}
	mode := runtimerecovery.ModePruneCompact
	if input != nil && input.Agent != nil {
		if v := strings.TrimSpace(input.Agent.ContextRecoveryMode); v != "" {
			mode = v
		}
	}
	ctx = runtimerecovery.WithMode(ctx, mode)

	input.RequestTime = time.Now()
	for {
		iter++
		ctx = runtimerequestctx.WithRunMeta(ctx, runtimerequestctx.RunMeta{RunID: turn.TurnID, Iteration: iter})
		iterHistoryMsgs := append([]*bindpkg.Message(nil), loopHistoryMsgs...)
		bypassAsyncWait := false
		if queryOutput != nil && queryOutput.nextSteerCheckpoint.Found {
			if len(queryOutput.nextSteerStatusMessages) > 0 {
				iterHistoryMsgs = mergeReplayMessages(iterHistoryMsgs, queryOutput.nextSteerStatusMessages)
			}
			if directive := steeringDirective(queryOutput.nextSteerCheckpoint); directive != nil {
				iterHistoryMsgs = append(iterHistoryMsgs, directive)
				bypassAsyncWait = true
			}
			queryOutput.nextSteerCheckpoint = turnTaskCheckpoint{}
			queryOutput.nextSteerStatusMessages = nil
		}
		if iter == 1 && len(iterHistoryMsgs) == 0 && s.skillSvc != nil {
			if name, args, body, ok := preactivatedSkillPayload(input); ok {
				logx.Infof("conversation", "agent.runPlan preactivated skill payload detected convo=%q turn_id=%q skill=%q body_len=%d agent_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(name), len(body), strings.TrimSpace(input.AgentID))
				input.Query = strings.TrimSpace(args)
			} else if name, args, ok := parseExplicitSkillInvocation(input.Query); ok {
				logx.Infof("conversation", "agent.runPlan explicit skill invocation detected convo=%q turn_id=%q skill=%q args_len=%d agent_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(name), len(args), strings.TrimSpace(input.AgentID))
				var (
					body string
					err  error
				)
				skillCtx := ctx
				if override := skillActivationModeOverride(input, name); override != "" {
					skillCtx = skillsvc.WithActivationModeOverride(skillCtx, name, override)
				}
				if convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx)); convID != "" && input.Agent != nil {
					body, err = s.skillSvc.ActivateForConversationWithAgent(skillCtx, convID, input.Agent, name, args)
				} else if convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx)); convID != "" {
					body, err = s.skillSvc.ActivateForConversation(skillCtx, convID, name, args)
				} else {
					body, err = s.skillSvc.Activate(input.Agent, name, args)
				}
				if err != nil {
					logx.Warnf("conversation", "agent.runPlan explicit skill activation failed convo=%q turn_id=%q skill=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(name), err)
				}
				if err == nil {
					logx.Infof("conversation", "agent.runPlan explicit skill activation ok convo=%q turn_id=%q skill=%q body_len=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(name), len(strings.TrimSpace(body)))
					activeInlineSkillNames = appendUniqueSkillName(activeInlineSkillNames, name)
					opID := "skill-activate-" + name
					input.Query = strings.TrimSpace(args)
					msg := &bindpkg.Message{
						ID:          opID,
						Kind:        bindpkg.MessageKindToolResult,
						Role:        string(llm.RoleAssistant),
						ToolOpID:    opID,
						ToolName:    "llm/skills:activate",
						ToolArgs:    map[string]interface{}{"name": name, "args": args},
						ToolTraceID: strings.TrimSpace(runtimerequestctx.TurnTrace(turn.TurnID)),
						Content:     strings.TrimSpace(body),
					}
					iterHistoryMsgs = append(iterHistoryMsgs, msg)
					loopHistoryMsgs = mergeReplayMessages(loopHistoryMsgs, []*bindpkg.Message{msg})
				}
			}
		}
		iterStart := time.Now()
		s.updateRunIteration(ctx, turn, iter, input.ScheduleId)

		checkpoint, ckErr := s.latestTurnTaskCheckpoint(ctx, turn)
		if ckErr != nil {
			logx.Warnf("conversation", "agent.runPlan checkpoint error convo=%q turn_id=%q iter=%d err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, ckErr)
		}
		if queryOutput != nil {
			queryOutput.lastTaskCheckpoint = checkpoint
		}
		logx.Infof("conversation", "agent.runPlan iter start convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
		iterCtx := withLoopHistoryMessages(ctx, iterHistoryMsgs)
		bindingStart := time.Now()
		binding, bErr := s.BuildBinding(iterCtx, input)
		if bErr != nil {
			return bErr
		}
		logx.Infof("conversation", "agent.runPlan iter binding ready convo=%q turn_id=%q iter=%d binding_elapsed=%s total_elapsed=%s",
			strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, time.Since(bindingStart), time.Since(iterStart))
		appendRuntimeClockSystemDocument(binding, time.Now())
		appendMissingReplayMessages(&binding.History, iterHistoryMsgs)
		activeState := activeInlineSkillState{}
		activeNames := []string(nil)
		if s.skillSvc != nil {
			activeState = resolveActiveInlineSkillState(&binding.History, input, s.skillSvc, input.Agent)
			activeNames = mergeActiveSkillNames(activeState.Names, activeInlineSkillNames)
		}
		if s.skillSvc != nil && input.Agent != nil {
			ctx = skillsvc.WithRuntimeState(ctx, s.skillSvc, input.Agent, activeNames)
		}
		keys := []string{}
		for k := range binding.Context {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		modelSelection := input.Agent.ModelSelection
		modelSource := ""
		if strings.TrimSpace(resolvedModel) != "" && strings.TrimSpace(input.ModelOverride) == "" {
			modelSelection.Model = resolvedModel
			modelSelection.Preferences = nil
			modelSource = "agent.model"
		} else {
			if input.ModelOverride != "" {
				modelSelection.Model = input.ModelOverride
				modelSource = runtimeModelSource(input)
				if modelSource == "" {
					modelSource = "query.modelOverride"
				}
			} else if input.ModelPreferences != nil {
				modelSelection.Model = ""
			}
			if input.ModelPreferences != nil {
				modelSelection.Preferences = input.ModelPreferences
			}
			if input.Agent != nil {
				modelSelection.AllowedModels = nil
				if len(input.Agent.AllowedModels) > 0 {
					for _, id := range input.Agent.AllowedModels {
						if v := strings.TrimSpace(id); v != "" {
							modelSelection.AllowedModels = append(modelSelection.AllowedModels, v)
						}
					}
				}
				modelSelection.AllowedProviders = nil
				if len(input.Agent.AllowedProviders) > 0 {
					for _, p := range input.Agent.AllowedProviders {
						if v := strings.TrimSpace(p); v != "" {
							modelSelection.AllowedProviders = append(modelSelection.AllowedProviders, v)
						}
					}
				} else if prov := deriveProviderFromModelRef(input.Agent.Model); prov != "" {
					modelSelection.AllowedProviders = []string{prov}
				}
			}
		}
		if input.Agent != nil && len(modelSelection.AllowedProviders) == 0 && len(modelSelection.AllowedModels) == 0 {
			if prov := deriveProviderFromModelRef(input.Agent.Model); prov != "" {
				modelSelection.AllowedProviders = []string{prov}
			}
		}
		if modelSource == "" && input.Agent != nil && strings.TrimSpace(modelSelection.Model) != "" {
			modelSource = "agent.model"
		}
		if modelSelection.Options == nil {
			modelSelection.Options = &llm.Options{}
		}
		if modelSelection.Options.Metadata == nil {
			modelSelection.Options.Metadata = map[string]interface{}{}
		}
		if modelSource != "" {
			setRuntimeModelSource(input, modelSource)
			modelSelection.Options.Metadata["modelSource"] = modelSource
		}
		if s.defaults != nil {
			previewLimit := s.defaults.PreviewSettings.ToolResultLimit
			if previewLimit <= 0 {
				previewLimit = s.defaults.PreviewSettings.Limit
			}
			if previewLimit > 0 {
				modelSelection.Options.Metadata["toolResultPreviewLimit"] = previewLimit
			}
		}
		// activeSkill is hoisted out of the `if s.skillSvc != nil` block
		// so downstream resolvers (e.g. the async narrator system-prompt
		// ladder) can inspect it without re-running skill discovery.
		activeSkill := activeState.Primary
		if s.skillSvc != nil {
			if len(activeNames) > 0 {
				if input.ModelOverride == "" && activeSkill != nil && strings.TrimSpace(activeSkill.Frontmatter.ModelValue()) != "" {
					if _, err := s.llm.ModelFinder().Find(ctx, strings.TrimSpace(activeSkill.Frontmatter.ModelValue())); err == nil {
						modelSelection.Model = strings.TrimSpace(activeSkill.Frontmatter.ModelValue())
						modelSelection.Preferences = nil
						modelSource = "skill.frontmatter"
					} else {
						modelSelection.Options.Metadata["modelSourceIntended"] = "skill.frontmatter"
						modelSelection.Options.Metadata["modelSourceIntendedValue"] = strings.TrimSpace(activeSkill.Frontmatter.ModelValue())
						modelSelection.Options.Metadata["modelSourceError"] = "model not in finder registry"
					}
				}
				if input.ModelOverride == "" && activeSkill != nil && modelSource != "skill.frontmatter" {
					if prefs := activeSkill.Frontmatter.ModelPreferencesValue(); prefs != nil {
						modelSelection.Model = ""
						modelSelection.Preferences = prefs
						modelSource = "skill.metadata.modelPreferences"
						modelSelection.Options.Metadata["activeSkillModelPreferences"] = map[string]interface{}{
							"intelligencePriority": prefs.IntelligencePriority,
							"speedPriority":        prefs.SpeedPriority,
							"costPriority":         prefs.CostPriority,
							"hints":                append([]string(nil), prefs.Hints...),
						}
					}
				}
				if input.ModelOverride == "" && s.defaults != nil && strings.TrimSpace(s.defaults.Skills.Model) != "" {
					if (modelSource == "" || modelSource == "agent.model") && modelSelection.Preferences == nil {
						modelSelection.Model = strings.TrimSpace(s.defaults.Skills.Model)
						modelSelection.Preferences = nil
						modelSource = "skills.model"
					}
				}
				if activeSkill != nil {
					modelSelection.Options.Metadata["activeSkillSourceName"] = strings.TrimSpace(activeSkill.Frontmatter.Name)
					if temp := activeSkill.Frontmatter.TemperatureValue(); temp != nil {
						modelSelection.Options.Temperature = *temp
						modelSelection.Options.Metadata["activeSkillTemperature"] = *temp
					}
					if maxTokens := activeSkill.Frontmatter.MaxTokensValue(); maxTokens > 0 {
						modelSelection.Options.MaxTokens = maxTokens
						modelSelection.Options.Metadata["activeSkillMaxTokens"] = maxTokens
					}
					if effort := strings.TrimSpace(strings.ToLower(activeSkill.Frontmatter.EffortValue())); effort != "" {
						if modelSelection.Options.Reasoning == nil {
							modelSelection.Options.Reasoning = &llm.Reasoning{}
						}
						modelSelection.Options.Reasoning.Effort = effort
						modelSelection.Options.Metadata["activeSkillReasoningEffort"] = effort
					}
					if activeSkill.Frontmatter.PreprocessEnabled() {
						modelSelection.Options.Metadata["activeSkillPreprocess"] = true
						timeoutSec := activeSkill.Frontmatter.PreprocessTimeoutValue()
						if timeoutSec <= 0 {
							timeoutSec = 10
						}
						modelSelection.Options.Metadata["activeSkillPreprocessTimeoutSeconds"] = timeoutSec
					}
				}
				names := make([]string, len(activeNames))
				copy(names, activeNames)
				modelSelection.Options.Metadata["activeSkillNames"] = names
				if v := strings.TrimSpace(modelSelection.Model); v != "" {
					modelSelection.Options.Metadata["activeSkillModel"] = v
				}
				if modelSource != "" {
					modelSelection.Options.Metadata["modelSource"] = modelSource
				}
				modelSelection.Options.Metadata["activeSkillConstraints"] = map[string]interface{}{}
			}
		}
		if s.asyncManager != nil && !bypassAsyncWait && s.asyncManager.HasActiveWaitOps(ctx, turn.ConversationID, turn.TurnID) {
			changedOps := s.asyncManager.ConsumeChanged(turn.ConversationID, turn.TurnID)
			if len(changedOps) > 0 {
				queryOutput.Content = ""
				continue
			}
			logx.Infof("conversation", "agent.runPlan async-wait-pre-model convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
			steerTriggered, err := s.waitForAsyncChangeOrSteer(ctx, turn, checkpoint)
			if err != nil {
				return err
			}
			if steerTriggered {
				if latest, latestErr := s.latestTurnTaskCheckpoint(ctx, turn); latestErr == nil && latest.Found && queryOutput != nil {
					queryOutput.nextSteerCheckpoint = latest
					queryOutput.nextSteerStatusMessages = s.asyncStatusReplayMessages(ctx, turn, latest)
				}
				logx.Infof("conversation", "agent.runPlan steer wake convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
			}
			queryOutput.Content = ""
			continue
		}
		if modelSelection.Options == nil {
			modelSelection.Options = &llm.Options{}
		}
		queryOutput.Model = modelSelection.Model
		queryOutput.Agent = input.Agent
		genInput := &core.GenerateInput{
			Prompt:         input.Agent.Prompt,
			SystemPrompt:   input.Agent.SystemPrompt,
			Instruction:    input.Agent.EffectiveInstructionPrompt(),
			Binding:        binding,
			ModelSelection: modelSelection,
		}
		genInput.UserID = strings.TrimSpace(input.UserId)
		if input.Agent != nil {
			genInput.AgentID = strings.TrimSpace(input.Agent.ID)
		}
		EnsureGenerateOptions(ctx, genInput, input.Agent)
		applyParallelToolCallOverride(input, genInput)
		if input.ReasoningEffort != nil {
			if v := strings.TrimSpace(*input.ReasoningEffort); v != "" {
				if genInput.ModelSelection.Options.Reasoning == nil {
					genInput.ModelSelection.Options.Reasoning = &llm.Reasoning{}
				}
				genInput.ModelSelection.Options.Reasoning.Effort = v
			}
		}
		logx.Infof("conversation", "agent.runPlan iter pre-orchestrator ready convo=%q turn_id=%q iter=%d total_elapsed=%s model=%q",
			strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, time.Since(iterStart), strings.TrimSpace(genInput.ModelSelection.Model))
		// Narrator system-prompt resolution ladder (lowest precedence
		// to highest):
		//
		//   1. Workspace default (`default.async.narrator.prompt`)
		//   2. Agent override (`Agent.AsyncNarratorPrompt`)
		//   3. Active-skill override (`Skill.Frontmatter.AsyncNarratorPrompt`)
		//
		// Each level overrides only when non-empty, so a missing field
		// at any tier falls through to the next lower tier. Empty at
		// all three levels is a bootstrap misconfiguration surfaced
		// inside the runner closure below.
		narratorSystemPrompt := ""
		narratorPromptSource := ""
		if s.defaults != nil && s.defaults.Async != nil && s.defaults.Async.Narrator != nil {
			if v := strings.TrimSpace(s.defaults.Async.Narrator.Prompt); v != "" {
				narratorSystemPrompt = v
				narratorPromptSource = "workspace.default"
			}
		}
		if input.Agent != nil {
			if v := strings.TrimSpace(input.Agent.AsyncNarratorPrompt); v != "" {
				narratorSystemPrompt = v
				narratorPromptSource = "agent"
			}
		}
		if activeSkill != nil {
			if v := strings.TrimSpace(activeSkill.Frontmatter.AsyncNarratorPromptValue()); v != "" {
				narratorSystemPrompt = v
				narratorPromptSource = "skill:" + strings.TrimSpace(activeSkill.Frontmatter.Name)
			}
		}
		ctx = toolexec.WithAsyncNarratorRunner(ctx, func(narratorCtx context.Context, in asyncnarrator.LLMInput) (string, error) {
			modelName := strings.TrimSpace(genInput.ModelSelection.Model)
			if modelName == "" {
				return "", fmt.Errorf("async narrator model is empty")
			}
			model, err := s.llm.ModelFinder().Find(narratorCtx, modelName)
			if err != nil {
				return "", err
			}
			if narratorSystemPrompt == "" {
				// Bootstrap should always populate this from the workspace
				// `default.async.narrator.prompt` baseline. An empty value
				// here means a caller bypassed DefaultsWithFallback —
				// surface it so the misconfiguration is visible.
				return "", fmt.Errorf("async narrator system prompt is empty (workspace default.async.narrator.prompt not initialized)")
			}
			req := &llm.GenerateRequest{
				Messages: []llm.Message{
					llm.NewSystemMessage(narratorSystemPrompt),
					llm.NewTextMessage(llm.RoleUser, strings.TrimSpace("user_ask: "+in.UserAsk+"\nintent: "+in.Intent+"\nsummary: "+in.Summary+"\nmessage: "+in.Message+"\nstatus: "+in.Status+"\ntool: "+in.Tool)),
				},
				Options: &llm.Options{
					Metadata: map[string]interface{}{
						"asyncNarrator":             true,
						"asyncNarrationMode":        "llm",
						"asyncNarratorOpID":         strings.TrimSpace(in.OperationID),
						"asyncNarratorUserAsk":      strings.TrimSpace(in.UserAsk),
						"asyncNarratorIntent":       strings.TrimSpace(in.Intent),
						"asyncNarratorSummary":      strings.TrimSpace(in.Summary),
						"asyncNarratorTool":         strings.TrimSpace(in.Tool),
						"asyncNarratorStatus":       strings.TrimSpace(in.Status),
						"asyncNarratorPromptSource": narratorPromptSource,
						"modelSource":               "async.narrator",
					},
				},
			}
			core.WriteLLMRequestDebugPayload(narratorCtx, modelName, req, nil, "narrator-"+strings.TrimSpace(in.OperationID))
			resp, err := model.Generate(narratorCtx, req)
			if err != nil {
				return "", err
			}
			var builder strings.Builder
			for _, choice := range resp.Choices {
				if txt := strings.TrimSpace(choice.Message.Content); txt != "" {
					builder.WriteString(txt)
				}
			}
			return strings.TrimSpace(builder.String()), nil
		})
		genOutput := &core.GenerateOutput{}
		planStart := time.Now()

		if logx.Enabled() && genInput.Binding != nil {
			msgs := genInput.Binding.History.LLMMessages()
			logx.Infof("conversation", "agent.runPlan iter=%d history_msgs=%d model=%q convo=%q turn_id=%q",
				iter, len(msgs), genInput.ModelSelection.Model,
				strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
			for i, m := range msgs {
				content := textutil.Head(m.Content, 120)
				toolCallCount := len(m.ToolCalls)
				logx.Infof("conversation", "  history[%d] role=%s tool_call_id=%q tool_calls=%d content_len=%d content_head=%q",
					i, m.Role, m.ToolCallId, toolCallCount, len(m.Content), content)
			}
			if debugtrace.Enabled() {
				debugtrace.Write("agent", "run_plan_request", map[string]any{
					"conversationID": strings.TrimSpace(turn.ConversationID),
					"turnID":         strings.TrimSpace(turn.TurnID),
					"iteration":      iter,
					"model":          strings.TrimSpace(genInput.ModelSelection.Model),
					"history":        debugtrace.SummarizeMessages(msgs),
				})
			}
		}

		aPlan, pErr := s.orchestrator.Run(ctx, genInput, genOutput)
		stepCount := 0
		if aPlan != nil {
			stepCount = len(aPlan.Steps)
		}
		logx.Infof("conversation", "agent.runPlan orchestrator done convo=%q turn_id=%q iter=%d steps=%d duration=%s",
			strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID),
			iter, stepCount, time.Since(planStart))
		if pErr != nil {
			return pErr
		}
		if s.orchestrator != nil {
			toolCalls := s.orchestrator.TurnToolResults(strings.TrimSpace(turn.TurnID))
			if len(toolCalls) > 0 {
				turnTraceID := strings.TrimSpace(runtimerequestctx.TurnTrace(turn.TurnID))
				nextHistory := make([]*bindpkg.Message, 0, len(toolCalls))
				for _, call := range toolCalls {
					id := strings.TrimSpace(call.ID)
					name := strings.TrimSpace(call.Name)
					if id == "" || name == "" {
						continue
					}
					if shouldSkipInjectedDocumentToolResultBody(call.Result) {
						continue
					}
					msgID := s.findToolMessageIDByOpID(ctx, turn.ConversationID, turn.TurnID, id)
					if msgID != "" {
						continue
					}
					nextHistory = append(nextHistory, &bindpkg.Message{
						ID:          msgID,
						Kind:        bindpkg.MessageKindToolResult,
						Role:        string(llm.RoleAssistant),
						ToolOpID:    id,
						ToolName:    name,
						ToolArgs:    call.Arguments,
						ToolTraceID: turnTraceID,
						Content:     strings.TrimSpace(call.Result),
					})
				}
				if len(nextHistory) > 0 {
					loopHistoryMsgs = mergeReplayMessages(loopHistoryMsgs, nextHistory)
				}
			}
		}
		if aPlan == nil {
			return fmt.Errorf("unable to generate plan")
		}
		if s.asyncManager != nil {
			changedOps := s.asyncManager.ConsumeChanged(turn.ConversationID, turn.TurnID)
			hasActiveWaitOps := s.asyncManager.HasActiveWaitOps(ctx, turn.ConversationID, turn.TurnID)
			terminalContentReady := aPlan.IsEmpty() && !hasActiveWaitOps && strings.TrimSpace(genOutput.Content) != ""
			if shouldContinueAfterAsyncChange(aPlan.IsEmpty(), hasActiveWaitOps, len(changedOps), terminalContentReady) {
				s.markAssistantMessageInterim(ctx, &turn, genOutput)
				if !hasActiveWaitOps {
					logx.Infof("conversation", "agent.runPlan async-rerun-after-status convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
				} else {
					logx.Infof("conversation", "agent.runPlan async-wait-after-status convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
				}
				queryOutput.Content = ""
				continue
			}
		}
		if strings.TrimSpace(resolvedModel) == "" && genInput != nil {
			if m := strings.TrimSpace(genInput.ModelSelection.Model); m != "" {
				resolvedModel = m
			}
		}
		queryOutput.Plan = aPlan
		logx.Infof("conversation", "agent.runPlan plan ready convo=%q turn_id=%q iter=%d steps=%d elicitation=%v empty=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, stepCount, aPlan != nil && aPlan.Elicitation != nil, aPlan != nil && aPlan.IsEmpty())
		if debugtrace.Enabled() {
			debugtrace.Write("agent", "run_plan_result", map[string]any{
				"conversationID": strings.TrimSpace(turn.ConversationID),
				"turnID":         strings.TrimSpace(turn.TurnID),
				"iteration":      iter,
				"planEmpty":      aPlan != nil && aPlan.IsEmpty(),
				"hasElicitation": aPlan != nil && aPlan.Elicitation != nil,
				"contentLen":     len(genOutput.Content),
				"steps":          summarizePlanSteps(aPlan),
			})
		}

		if aPlan.Elicitation != nil {
			if missing := missingRequired(aPlan.Elicitation, binding.Context); len(missing) == 0 {
				logx.Infof("conversation", "agent.runPlan elicitation satisfied by context convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
				aPlan.Elicitation = nil
				continue
			}
			if strings.EqualFold(strings.TrimSpace(input.ElicitationMode), "deferred") || strings.EqualFold(strings.TrimSpace(input.ElicitationMode), "async") {
				if _, err := s.elicitation.Record(ctx, &turn, "assistant", aPlan.Elicitation); err != nil {
					return err
				}
				queryOutput.Elicitation = aPlan.Elicitation
				return nil
			}
			logx.Infof("conversation", "agent.runPlan elicitation start convo=%q turn_id=%q iter=%d elicitation_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, strings.TrimSpace(aPlan.Elicitation.ElicitationId))
			ectx := ctx
			var cancel func()
			if s.defaults != nil && s.defaults.ElicitationTimeoutSec > 0 {
				ectx, cancel = context.WithTimeout(ctx, time.Duration(s.defaults.ElicitationTimeoutSec)*time.Second)
				defer cancel()
			}
			_, status, elicitPayload, err := s.elicitation.Elicit(ectx, &turn, "assistant", aPlan.Elicitation)
			if err != nil {
				logx.Errorf("conversation", "agent.runPlan elicitation done convo=%q turn_id=%q iter=%d status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, strings.TrimSpace(status), err)
			} else {
				logx.Infof("conversation", "agent.runPlan elicitation done convo=%q turn_id=%q iter=%d status=%q payload_keys=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, strings.TrimSpace(status), len(elicitPayload))
			}
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					if resolveErr := s.elicitation.Resolve(context.Background(), turn.ConversationID, aPlan.Elicitation.ElicitationId, "cancel", nil, "user_timeout"); resolveErr != nil {
						return resolveErr
					}
					queryOutput.Content = ""
					continue
				}
				return err
			}
			if elact.Normalize(status) != elact.Accept {
				return nil
			}
			continue
		}

		isTerminal := aPlan.IsEmpty()
		if isTerminal {
			if s.asyncManager != nil && s.asyncManager.HasActiveWaitOps(ctx, turn.ConversationID, turn.TurnID) {
				logx.Infof("conversation", "agent.runPlan async-wait-terminal convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
				s.markAssistantMessageInterim(ctx, &turn, genOutput)
				queryOutput.Content = ""
				continue
			}
			if strings.TrimSpace(genOutput.Content) != "" {
				modelcallctx.WaitFinish(ctx, 1500*time.Millisecond)
				msgID := strings.TrimSpace(genOutput.MessageID)
				if msgID == "" {
					msgID = strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
				}
				if msgID == "" {
					msgID = s.findLastInterimAssistantMessageID(ctx, turn.ConversationID, turn.TurnID)
				}
				if msgID == "" {
					msgID = s.findLastAssistantMessageID(ctx, turn.ConversationID, turn.TurnID)
				}
				logx.Infof("conversation", "runPlan-final patching msgID=%q interim=0 convo=%q turn=%q contentLen=%d",
					msgID, turn.ConversationID, turn.TurnID, len(genOutput.Content))
				if err := s.persistFinalAssistantMessage(ctx, &turn, msgID, genOutput.Content); err != nil {
					logx.Errorf("conversation", "runPlan-final patching msg=%q err=%v", msgID, err)
				}
			}
			pending, pErr := s.hasNewTurnTaskSince(ctx, turn, checkpoint)
			if pErr != nil {
				logx.Warnf("conversation", "agent.runPlan follow-up check error convo=%q turn_id=%q iter=%d err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, pErr)
			} else if pending {
				if latest, latestErr := s.latestTurnTaskCheckpoint(ctx, turn); latestErr == nil && latest.Found && queryOutput != nil {
					queryOutput.nextSteerCheckpoint = latest
				}
				logx.Infof("conversation", "agent.runPlan steer follow-up convo=%q turn_id=%q iter=%d duration=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, time.Since(iterStart))
				queryOutput.Content = ""
				continue
			}
			logx.Infof("conversation", "agent.runPlan completed convo=%q turn_id=%q iter=%d content_len=%d duration=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, len(genOutput.Content), time.Since(iterStart))
			queryOutput.Content = genOutput.Content
			return nil
		}
		waitingForUser, waitErr := s.turnAwaitingUserAction(ctx, turn)
		if waitErr != nil {
			return waitErr
		}
		if waitingForUser {
			logx.Infof("conversation", "agent.runPlan waiting-for-user convo=%q turn_id=%q iter=%d duration=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, time.Since(iterStart))
			queryOutput.Content = ""
			return nil
		}

		logx.Infof("conversation", "agent.runPlan continue convo=%q turn_id=%q iter=%d duration=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, time.Since(iterStart))
	}
}

func appendUniqueSkillName(names []string, name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return names
	}
	for _, existing := range names {
		if strings.EqualFold(strings.TrimSpace(existing), name) {
			return names
		}
	}
	return append(names, name)
}

func mergeActiveSkillNames(left, right []string) []string {
	if len(right) == 0 {
		return left
	}
	out := append([]string(nil), left...)
	for _, name := range right {
		out = appendUniqueSkillName(out, name)
	}
	return out
}

func (s *Service) findToolMessageIDByOpID(ctx context.Context, conversationID, turnID, opID string) string {
	if s == nil || s.conversation == nil {
		return ""
	}
	conversationID = strings.TrimSpace(conversationID)
	turnID = strings.TrimSpace(turnID)
	opID = strings.TrimSpace(opID)
	if conversationID == "" || turnID == "" || opID == "" {
		return ""
	}
	conv, err := s.conversation.GetConversation(ctx, conversationID, apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	if err != nil || conv == nil {
		return ""
	}
	for _, turn := range conv.GetTranscript() {
		if turn == nil || strings.TrimSpace(turn.Id) != turnID {
			continue
		}
		for _, msg := range turn.GetMessages() {
			if msg == nil {
				continue
			}
			for _, tm := range msg.ToolMessage {
				if tm == nil || tm.ToolCall == nil {
					continue
				}
				if strings.TrimSpace(tm.ToolCall.OpId) == opID {
					return strings.TrimSpace(tm.Id)
				}
			}
			if strings.EqualFold(strings.TrimSpace(msg.Type), "tool_op") {
				for _, tm := range msg.ToolMessage {
					if tm == nil || tm.ToolCall == nil {
						continue
					}
					if strings.TrimSpace(tm.ToolCall.OpId) == opID {
						return strings.TrimSpace(msg.Id)
					}
				}
			}
		}
	}
	return ""
}

func deriveProviderFromModelRef(modelRef string) string {
	v := strings.TrimSpace(modelRef)
	if v == "" {
		return ""
	}
	if idx := strings.IndexRune(v, '_'); idx > 0 {
		return strings.TrimSpace(v[:idx])
	}
	return ""
}

var sandboxMarkdownLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\((sandbox:[^)]+)\)`)

func (s *Service) rewriteGeneratedFileLinks(ctx context.Context, conversationID, turnID, msgID, content string) string {
	value := strings.TrimSpace(content)
	if value == "" || !strings.Contains(strings.ToLower(value), "sandbox:/") {
		return strings.TrimSpace(content)
	}

	store, ok := s.conversation.(apiconv.GeneratedFileClient)
	if !ok {
		return strings.TrimSpace(content)
	}
	in := &gfread.Input{
		ConversationID: strings.TrimSpace(conversationID),
		TurnID:         strings.TrimSpace(turnID),
		MessageID:      strings.TrimSpace(msgID),
		Has: &gfread.Has{
			ConversationID: true,
			TurnID:         strings.TrimSpace(turnID) != "",
			MessageID:      strings.TrimSpace(msgID) != "",
		},
	}
	files, err := store.GetGeneratedFiles(ctx, in)
	if err != nil || len(files) == 0 {
		return strings.TrimSpace(content)
	}
	return rewriteSandboxMarkdownLinks(strings.TrimSpace(content), files)
}

func appendMissingReplayMessages(history *bindpkg.History, msgs []*bindpkg.Message) {
	if history == nil || len(msgs) == 0 {
		return
	}
	existingIDs := map[string]struct{}{}
	existingToolOps := map[string]struct{}{}
	collect := func(items []*bindpkg.Message) {
		for _, msg := range items {
			if msg == nil {
				continue
			}
			if id := strings.TrimSpace(msg.ID); id != "" {
				existingIDs[id] = struct{}{}
			}
			if op := strings.TrimSpace(msg.ToolOpID); op != "" {
				existingToolOps[op] = struct{}{}
			}
		}
	}
	for _, turn := range history.Past {
		if turn != nil {
			collect(turn.Messages)
		}
	}
	if history.Current != nil {
		collect(history.Current.Messages)
	}
	var pending []*bindpkg.Message
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		id := strings.TrimSpace(msg.ID)
		op := strings.TrimSpace(msg.ToolOpID)
		if id != "" {
			if _, ok := existingIDs[id]; ok {
				continue
			}
		}
		if op != "" {
			if _, ok := existingToolOps[op]; ok {
				continue
			}
		}
		pending = append(pending, msg)
		if id != "" {
			existingIDs[id] = struct{}{}
		}
		if op != "" {
			existingToolOps[op] = struct{}{}
		}
	}
	appendCurrentMessages(history, pending...)
}

func mergeReplayMessages(existing []*bindpkg.Message, incoming []*bindpkg.Message) []*bindpkg.Message {
	if len(existing) == 0 {
		return append([]*bindpkg.Message(nil), incoming...)
	}
	if len(incoming) == 0 {
		return append([]*bindpkg.Message(nil), existing...)
	}
	merged := append([]*bindpkg.Message(nil), existing...)
	seenIDs := map[string]struct{}{}
	seenOps := map[string]struct{}{}
	for _, msg := range existing {
		if msg == nil {
			continue
		}
		if id := strings.TrimSpace(msg.ID); id != "" {
			seenIDs[id] = struct{}{}
		}
		if op := strings.TrimSpace(msg.ToolOpID); op != "" {
			seenOps[op] = struct{}{}
		}
	}
	for _, msg := range incoming {
		if msg == nil {
			continue
		}
		if id := strings.TrimSpace(msg.ID); id != "" {
			if _, ok := seenIDs[id]; ok {
				continue
			}
		}
		if op := strings.TrimSpace(msg.ToolOpID); op != "" {
			if _, ok := seenOps[op]; ok {
				continue
			}
		}
		merged = append(merged, msg)
		if id := strings.TrimSpace(msg.ID); id != "" {
			seenIDs[id] = struct{}{}
		}
		if op := strings.TrimSpace(msg.ToolOpID); op != "" {
			seenOps[op] = struct{}{}
		}
	}
	return merged
}

func parseExplicitSkillInvocation(query string) (string, string, bool) {
	if query == "" {
		return "", "", false
	}
	if strings.HasPrefix(query, "$$") || strings.HasPrefix(query, "//") {
		return "", "", false
	}
	if !strings.HasPrefix(query, "$") && !strings.HasPrefix(query, "/") {
		return "", "", false
	}
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return "", "", false
	}
	head := fields[0]
	if len(head) < 2 {
		return "", "", false
	}
	name := head[1:]
	switch strings.ToLower(name) {
	case "help", "clear":
		return "", "", false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return "", "", false
	}
	return name, strings.TrimSpace(strings.TrimPrefix(query, head)), true
}

func rewriteSandboxMarkdownLinks(content string, files []*gfread.GeneratedFileView) string {
	if strings.TrimSpace(content) == "" || len(files) == 0 {
		return content
	}
	return sandboxMarkdownLinkPattern.ReplaceAllStringFunc(content, func(match string) string {
		parts := sandboxMarkdownLinkPattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		label := parts[1]
		sandboxURL := strings.TrimSpace(parts[2])
		href := resolveGeneratedFileDownloadHref(sandboxURL, files)
		if href == "" {
			return match
		}
		return fmt.Sprintf("[%s](%s)", label, href)
	})
}

func resolveGeneratedFileDownloadHref(sandboxURL string, files []*gfread.GeneratedFileView) string {
	filename := normalizeSandboxFilename(sandboxURL)
	if filename == "" {
		return ""
	}
	want := strings.ToLower(filename)
	for _, file := range files {
		if file == nil {
			continue
		}
		id := strings.TrimSpace(file.ID)
		name := strings.ToLower(strings.TrimSpace(optionalString(file.Filename)))
		if id == "" || name == "" || name != want {
			continue
		}
		return fmt.Sprintf("/v1/api/generated-files/%s/download", id)
	}
	return ""
}

func normalizeSandboxFilename(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || !strings.HasPrefix(strings.ToLower(value), "sandbox:/") {
		return ""
	}
	value = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(value, "sandbox:///"), "sandbox://"), "sandbox:/")
	name := strings.TrimSpace(path.Base(value))
	if name == "." || name == "/" {
		return ""
	}
	return name
}

func optionalString(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}
