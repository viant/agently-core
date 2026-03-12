package reactor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/base"
	"github.com/viant/agently-core/internal/textutil"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/agent/plan"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/service/agent/prompts"
	core2 "github.com/viant/agently-core/service/core"
	modelcall "github.com/viant/agently-core/service/core/modelcall"
	"github.com/viant/agently-core/service/core/stream"
	executil "github.com/viant/agently-core/service/shared/executil"
)

var freeTokenPrompt = prompts.Prune

type Service struct {
	llm        *core2.Service
	registry   tool.Registry
	convClient apiconv.Client
	// Finder for agent metadata (prompts, model, prefs) to mirror agent-run plan input
	agentFinder agentmdl.Finder
	// Optional builder to produce a GenerateInput identical to agent.runPlanLoop,
	// with the exception that the user query is provided as `instruction`.
	buildPlanInput func(ctx context.Context, conv *apiconv.Conversation, instruction string) (*core2.GenerateInput, error)

	// lastPreamble deduplicates patchStreamingToolPreamble calls: only patches
	// when the preamble text has actually changed. Keyed by message ID.
	lastPreambleMu sync.Mutex
	lastPreamble   map[string]string
}

// ctxKeyLimitRecoveryAttempted guards one-shot presentation of the context-limit guidance within a single Run invocation.
type ctxKeyPresentedType int

const ctxKeyLimitRecoveryAttempted ctxKeyPresentedType = 1

// ctxKeyContinuationMode marks runs that are invoked as part of a
// continuation/recovery flow (e.g., context-limit handling). Duplicate
// protection is disabled in this mode so internal/message tools can iterate
// freely when trimming history.
const ctxKeyContinuationMode ctxKeyPresentedType = 2
const ctxKeyDuplicateGuard ctxKeyPresentedType = 3

const (
	pruneMinRemove        = 20
	pruneMaxRemove        = 50
	pruneCandidateLimit   = 50
	compactCandidateLimit = 200
)

func inContinuationMode(ctx context.Context) bool {
	if v, ok := ctx.Value(ctxKeyContinuationMode).(bool); ok {
		return v
	}
	return false
}

func WithDuplicateGuard(ctx context.Context, guard *DuplicateGuard) context.Context {
	if ctx == nil || guard == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyDuplicateGuard, guard)
}

func duplicateGuardFromContext(ctx context.Context) *DuplicateGuard {
	if ctx == nil {
		return nil
	}
	if guard, ok := ctx.Value(ctxKeyDuplicateGuard).(*DuplicateGuard); ok && guard != nil {
		return guard
	}
	return nil
}

