package reactor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	debugtrace "github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/agent/plan"
	"github.com/viant/agently-core/protocol/tool"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/agent/prompts"
	core2 "github.com/viant/agently-core/service/core"
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

	// turnToolResults carries prior successful tool results across repeated
	// reactor.Run invocations within the same turn. This protects against
	// loops when prompt history/transcript does not yet expose prior tool
	// results back to the model on the next iteration.
	turnToolResultsMu sync.Mutex
	turnToolResults   map[string][]llm.ToolCall

	// lastPreamble deduplicates patchStreamingToolPreamble calls: only patches
	// when the preamble text has actually changed. Keyed by message ID.
	lastPreambleMu sync.Mutex
	lastPreamble   map[string]string
}

// ctxKeyLimitRecoveryAttempted guards one-shot presentation of the context-limit guidance within a single Run invocation.
type ctxKeyPresentedType int

const ctxKeyLimitRecoveryAttempted ctxKeyPresentedType = 1

// ctxKeyContinuationMode marks runs that are invoked as part of a
// continuation/recovery flow (for example, context-limit handling). Duplicate
// protection is disabled in this mode so message tools can iterate
// freely when trimming history.
const ctxKeyContinuationMode ctxKeyPresentedType = 2

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

func (s *Service) Run(ctx context.Context, genInput *core2.GenerateInput, genOutput *core2.GenerateOutput) (*plan.Plan, error) {
	aPlan := plan.New()
	priorResults := extractPriorToolResults(genInput)
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		priorResults = mergePriorToolResults(priorResults, s.getTurnToolResults(strings.TrimSpace(tm.TurnID)))
	}

	var wg sync.WaitGroup
	nextStepIdx := 0
	// Binding registry to current conversation (if any) so tool.Execute receives ctx with convID.
	reg := tool.WithConversation(s.registry, runtimerequestctx.ConversationIDFromContext(ctx))
	// Do not create child cancels here; errors must not cancel context.
	streamId := s.registerStreamPlannerHandler(ctx, reg, aPlan, &wg, &nextStepIdx, genOutput, priorResults)
	canStream, err := s.canStream(ctx, genInput)
	if err != nil {
		return nil, fmt.Errorf("failed to check if model can stream: %w", err)
	}
	if canStream {
		logx.Debugf("reactor", "Run starting stream")
		cleanup, err := s.llm.Stream(ctx, &core2.StreamInput{StreamID: streamId, GenerateInput: genInput}, &core2.StreamOutput{})
		logx.Debugf("reactor", "Run stream returned err=%v", err)
		defer cleanup()
		if err != nil {
			// Context cancellation means the turn was stopped externally (user cancel, deadline).
			// Do not propagate as a turn error — wait for any in-flight tool goroutines to
			// finalize their results (via detachedFinalizeCtx) and return the partial plan.
			// The turn will complete with whatever was generated up to the cancellation point.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				logx.Debugf("reactor", "Run stream canceled; waiting for tool goroutines")
				wg.Wait()
				logx.Debugf("reactor", "Run tool goroutines finalized after stream cancel")
				return aPlan, nil
			}
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
		logx.Debugf("reactor", "Run waiting for tool goroutines")
		wg.Wait()
		logx.Debugf("reactor", "Run all tool goroutines done")
		s.synthesizeFinalResponse(genOutput)

	} else {
		if err := s.llm.Generate(ctx, genInput, genOutput); err != nil {
			// Context cancellation: same as stream — return partial plan without error
			// so the turn can complete with whatever content was produced.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				logx.Debugf("reactor", "Run generate canceled; returning partial plan")
				return aPlan, nil
			}
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
			// Ensure genOutput.MessageID is populated for non-streaming tool
			// calls (e.g., extendPlanFromResponse path). OnCallStart stored
			// the assistant message ID at the turn level during streaming.
			if genOutput.MessageID == "" {
				if tm, ok2 := runtimerequestctx.TurnMetaFromContext(ctx); ok2 {
					if mid := strings.TrimSpace(runtimerequestctx.TurnModelMessageID(tm.TurnID)); mid != "" {
						genOutput.MessageID = mid
					}
				}
			}
			logx.Debugf("reactor", "Run streamPlanSteps starting steps=%d msgID=%q", len(aPlan.Steps), genOutput.MessageID)
			if err = s.streamPlanSteps(ctx, streamId, aPlan); err != nil {
				return nil, fmt.Errorf("failed to stream plan steps: %w", err)
			}
			logx.Debugf("reactor", "Run streamPlanSteps done; waiting for wg")
			wg.Wait()
			logx.Debugf("reactor", "Run extendPlan wg done")
		}
	}

	RefinePlan(aPlan)

	// Debug trace: log plan summary to /tmp/agently-debug.log
	{
		toolNames := make([]string, 0, len(aPlan.Steps))
		for _, st := range aPlan.Steps {
			toolNames = append(toolNames, fmt.Sprintf("%s(%s)", st.Name, st.ID))
		}
		debugtrace.LogToFile("reactor", "plan_after_run", map[string]interface{}{
			"stepCount":   len(aPlan.Steps),
			"steps":       toolNames,
			"contentLen":  len(genOutput.Content),
			"contentHead": truncStr(genOutput.Content, 120),
			"messageID":   genOutput.MessageID,
		})
	}

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

