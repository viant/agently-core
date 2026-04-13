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

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	"github.com/viant/agently-core/internal/debugtrace"
	gfread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
	"github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/protocol/tool"
	toolapprovalqueue "github.com/viant/agently-core/protocol/tool/approvalqueue"
	toolasyncconfig "github.com/viant/agently-core/protocol/tool/asyncconfig"
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
)

type skipConversationStatusPatchKey struct{}
type skipTaskCheckpointLoadKey struct{}

var queueDrainGuards = &convGuardMap{m: make(map[string]*int32)}

type convGuardMap struct {
	mu sync.Mutex
	m  map[string]*int32
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
		infof("agent.Query serviceStart at=%s convo=%q message_id=%q agent_id=%q user_id=%q", queryStarted.Format(time.RFC3339Nano), strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), strings.TrimSpace(input.AgentID), strings.TrimSpace(input.UserId))
	}
	if input != nil && strings.TrimSpace(input.MessageID) == "" {
		input.MessageID = uuid.New().String()
	}
	if isFreshEmbeddedConversation(ctx) {
		ctx = context.WithValue(ctx, skipConversationStatusPatchKey{}, true)
	}
	if isFreshEmbeddedConversation(ctx) {
		ctx = context.WithValue(ctx, skipTaskCheckpointLoadKey{}, true)
	}

	envStarted := time.Now()
	if err := s.ensureEnvironment(ctx, input); err != nil {
		return err
	}
	infof("agent.Query stage ensureEnvironment convo=%q elapsed=%s", strings.TrimSpace(input.ConversationID), time.Since(envStarted))
	if input == nil || input.Agent == nil {
		return fmt.Errorf("invalid input: agent is required")
	}
	output.ConversationID = input.ConversationID
	if queued, err := s.tryQueueTurn(ctx, input); err != nil {
		return err
	} else if queued {
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
	infof("agent.Query start convo=%q agent_id=%q user_id=%q query_len=%d query_head=%q query_tail=%q tools_allowed=%d", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.Agent.ID), strings.TrimSpace(input.UserId), len(input.Query), headString(input.Query, 512), tailString(input.Query, 512), len(input.ToolsAllowed))
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
	infof("agent.Query config agent_id=%q delegation.enabled=%v delegation.maxDepth=%d systemPrompt.engine=%q systemPrompt.uri=%q instruction.engine=%q instruction.uri=%q", strings.TrimSpace(input.Agent.ID), delegEnabled, delegDepth, sysPromptEngine, sysPromptURI, instructionEngine, instructionURI)

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

	toolRouterStarted := time.Now()
	s.maybeAutoSelectToolBundles(ctx, input)
	infof("agent.Query stage toolAutoSelection convo=%q message_id=%q elapsed=%s bundles=%d", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), time.Since(toolRouterStarted), len(input.ToolBundles))

	s.tryMergePromptIntoContext(input)
	workdir := ensureResolvedWorkdir(input)
	ctx = toolexec.WithWorkdir(ctx, workdir)
	contextStarted := time.Now()
	if err := s.updatedConversationContext(ctx, input.ConversationID, input); err != nil {
		return err
	}
	infof("agent.Query stage updateConversationContext convo=%q elapsed=%s", strings.TrimSpace(input.ConversationID), time.Since(contextStarted))
	infof("agent.Query prepared convo=%q turn_id=%q message_id=%q", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), strings.TrimSpace(input.MessageID))

	ctx, agg := usage.WithAggregator(ctx)
	turn := runtimerequestctx.TurnMeta{
		Assistant:       input.Agent.ID,
		ConversationID:  input.ConversationID,
		TurnID:          input.MessageID,
		ParentMessageID: input.MessageID,
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

	if err := s.startTurn(ctx, turn, strings.TrimSpace(input.ScheduleId)); err != nil {
		return err
	}
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
		if err := s.finalizeTurn(ctx, turn, finalStatus, finalErr); err != nil {
			if retErr == nil {
				retErr = err
			}
			return
		}
		turnFinalized = true
		infof("agent.Query finalize ok convo=%q turn_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(finalStatus))
	}()
	infof("agent.Query startTurn ok convo=%q turn_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
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
				warnf("agent.turn stuck warning convo=%q turn_id=%q elapsed=%s", strings.TrimSpace(convoID), strings.TrimSpace(turnID), dur.String())
			}
		}(turn.ConversationID, turn.TurnID, d)
	}
	rawUserContent := input.Query
	content := strings.TrimSpace(input.Query)
	if input.IsNewConversation && s.llm != nil && input.Agent != nil {
		bStart := time.Now()
		debugf("agent.Query BuildBinding start convo=%q turn_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
		b, berr := s.BuildBinding(ctx, input)
		if berr != nil {
			debugf("agent.Query BuildBinding error convo=%q turn_id=%q elapsed=%s err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), time.Since(bStart).String(), berr)
		} else {
			debugf("agent.Query BuildBinding ok convo=%q turn_id=%q elapsed=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), time.Since(bStart).String())
			var expOut core.ExpandUserPromptOutput
			expIn := &core.ExpandUserPromptInput{Prompt: input.Agent.Prompt, Binding: b}
			expStart := time.Now()
			debugf("agent.Query ExpandUserPrompt start convo=%q turn_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
			if err := s.llm.ExpandUserPrompt(ctx, expIn, &expOut); err == nil && strings.TrimSpace(expOut.ExpandedUserPrompt) != "" {
				debugf("agent.Query ExpandUserPrompt ok convo=%q turn_id=%q elapsed=%s expanded_len=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), time.Since(expStart).String(), len(expOut.ExpandedUserPrompt))
				content = expOut.ExpandedUserPrompt
			} else if err != nil {
				debugf("agent.Query ExpandUserPrompt error convo=%q turn_id=%q elapsed=%s err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), time.Since(expStart).String(), err)
			} else {
				debugf("agent.Query ExpandUserPrompt empty convo=%q turn_id=%q elapsed=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), time.Since(expStart).String())
			}
		}
	}
	if input.SkipInitialUserMessage {
		infof("agent.Query skip addUserMessage convo=%q turn_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	} else {
		if err := s.addUserMessage(ctx, &turn, input.UserId, content, rawUserContent); err != nil {
			return err
		}
		infof("agent.Query addUserMessage ok convo=%q turn_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	}

	if err := s.processAttachments(ctx, turn, input); err != nil {
		return err
	}
	infof("agent.Query processAttachments ok convo=%q turn_id=%q count=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), len(input.Attachments))

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
			warnf("agent.Query steer checkpoint error convo=%q turn_id=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), ckErr)
		}
		status, err = s.runPlanAndStatus(ctx, input, output)

		turnStatus = status
		turnRunErr = err

		infof("agent.Query stage runPlanAndStatus convo=%q turn_id=%q status=%q elapsed=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status), time.Since(runPlanStarted))
		if err != nil {
			errorf("agent.Query runPlan error convo=%q turn_id=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), err)
			break
		}
		infof("agent.Query runPlan ok convo=%q turn_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status))
		if !strings.EqualFold(strings.TrimSpace(status), "succeeded") {
			break
		}
		followUpCheckpoint := effectiveFollowUpCheckpoint(checkpoint, output)
		pending, pErr := s.hasNewTurnTaskSince(ctx, turn, followUpCheckpoint)
		if pErr != nil {
			warnf("agent.Query steer follow-up check error convo=%q turn_id=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), pErr)
			break
		}
		if !pending {
			break
		}
		infof("agent.Query steer follow-up detected convo=%q turn_id=%q rerunning plan loop", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	}

	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("execution of query function canceled: %w", err)
		}
		return fmt.Errorf("execution of query function failed: %w", err)
	}

	if err := s.finalizeTurn(ctx, turn, status, err); err != nil {
		return err
	}
	turnFinalized = true
	infof("agent.Query finalize ok convo=%q turn_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status))
	_ = s.updateDefaultModel(ctx, turn, output)

	fetchStarted := time.Now()
	conv, err := s.fetchConversationWithRetry(ctx, input.ConversationID, apiconv.WithIncludeToolCall(true))
	if err != nil {
		return fmt.Errorf("cannot get conversation: %w", err)
	}
	infof("agent.Query stage fetchConversation convo=%q elapsed=%s", strings.TrimSpace(input.ConversationID), time.Since(fetchStarted))
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
		infof("agent.Query done convo=%q turn_id=%q elapsed=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), time.Since(queryStarted))
		return nil
	}
	err = s.summarizeIfNeeded(ctx, input, conv)
	if err != nil {
		return fmt.Errorf("failed summarizing: %w", err)
	}
	infof("agent.Query done convo=%q turn_id=%q elapsed=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), time.Since(queryStarted))
	return nil
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

func (s *Service) addAttachment(ctx context.Context, turn runtimerequestctx.TurnMeta, att *prompt.Attachment) error {
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
		iterStart := time.Now()
		s.updateRunIteration(ctx, turn, iter)

		checkpoint, ckErr := s.latestTurnTaskCheckpoint(ctx, turn)
		if ckErr != nil {
			warnf("agent.runPlan checkpoint error convo=%q turn_id=%q iter=%d err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, ckErr)
		}
		if queryOutput != nil {
			queryOutput.lastTaskCheckpoint = checkpoint
		}
		debugf("agent.runPlan iter start convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
		binding, bErr := s.BuildBinding(ctx, input)
		if bErr != nil {
			return bErr
		}
		keys := []string{}
		for k := range binding.Context {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		modelSelection := input.Agent.ModelSelection
		if strings.TrimSpace(resolvedModel) != "" && strings.TrimSpace(input.ModelOverride) == "" {
			modelSelection.Model = resolvedModel
			modelSelection.Preferences = nil
		} else {
			if input.ModelOverride != "" {
				modelSelection.Model = input.ModelOverride
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
		genInput.UserPromptAlreadyInHistory = false
		genInput.UserID = strings.TrimSpace(input.UserId)
		if input.Agent != nil {
			genInput.AgentID = strings.TrimSpace(input.Agent.ID)
		}
		EnsureGenerateOptions(ctx, genInput, input.Agent)
		if input.ReasoningEffort != nil {
			if v := strings.TrimSpace(*input.ReasoningEffort); v != "" {
				if genInput.ModelSelection.Options.Reasoning == nil {
					genInput.ModelSelection.Options.Reasoning = &llm.Reasoning{}
				}
				genInput.ModelSelection.Options.Reasoning.Effort = v
			}
		}
		genOutput := &core.GenerateOutput{}
		planStart := time.Now()

		if DebugEnabled() && genInput.Binding != nil {
			msgs := genInput.Binding.History.LLMMessages()
			debugf("agent.runPlan iter=%d history_msgs=%d model=%q convo=%q turn_id=%q",
				iter, len(msgs), genInput.ModelSelection.Model,
				strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
			for i, m := range msgs {
				content := headString(m.Content, 120)
				toolCallCount := len(m.ToolCalls)
				debugf("  history[%d] role=%s tool_call_id=%q tool_calls=%d content_len=%d content_head=%q",
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
		debugf("agent.runPlan orchestrator done convo=%q turn_id=%q iter=%d steps=%d duration=%s",
			strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID),
			iter, stepCount, time.Since(planStart))
		if pErr != nil {
			return pErr
		}
		if aPlan == nil {
			return fmt.Errorf("unable to generate plan")
		}
		if s.asyncManager != nil {
			changedOps := s.asyncManager.ConsumeChanged(turn.ConversationID, turn.TurnID)
			if len(changedOps) > 0 {
				s.markAssistantMessageInterim(ctx, &turn, genOutput)
				if s.asyncManager.HasActiveWaitOps(ctx, turn.ConversationID, turn.TurnID) {
					infof("agent.runPlan async-wait-after-status convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
					if err := s.asyncManager.WaitForNextPoll(ctx, turn.ConversationID, turn.TurnID); err != nil {
						return err
					}
					changedOps = s.asyncManager.ActiveWaitOps(ctx, turn.ConversationID, turn.TurnID)
				} else {
					infof("agent.runPlan async-terminal-after-status convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
				}
				s.injectAsyncReinforcementForRecords(ctx, &turn, changedOps)
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
		debugf("agent.runPlan plan ready convo=%q turn_id=%q iter=%d steps=%d elicitation=%v empty=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, stepCount, aPlan != nil && aPlan.Elicitation != nil, aPlan != nil && aPlan.IsEmpty())
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
			s.replaceInterimContentForElicitation(ctx, &turn, genOutput, strings.TrimSpace(aPlan.Elicitation.Message))

			if missing := missingRequired(aPlan.Elicitation, binding.Context); len(missing) == 0 {
				debugf("agent.runPlan elicitation satisfied by context convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
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
			debugf("agent.runPlan elicitation start convo=%q turn_id=%q iter=%d elicitation_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, strings.TrimSpace(aPlan.Elicitation.ElicitationId))
			ectx := ctx
			var cancel func()
			if s.defaults != nil && s.defaults.ElicitationTimeoutSec > 0 {
				ectx, cancel = context.WithTimeout(ctx, time.Duration(s.defaults.ElicitationTimeoutSec)*time.Second)
				defer cancel()
			}
			_, status, elicitPayload, err := s.elicitation.Elicit(ectx, &turn, "assistant", aPlan.Elicitation)
			if err != nil {
				errorf("agent.runPlan elicitation done convo=%q turn_id=%q iter=%d status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, strings.TrimSpace(status), err)
			} else {
				infof("agent.runPlan elicitation done convo=%q turn_id=%q iter=%d status=%q payload_keys=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, strings.TrimSpace(status), len(elicitPayload))
			}
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					_ = s.elicitation.Resolve(context.Background(), turn.ConversationID, aPlan.Elicitation.ElicitationId, "decline", nil, "timeout")
					return nil
				}
				return err
			}
			if elact.Normalize(status) != elact.Accept {
				return nil
			}
			if len(elicitPayload) > 0 {
				payloadJSON, _ := json.Marshal(elicitPayload)
				if len(payloadJSON) > 0 {
					s.addMessage(ctx, &turn, "user", "", string(payloadJSON), nil, "", "")
				}
			}
			continue
		}

		isTerminal := aPlan.IsEmpty()
		if isTerminal {
			if s.asyncManager != nil && s.asyncManager.HasActiveWaitOps(ctx, turn.ConversationID, turn.TurnID) {
				infof("agent.runPlan async-wait-terminal convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
				s.markAssistantMessageInterim(ctx, &turn, genOutput)
				if err := s.asyncManager.WaitForNextPoll(ctx, turn.ConversationID, turn.TurnID); err != nil {
					return err
				}
				s.injectAsyncReinforcement(ctx, &turn)
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
				debugf("runPlan-final patching msgID=%q interim=0 convo=%q turn=%q contentLen=%d",
					msgID, turn.ConversationID, turn.TurnID, len(genOutput.Content))
				if msgID != "" {
					msg := apiconv.NewMessage()
					msg.SetId(msgID)
					msg.SetConversationID(turn.ConversationID)
					msg.SetContent(strings.TrimSpace(s.rewriteGeneratedFileLinks(ctx, turn.ConversationID, turn.TurnID, msgID, genOutput.Content)))
					msg.SetInterim(0)
					if err := s.conversation.PatchMessage(ctx, msg); err != nil {
						errorf("runPlan-final patching msg=%q err=%v", msgID, err)
					}
				}
			}
			pending, pErr := s.hasNewTurnTaskSince(ctx, turn, checkpoint)
			if pErr != nil {
				warnf("agent.runPlan follow-up check error convo=%q turn_id=%q iter=%d err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, pErr)
			} else if pending {
				debugf("agent.runPlan steer follow-up convo=%q turn_id=%q iter=%d duration=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, time.Since(iterStart))
				queryOutput.Content = ""
				continue
			}
			debugf("agent.runPlan completed convo=%q turn_id=%q iter=%d content_len=%d duration=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, len(genOutput.Content), time.Since(iterStart))
			queryOutput.Content = genOutput.Content
			return nil
		}
		waitingForUser, waitErr := s.turnAwaitingUserAction(ctx, turn)
		if waitErr != nil {
			return waitErr
		}
		if waitingForUser {
			debugf("agent.runPlan waiting-for-user convo=%q turn_id=%q iter=%d duration=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, time.Since(iterStart))
			queryOutput.Content = ""
			return nil
		}

		debugf("agent.runPlan continue convo=%q turn_id=%q iter=%d duration=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, time.Since(iterStart))
	}
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
		fmt.Printf("###TODO sandobx:/ not found\n")
		return strings.TrimSpace(content)
	}

	fmt.Printf("###TODO sandobx:/ FOUND OK\n")

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
