package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
)

type proactiveKey struct{}
type fullHistoryKey struct{}

const ProactiveSummaryMarker = "agently:proactive-compaction"
const FullHistoryResponseMarker = "agently:compacted-full-history"

// NeedsFullHistory survives a request failure or process restart. A successful
// full-history call must have started after the latest persisted handoff.
func NeedsFullHistory(conv *apiconv.Conversation) bool {
	if conv == nil {
		return false
	}
	var summaryAt, responseStartedAt time.Time
	for _, turn := range conv.GetTranscript() {
		if turn == nil {
			continue
		}
		for _, m := range turn.Message {
			if m == nil || m.ContextSummary == nil {
				continue
			}
			if *m.ContextSummary == ProactiveSummaryMarker && m.CreatedAt.After(summaryAt) {
				summaryAt = m.CreatedAt
			}
			if *m.ContextSummary == FullHistoryResponseMarker && m.ModelCall != nil && m.ModelCall.Status == "completed" && m.ModelCall.StartedAt != nil && m.ModelCall.TraceId != nil && strings.TrimSpace(*m.ModelCall.TraceId) != "" && m.ModelCall.StartedAt.After(responseStartedAt) {
				responseStartedAt = *m.ModelCall.StartedAt
			}
		}
	}
	return !summaryAt.IsZero() && !responseStartedAt.After(summaryAt)
}

// CompactionScope is an authoritative snapshot of original messages that may
// be archived. The removal service rechecks it against the current transcript.
type CompactionScope struct {
	ConversationID string
	EligibleIDs    map[string]bool
}

func WithProactive(ctx context.Context, scope *CompactionScope) context.Context {
	return context.WithValue(ctx, proactiveKey{}, scope)
}
func ProactiveScope(ctx context.Context) *CompactionScope {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(proactiveKey{}).(*CompactionScope)
	return value
}
func IsProactive(ctx context.Context) bool { return ProactiveScope(ctx) != nil }
func WithFullHistory(ctx context.Context, required bool) context.Context {
	return context.WithValue(ctx, fullHistoryKey{}, required)
}
func FullHistoryRequired(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(fullHistoryKey{}).(bool)
	return value
}

func terminalTool(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded", "completed", "done", "failed", "error", "canceled", "cancelled", "denied":
		return true
	}
	return false
}

// EligibleMessages protects the latest user, summaries/recovery artifacts and
// every unfinished tool operation. Tool messages project atomic call/output pairs.
func EligibleMessages(conv *apiconv.Conversation) map[string]*conversationmodel.MessageView {
	result := map[string]*conversationmodel.MessageView{}
	if conv == nil {
		return result
	}
	transcript := conv.GetTranscript()
	latestUser := ""
	for _, turn := range transcript {
		if turn == nil {
			continue
		}
		for _, m := range turn.Message {
			if m != nil && m.Interim == 0 && (m.Archived == nil || *m.Archived == 0) && strings.EqualFold(m.Role, "user") {
				latestUser = m.Id
			}
		}
	}
	for _, turn := range transcript {
		if turn == nil {
			continue
		}
		for _, m := range turn.Message {
			if m == nil || m.Id == "" || m.Id == latestUser || m.ConversationId != conv.Id || m.Interim != 0 || (m.Archived != nil && *m.Archived != 0) {
				continue
			}
			status := ""
			if m.Status != nil {
				status = strings.ToLower(strings.TrimSpace(*m.Status))
			}
			if status == "summary" || status == "error" || status == "pending" || status == "running" || status == "queued" || status == "in_progress" {
				continue
			}
			if m.Mode != nil {
				switch strings.ToLower(strings.TrimSpace(*m.Mode)) {
				case "recovery", "chain", "router":
					continue
				}
			}
			protected := false
			if tc := m.MessageToolCall; tc != nil {
				protected = !terminalTool(tc.Status) || isRecoveryTool(tc.ToolName)
			}
			for _, tm := range m.ToolMessage {
				if tm != nil && tm.ToolCall != nil && (!terminalTool(tm.ToolCall.Status) || isRecoveryTool(tm.ToolCall.ToolName)) {
					protected = true
				}
			}
			if protected {
				continue
			}
			if strings.EqualFold(m.Type, "tool_op") && m.MessageToolCall == nil && len(m.ToolMessage) == 0 {
				continue
			}
			if strings.EqualFold(m.Role, "user") || strings.EqualFold(m.Role, "assistant") || strings.EqualFold(m.Role, "tool") {
				result[m.Id] = m
			}
		}
	}
	return result
}

func isRecoveryTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "message-remove" || name == "message_remove" || name == "message/remove" || strings.HasSuffix(name, "message:remove")
}

// EligibleSignature excludes immutable prompts and generated summaries. Only
// changed original history rearms compaction after a previous attempt.
func EligibleSignature(messages map[string]*conversationmodel.MessageView) string {
	if len(messages) == 0 {
		return ""
	}
	ids := make([]string, 0, len(messages))
	for id := range messages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	hash := sha256.New()
	for _, id := range ids {
		m := messages[id]
		body, _ := json.Marshal(struct {
			ID      string
			Content *string
			Call    interface{}
			Tools   interface{}
		}{id, m.Content, m.MessageToolCall, m.ToolMessage})
		hash.Write(body)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