func (s *Service) Run(ctx context.Context, genInput *core2.GenerateInput, genOutput *core2.GenerateOutput) (*plan.Plan, error) {
	aPlan := plan.New()

	var wg sync.WaitGroup
	nextStepIdx := 0
	// Binding registry to current conversation (if any) so tool.Execute receives ctx with convID.
	reg := tool.WithConversation(s.registry, memory.ConversationIDFromContext(ctx))
	// Do not create child cancels here; errors must not cancel context.
	streamId, stepErrCh := s.registerStreamPlannerHandler(ctx, reg, aPlan, &wg, &nextStepIdx, genOutput)
	canStream, err := s.canStream(ctx, genInput)
	if err != nil {
		return nil, fmt.Errorf("failed to check if model can stream: %w", err)
	}
	if canStream {
		cleanup, err := s.llm.Stream(ctx, &core2.StreamInput{StreamID: streamId, GenerateInput: genInput}, &core2.StreamOutput{})
		defer cleanup()
		if err != nil {
			if errors.Is(err, core2.ErrContextLimitExceeded) {
				// One-shot guard: present only once per Run
				if ctx.Value(ctxKeyLimitRecoveryAttempted) == nil {
					ctx = context.WithValue(ctx, ctxKeyLimitRecoveryAttempted, true)
					if perr := s.presentContextLimitExceeded(ctx, genInput, err, strings.ReplaceAll(err.Error(), core2.ErrContextLimitExceeded.Error(), "")); perr != nil {
						return nil, fmt.Errorf("failed to handle context limit: %w", perr)
					}
				}
			}
			return nil, fmt.Errorf("failed to stream: %w", err)
		}
		wg.Wait()
		// propagate first tool error if any
		select {
		case toolErr := <-stepErrCh:
			if toolErr != nil {
				return nil, fmt.Errorf("tool execution failed: %w", toolErr)
			}
		default:
		}
		s.synthesizeFinalResponse(genOutput)

	} else {
		if err := s.llm.Generate(ctx, genInput, genOutput); err != nil {
			if errors.Is(err, core2.ErrContextLimitExceeded) {
				// One-shot guard: present only once per Run
				if ctx.Value(ctxKeyLimitRecoveryAttempted) == nil {
					ctx = context.WithValue(ctx, ctxKeyLimitRecoveryAttempted, true)
					if perr := s.presentContextLimitExceeded(ctx, genInput, err, strings.ReplaceAll(err.Error(), core2.ErrContextLimitExceeded.Error(), "")); perr != nil {
						return nil, fmt.Errorf("failed to handle context limit: %w", perr)
					}
				}
			}
			return nil, fmt.Errorf("failed to generate: %w", err)
		}
	}

	if aPlan.IsEmpty() {
		ok, err := s.extendPlanFromResponse(ctx, genOutput, aPlan)
		if err != nil {
			return nil, fmt.Errorf("failed to extend plan from response: %w", err)
		}
		if ok {
			if err = s.streamPlanSteps(ctx, streamId, aPlan); err != nil {
				return nil, fmt.Errorf("failed to stream plan steps: %w", err)
			}
			wg.Wait()
			// propagate first tool error if any
			select {
			case toolErr := <-stepErrCh:
				if toolErr != nil {
					return nil, fmt.Errorf("tool execution failed: %w", toolErr)
				}
			default:
			}
		}
	}

	// Check if all tool steps were blocked by the duplicate guard.
	// When this happens, the LLM is stuck in a loop calling the same tools;
	// mark the plan so the ReAct loop can terminate.
	if guard := duplicateGuardFromContext(ctx); guard != nil && guard.AllBlockedInRound() {
		aPlan.AllBlocked = true
		fmt.Printf("[debug] reactor.Run: all %d tool steps blocked by duplicate guard — marking plan AllBlocked\n", len(aPlan.Steps))
	}

	RefinePlan(aPlan)
	// If this turn executed message:remove, perform one retry generation automatically
	if hasRemovalTool(aPlan) {
		// Retry once to produce final assistant content with reduced context
		if err := s.llm.Generate(ctx, genInput, genOutput); err != nil {
			return nil, fmt.Errorf("retry after removal failed: %w", err)
		}
		// Extend/stream any additional steps if present
		if ok, _ := s.extendPlanFromResponse(ctx, genOutput, aPlan); ok {
			if err2 := s.streamPlanSteps(ctx, streamId, aPlan); err2 != nil {
				return nil, fmt.Errorf("failed to stream plan steps (retry): %w", err2)
			}
		}
	}
	return aPlan, nil
}

// hasRemovalTool returns true when the plan contains a message removal tool call.
func hasRemovalTool(p *plan.Plan) bool {
	if p == nil || len(p.Steps) == 0 {
		return false
	}
	for _, st := range p.Steps {
		name := strings.ToLower(strings.TrimSpace(st.Name))
		if name == "internal/message:remove" || name == "message:remove" || strings.HasSuffix(name, ":remove") {
			return true
		}
	}
	return false
}