func New(service *core2.Service, registry tool.Registry, convClient apiconv.Client, finder agentmdl.Finder, builder func(ctx context.Context, conv *apiconv.Conversation, instruction string) (*core2.GenerateInput, error)) *Service {
	return &Service{
		llm:             service,
		registry:        registry,
		convClient:      convClient,
		agentFinder:     finder,
		buildPlanInput:  builder,
		turnToolResults: make(map[string][]llm.ToolCall),
	}
}

func mergePriorToolResults(existing []llm.ToolCall, extra []llm.ToolCall) []llm.ToolCall {
	if len(extra) == 0 {
		return existing
	}
	if len(existing) == 0 {
		out := make([]llm.ToolCall, 0, len(extra))
		out = append(out, extra...)
		return out
	}
	seen := map[string]struct{}{}
	out := make([]llm.ToolCall, 0, len(existing)+len(extra))
	keyOf := func(call llm.ToolCall) string {
		id := strings.TrimSpace(call.ID)
		if id != "" {
			return "id:" + id
		}
		return strings.TrimSpace(strings.ToLower(call.Name)) + "::" + CanonicalArgs(call.Arguments)
	}
	for _, call := range existing {
		key := keyOf(call)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, call)
	}
	for _, call := range extra {
		key := keyOf(call)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, call)
	}
	return out
}

func (s *Service) getTurnToolResults(turnID string) []llm.ToolCall {
	if s == nil || strings.TrimSpace(turnID) == "" {
		return nil
	}
	s.turnToolResultsMu.Lock()
	defer s.turnToolResultsMu.Unlock()
	stored := s.turnToolResults[strings.TrimSpace(turnID)]
	if len(stored) == 0 {
		return nil
	}
	out := make([]llm.ToolCall, 0, len(stored))
	out = append(out, stored...)
	return out
}

func (s *Service) rememberTurnToolResult(turnID string, call llm.ToolCall) {
	if s == nil || strings.TrimSpace(turnID) == "" {
		return
	}
	if strings.TrimSpace(call.Name) == "" {
		return
	}
	s.turnToolResultsMu.Lock()
	defer s.turnToolResultsMu.Unlock()
	keyOf := func(call llm.ToolCall) string {
		id := strings.TrimSpace(call.ID)
		if id != "" {
			return "id:" + id
		}
		return strings.TrimSpace(strings.ToLower(call.Name)) + "::" + CanonicalArgs(call.Arguments)
	}
	key := keyOf(call)
	current := s.turnToolResults[strings.TrimSpace(turnID)]
	for i, existing := range current {
		if keyOf(existing) == key {
			current[i] = call
			s.turnToolResults[strings.TrimSpace(turnID)] = current
			return
		}
	}
	s.turnToolResults[strings.TrimSpace(turnID)] = append(current, call)
}
