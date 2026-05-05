package reactor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/base"
	debugtrace "github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/internal/textutil"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/protocol/tool"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	core2 "github.com/viant/agently-core/service/core"
	modelcall "github.com/viant/agently-core/service/core/modelcall"
	"github.com/viant/agently-core/service/core/stream"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
)

func normalizeStreamContentForMerge(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
}

func mergeStreamContent(current, incoming string) string {
	currentRaw := current
	incomingRaw := incoming
	current = normalizeStreamContentForMerge(currentRaw)
	incoming = normalizeStreamContentForMerge(incomingRaw)
	if incomingRaw == "" {
		return currentRaw
	}
	if currentRaw == "" {
		return incomingRaw
	}
	// Preserve raw whitespace-only deltas; they are significant in streamed text.
	if incoming == "" {
		return currentRaw + incomingRaw
	}
	// Prefer exact raw cumulative snapshots before falling back to normalized
	// comparisons so trailing spaces are not lost.
	if strings.HasPrefix(incomingRaw, currentRaw) {
		return incomingRaw
	}
	if strings.HasPrefix(currentRaw, incomingRaw) {
		return currentRaw
	}
	if incoming == current {
		return currentRaw
	}
	if strings.HasPrefix(incoming, current) {
		return incomingRaw
	}
	if strings.HasPrefix(current, incoming) {
		return currentRaw
	}
	return currentRaw + incomingRaw
}

func plannedToolCalls(choice *llm.Choice) []streaming.PlannedToolCall {
	if choice == nil || len(choice.Message.ToolCalls) == 0 {
		return nil
	}
	result := make([]streaming.PlannedToolCall, 0, len(choice.Message.ToolCalls))
	for _, call := range choice.Message.ToolCalls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			name = strings.TrimSpace(call.Function.Name)
		}
		result = append(result, streaming.PlannedToolCall{
			ToolCallID: strings.TrimSpace(call.ID),
			ToolName:   name,
		})
	}
	return result
}

func (s *Service) publishPlannedToolCallsEvent(ctx context.Context, responseID string, choice *llm.Choice) {
	pub, ok := modelcall.StreamPublisherFromContext(ctx)
	if !ok || choice == nil {
		return
	}
	toolCalls := plannedToolCalls(choice)
	if len(toolCalls) == 0 {
		return
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	runMeta, _ := runtimerequestctx.RunMetaFromContext(ctx)
	assistantMessageID := strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	if assistantMessageID == "" {
		return
	}
	content := strings.TrimSpace(choice.Message.Content)
	resp := &llm.GenerateResponse{
		Choices:    []llm.Choice{*choice},
		ResponseID: strings.TrimSpace(responseID),
	}
	preamble := strings.TrimSpace(modelcall.AssistantPreambleFromResponse(resp, content))
	iteration := 0
	if runMeta.Iteration > 0 {
		iteration = runMeta.Iteration
	}
	status := "thinking"
	if strings.TrimSpace(choice.FinishReason) != "" {
		status = strings.TrimSpace(choice.FinishReason)
	}
	modelName := ""
	if resp != nil {
		modelName = strings.TrimSpace(resp.Model)
	}
	_ = pub.Publish(ctx, &modelcall.StreamEvent{
		ConversationID: strings.TrimSpace(turn.ConversationID),
		Event: &streaming.Event{
			ID:               assistantMessageID,
			ConversationID:   strings.TrimSpace(turn.ConversationID),
			StreamID:         strings.TrimSpace(turn.ConversationID),
			MessageID:        assistantMessageID,
			Type:             streaming.EventTypeToolCallsPlanned,
			TurnID:           strings.TrimSpace(turn.TurnID),
			ParentMessageID:  strings.TrimSpace(turn.ParentMessageID),
			ResponseID:       strings.TrimSpace(responseID),
			Status:           status,
			Content:          content,
			Narration:        preamble,
			Iteration:        iteration,
			PageIndex:        iteration,
			PageCount:        iteration,
			LatestPage:       true,
			Model:            &streaming.EventModel{Model: modelName},
			ToolCallsPlanned: toolCalls,
		},
	})
}

func (s *Service) streamPlanSteps(ctx context.Context, streamId string, aPlan *execution.Plan) error {
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
	return model.Implements(base.CanStream), nil
}

func (s *Service) registerStreamPlannerHandler(ctx context.Context, reg tool.Registry, aPlan *execution.Plan, wg *sync.WaitGroup, nextStepIdx *int, genOutput *core2.GenerateOutput) string {
	runCtx := ctx
	var mux sync.Mutex
	var stopped atomic.Bool
	id := stream.Register(func(callbackCtx context.Context, event *llm.StreamEvent) error {
		if stopped.Load() || event == nil {
			return nil
		}
		if event.Err != nil {
			return event.Err
		}
		if mid := strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(callbackCtx)); mid != "" {
			genOutput.MessageID = mid
		} else if tm, ok := runtimerequestctx.TurnMetaFromContext(runCtx); ok {
			if mid := strings.TrimSpace(runtimerequestctx.TurnModelMessageID(tm.TurnID)); mid != "" {
				genOutput.MessageID = mid
			}
		}
		if event.Kind != "" {
			return s.handleTypedStreamEvent(runCtx, event, &mux, genOutput, aPlan, nextStepIdx, wg, reg)
		}
		if event.Response == nil || len(event.Response.Choices) == 0 {
			return nil
		}
		choice := event.Response.Choices[0]
		if debugtrace.Enabled() {
			debugtrace.Write("reactor", "stream_choice", map[string]any{
				"responseID":    strings.TrimSpace(event.Response.ResponseID),
				"finishReason":  strings.TrimSpace(choice.FinishReason),
				"contentHead":   textutil.RuneTruncate(strings.TrimSpace(choice.Message.Content), 200),
				"toolCallCount": len(choice.Message.ToolCalls),
				"toolCalls":     debugtrace.SummarizeToolCalls(choice.Message.ToolCalls),
			})
		}
		mux.Lock()
		defer mux.Unlock()
		if content := choice.Message.Content; content != "" {
			genOutput.Content = mergeStreamContent(genOutput.Content, content)
		}
		s.publishPlannedToolCallsEvent(runCtx, event.Response.ResponseID, &choice)
		s.patchStreamingToolPreamble(runCtx, choice)
		s.extendPlanWithToolCalls(event.Response.ResponseID, &choice, aPlan)
		s.launchPendingSteps(runCtx, aPlan, nextStepIdx, wg, reg, genOutput.MessageID)
		return nil
	})
	return id
}