// presentContextLimitExceeded composes a concise guidance note with removable-candidate lines,
// then triggers a best‑effort, tool‑driven recovery loop to free tokens (via internal/message tools),
// and finally inserts an assistant message with the guidance for the user.
func (s *Service) presentContextLimitExceeded(ctx context.Context, oldGenInput *core2.GenerateInput, causeErr error, errMessage string) error {
	convID := memory.ConversationIDFromContext(ctx)
	if strings.TrimSpace(convID) == "" || s.convClient == nil {
		return fmt.Errorf("missing conversation context")
	}
	// Fetch conversation with tool calls to build candidates
	conv, convErr := s.convClient.GetConversation(ctx, convID, apiconv.WithIncludeToolCall(true))
	if convErr != nil {
		return fmt.Errorf("failed to get conversation: %w", convErr)
	}
	if conv == nil {
		return fmt.Errorf("failed to get conversation: conversation %q not found", convID)
	}
	lines, ids := s.buildRemovalCandidates(ctx, conv, pruneCandidateLimit)
	if len(lines) == 0 {
		lines = []string{"(no removable items identified)"}
	}
	prunePrompt := s.composeFreeTokenPrompt(errMessage, lines, ids)

	overlimit := 0
	if v, ok := extractOverlimitTokens(errMessage); ok {
		overlimit = v
		fmt.Printf("[debug] overlimit tokens: %d\n", overlimit)
	}

	mode := memory.ContextRecoveryPruneCompact
	if v, ok := memory.ContextRecoveryModeFromContext(ctx); ok && strings.TrimSpace(v) != "" {
		mode = strings.TrimSpace(v)
	}
	// In continuation mode, force compact regardless of configured mode.
	if core2.IsContinuationContextLimit(causeErr) {
		mode = memory.ContextRecoveryCompact
	}
	promptText := prunePrompt
	var recoveryErr error
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case strings.ToLower(memory.ContextRecoveryCompact):
		compactLines, compactIDs := s.buildRemovalCandidates(ctx, conv, compactCandidateLimit)
		if len(compactLines) == 0 {
			compactLines = []string{"(no removable items identified)"}
		}
		promptText = s.composeCompactPrompt(errMessage, compactLines, compactIDs)
		if recoveryErr = s.compactHistoryLLM(ctx, conv, errMessage, oldGenInput, overlimit); recoveryErr != nil {
			return fmt.Errorf("failed to compact history via llm: %w", recoveryErr)
		}
	default:
		recoveryErr = s.freeMessageTokensLLM(ctx, conv, prunePrompt, oldGenInput, overlimit)
		if recoveryErr != nil {
			if errors.Is(recoveryErr, core2.ErrContextLimitExceeded) {
				compactLines, compactIDs := s.buildRemovalCandidates(ctx, conv, compactCandidateLimit)
				if len(compactLines) == 0 {
					compactLines = []string{"(no removable items identified)"}
				}
				promptText = s.composeCompactPrompt(errMessage, compactLines, compactIDs)
				if cerr := s.compactHistoryLLM(ctx, conv, errMessage, oldGenInput, overlimit); cerr != nil {
					return fmt.Errorf("failed to compact history via llm: %w", cerr)
				}
			} else {
				return fmt.Errorf("failed to free message tokens via llm: %w", recoveryErr)
			}
		}
	}

	// Insert assistant message in current conversation turn
	turn := s.ensureTurnMeta(ctx, conv)
	if _, aerr := apiconv.AddMessage(ctx, s.convClient, &turn,
		apiconv.WithRole("assistant"),
		apiconv.WithType("text"),
		apiconv.WithStatus("error"),
		apiconv.WithContent(promptText),
		apiconv.WithInterim(1),
	); aerr != nil {
		return fmt.Errorf("failed to insert context-limit message: %w", aerr)
	}

	return nil
}

