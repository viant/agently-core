package agent

import (
	"context"
	"fmt"
	"math"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider"
	"github.com/viant/agently-core/genai/llm/provider/base"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/runtime/recovery"
	core "github.com/viant/agently-core/service/core"
)

type contextCapacityFinder interface{ ConfigByIDOrModel(string) *provider.Config }

func (s *Service) prepareProactiveCompaction(ctx context.Context, query *QueryInput, gen *core.GenerateInput, replay []*binding.Message) (context.Context, []*binding.Message, error) {
	// Off is deliberately the first branch: no finder, count or transcript calls.
	if query == nil || query.Agent == nil || query.Agent.ContextCompactionPercent == nil {
		return ctx, replay, nil
	}
	percent := *query.Agent.ContextCompactionPercent
	if math.IsNaN(percent) || math.IsInf(percent, 0) || percent <= 0 || percent > 100 {
		return ctx, replay, fmt.Errorf("contextCompactionPercent must be finite and greater than 0 and at most 100")
	}
	gen.MatchModelIfNeeded(s.llm.ModelMatcher())
	finder, ok := s.llm.ModelFinder().(contextCapacityFinder)
	if !ok {
		return ctx, replay, fmt.Errorf("proactive compaction requires a selected model with configured contextWindow")
	}
	cfg := finder.ConfigByIDOrModel(gen.Model)
	if cfg == nil || cfg.Options.ContextWindow <= 0 {
		return ctx, replay, fmt.Errorf("proactive compaction requires positive options.contextWindow for model %q", gen.Model)
	}
	capacity := cfg.Options.ContextWindow
	model, err := s.llm.ModelFinder().Find(ctx, gen.Model)
	if err != nil {
		return ctx, replay, err
	}
	if gen.Options == nil {
		gen.Options = &llm.Options{}
	}
	gen.Options.Stream = model.Implements(base.CanStream)
	conv, err := s.conversation.GetConversation(ctx, query.ConversationID, apiconv.WithIncludeToolCall(true), apiconv.WithIncludeModelCall(true))
	if err != nil {
		return ctx, replay, fmt.Errorf("load proactive compaction history: %w", err)
	}
	if conv == nil || conv.Id != query.ConversationID {
		return ctx, replay, fmt.Errorf("proactive compaction conversation unavailable")
	}
	if recovery.NeedsFullHistory(conv) {
		ctx = recovery.WithFullHistory(ctx, true)
	}

	// Keep an unprepared input for the post-compaction recount. Init appends.
	original := *gen
	original.Message = append([]llm.Message(nil), gen.Message...)
	if gen.Options != nil {
		options := *gen.Options
		options.Tools = append([]llm.Tool(nil), gen.Options.Tools...)
		original.Options = &options
	}
	tokens, err := s.llm.CountInputTokens(ctx, gen)
	if err != nil {
		return ctx, replay, err
	}
	if float64(tokens)*100 < percent*float64(capacity) {
		return ctx, replay, nil
	}
	eligible := recovery.EligibleMessages(conv)
	signature := recovery.EligibleSignature(eligible)
	if signature == "" {
		return ctx, replay, nil
	}
	// Claim once even if two callers inspect the same completed-history state.
	previous, exists := s.proactiveCompactions.LoadOrStore(conv.Id, signature)
	if exists && (previous == signature || !s.proactiveCompactions.CompareAndSwap(conv.Id, previous, signature)) {
		return ctx, replay, nil
	}
	// One attempt for this original-history state. A failed recovery cannot spin.
	ids := make(map[string]bool, len(eligible))
	for id := range eligible {
		ids[id] = true
	}
	if err := s.orchestrator.CompactProactively(ctx, conv, gen, &recovery.CompactionScope{ConversationID: conv.Id, EligibleIDs: ids}, tokens, capacity, percent); err != nil {
		return ctx, replay, fmt.Errorf("proactive history compaction failed: %w", err)
	}
	fresh, err := s.conversation.GetConversation(ctx, conv.Id, apiconv.WithIncludeToolCall(true))
	if err != nil {
		return ctx, replay, fmt.Errorf("reload compacted history: %w", err)
	}
	if fresh == nil {
		return ctx, replay, fmt.Errorf("compacted conversation unavailable")
	}
	replay = filterArchivedCompactionReplay(fresh, replay)
	result, err := s.buildHistoryWithLimit(ctx, fresh.GetTranscript(), query)
	if err != nil {
		return ctx, replay, fmt.Errorf("rebuild compacted history: %w", err)
	}
	original.Binding.History = result.History
	appendMissingReplayMessages(&original.Binding.History, replay)
	*gen = original
	ctx = recovery.WithFullHistory(ctx, true)
	after, err := s.llm.CountInputTokens(ctx, gen)
	if err != nil {
		return ctx, replay, err
	}
	// Store the remaining original state, excluding summary/recovery artifacts.
	s.proactiveCompactions.Store(conv.Id, recovery.EligibleSignature(recovery.EligibleMessages(fresh)))
	logx.Infof("conversation", "proactive compaction convo=%q model=%q input_tokens_before=%d input_tokens_after=%d context_capacity=%d threshold_percent=%g", conv.Id, gen.Model, tokens, after, capacity, percent)
	// This is a trigger, not a hard cap. Recounted requests proceed and retain
	// the existing provider-overflow recovery behavior.
	return ctx, replay, nil
}

func filterArchivedCompactionReplay(conv *apiconv.Conversation, replay []*binding.Message) []*binding.Message {
	archivedIDs, archivedOps := map[string]bool{}, map[string]bool{}
	for _, turn := range conv.GetTranscript() {
		if turn == nil {
			continue
		}
		for _, m := range turn.Message {
			if m == nil || m.Archived == nil || *m.Archived == 0 {
				continue
			}
			archivedIDs[m.Id] = true
			if m.MessageToolCall != nil {
				archivedOps[m.MessageToolCall.OpId] = true
			}
			for _, tm := range m.ToolMessage {
				if (strings.EqualFold(m.Type, "tool_op") || strings.EqualFold(m.Role, "tool")) && tm != nil && tm.ToolCall != nil {
					archivedOps[tm.ToolCall.OpId] = true
				}
			}
		}
	}
	kept := make([]*binding.Message, 0, len(replay))
	for _, m := range replay {
		if m == nil || archivedIDs[strings.TrimSpace(m.ID)] || archivedOps[strings.TrimSpace(m.ToolOpID)] {
			continue
		}
		kept = append(kept, m)
	}
	return kept
}