func (s *Service) handleTypedStreamEvent(
	ctx context.Context,
	event *llm.StreamEvent,
	mux *sync.Mutex,
	genOutput *core2.GenerateOutput,
	aPlan *execution.Plan,
	nextStepIdx *int,
	wg *sync.WaitGroup,
	reg tool.Registry,
) error {
	switch event.Kind {
	case llm.StreamEventTextDelta:
		mux.Lock()
		genOutput.Content += event.Delta
		mux.Unlock()
	case llm.StreamEventToolCallCompleted:
		mux.Lock()
		defer mux.Unlock()
		stepID := strings.TrimSpace(event.ToolCallID)
		if stepID == "" {
			stepID = fallbackToolStepID(event.ResponseID, len(aPlan.Steps), event.ToolName)
		}
		if prev := aPlan.Steps.Find(stepID); prev != nil {
			prev.Name = strings.TrimSpace(event.ToolName)
			prev.Args = event.Arguments
			prev.Reason = strings.TrimSpace(genOutput.Content)
		} else {
			aPlan.Steps = append(aPlan.Steps, execution.Step{
				ID:         stepID,
				Type:       "tool",
				Name:       strings.TrimSpace(event.ToolName),
				Args:       event.Arguments,
				Reason:     strings.TrimSpace(genOutput.Content),
				ResponseID: strings.TrimSpace(event.ResponseID),
			})
		}
		s.publishTypedToolCallEvent(ctx, event)
		s.launchPendingSteps(ctx, aPlan, nextStepIdx, wg, reg, genOutput.MessageID)
	case llm.StreamEventToolCallStarted:
	case llm.StreamEventTurnCompleted:
		mux.Lock()
		if event.Response != nil {
			genOutput.Response = event.Response
			for idx := len(event.Response.Choices) - 1; idx >= 0; idx-- {
				choice := event.Response.Choices[idx]
				if len(choice.Message.ToolCalls) > 0 {
					continue
				}
				if content := strings.TrimSpace(choice.Message.Content); content != "" {
					genOutput.Content = content
					break
				}
			}
		}
		mux.Unlock()
	case llm.StreamEventReasoningDelta, llm.StreamEventToolCallDelta, llm.StreamEventUsage, llm.StreamEventItemCompleted:
	default:
		if debugtrace.Enabled() {
			debugtrace.Write("reactor", "unhandled_kind", map[string]any{"kind": string(event.Kind)})
		}
	}
	return nil
}