// buildRemovalCandidates constructs concise one-line entries for removable items
// excluding the last user message, capped by limit when > 0.
func (s *Service) buildRemovalCandidates(ctx context.Context, conv *apiconv.Conversation, limit int) ([]string, []string) {
	if conv == nil {
		return nil, nil
	}
	tr := conv.GetTranscript()
	lastUserID := ""
	// Identify the last non-interim user message id
	for i := len(tr) - 1; i >= 0 && lastUserID == ""; i-- {
		t := tr[i]
		if t == nil || len(t.Message) == 0 {
			continue
		}
		for j := len(t.Message) - 1; j >= 0; j-- {
			m := t.Message[j]
			if m == nil || m.Interim != 0 || m.Content == nil || strings.TrimSpace(*m.Content) == "" {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(m.Role), "user") {
				lastUserID = m.Id
				break
			}
		}
	}
	// Build candidates (prioritize low-value items for pruning)
	const previewLen = 1000
	type cand struct {
		line  string
		id    string
		kind  int
		size  int
		order int
	}
	var cands []cand
	order := 0
	for _, t := range tr {
		if t == nil || len(t.Message) == 0 {
			continue
		}
		for _, m := range t.Message {
			order++
			if m == nil || m.Id == lastUserID || m.Interim != 0 || (m.Archived != nil && *m.Archived == 1) {
				continue
			}
			typ := strings.ToLower(strings.TrimSpace(m.Type))
			role := strings.ToLower(strings.TrimSpace(m.Role))
			tc := firstToolCall(m)
			if typ != "text" && tc == nil {
				continue
			}
			// Build preview and size
			var line string
			if tc != nil {
				toolName := strings.TrimSpace(tc.ToolName)
				// args preview
				var args map[string]interface{}
				if tc.RequestPayload != nil && tc.RequestPayload.InlineBody != nil {
					raw := strings.TrimSpace(*tc.RequestPayload.InlineBody)
					if raw != "" {
						var parsed map[string]interface{}
						if json.Unmarshal([]byte(raw), &parsed) == nil {
							args = parsed
						}
					}
				}
				argStr, _ := json.Marshal(args)
				ap := textutil.RuneTruncate(string(argStr), previewLen)
				body := ""
				if tc.ResponsePayload != nil && tc.ResponsePayload.InlineBody != nil {
					body = *tc.ResponsePayload.InlineBody
				}
				sz := len(body)
				line = fmt.Sprintf("messageId: %s, type: tool, tool: %s, args_preview: \"%s\", size: %d bytes (~%d tokens)", m.Id, toolName, ap, sz, estimateTokens(body))
				cands = append(cands, cand{line: line, id: m.Id, kind: 0, size: sz, order: order})
			} else if role == "user" || role == "assistant" {
				body := ""
				if m.Content != nil {
					body = *m.Content
				}
				pv := textutil.RuneTruncate(body, previewLen)
				sz := len(body)
				line = fmt.Sprintf("messageId: %s, type: %s, preview: \"%s\", size: %d bytes (~%d tokens)", m.Id, role, pv, sz, estimateTokens(body))
				kind := 2
				if role == "assistant" {
					kind = 1
				}
				cands = append(cands, cand{line: line, id: m.Id, kind: kind, size: sz, order: order})
			} else {
				continue
			}
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].kind != cands[j].kind {
			return cands[i].kind < cands[j].kind
		}
		if cands[i].size != cands[j].size {
			return cands[i].size > cands[j].size
		}
		return cands[i].order < cands[j].order
	})
	if limit > 0 && len(cands) > limit {
		cands = cands[:limit]
	}
	out := make([]string, 0, len(cands))
	msgIDs := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.line)
		msgIDs = append(msgIDs, c.id)
	}
	return out, msgIDs
}

// ensureTurnMeta returns a TurnMeta for adding messages: uses existing context when present, otherwise derives from conversation.
func (s *Service) ensureTurnMeta(ctx context.Context, conv *apiconv.Conversation) memory.TurnMeta {
	if tm, ok := memory.TurnMetaFromContext(ctx); ok {
		return tm
	}
	turnID := ""
	if conv != nil && conv.LastTurnId != nil {
		turnID = *conv.LastTurnId
	}
	return memory.TurnMeta{ConversationID: conv.Id, TurnID: turnID, ParentMessageID: turnID}
}

func estimateTokens(s string) int {
	return estimateTokensInt(len(s))
}

func estimateTokensInt(stringLength int) int {
	if stringLength == 0 {
		return 0
	}
	if stringLength < 8 {
		return 1
	}
	return (stringLength + 3) / 4
}

