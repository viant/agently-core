package reactor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/textutil"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/runtime/memory"
	core2 "github.com/viant/agently-core/service/core"
)

func (s *Service) presentContextLimitExceeded(ctx context.Context, oldGenInput *core2.GenerateInput, causeErr error, errMessage string) error {
	convID := memory.ConversationIDFromContext(ctx)
	if strings.TrimSpace(convID) == "" || s.convClient == nil {
		return fmt.Errorf("missing conversation context")
	}
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

func (s *Service) buildRemovalCandidates(ctx context.Context, conv *apiconv.Conversation, limit int) ([]string, []string) {
	_ = ctx
	if conv == nil {
		return nil, nil
	}
	tr := conv.GetTranscript()
	lastUserID := ""
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
			var line string
			if tc != nil {
				toolName := strings.TrimSpace(tc.ToolName)
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

func estimateTokens(s string) int { return estimateTokensInt(len(s)) }

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
