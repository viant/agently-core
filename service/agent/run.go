package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
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
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	agmessagelist "github.com/viant/agently-core/pkg/agently/message/list"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturnbyid "github.com/viant/agently-core/pkg/agently/turn/byId"
	agturnnext "github.com/viant/agently-core/pkg/agently/turn/nextQueued"
	agturncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
	turnqueuewrite "github.com/viant/agently-core/pkg/agently/turnqueue/write"
	"github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/runtime/usage"
	"github.com/viant/agently-core/service/core"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
	elact "github.com/viant/agently-core/service/elicitation/action"
	"github.com/viant/agently-core/service/reactor"
	executil "github.com/viant/agently-core/service/shared/executil"
)

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

// executeChains filters, evaluates and dispatches supervised follow-up chains
// declared on the parent agent.

// Query executes a query against an agent.
func (s *Service) Query(ctx context.Context, input *QueryInput, output *QueryOutput) error {
	queryStarted := time.Now()
	// Bridge auth/user identity first so conversation bootstrap can persist owner.
	ctx = s.bindAuthFromInputContext(ctx, input)
	ctx = bindEffectiveUserFromInput(ctx, input)

	envStarted := time.Now()
	if err := s.ensureEnvironment(ctx, input); err != nil {
		return err
	}
	infof("agent.Query stage ensureEnvironment convo=%q elapsed=%s", strings.TrimSpace(input.ConversationID), time.Since(envStarted))
	if input == nil || input.Agent == nil {
		return fmt.Errorf("invalid input: agent is required")
	}
	if input.MessageID == "" {
		input.MessageID = uuid.New().String()
	}
	output.ConversationID = input.ConversationID
	if queued, err := s.tryQueueTurn(ctx, input); err != nil {
		return err
	} else if queued {
		output.MessageID = input.MessageID
		output.Content = ""
		return nil
	}
	// Seed provisional turn metadata early so pre-plan LLM calls (auto-routing)
	// can participate in the same run tracking context.
	ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{
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

	// Ensure fresh tokens via token provider.
	if s.tokenProvider != nil {
		userID := authctx.EffectiveUserID(ctx)
		if userID != "" {
			provider := "default"
			ctx, _ = s.tokenProvider.EnsureTokens(ctx, token.Key{Subject: userID, Provider: provider})
		}
	}

	// Capture auth state to run record for persistence and resume.
	s.captureSecurityContext(ctx, input)

	// Install a warnings collector in context for this turn.
	ctx, _ = withWarnings(ctx)

	// Optional tool auto-selection (bundle-first). Executed before run-plan loop
	// so the selected tool set stays stable for the whole turn.
	toolRouterStarted := time.Now()
	s.maybeAutoSelectToolBundles(ctx, input)
	infof("agent.Query stage toolAutoSelection convo=%q message_id=%q elapsed=%s bundles=%d", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), time.Since(toolRouterStarted), len(input.ToolBundles))

	// Conversation already ensured above (fills AgentID/Model/Tool when missing)
	s.tryMergePromptIntoContext(input)
	workdir := ensureResolvedWorkdir(input)
	ctx = executil.WithWorkdir(ctx, workdir)
	contextStarted := time.Now()
	if err := s.updatedConversationContext(ctx, input.ConversationID, input); err != nil {
		return err
	}
	infof("agent.Query stage updateConversationContext convo=%q elapsed=%s", strings.TrimSpace(input.ConversationID), time.Since(contextStarted))
	infof("agent.Query prepared convo=%q turn_id=%q message_id=%q", strings.TrimSpace(input.ConversationID), strings.TrimSpace(input.MessageID), strings.TrimSpace(input.MessageID))

	ctx, agg := usage.WithAggregator(ctx)
	turn := memory.TurnMeta{
		Assistant:       input.Agent.ID,
		ConversationID:  input.ConversationID,
		TurnID:          input.MessageID,
		ParentMessageID: input.MessageID,
	}
	ctx = memory.WithTurnMeta(ctx, turn)

	// Establish authoritative cancel and register it if available
	var cancel func()
	ctx, cancel = s.registerTurnCancel(ctx, turn)
	defer cancel()
	if pol := s.resolveToolPolicy(input); pol != nil {
		ctx = tool.WithPolicy(ctx, pol)
	}
	ctx = tool.WithApprovalQueueState(ctx)

	// Start turn and persist initial user message. Prefer using the
	// expanded user prompt (via llm/core:expandUserPrompt) so the
	// conversation stores a single, canonical task for this turn.
	if err := s.startTurn(ctx, turn); err != nil {
		return err
	}
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
	// Best-effort expansion of the user prompt only on the very first turn of a conversation.
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

	// Persist attachments if any. Once persisted into history, avoid also
	// sending them as task-scoped attachments to prevent duplicate media in
	// the provider request payload.
	if err := s.processAttachments(ctx, turn, input); err != nil {
		return err
	}
	infof("agent.Query processAttachments ok convo=%q turn_id=%q count=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), len(input.Attachments))

	// TODO delete if not needed
	//if len(input.Attachments) > 0 {
	//    input.Attachments = nil
	//}

	// No pre-execution elicitation. Templates can instruct LLM to elicit details
	// using binding.Elicitation. Orchestrator handles assistant-originated elicitations.
	// Apply workspace-configured tool timeout to context, if set.
	if s.defaults != nil && s.defaults.ToolCallTimeoutSec > 0 {
		d := time.Duration(s.defaults.ToolCallTimeoutSec) * time.Second
		ctx = executil.WithToolTimeout(ctx, d)
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
		return fmt.Errorf("execution of query function failed: %w", err)
	}

	if err := s.finalizeTurn(ctx, turn, status, err); err != nil {
		return err
	}
	infof("agent.Query finalize ok convo=%q turn_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status))
	// Persist/refresh conversation default model with the actually used model this turn
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
	// Elicitation and final content persistence are handled inside runPlanLoop now
	output.Usage = agg
	// Expose any collected warnings on query output.
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

// loopControls captures continuation flags from Context.chain.loop (supervised follow-up chains)
func (s *Service) addAttachment(ctx context.Context, turn memory.TurnMeta, att *prompt.Attachment) error {
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
	duplicateGuard := reactor.NewDuplicateGuard(nil)
	// resolvedModel tracks the first model selected (either via explicit
	// override or matcher-based preferences) for this Query turn. Once set,
	// subsequent iterations within the same turn stick to this model instead
	// of re-evaluating preferences, to keep provider/model stable.
	var resolvedModel string

	turn, ok := memory.TurnMetaFromContext(ctx)
	if !ok {
		return fmt.Errorf("failed to get turn meta")
	}
	// Propagate context recovery mode into the turn context (agent-level).
	mode := memory.ContextRecoveryPruneCompact
	if input != nil && input.Agent != nil {
		if v := strings.TrimSpace(input.Agent.ContextRecoveryMode); v != "" {
			mode = v
		}
	}
	ctx = memory.WithContextRecoveryMode(ctx, mode)

	input.RequestTime = time.Now()
	for {
		iter++
		iterStart := time.Now()
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
		// Context keys snapshot
		keys := []string{}
		for k := range binding.Context {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		modelSelection := input.Agent.ModelSelection
		// Once a model has been resolved earlier in this turn (either via
		// explicit override or matcher-based preferences), stick to it for
		// the rest of the turn to avoid re-evaluating preferences and
		// changing models mid‑execution.
		if strings.TrimSpace(resolvedModel) != "" && strings.TrimSpace(input.ModelOverride) == "" {
			modelSelection.Model = resolvedModel
			modelSelection.Preferences = nil
		} else {
			// ModelOverride, when present, always wins for this turn.
			if input.ModelOverride != "" {
				modelSelection.Model = input.ModelOverride
			} else if input.ModelPreferences != nil {
				// When the caller supplies per-turn model preferences without an
				// explicit override, clear the configured model so that
				// GenerateInput.MatchModelIfNeeded can pick the best candidate
				// using the workspace model matcher. This allows callers of
				// llm/agents:run (and direct Query) to influence model choice
				// beyond the agent's static modelRef.
				modelSelection.Model = ""
			}
			if input.ModelPreferences != nil {
				modelSelection.Preferences = input.ModelPreferences
			}
			// Gatekeeper: set allowed reductions from agent config; when providers empty, derive from agent default model provider.
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
		// Keep allowed reductions across iterations when available.
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
		// The user task for this turn has already been expanded and
		// persisted as the latest user message in history; avoid adding
		// another synthetic user message in History.Current.
		genInput.UserPromptAlreadyInHistory = true
		// Attribute participants for multi-user/agent naming in LLM messages
		genInput.UserID = strings.TrimSpace(input.UserId)
		if input.Agent != nil {
			genInput.AgentID = strings.TrimSpace(input.Agent.ID)
		}
		// genInput.Options.Mode = "plan"
		EnsureGenerateOptions(ctx, genInput, input.Agent)
		// Apply per-turn override for reasoning effort when requested
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
		runCtx := reactor.WithDuplicateGuard(ctx, duplicateGuard)
		aPlan, pErr := s.orchestrator.Run(runCtx, genInput, genOutput)
		debugf("agent.runPlan orchestrator done convo=%q turn_id=%q iter=%d duration=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, time.Since(planStart))
		if pErr != nil {
			return pErr
		}
		if aPlan == nil {
			return fmt.Errorf("unable to generate plan")
		}
		// Capture the first resolved model for this turn and stick to it on
		// subsequent iterations when preferences are used.
		if strings.TrimSpace(resolvedModel) == "" && genInput != nil {
			if m := strings.TrimSpace(genInput.ModelSelection.Model); m != "" {
				resolvedModel = m
			}
		}
		queryOutput.Plan = aPlan
		stepCount := 0
		if aPlan != nil {
			stepCount = len(aPlan.Steps)
		}
		debugf("agent.runPlan plan ready convo=%q turn_id=%q iter=%d steps=%d elicitation=%v empty=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, stepCount, aPlan != nil && aPlan.Elicitation != nil, aPlan != nil && aPlan.IsEmpty())

		// Detect duplicated tool steps in the plan and attach warnings to the turn context.
		reactor.WarnOnDuplicateSteps(aPlan, func(msg string) { appendWarning(ctx, msg) })

		// Handle elicitation inside the loop as a single-turn interaction.
		if aPlan.Elicitation != nil {
			if missing := missingRequired(aPlan.Elicitation, binding.Context); len(missing) == 0 {
				debugf("agent.runPlan elicitation satisfied by context convo=%q turn_id=%q iter=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter)
				// Elicitation already satisfied by context; re-run plan with updated context.
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
			_, status, _, err := s.elicitation.Elicit(ectx, &turn, "assistant", aPlan.Elicitation)
			if err != nil {
				errorf("agent.runPlan elicitation done convo=%q turn_id=%q iter=%d status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, strings.TrimSpace(status), err)
			} else {
				infof("agent.runPlan elicitation done convo=%q turn_id=%q iter=%d status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, strings.TrimSpace(status))
			}
			if err != nil {
				// If timed out or canceled, auto-decline to avoid getting stuck
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					_ = s.elicitation.Resolve(context.Background(), turn.ConversationID, aPlan.Elicitation.ElicitationId, "decline", nil, "timeout")
					return nil
				}
				return err
			}
			if elact.Normalize(status) != elact.Accept {
				// User declined/cancelled; finish turn without additional content
				return nil
			}
			// Continue loop with updated binding (which should include payload/user response)
			continue
		}

		// No elicitation: plan either completed with final content or produced tool calls.
		if aPlan.IsEmpty() {
			// Persist final assistant text using the shared message ID
			if strings.TrimSpace(genOutput.Content) != "" {
				modelcallctx.WaitFinish(ctx, 1500*time.Millisecond)
				msgID := memory.ModelMessageIDFromContext(ctx)
				if msgID == "" {
					msgID = genOutput.MessageID
				}
				// Attribute assistant message to the agent ID for history and UI display
				actor := input.Actor()
				if shouldSkipFinalAssistantPersist(ctx, s.conversation, &turn, genOutput.Content) {
					debugf("agent.runPlan skip duplicate final assistant convo=%q turn_id=%q content_len=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), len(genOutput.Content))
				} else if _, err := s.addMessage(ctx, &turn, "assistant", actor, genOutput.Content, nil, "plan", msgID); err != nil {
					return err
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
		// Otherwise, continue loop to allow the orchestrator to perform next step
		debugf("agent.runPlan continue convo=%q turn_id=%q iter=%d duration=%s", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iter, time.Since(iterStart))
	}
}

// addPreferenceHintsFromAgent appends model preference hints derived from the
// agent configuration. When AllowedModels are set, they are preferred. When
// AllowedProviders are set, they are used. When both are empty, this falls back
// to the current agent provider (derived from modelRef) as an allowed provider.
// NOTE: AllowedProviders/AllowedModels now act as gatekeepers (candidate reducer)
// and must not be written into hints. Selection reduction is handled in
// core.GenerateInput.MatchModelIfNeeded via ReducingMatcher.

// deriveProviderFromModelRef returns the provider name from a modelRef in the
// common form "provider_model". Returns empty string when it cannot be derived.
func deriveProviderFromModelRef(modelRef string) string {
	v := strings.TrimSpace(modelRef)
	if v == "" {
		return ""
	}
	// Heuristic: take the prefix before the first underscore as provider id.
	if idx := strings.IndexRune(v, '_'); idx > 0 {
		return strings.TrimSpace(v[:idx])
	}
	return ""
}

// waitForElicitation registers a waiter on the elicitation router and optionally
// spawns a local awaiter to resolve the elicitation in interactive environments.
// It returns true when the elicitation was accepted.
// waitForElicitation was inlined into elicitation.Service.Wait

func (s *Service) addMessage(ctx context.Context, turn *memory.TurnMeta, role, actor, content string, raw *string, mode, id string) (string, error) {
	if executil.IsChainMode(ctx) {
		mode = "chain"
	}
	opts := []apiconv.MessageOption{
		apiconv.WithRole(role),
		apiconv.WithCreatedByUserID(actor),
		apiconv.WithContent(content),
		apiconv.WithMode(mode),
	}
	if raw != nil {
		trimmed := strings.TrimSpace(*raw)
		if trimmed != "" {
			val := *raw
			opts = append(opts, apiconv.WithRawContent(val))
		}
	}
	if strings.TrimSpace(id) != "" {
		opts = append(opts, apiconv.WithId(id))
	}
	infof("agent.addMessage start convo=%q turn_id=%q role=%q actor=%q mode=%q id=%q content_len=%d content_head=%q content_tail=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(role), strings.TrimSpace(actor), strings.TrimSpace(mode), strings.TrimSpace(id), len(content), headString(content, 512), tailString(content, 512))
	msg, err := apiconv.AddMessage(ctx, s.conversation, turn, opts...)
	if err != nil {
		errorf("agent.addMessage error convo=%q turn_id=%q role=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(role), err)
		return "", fmt.Errorf("failed to add message: %w", err)
	}
	infof("agent.addMessage ok convo=%q turn_id=%q role=%q message_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(role), strings.TrimSpace(msg.Id))
	return msg.Id, nil
}

// mergeInlineJSONIntoContext copies JSON object fields from qi.Query into qi.Context (non-destructive).
func (s *Service) tryMergePromptIntoContext(input *QueryInput) {
	if input == nil || strings.TrimSpace(input.Query) == "" {
		return
	}
	var tmp map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(input.Query)), &tmp); err == nil && len(tmp) > 0 {
		if input.Context == nil {
			input.Context = map[string]interface{}{}
		}
		for k, v := range tmp {
			if _, exists := input.Context[k]; !exists {
				input.Context[k] = v
			}
		}
	}
}

func ensureResolvedWorkdir(input *QueryInput) string {
	if input == nil {
		return ""
	}
	if input.Context == nil {
		input.Context = map[string]interface{}{}
	}
	if existing := normalizeWorkdirValue(input.Context["workdir"]); existing != "" {
		input.Context["workdir"] = existing
		input.Context["resolvedWorkdir"] = existing
		return existing
	}
	candidates := []string{}
	if input.Agent != nil && strings.TrimSpace(input.Agent.DefaultWorkdir) != "" {
		candidates = append(candidates, strings.TrimSpace(input.Agent.DefaultWorkdir))
	}
	candidates = append(candidates, extractPathCandidates(input.Query)...)
	for _, candidate := range candidates {
		if resolved := resolveExistingWorkdir(candidate); resolved != "" {
			input.Context["workdir"] = resolved
			input.Context["resolvedWorkdir"] = resolved
			return resolved
		}
	}
	return ""
}

func normalizeWorkdirValue(raw interface{}) string {
	switch actual := raw.(type) {
	case string:
		return resolveExistingWorkdir(actual)
	default:
		return ""
	}
}

func extractPathCandidates(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	trimRunes := "\"'`,.;:()[]{}<>"
	seen := map[string]struct{}{}
	var result []string
	for _, token := range strings.Fields(text) {
		candidate := strings.Trim(strings.TrimSpace(token), trimRunes)
		if candidate == "" {
			continue
		}
		if !strings.HasPrefix(candidate, "/") && !strings.HasPrefix(candidate, "~/") {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		result = append(result, candidate)
	}
	return result
}

func resolveExistingWorkdir(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return ""
		}
		raw = filepath.Join(home, strings.TrimPrefix(raw, "~/"))
	}
	if !filepath.IsAbs(raw) {
		return ""
	}
	info, err := os.Stat(raw)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return filepath.Clean(raw)
	}
	return filepath.Dir(filepath.Clean(raw))
}

// ensureEnvironment ensures conversation and agent are initialized and sets defaults.
func (s *Service) ensureEnvironment(ctx context.Context, input *QueryInput) error {
	if err := s.ensureConversation(ctx, input); err != nil {
		return err
	}
	if err := s.ensureAgent(ctx, input); err != nil {
		return err
	}
	if input.EmbeddingModel == "" {
		input.EmbeddingModel = s.defaults.Embedder
	}
	return nil
}

// bindAuthFromInputContext extracts bearer tokens from input.Context and attaches to ctx.
func (s *Service) bindAuthFromInputContext(ctx context.Context, input *QueryInput) context.Context {
	if input == nil || input.Context == nil {
		return ctx
	}
	if v, ok := input.Context["authorization"].(string); ok && strings.TrimSpace(v) != "" {
		if tok := extractBearer(v); tok != "" {
			ctx = authctx.WithBearer(ctx, tok)
		}
	}
	if v, ok := input.Context["authToken"].(string); ok && strings.TrimSpace(v) != "" {
		ctx = authctx.WithBearer(ctx, v)
	}
	if v, ok := input.Context["token"].(string); ok && strings.TrimSpace(v) != "" {
		ctx = authctx.WithBearer(ctx, v)
	}
	if v, ok := input.Context["bearer"].(string); ok && strings.TrimSpace(v) != "" {
		ctx = authctx.WithBearer(ctx, v)
	}
	return ctx
}

func extractBearer(authHeader string) string {
	authHeader = strings.TrimSpace(authHeader)
	if authHeader == "" {
		return ""
	}
	const prefix = "bearer "
	if len(authHeader) >= len(prefix) && strings.EqualFold(authHeader[:len(prefix)], prefix) {
		return strings.TrimSpace(authHeader[len(prefix):])
	}
	return authHeader
}

func shouldSkipFinalAssistantPersist(ctx context.Context, client apiconv.Client, turn *memory.TurnMeta, content string) bool {
	if client == nil || turn == nil {
		return false
	}
	conversationID := strings.TrimSpace(turn.ConversationID)
	turnID := strings.TrimSpace(turn.TurnID)
	finalContent := strings.TrimSpace(content)
	if conversationID == "" || turnID == "" || finalContent == "" {
		return false
	}
	conv, err := client.GetConversation(ctx, conversationID)
	if err != nil || conv == nil {
		return false
	}
	transcript := conv.GetTranscript()
	for i := len(transcript) - 1; i >= 0; i-- {
		t := transcript[i]
		if t == nil || len(t.Message) == 0 {
			continue
		}
		for j := len(t.Message) - 1; j >= 0; j-- {
			msg := t.Message[j]
			if msg == nil {
				continue
			}
			if strings.TrimSpace(stringOrEmpty(msg.TurnId)) != turnID {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
				continue
			}
			if msg.Interim != 0 {
				continue
			}
			if strings.TrimSpace(stringOrEmpty(msg.Content)) == finalContent {
				return true
			}
			return false
		}
	}
	return false
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func bindEffectiveUserFromInput(ctx context.Context, input *QueryInput) context.Context {
	if ctx == nil || input == nil {
		return ctx
	}
	// Preserve authenticated identity when already present.
	if strings.TrimSpace(authctx.EffectiveUserID(ctx)) != "" {
		return ctx
	}
	userID := strings.TrimSpace(input.UserId)
	if userID == "" {
		return ctx
	}
	return authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: userID})
}

type turnTaskCheckpoint struct {
	MessageID string
	CreatedAt time.Time
	Found     bool
}

func (s *Service) latestTurnTaskCheckpoint(ctx context.Context, turn memory.TurnMeta) (turnTaskCheckpoint, error) {
	checkpoint := turnTaskCheckpoint{}
	if s == nil {
		return checkpoint, nil
	}
	conversationID := strings.TrimSpace(turn.ConversationID)
	turnID := strings.TrimSpace(turn.TurnID)
	if conversationID == "" || turnID == "" {
		return checkpoint, nil
	}
	if s.dataService != nil {
		page, err := s.dataService.GetMessagesPage(ctx, &agmessagelist.MessageRowsInput{
			ConversationId: conversationID,
			TurnId:         turnID,
			Roles:          []string{"user"},
			Types:          []string{"task"},
			Has: &agmessagelist.MessageRowsInputHas{
				ConversationId: true,
				TurnId:         true,
				Roles:          true,
				Types:          true,
			},
		}, nil)
		if err != nil {
			return checkpoint, err
		}
		for _, row := range page.Rows {
			if row == nil {
				continue
			}
			candidate := turnTaskCheckpoint{
				MessageID: strings.TrimSpace(row.Id),
				CreatedAt: row.CreatedAt,
				Found:     strings.TrimSpace(row.Id) != "",
			}
			if compareTurnTaskCheckpoint(candidate, checkpoint) > 0 {
				checkpoint = candidate
			}
		}
		if checkpoint.Found {
			return checkpoint, nil
		}
	}
	if s.conversation == nil {
		return checkpoint, nil
	}
	conv, err := s.conversation.GetConversation(ctx, conversationID)
	if err != nil || conv == nil {
		return checkpoint, err
	}
	for _, transcriptTurn := range conv.Transcript {
		if transcriptTurn == nil || strings.TrimSpace(transcriptTurn.Id) != turnID {
			continue
		}
		for _, msg := range transcriptTurn.Message {
			if msg == nil || msg.Interim != 0 {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") || !strings.EqualFold(strings.TrimSpace(msg.Type), "task") {
				continue
			}
			candidate := turnTaskCheckpoint{MessageID: strings.TrimSpace(msg.Id), CreatedAt: msg.CreatedAt, Found: true}
			if compareTurnTaskCheckpoint(candidate, checkpoint) > 0 {
				checkpoint = candidate
			}
		}
	}
	return checkpoint, nil
}

func (s *Service) hasNewTurnTaskSince(ctx context.Context, turn memory.TurnMeta, checkpoint turnTaskCheckpoint) (bool, error) {
	latest, err := s.latestTurnTaskCheckpoint(ctx, turn)
	if err != nil {
		return false, err
	}
	if !latest.Found {
		return false, nil
	}
	if !checkpoint.Found {
		return true, nil
	}
	return compareTurnTaskCheckpoint(latest, checkpoint) > 0, nil
}

func effectiveFollowUpCheckpoint(initial turnTaskCheckpoint, output *QueryOutput) turnTaskCheckpoint {
	if output != nil && output.lastTaskCheckpoint.Found {
		return output.lastTaskCheckpoint
	}
	return initial
}

func compareTurnTaskCheckpoint(a, b turnTaskCheckpoint) int {
	if !a.Found && !b.Found {
		return 0
	}
	if a.Found && !b.Found {
		return 1
	}
	if !a.Found && b.Found {
		return -1
	}
	if a.CreatedAt.Before(b.CreatedAt) {
		return -1
	}
	if a.CreatedAt.After(b.CreatedAt) {
		return 1
	}
	return strings.Compare(a.MessageID, b.MessageID)
}

func (s *Service) tryQueueTurn(ctx context.Context, input *QueryInput) (bool, error) {
	if s == nil || s.dataService == nil || s.conversation == nil || input == nil {
		return false, nil
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	turnID := strings.TrimSpace(input.MessageID)
	if conversationID == "" || turnID == "" {
		return false, nil
	}
	active, err := s.dataService.GetActiveTurn(ctx, &agturnactive.ActiveTurnsInput{
		ConversationID: conversationID,
		Has:            &agturnactive.ActiveTurnsInputHas{ConversationID: true},
	})
	if err != nil {
		return false, fmt.Errorf("failed to check active turn: %w", err)
	}
	if active == nil || strings.TrimSpace(active.Id) == "" || strings.TrimSpace(active.Id) == turnID {
		return false, nil
	}
	queuedCount, err := s.dataService.CountQueuedTurns(ctx, &agturncount.QueuedTotalInput{
		ConversationID: conversationID,
		Has:            &agturncount.QueuedTotalInputHas{ConversationID: true},
	})
	if err != nil {
		return false, fmt.Errorf("failed to count queued turns: %w", err)
	}
	if queuedCount >= 20 {
		return false, fmt.Errorf("turn queue limit reached for conversation %s", conversationID)
	}
	queueSeq := time.Now().UnixNano()
	now := time.Now()
	rec := apiconv.NewTurn()
	rec.SetId(turnID)
	rec.SetConversationID(conversationID)
	rec.SetStatus("queued")
	rec.SetQueueSeq(queueSeq)
	rec.SetCreatedAt(now)
	rec.SetStartedByMessageID(turnID)
	if err := s.conversation.PatchTurn(ctx, rec); err != nil {
		return false, fmt.Errorf("failed to queue turn: %w", err)
	}
	msg := apiconv.NewMessage()
	msg.SetId(turnID)
	msg.SetConversationID(conversationID)
	msg.SetTurnID(turnID)
	msg.SetRole("user")
	msg.SetType("task")
	msg.SetContent(strings.TrimSpace(input.Query))
	msg.SetRawContent(strings.TrimSpace(input.Query))
	msg.SetCreatedAt(now)
	if userID := strings.TrimSpace(input.UserId); userID != "" {
		msg.SetCreatedByUserID(userID)
	}
	if err := s.conversation.PatchMessage(ctx, msg); err != nil {
		return false, fmt.Errorf("failed to persist queued message: %w", err)
	}
	if patcher, ok := s.dataService.(interface {
		PatchTurnQueue(ctx context.Context, in *turnqueuewrite.TurnQueue) error
	}); ok {
		q := &turnqueuewrite.TurnQueue{Has: &turnqueuewrite.TurnQueueHas{}}
		q.SetId(turnID)
		q.SetConversationId(conversationID)
		q.SetTurnId(turnID)
		q.SetMessageId(turnID)
		q.SetQueueSeq(queueSeq)
		q.SetStatus("queued")
		q.SetCreatedAt(now)
		q.SetUpdatedAt(now)
		if err := patcher.PatchTurnQueue(ctx, q); err != nil {
			return false, fmt.Errorf("failed to persist turn queue: %w", err)
		}
	}
	infof("agent.Query queued convo=%q turn_id=%q active_turn=%q queue_seq=%d", conversationID, turnID, strings.TrimSpace(active.Id), queueSeq)
	return true, nil
}

// registerTurnCancel returns a derived context and a deferred cancel wrapper that patches status=canceled.
func (s *Service) registerTurnCancel(ctx context.Context, turn memory.TurnMeta) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	wrappedCancel := func() {
		cancel()
		if s.conversation != nil {
			upd := apiconv.NewTurn()
			upd.SetId(turn.TurnID)
			upd.SetStatus("canceled")
			_ = s.conversation.PatchTurn(context.Background(), upd)
		}
		warnf("agent.turn cancel convo=%q turn_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	}
	if s.cancelReg != nil {
		s.cancelReg.Register(turn.ConversationID, turn.TurnID, wrappedCancel)
		return ctx, func() { s.cancelReg.Complete(turn.ConversationID, turn.TurnID, wrappedCancel) }
	}
	return ctx, wrappedCancel
}

func (s *Service) startTurn(ctx context.Context, turn memory.TurnMeta) error {
	rec := apiconv.NewTurn()
	rec.SetId(turn.TurnID)
	rec.SetConversationID(turn.ConversationID)
	rec.SetStatus("running")
	rec.SetCreatedAt(time.Now()) // it overrides queued turns createdAt, don't delete this line
	debugf("agent.startTurn convo=%q turn_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	return s.conversation.PatchTurn(ctx, rec)
}

func (s *Service) addUserMessage(ctx context.Context, turn *memory.TurnMeta, userID, content, raw string) error {
	var rawPtr *string
	if strings.TrimSpace(raw) != "" {
		rawCopy := raw
		rawPtr = &rawCopy
	}
	debugf("agent.addUserMessage convo=%q turn_id=%q user_id=%q content_len=%d content_head=%q content_tail=%q raw_len=%d raw_head=%q raw_tail=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(userID), len(content), headString(content, 512), tailString(content, 512), len(raw), headString(raw, 512), tailString(raw, 512))
	_, err := s.addMessage(ctx, turn, "user", userID, content, rawPtr, "task", turn.TurnID)
	if err != nil {
		return fmt.Errorf("failed to add message: %w", err)
	}
	return nil
}

func (s *Service) processAttachments(ctx context.Context, turn memory.TurnMeta, input *QueryInput) error {
	if len(input.Attachments) == 0 {
		return nil
	}
	modelName := ""
	if input.ModelOverride != "" {
		modelName = input.ModelOverride
	} else if input.Agent != nil {
		modelName = input.Agent.Model
	}
	model, _ := s.llm.ModelFinder().Find(ctx, modelName)
	var limit int64
	if input.Agent != nil && input.Agent.Attachment != nil && input.Agent.Attachment.LimitBytes > 0 {
		limit = input.Agent.Attachment.LimitBytes
	} else {
		limit = s.llm.ProviderAttachmentLimit(model)
	}
	used := s.llm.AttachmentUsage(turn.ConversationID)
	var appended int64
	for _, att := range input.Attachments {
		if att == nil || len(att.Data) == 0 {
			continue
		}
		if limit > 0 {
			remain := limit - used - appended
			size := int64(len(att.Data))
			if remain <= 0 || size > remain {
				name := strings.TrimSpace(att.Name)
				if name == "" {
					name = "(unnamed)"
				}
				limMB := float64(limit) / (1024.0 * 1024.0)
				usedMB := float64(used+appended) / (1024.0 * 1024.0)
				curMB := float64(size) / (1024.0 * 1024.0)
				return fmt.Errorf("attachments exceed agent cap: limit %.3f MB, used %.3f MB, current (%s) %.3f MB", limMB, usedMB, name, curMB)
			}
		}
		if err := s.addAttachment(ctx, turn, att); err != nil {
			return err
		}
		appended += int64(len(att.Data))
	}
	if appended > 0 {
		s.llm.SetAttachmentUsage(turn.ConversationID, used+appended)
		_ = s.updateAttachmentUsageMetadata(ctx, turn.ConversationID, used+appended)
	}
	return nil
}

func (s *Service) runPlanAndStatus(ctx context.Context, input *QueryInput, output *QueryOutput) (string, error) {
	if err := s.runPlanLoop(ctx, input, output); err != nil {
		if errors.Is(err, context.Canceled) {
			return "canceled", err
		}
		return "failed", err
	}
	return "succeeded", nil
}

func (s *Service) finalizeTurn(ctx context.Context, turn memory.TurnMeta, status string, runErr error) error {
	var emsg string
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		emsg = runErr.Error()
	}
	patchCtx := ctx
	if status == "canceled" {
		patchCtx = context.Background()
	}
	upd := apiconv.NewTurn()
	upd.SetId(turn.TurnID)
	upd.SetStatus(status)
	if emsg != "" {
		upd.SetErrorMessage(emsg)
	}

	if err := s.conversation.PatchTurn(patchCtx, upd); runErr != nil {
		return runErr
	} else if err != nil {
		return err
	}
	if err := s.conversation.PatchConversations(ctx, convw.NewConversationStatus(turn.ConversationID, status)); err != nil {
		return fmt.Errorf("failed to update conversation: %w", err)
	}
	if runErr != nil {
		errorf("agent.finalizeTurn convo=%q turn_id=%q status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status), runErr)
	} else {
		infof("agent.finalizeTurn convo=%q turn_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status))
	}
	s.triggerQueueDrain(turn.ConversationID)
	return nil
}

func (s *Service) triggerQueueDrain(conversationID string) {
	if s == nil || s.dataService == nil || s.conversation == nil {
		return
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return
	}
	if !queueDrainGuards.acquire(conversationID) {
		return
	}
	go func(convID string) {
		defer queueDrainGuards.release(convID)
		if err := s.drainQueuedTurns(convID); err != nil {
			warnf("agent.queueDrain error convo=%q err=%v", convID, err)
		}
	}(conversationID)
}

func (s *Service) drainQueuedTurns(conversationID string) error {
	for {
		next, err := s.dataService.GetNextQueuedTurn(context.Background(), &agturnnext.QueuedTurnInput{
			ConversationID: conversationID,
			Has:            &agturnnext.QueuedTurnInputHas{ConversationID: true},
		})
		if err != nil {
			return fmt.Errorf("failed to load next queued turn: %w", err)
		}
		if next == nil || strings.TrimSpace(next.Id) == "" {
			return nil
		}

		turnID := strings.TrimSpace(next.Id)
		starterID := strings.TrimSpace(valueOrEmpty(next.StartedByMessageId))
		if starterID == "" {
			starterID = turnID
		}
		starter, err := s.conversation.GetMessage(context.Background(), starterID)
		if err != nil || starter == nil {
			upd := apiconv.NewTurn()
			upd.SetId(turnID)
			upd.SetStatus("failed")
			upd.SetErrorMessage("queued starter message not found")
			_ = s.conversation.PatchTurn(context.Background(), upd)
			warnf("agent.queueDrain failed to load starter message convo=%q turn_id=%q starter_id=%q err=%v", conversationID, turnID, starterID, err)
			continue
		}

		queryText := strings.TrimSpace(valueOrEmpty(starter.RawContent))
		if queryText == "" {
			queryText = strings.TrimSpace(valueOrEmpty(starter.Content))
		}
		if queryText == "" {
			upd := apiconv.NewTurn()
			upd.SetId(turnID)
			upd.SetStatus("failed")
			upd.SetErrorMessage("queued starter message is empty")
			_ = s.conversation.PatchTurn(context.Background(), upd)
			warnf("agent.queueDrain empty starter message convo=%q turn_id=%q starter_id=%q", conversationID, turnID, starterID)
			continue
		}

		input := &QueryInput{
			RequestTime:            time.Now(),
			ConversationID:         conversationID,
			MessageID:              turnID,
			AgentID:                strings.TrimSpace(valueOrEmpty(next.AgentIdUsed)),
			UserId:                 strings.TrimSpace(valueOrEmpty(starter.CreatedByUserId)),
			Query:                  queryText,
			ModelOverride:          strings.TrimSpace(valueOrEmpty(next.ModelOverride)),
			SkipInitialUserMessage: true,
			IsNewConversation:      false,
			ParentConversationID:   "",
		}
		out := &QueryOutput{}
		err = s.Query(context.Background(), input, out)
		if err != nil {
			warnf("agent.queueDrain query failed convo=%q turn_id=%q err=%v", conversationID, turnID, err)
		}

		// Another turn may have become active concurrently. If this turn is still
		// queued after attempted drain, stop and let the active turn completion
		// re-trigger queue draining.
		refreshed, rErr := s.dataService.GetTurnByID(context.Background(), &agturnbyid.TurnLookupInput{
			ID:             turnID,
			ConversationID: conversationID,
			Has:            &agturnbyid.TurnLookupInputHas{ID: true, ConversationID: true},
		})
		if rErr == nil && refreshed != nil && strings.EqualFold(strings.TrimSpace(refreshed.Status), "queued") {
			return nil
		}
	}
}

func (s *Service) updateDefaultModel(ctx context.Context, turn memory.TurnMeta, output *QueryOutput) error {
	if strings.TrimSpace(output.Model) == "" {
		return nil
	}
	w := &convw.Conversation{Has: &convw.ConversationHas{}}
	w.SetId(turn.ConversationID)
	w.SetDefaultModel(output.Model)
	if s.conversation != nil {
		mw := convw.Conversation(*w)
		_ = s.conversation.PatchConversations(ctx, (*apiconv.MutableConversation)(&mw))
	}
	return nil
}

func (s *Service) executeChainsAfter(ctx context.Context, input *QueryInput, output *QueryOutput, turn memory.TurnMeta, conv *apiconv.Conversation, status string) error {
	cc := NewChainContext(input, output, &turn)
	cc.Conversation = conv
	return s.executeChains(ctx, cc, status)
}

// captureSecurityContext persists auth tokens and effective user ID onto the
// run record so that stale/resumed runs can restore the caller's identity.
func (s *Service) captureSecurityContext(ctx context.Context, input *QueryInput) {
	if s.dataService == nil || input == nil {
		return
	}
	runID := strings.TrimSpace(input.MessageID)
	if runID == "" {
		return
	}
	secData, err := token.MarshalSecurityContext(ctx)
	if err != nil || secData == "" {
		return
	}
	run := &agrunwrite.MutableRunView{}
	run.SetId(runID)
	run.SetSecurityContext(secData)
	userID := authctx.EffectiveUserID(ctx)
	if userID != "" {
		run.SetEffectiveUserID(userID)
	}
	_, _ = s.dataService.PatchRuns(ctx, []*agrunwrite.MutableRunView{run})
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