func firstToolCall(m *agconv.MessageView) *apiconv.ToolCallView {
	if m == nil {
		return nil
	}
	for _, tm := range m.ToolMessage {
		if tm != nil && tm.ToolCall != nil {
			return tm.ToolCall
		}
	}
	return nil
}

func (s *Service) streamPlanSteps(ctx context.Context, streamId string, aPlan *plan.Plan) error {
	handler, cleanup, err := stream.PrepareStreamHandler(ctx, streamId)
	if err != nil {
		return err
	}
	defer cleanup()
	for _, step := range aPlan.Steps {
		if err = handler(ctx, &llm.StreamEvent{
			Response: &llm.GenerateResponse{
				Choices: []llm.Choice{{
					Message: llm.Message{Role: llm.RoleAssistant,
						ToolCalls: []llm.ToolCall{{
							ID:        step.ID,
							Name:      step.Name,
							Arguments: step.Args,
						}},
						Content: step.Reason},
					FinishReason: "tool",
				}},
			},
		}); err != nil {
			return fmt.Errorf("failed to emit stream event: %w", err)
		}
	}
	return nil
}

func (s *Service) canStream(ctx context.Context, genInput *core2.GenerateInput) (bool, error) {
	genInput.MatchModelIfNeeded(s.llm.ModelMatcher())
	model, err := s.llm.ModelFinder().Find(ctx, genInput.Model)
	if err != nil {
		return false, err
	}
	doStream := model.Implements(base.CanStream)
	return doStream, nil
}

func (s *Service) registerStreamPlannerHandler(ctx context.Context, reg tool.Registry, aPlan *plan.Plan, wg *sync.WaitGroup, nextStepIdx *int, genOutput *core2.GenerateOutput) (string, <-chan error) {
	// Use the orchestrator.Run context for executing tools so auth (e.g. MCP/BFF tokens)
	// propagates into tool execution. The stream callback context may not carry it.
	runCtx := ctx
	var mux sync.Mutex
	stepErrCh := make(chan error, 1)
	// Enable duplicate guard only in non-continuation mode so that recovery
	// flows (e.g., context-limit handling) can freely iterate internal/message
	// tools without being short-circuited.
	var guard *DuplicateGuard
	if !inContinuationMode(ctx) {
		guard = duplicateGuardFromContext(ctx)
		if guard == nil {
			guard = NewDuplicateGuard(nil)
		}
	}
	// Execute steps in order; do not de-duplicate by tool/args.
	// Duplicated tool steps will each execute independently.
	var stopped atomic.Bool
	id := stream.Register(func(_ context.Context, event *llm.StreamEvent) error {
		if stopped.Load() {
			return nil
		}
		if event == nil || event.Response == nil || len(event.Response.Choices) == 0 {
			if event != nil {
				return event.Err
			}
			return nil
		}
		choice := event.Response.Choices[0]
		mux.Lock()
		defer mux.Unlock()
		if content := strings.TrimSpace(choice.Message.Content); content != "" {
			if genOutput.Content == "" {
				genOutput.Content = content
			} else {
				genOutput.Content += content
			}
		}

		s.patchStreamingToolPreamble(runCtx, choice)
		s.extendPlanWithToolCalls(event.Response.ResponseID, &choice, aPlan)

		for *nextStepIdx < len(aPlan.Steps) {
			st := aPlan.Steps[*nextStepIdx]
			*nextStepIdx++
			if st.Type != "tool" {
				continue
			}
			// Previously we de-duplicated by tool name + canonical args and fanned-out
			// the result. We now always execute each step.
			wg.Add(1)
			step := st
			go func() {
				defer wg.Done()
				stepInfo := executil.StepInfo{ID: step.ID, Name: step.Name, Args: step.Args, ResponseID: step.ResponseID}
				// Optional duplicate protection: when enabled, block pathological
				// repetition patterns (same tool+args over and over) while still
				// preserving transcript via synthetic tool results.
				if guard != nil {
					if block, _ := guard.ShouldBlock(step.Name, step.Args); block {
						fmt.Printf("[debug] reactor: duplicate guard BLOCKED tool=%q args=%s — skipping (no synthesis)\n",
							step.Name, CanonicalArgs(step.Args))
						// Do NOT synthesize tool results. Synthesis pollutes the
						// transcript and confuses the LLM into re-calling the tool.
						// Instead, the AllBlocked flag on the plan will terminate
						// the ReAct loop, and the existing tool result from the
						// first execution remains in history for the model to use.
						return
					}
					fmt.Printf("[debug] reactor: duplicate guard ALLOWED tool=%q args=%s\n",
						step.Name, CanonicalArgs(step.Args))
				}
				// Execute tool; even on error we let the LLM decide next steps.
				// Errors are persisted on the tool call and exposed via tool result payload.
				call, _, err := executil.ExecuteToolStep(runCtx, reg, stepInfo, s.convClient)
				if err != nil {
					fmt.Printf("error: tool step %s execution failed: %v\n", step.Name, err)
				}

				if guard != nil {
					guard.RegisterResult(step.Name, step.Args, call)
				}
			}()
		}
		return nil
	})
	return id, stepErrCh
}