func (s *Service) publishTypedToolCallEvent(ctx context.Context, event *llm.StreamEvent) {
	pub, ok := modelcall.StreamPublisherFromContext(ctx)
	if !ok {
		return
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	assistantMessageID := strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	if assistantMessageID == "" {
		return
	}
	runMeta, _ := runtimerequestctx.RunMetaFromContext(ctx)
	iteration := 0
	if runMeta.Iteration > 0 {
		iteration = runMeta.Iteration
	}
	_ = pub.Publish(ctx, &modelcall.StreamEvent{
		ConversationID: strings.TrimSpace(turn.ConversationID),
		Event: &streaming.Event{
			ID:              assistantMessageID,
			ConversationID:  strings.TrimSpace(turn.ConversationID),
			StreamID:        strings.TrimSpace(turn.ConversationID),
			Type:            streaming.EventTypeToolCallsPlanned,
			TurnID:          strings.TrimSpace(turn.TurnID),
			MessageID:       assistantMessageID,
			ParentMessageID: strings.TrimSpace(turn.ParentMessageID),
			ResponseID:      strings.TrimSpace(event.ResponseID),
			Status:          "tool_calls",
			Iteration:       iteration,
			PageIndex:       iteration,
			PageCount:       iteration,
			LatestPage:      true,
			ToolCallsPlanned: []streaming.PlannedToolCall{{
				ToolCallID: strings.TrimSpace(event.ToolCallID),
				ToolName:   strings.TrimSpace(event.ToolName),
			}},
		},
	})
}

func (s *Service) launchPendingSteps(ctx context.Context, aPlan *execution.Plan, nextStepIdx *int, wg *sync.WaitGroup, reg tool.Registry, assistantMsgID ...string) {
	toolCtx := ctx
	turnID := ""
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		turnID = strings.TrimSpace(tm.TurnID)
	}
	if len(assistantMsgID) > 0 {
		if id := strings.TrimSpace(assistantMsgID[0]); id != "" {
			toolCtx = context.WithValue(ctx, runtimerequestctx.ModelMessageIDKey, id)
			logx.Debugf("reactor", "launchPendingSteps enriched context with assistant_msg_id=%s", id)
		} else {
			logx.Debugf("reactor", "launchPendingSteps assistantMsgID param is empty")
		}
	} else {
		existing := strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
		logx.Debugf("reactor", "launchPendingSteps no assistantMsgID param; ctx has ModelMessageID=%q", existing)
	}
	for *nextStepIdx < len(aPlan.Steps) {
		st := aPlan.Steps[*nextStepIdx]
		*nextStepIdx++
		if st.Type != "tool" {
			continue
		}
		step := st
		if isActivationBarrierTool(step.Name) {
			call := s.executePendingToolStep(toolCtx, reg, step, turnID)
			if turnID != "" {
				s.rememberTurnToolResult(turnID, call)
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			call := s.executePendingToolStep(toolCtx, reg, step, turnID)
			if turnID != "" {
				s.rememberTurnToolResult(turnID, call)
			}
		}()
	}
}

func (s *Service) executePendingToolStep(toolCtx context.Context, reg tool.Registry, step execution.Step, turnID string) llm.ToolCall {
	stepInfo := toolexec.StepInfo{ID: step.ID, Name: step.Name, Args: step.Args, ResponseID: step.ResponseID}
	if debugtrace.Enabled() {
		debugtrace.Write("reactor", "tool_step_scheduled", map[string]any{
			"stepID":      strings.TrimSpace(step.ID),
			"name":        strings.TrimSpace(step.Name),
			"responseID":  strings.TrimSpace(step.ResponseID),
			"args":        step.Args,
			"currentTurn": turnID,
		})
	}
	call, _, err := toolexec.ExecuteToolStep(toolCtx, reg, stepInfo, s.convClient)
	if logx.Enabled() {
		logx.Infof("reactor", "tool step executed name=%q result_len=%d err=%v", strings.TrimSpace(step.Name), len(call.Result), err)
	}
	if err != nil {
		logx.Warnf("reactor", "tool step %s execution failed: %v", step.Name, err)
	}
	if debugtrace.Enabled() {
		debugtrace.Write("reactor", "tool_step_executed", map[string]any{
			"stepID":     strings.TrimSpace(step.ID),
			"name":       strings.TrimSpace(step.Name),
			"responseID": strings.TrimSpace(step.ResponseID),
			"args":       step.Args,
			"result":     debugtrace.SummarizeToolCalls([]llm.ToolCall{call}),
			"error":      errorString(err),
		})
	}
	return call
}

func isActivationBarrierTool(name string) bool {
	name = strings.TrimSpace(name)
	return name == "llm/skills:activate" || name == "llm/skills/activate" || name == "llm/skills-activate"
}

func (s *Service) patchStreamingToolPreamble(ctx context.Context, choice llm.Choice) {
	if s == nil || s.convClient == nil {
		return
	}
	if len(choice.Message.ToolCalls) == 0 && choice.Message.FunctionCall == nil {
		return
	}
	msgID := strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	if msgID == "" {
		return
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	conversationID := strings.TrimSpace(turn.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
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
	s.lastPreambleMu.Lock()
	if s.lastNarration == nil {
		s.lastNarration = make(map[string]string)
	}
	if s.lastNarration[msgID] == preamble {
		s.lastPreambleMu.Unlock()
		return
	}
	s.lastNarration[msgID] = preamble
	s.lastPreambleMu.Unlock()

	msg := apiconv.NewMessage()
	msg.SetId(msgID)
	msg.SetConversationID(conversationID)
	if strings.TrimSpace(turn.TurnID) != "" {
		msg.SetTurnID(turn.TurnID)
	}
	msg.SetContent(content)
	msg.SetNarration(preamble)
	msg.SetRawContent(content)
	msg.SetInterim(1)
	_ = s.convClient.PatchMessage(ctx, msg)
}
