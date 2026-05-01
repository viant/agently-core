package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/internal/textutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	agmessagelist "github.com/viant/agently-core/pkg/agently/message/list"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
	"github.com/viant/agently-core/workspace"
)

func (s *Service) addMessage(ctx context.Context, turn *runtimerequestctx.TurnMeta, role, actor, content string, raw *string, mode, id string) (string, error) {
	if toolexec.IsChainMode(ctx) {
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
	if runMeta, ok := runtimerequestctx.RunMetaFromContext(ctx); ok && runMeta.Iteration > 0 {
		opts = append(opts, apiconv.WithIteration(runMeta.Iteration))
	}
	logx.Infof("conversation", "agent.addMessage start convo=%q turn_id=%q role=%q actor=%q mode=%q id=%q content_len=%d content_head=%q content_tail=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(role), strings.TrimSpace(actor), strings.TrimSpace(mode), strings.TrimSpace(id), len(content), textutil.Head(content, 512), textutil.Tail(content, 512))
	msg, err := apiconv.AddMessage(ctx, s.conversation, turn, opts...)
	if err != nil {
		logx.Errorf("conversation", "agent.addMessage error convo=%q turn_id=%q role=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(role), err)
		return "", fmt.Errorf("failed to add message: %w", err)
	}
	logx.Infof("conversation", "agent.addMessage ok convo=%q turn_id=%q role=%q message_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(role), strings.TrimSpace(msg.Id))
	return msg.Id, nil
}

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
	if root := resolveExistingWorkdir(workspace.Root()); root != "" {
		input.Context["workdir"] = root
		input.Context["resolvedWorkdir"] = root
		return root
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

func (s *Service) ensureEnvironment(ctx context.Context, input *QueryInput) error {
	if err := s.ensureConversation(ctx, input); err != nil {
		return err
	}
	// Capability-question forced mode removed: the workspace intake LLM
	// router (agent_classifier.classifyAgentIDWithLLM) is the single decider
	// for whether a turn routes to an agent or answers a capability question
	// directly. No heuristic markers, no zero-LLM shortcuts. The router's
	// unified output schema covers {action: "route" | "answer" | "clarify"}.
	if err := s.ensureAgent(ctx, input); err != nil {
		return err
	}
	if input.EmbeddingModel == "" {
		input.EmbeddingModel = s.defaults.Embedder
	}
	return nil
}

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

func shouldSkipFinalAssistantPersist(ctx context.Context, client apiconv.Client, turn *runtimerequestctx.TurnMeta, content string) bool {
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

func isTurnTaskMessage(role, msgType, mode string, interim int) bool {
	if interim != 0 {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(role), "user") {
		return false
	}
	typeLower := strings.ToLower(strings.TrimSpace(msgType))
	modeLower := strings.ToLower(strings.TrimSpace(mode))
	return typeLower == "task" || modeLower == "task"
}

func (s *Service) latestTurnTaskCheckpoint(ctx context.Context, turn runtimerequestctx.TurnMeta) (turnTaskCheckpoint, error) {
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
			Has: &agmessagelist.MessageRowsInputHas{
				ConversationId: true,
				TurnId:         true,
				Roles:          true,
			},
		}, nil)
		if err != nil {
			return checkpoint, err
		}
		for _, row := range page.Rows {
			if row == nil {
				continue
			}
			if !isTurnTaskMessage(row.Role, row.Type, valueOrEmpty(row.Mode), row.Interim) {
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
			if msg == nil {
				continue
			}
			if !isTurnTaskMessage(msg.Role, msg.Type, valueOrEmpty(msg.Mode), msg.Interim) {
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

func (s *Service) hasNewTurnTaskSince(ctx context.Context, turn runtimerequestctx.TurnMeta, checkpoint turnTaskCheckpoint) (bool, error) {
	latest, err := s.latestTurnTaskCheckpoint(ctx, turn)
	if err != nil {
		return false, err
	}
	if !latest.Found {
		logx.DebugCtxf(ctx, "conversation", "steer.check convo=%q turn_id=%q checkpoint_found=%t latest_found=false", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), checkpoint.Found)
		return false, nil
	}
	if !checkpoint.Found {
		logx.Infof("conversation", "steer.check convo=%q turn_id=%q checkpoint_found=false latest_message_id=%q latest_at=%s pending=true", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), latest.MessageID, latest.CreatedAt.Format(time.RFC3339Nano))
		return true, nil
	}
	pending := compareTurnTaskCheckpoint(latest, checkpoint) > 0
	logx.Infof("conversation", "steer.check convo=%q turn_id=%q checkpoint_message_id=%q checkpoint_at=%s latest_message_id=%q latest_at=%s pending=%t", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), checkpoint.MessageID, checkpoint.CreatedAt.Format(time.RFC3339Nano), latest.MessageID, latest.CreatedAt.Format(time.RFC3339Nano), pending)
	return pending, nil
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