// toolDedupKey builds a stable key from tool name and arguments.
// It canonicalizes map/array structures so logically equivalent args
// produce identical keys independent of map iteration order.
// (dedup helpers removed; duplicates are now executed independently)

func (s *Service) extendPlanFromResponse(ctx context.Context, genOutput *core2.GenerateOutput, aPlan *plan.Plan) (bool, error) {
	if genOutput.Response == nil || len(genOutput.Response.Choices) == 0 {
		return false, nil
	}
	for j := range genOutput.Response.Choices {
		choice := &genOutput.Response.Choices[j]
		s.extendPlanWithToolCalls(genOutput.Response.ResponseID, choice, aPlan)
	}
	if len(aPlan.Steps) == 0 {
		if err := s.extendPlanFromContent(ctx, genOutput, aPlan); err != nil {
			return false, err
		}
	}
	return !aPlan.IsEmpty(), nil
}

func (s *Service) extendPlanWithToolCalls(responseID string, choice *llm.Choice, aPlan *plan.Plan) {
	if len(choice.Message.ToolCalls) == 0 {
		return
	}
	reason := strings.TrimSpace(choice.Message.Content)
	if reason == "" {
		resp := &llm.GenerateResponse{Choices: []llm.Choice{*choice}}
		reason = strings.TrimSpace(modelcall.AssistantPreambleFromResponse(resp, ""))
	}
	steps := make(plan.Steps, 0, len(choice.Message.ToolCalls))
	for idx, tc := range choice.Message.ToolCalls {
		name := tc.Name
		args := tc.Arguments
		if name == "" && tc.Function.Name != "" {
			name = tc.Function.Name
		}
		if args == nil && tc.Function.Arguments != "" {
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &parsed); err == nil {
				args = parsed
			}
		}
		stepID := strings.TrimSpace(tc.ID)
		if stepID == "" {
			stepID = fallbackToolStepID(responseID, idx, name)
		}

		if prev := aPlan.Steps.Find(stepID); prev != nil {
			prev.Name = name
			prev.Args = args
			prev.Reason = reason
			continue
		}

		steps = append(steps, plan.Step{
			ID:         stepID,
			Type:       "tool",
			Name:       name,
			Args:       args,
			Reason:     reason,
			ResponseID: strings.TrimSpace(responseID),
		})
	}
	aPlan.Steps = append(aPlan.Steps, steps...)
}

