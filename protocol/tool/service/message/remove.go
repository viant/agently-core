package message

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/textutil"
	"github.com/viant/agently-core/runtime/recovery"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

type RemoveTuple struct {
	Summary    string   `json:"summary" description:"summary to associate with removed messages"`
	MessageIds []string `json:"messageIds"`
	Role       string   `json:"role,omitempty"`
}

type RemoveInput struct {
	Tuples []RemoveTuple `json:"tuples"`
}

type RemoveOutput struct {
	CreatedSummaryMessageIds []string `json:"createdSummaryMessageIds"`
	ArchivedMessages         int      `json:"archivedMessages"`
}

// remove accepts tuples of {summary, messageIds} and for each tuple creates a new
// assistant summary message, then flags the listed messages as archived (soft-removed).
// This operates within the current conversation turn supplied via context.
func (s *Service) remove(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*RemoveInput)
	if !ok {
		return fmt.Errorf("invalid input")
	}
	output, ok := out.(*RemoveOutput)
	if !ok {
		return fmt.Errorf("invalid output")
	}
	if s == nil || s.conv == nil {
		return fmt.Errorf("conversation client not initialised")
	}
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok || strings.TrimSpace(turn.ConversationID) == "" {
		return fmt.Errorf("missing conversation context")
	}
	operationHandoffs := map[int]string{}
	if scope := recovery.ProactiveScope(ctx); scope != nil {
		if scope.ConversationID != turn.ConversationID {
			return fmt.Errorf("proactive compaction conversation mismatch")
		}
		conv, err := s.conv.GetConversation(ctx, turn.ConversationID, apiconv.WithIncludeToolCall(true))
		if err != nil {
			return fmt.Errorf("validate proactive compaction scope: %w", err)
		}
		if conv == nil || conv.Id != scope.ConversationID {
			return fmt.Errorf("proactive compaction conversation unavailable")
		}
		eligible := recovery.EligibleMessages(conv)
		// Check every tuple before creating any summary or archiving any message.
		seen := map[string]bool{}
		if len(input.Tuples) == 0 {
			return fmt.Errorf("proactive compaction requires messages to archive")
		}
		for tupleIndex, tuple := range input.Tuples {
			type operation struct {
				ID     string `json:"operationId"`
				Tool   string `json:"tool"`
				Status string `json:"status"`
			}
			var operations []operation
			operationIDs := map[string]bool{}
			appendOperation := func(id, tool, status string) {
				if strings.TrimSpace(id) == "" || operationIDs[id] {
					return
				}
				operationIDs[id] = true
				operations = append(operations, operation{ID: id, Tool: tool, Status: status})
			}
			if len(tuple.MessageIds) == 0 {
				return fmt.Errorf("proactive compaction tuple has no eligible message IDs")
			}
			if tuple.Role != "" && !strings.EqualFold(strings.TrimSpace(tuple.Role), "assistant") {
				return fmt.Errorf("proactive compaction summary role must be assistant")
			}
			if strings.TrimSpace(tuple.Summary) == "" {
				return fmt.Errorf("proactive compaction requires a handoff summary")
			}
			for _, rawID := range tuple.MessageIds {
				id := strings.TrimSpace(rawID)
				if _, err := uuid.Parse(id); err != nil {
					return fmt.Errorf("invalid proactive compaction message ID")
				}
				if !scope.EligibleIDs[id] || eligible[id] == nil || seen[id] {
					return fmt.Errorf("message %q is outside the eligible proactive compaction scope", id)
				}
				seen[id] = true
				m := eligible[id]
				if tc := m.MessageToolCall; tc != nil {
					appendOperation(tc.OpId, tc.ToolName, tc.Status)
				}
				for _, tm := range m.ToolMessage {
					if tm != nil && tm.Id == m.Id && tm.ToolCall != nil {
						tc := tm.ToolCall
						appendOperation(tc.OpId, tc.ToolName, tc.Status)
					}
				}
			}
			if len(operations) > 0 {
				data, err := json.Marshal(operations)
				if err != nil {
					return fmt.Errorf("encode proactive operation handoff: %w", err)
				}
				operationHandoffs[tupleIndex] = "\n\nAuthoritative finished tool operations (these exact identities and statuses override conflicting summary text above; do not repeat successful operations):\n" + string(data)
			}
		}
	}
	// Identify and protect the last user message (do not remove)
	lastUserID := ""
	if conv, err := s.conv.GetConversation(ctx, turn.ConversationID, apiconv.WithIncludeToolCall(true)); err == nil && conv != nil {
		tr := conv.GetTranscript()
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
	}
	var created []string
	archived := 0
	for tupleIndex, tup := range input.Tuples {
		// validation before making any changes, llm can send invalid IDs (even source of them is valid)
		for _, id := range tup.MessageIds {
			id = strings.TrimSpace(id)
			_, err := uuid.Parse(id)
			if err != nil {
				return fmt.Errorf("invalid message ID in remove tuple: %v", id)
			}
		}

		sum := strings.TrimSpace(tup.Summary)
		sum += operationHandoffs[tupleIndex]
		if sum != "" {
			role := strings.TrimSpace(tup.Role)
			if role == "" {
				role = "assistant"
			}
			opts := []apiconv.MessageOption{
				apiconv.WithRole(role),
				apiconv.WithType("text"),
				apiconv.WithStatus("summary"),
				apiconv.WithContent(sum),
			}
			if recovery.IsProactive(ctx) {
				opts = append(opts, apiconv.WithContextSummary(recovery.ProactiveSummaryMarker))
			}
			if mm, err := apiconv.AddMessage(ctx, s.conv, &turn, opts...); err == nil && mm != nil {
				created = append(created, mm.Id)
			} else if err != nil {
				return err
			}
		}
		// Truncate summary for per-message field if needed
		sumForField := sum

		sumForField = textutil.RuneTruncate(sumForField, 500)

		for _, id := range tup.MessageIds {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if lastUserID != "" && id == lastUserID {
				continue
			}
			mm := apiconv.NewMessage()
			mm.SetId(id)
			if sumForField != "" {
				mm.SetSummary(sumForField)
			}
			mm.SetArchived(1)
			if err := s.conv.PatchMessage(ctx, mm); err != nil {
				return err
			}
			archived++
		}
	}
	output.CreatedSummaryMessageIds = created
	output.ArchivedMessages = archived
	return nil
}