func fallbackToolStepID(responseID string, idx int, name string) string {
	base := strings.TrimSpace(responseID)
	if base == "" {
		base = "stream"
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	return fmt.Sprintf("%s:%d:%s", base, idx, name)
}

func (s *Service) patchStreamingToolPreamble(ctx context.Context, choice llm.Choice) {
	if s == nil || s.convClient == nil {
		return
	}
	if len(choice.Message.ToolCalls) == 0 && choice.Message.FunctionCall == nil {
		return
	}
	msgID := strings.TrimSpace(memory.ModelMessageIDFromContext(ctx))
	if msgID == "" {
		return
	}
	turn, _ := memory.TurnMetaFromContext(ctx)
	conversationID := strings.TrimSpace(turn.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(memory.ConversationIDFromContext(ctx))
	}
	if conversationID == "" {
		return
	}
	resp := &llm.GenerateResponse{Choices: []llm.Choice{choice}}
	content, hasToolCalls := modelcall.AssistantContentFromResponse(resp)
	if !hasToolCalls {
		return
	}
	content = strings.TrimSpace(content)
	preamble := strings.TrimSpace(modelcall.AssistantPreambleFromResponse(resp, content))
	if preamble == "" {
		return
	}
	if content == "" {
		content = preamble
	}

	// Deduplicate: skip patch when preamble text hasn't changed for this message.
	s.lastPreambleMu.Lock()
	if s.lastPreamble == nil {
		s.lastPreamble = make(map[string]string)
	}
	if s.lastPreamble[msgID] == preamble {
		s.lastPreambleMu.Unlock()
		return
	}
	s.lastPreamble[msgID] = preamble
	s.lastPreambleMu.Unlock()

	msg := apiconv.NewMessage()
	msg.SetId(msgID)
	msg.SetConversationID(conversationID)
	if strings.TrimSpace(turn.TurnID) != "" {
		msg.SetTurnID(turn.TurnID)
	}
	msg.SetContent(content)
	msg.SetPreamble(preamble)
	msg.SetRawContent(content)
	msg.SetInterim(1)
	_ = s.convClient.PatchMessage(ctx, msg)
}

func (s *Service) extendPlanFromContent(ctx context.Context, genOutput *core2.GenerateOutput, aPlan *plan.Plan) error {
	var err error
	if strings.Contains(genOutput.Content, `"tool"`) {
		err = executil.EnsureJSONResponse(ctx, genOutput.Content, aPlan)
	}
	if strings.Contains(genOutput.Content, `"elicitation"`) {
		aPlan.Elicitation = &plan.Elicitation{}
		_ = executil.EnsureJSONResponse(ctx, genOutput.Content, aPlan.Elicitation)
		if aPlan.Elicitation.IsEmpty() {
			aPlan.Elicitation = nil
		} else {
			if aPlan.Elicitation.ElicitationId == "" {
				aPlan.Elicitation.ElicitationId = uuid.New().String()
			}
		}
	}

	aPlan.Steps.EnsureID()
	if len(aPlan.Steps) > 0 && strings.TrimSpace(aPlan.Steps[0].Reason) == "" {
		prefix := genOutput.Content
		if idx := strings.Index(prefix, "```json"); idx != -1 {
			prefix = prefix[:idx]
		} else if idx := strings.Index(prefix, "{"); idx != -1 {
			prefix = prefix[:idx]
		}
		prefix = strings.TrimSpace(prefix)
		if prefix != "" {
			aPlan.Steps[0].Reason = prefix
		}
	}
	return err
}

func (s *Service) synthesizeFinalResponse(genOutput *core2.GenerateOutput) {
	if strings.TrimSpace(genOutput.Content) == "" || genOutput.Response != nil {
		return
	}
	genOutput.Response = &llm.GenerateResponse{
		Choices: []llm.Choice{{
			Index:        0,
			Message:      llm.Message{Role: llm.RoleAssistant, Content: strings.TrimSpace(genOutput.Content)},
			FinishReason: "stop",
		}},
	}
}

func New(service *core2.Service, registry tool.Registry, convClient apiconv.Client, finder agentmdl.Finder, builder func(ctx context.Context, conv *apiconv.Conversation, instruction string) (*core2.GenerateInput, error)) *Service {
	return &Service{
		llm:            service,
		registry:       registry,
		convClient:     convClient,
		agentFinder:    finder,
		buildPlanInput: builder,
	}
}
