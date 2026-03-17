package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/runtime/memory"
)

func (s *Service) BuildHistory(ctx context.Context, transcript apiconv.Transcript, binding *prompt.Binding) error {
	hist, err := s.buildHistory(ctx, transcript)
	if err != nil {
		return err
	}
	binding.History = hist
	return nil
}

func (s *Service) buildTaskBinding(input *QueryInput) prompt.Task {
	task := input.Query
	if directive := runtimeDelegationDirective(input); directive != "" {
		task = directive + "\n\nUser request:\n" + strings.TrimSpace(task)
	}
	return prompt.Task{Prompt: task, Attachments: input.Attachments}
}

func runtimeDelegationDirective(input *QueryInput) string {
	if input == nil || input.Agent == nil {
		return ""
	}
	agentID := strings.TrimSpace(input.Agent.ID)
	if agentID != "coder" {
		return ""
	}
	if input.Agent.Delegation == nil || !input.Agent.Delegation.Enabled {
		return ""
	}
	maxDepth := input.Agent.Delegation.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}
	currentDepth := delegationDepthFromContextMap(input.Context, agentID)
	if currentDepth >= maxDepth || currentDepth > 0 {
		return ""
	}
	query := strings.TrimSpace(input.Query)
	if !looksLikeRepoAnalysisRequest(query) {
		return ""
	}
	workdir := resolveDelegationWorkdir(input)
	if workdir == "" {
		return ""
	}
	objective := fmt.Sprintf(
		"Inspect the repository at %s, explore its structure and key modules, and return a focused summary or findings for the parent to relay.",
		workdir,
	)
	return fmt.Sprintf(
		"Runtime directive: this is a top-level repository-analysis request. Before any local repo exploration, call `llm/agents:run` exactly once with `agentId: \"coder\"`, `context.workdir: %q`, and an objective equivalent to %q. Only `orchestration:updatePlan` may come before that delegated call. After the child returns, validate and relay its result in the parent response.",
		workdir,
		objective,
	)
}

func looksLikeRepoAnalysisRequest(query string) bool {
	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return false
	}
	hasAnalysisVerb := strings.Contains(lower, "analyze") ||
		strings.Contains(lower, "analyse") ||
		strings.Contains(lower, "review") ||
		strings.Contains(lower, "inspect") ||
		strings.Contains(lower, "scan") ||
		strings.Contains(lower, "summarize") ||
		strings.Contains(lower, "summarise") ||
		strings.Contains(lower, "explain")
	if !hasAnalysisVerb {
		return false
	}
	hasRepoNoun := strings.Contains(lower, "repo") ||
		strings.Contains(lower, "repository") ||
		strings.Contains(lower, "project") ||
		strings.Contains(lower, "codebase") ||
		strings.Contains(lower, "directory")
	if hasRepoNoun {
		return true
	}
	for _, candidate := range extractPathCandidates(query) {
		if resolveExistingWorkdir(candidate) != "" {
			return true
		}
	}
	return false
}

func resolveDelegationWorkdir(input *QueryInput) string {
	if input == nil {
		return ""
	}
	if existing := normalizeWorkdirValue(input.Context["workdir"]); existing != "" {
		return existing
	}
	if existing := normalizeWorkdirValue(input.Context["resolvedWorkdir"]); existing != "" {
		return existing
	}
	for _, candidate := range extractPathCandidates(input.Query) {
		if resolved := resolveExistingWorkdir(candidate); resolved != "" {
			return resolved
		}
	}
	return ""
}

// buildHistory derives history from a provided conversation transcript.
// It maps transcript turns and messages into prompt history without
// applying any overflow preview logic.
func (s *Service) buildHistory(ctx context.Context, transcript apiconv.Transcript) (prompt.History, error) {
	result, err := s.buildChronologicalHistory(ctx, transcript, nil, false)
	if err != nil {
		return prompt.History{}, err
	}
	return result.History, nil
}

// HistoryResult holds the combined result of building prompt history with
// overflow preview and elicitation extraction.
type HistoryResult struct {
	History          prompt.History
	Elicitation      []*prompt.Message
	Overflow         bool
	MaxOverflowBytes int
}

type normalizedMsg struct {
	turnIdx int
	msg     *apiconv.Message
}

// buildHistoryWithLimit maps transcript into prompt history applying overflow
// preview to user/assistant text messages and collecting current-turn
// elicitation messages separately.
func (s *Service) buildHistoryWithLimit(ctx context.Context, transcript apiconv.Transcript, input *QueryInput) (*HistoryResult, error) {
	// When no preview limit is configured, fall back to default mapping.
	if s.defaults == nil || s.defaults.PreviewSettings.Limit <= 0 {
		h, err := s.buildHistory(ctx, transcript)
		if err != nil {
			return nil, err
		}
		return &HistoryResult{History: h}, nil
	}
	return s.buildChronologicalHistory(ctx, transcript, input, true)
}

// buildChronologicalHistory constructs prompt history turns from the provided
// transcript. When applyPreview is true, it applies overflow preview to
// user/assistant text messages using the service's effective preview limit.
// It also extracts current-turn elicitation messages as a separate slice.
func (s *Service) buildChronologicalHistory(
	ctx context.Context,
	transcript apiconv.Transcript,
	input *QueryInput,
	applyPreview bool,
) (*HistoryResult, error) {
	var out prompt.History
	var elicitation []*prompt.Message
	// Empty transcript yields empty history.
	if len(transcript) == 0 {
		return &HistoryResult{History: out}, nil
	}

	// Skip queued turns so future user prompts do not get merged into a single
	// LLM request when the chat queue is used. Always keep the current turn
	// (by TurnMeta) even if it is still marked queued due to eventual
	// consistency or ordering.
	currentTurnID := ""
	if tm, ok := memory.TurnMetaFromContext(ctx); ok {
		currentTurnID = strings.TrimSpace(tm.TurnID)
	}

	lastAssistantMessage := transcript.LastAssistantMessage()
	lastElicitationMessage := transcript.LastElicitationMessage()
	currentElicitation := false
	if lastElicitationMessage != nil && lastAssistantMessage != nil {
		if lastElicitationMessage.Id == lastAssistantMessage.Id {
			currentElicitation = true
		}
		if lastElicitationMessage.CreatedAt.After(lastAssistantMessage.CreatedAt) {
			currentElicitation = true
		}
	}

	// Determine whether continuation preview format is enabled for the selected model.
	allowContinuation := s.allowContinuationPreview(ctx, input)

	normalized, elicitation := s.collectNormalizedMessages(ctx, transcript, applyPreview, currentTurnID, currentElicitation, lastElicitationMessage)

	// Dedupe current-turn user task messages that are effectively the
	// same instruction expressed twice (e.g., raw input and a later
	// Task: wrapper). The transcript remains unchanged for UI/summary;
	// this is only a prompt-history optimization for the LLM.
	if len(normalized) > 0 {
		if tm, ok := memory.TurnMetaFromContext(ctx); ok && strings.TrimSpace(tm.TurnID) != "" {
			// Find the index of the current turn in the transcript
			currentTurnIdx := -1
			for ti, turn := range transcript {
				if turn == nil {
					continue
				}
				if strings.TrimSpace(turn.Id) == strings.TrimSpace(tm.TurnID) {
					currentTurnIdx = ti
					break
				}
			}
			if currentTurnIdx != -1 {
				// Collect indices of user messages in the current turn
				userIdxs := []int{}
				for i, item := range normalized {
					if item.turnIdx != currentTurnIdx || item.msg == nil {
						continue
					}
					role := strings.ToLower(strings.TrimSpace(item.msg.Role))
					if role != "user" {
						continue
					}
					userIdxs = append(userIdxs, i)
				}
				// If we have multiple user messages in the current turn
				// with the same normalized content (ignoring a leading
				// "Task:" wrapper), keep only the last one.
				if len(userIdxs) > 1 {
					last := userIdxs[len(userIdxs)-1]
					lastMsg := normalized[last].msg
					lastText := normalizeUserTaskContent(lastMsg.GetContent())
					if lastText != "" {
						filtered := make([]normalizedMsg, 0, len(normalized))
						for i, item := range normalized {
							// Drop earlier user messages in the current
							// turn that normalize to the same content.
							if i < last && item.turnIdx == currentTurnIdx && item.msg != nil {
								role := strings.ToLower(strings.TrimSpace(item.msg.Role))
								if role == "user" {
									if normalizeUserTaskContent(item.msg.GetContent()) == lastText {
										continue
									}
								}
							}
							filtered = append(filtered, item)
						}
						normalized = filtered
					}
				}
			}
		}
	}
	overflow := false
	maxOverflowBytes := 0
	turns := make([]*prompt.Turn, len(transcript))
	totalTurns := len(transcript)
	lastUserByTurn := map[string]*prompt.Message{}
	pendingUserAttachmentsByTurn := map[string][]*prompt.Attachment{}
	promptByMessageID := map[string]*prompt.Message{}
	pendingAttachmentsByMessageID := map[string][]*prompt.Attachment{}
	payloadAttachmentCache := map[string]*prompt.Attachment{}

	// Second pass: map normalized messages into prompt turns with optional preview.
	for _, item := range normalized {
		msg := item.msg
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		orig := ""
		if messageToolCall(msg) != nil {
			orig = strings.TrimSpace(msg.GetContent())
		} else if msg.Content != nil {
			orig = *msg.Content
		}
		text := orig
		if applyPreview && orig != "" {
			limit := s.turnPreviewLimit(item.turnIdx, totalTurns, true)
			if limit > 0 {
				preview, of := buildOverflowPreview(orig, limit, msg.Id, allowContinuation)
				if of {
					overflow = true
					if size := len(orig); size > maxOverflowBytes {
						maxOverflowBytes = size
					}
				}
				text = preview
			}
		}

		attachments, err := s.attachmentsFromMessage(ctx, msg, payloadAttachmentCache)
		if err != nil {
			return nil, err
		}

		turnIdx := item.turnIdx
		pt := turns[turnIdx]
		if pt == nil {
			pt = &prompt.Turn{ID: transcript[turnIdx].Id}
			turns[turnIdx] = pt
		}

		// Attachments are persisted as *control* child messages (QueryInput
		// attachments and tool-produced images). LLM providers need multimodal
		// content on user/system/assistant messages, so we merge carrier
		// attachments into the referenced parent message instead of emitting the
		// carrier itself.
		if isAttachmentCarrier(msg) && len(attachments) > 0 {
			parentID := ""
			if msg.ParentMessageId != nil {
				parentID = strings.TrimSpace(*msg.ParentMessageId)
			}
			if parentID != "" {
				if parent := promptByMessageID[parentID]; parent != nil {
					parent.Attachment = append(parent.Attachment, attachments...)
					debugAttachmentf("merged %d attachment(s) from carrier=%s into parent=%s", len(attachments), strings.TrimSpace(msg.Id), parentID)
				} else {
					pendingAttachmentsByMessageID[parentID] = append(pendingAttachmentsByMessageID[parentID], attachments...)
					debugAttachmentf("queued %d attachment(s) from carrier=%s for parent=%s", len(attachments), strings.TrimSpace(msg.Id), parentID)
				}
				continue
			}

			turnID := strings.TrimSpace(pt.ID)
			if turnID != "" {
				if last := lastUserByTurn[turnID]; last != nil {
					last.Attachment = append(last.Attachment, attachments...)
					debugAttachmentf("merged %d attachment(s) from carrier=%s into last user message=%s (turn=%s)", len(attachments), strings.TrimSpace(msg.Id), strings.TrimSpace(last.ID), turnID)
					continue
				}
				pendingUserAttachmentsByTurn[turnID] = append(pendingUserAttachmentsByTurn[turnID], attachments...)
				debugAttachmentf("queued %d attachment(s) from carrier=%s for turn=%s", len(attachments), strings.TrimSpace(msg.Id), turnID)
				continue
			}
			// Fallback: if turn id is missing, append to task-scoped attachments
			// so the binaries still reach the model with the user prompt.
			if input != nil {
				input.Attachments = append(input.Attachments, attachments...)
			}
			continue
		}

		pmsgRole := role
		// Normalize tool-call messages to assistant role for history so
		// they are rendered as assistant context rather than tool role
		// messages, which require a preceding tool_calls message.
		if messageToolCall(msg) != nil {
			pmsgRole = "assistant"
		}

		pmsg := &prompt.Message{
			Role:       pmsgRole,
			Content:    text,
			Attachment: attachments,
			CreatedAt:  msg.CreatedAt,
			ID:         msg.Id,
		}
		msgID := strings.TrimSpace(msg.Id)
		if msgID != "" {
			promptByMessageID[msgID] = pmsg
			if pending := pendingAttachmentsByMessageID[msgID]; len(pending) > 0 {
				pmsg.Attachment = append(pmsg.Attachment, pending...)
				delete(pendingAttachmentsByMessageID, msgID)
			}
		}

		// Classify message kind and, when applicable, attach tool metadata.
		if tc := messageToolCall(msg); tc != nil {
			pmsg.Kind = prompt.MessageKindToolResult
			pmsg.ToolOpID = tc.OpId
			pmsg.ToolName = tc.ToolName
			pmsg.ToolArgs = msg.ToolCallArguments()
			if tc.TraceId != nil {
				pmsg.ToolTraceID = strings.TrimSpace(*tc.TraceId)
			}
			debugf("agent.buildHistory tool_result msg=%q tool=%q len=%d gzip=%t", strings.TrimSpace(msg.Id), strings.TrimSpace(tc.ToolName), len(pmsg.Content), strings.HasPrefix(pmsg.Content, "\x1f\x8b"))
		} else {
			// Classify chat and elicitation messages. For past elicitation
			// flows, ElicitationId will be set on assistant/user messages;
			// current-turn elicitation has already been extracted into the
			// separate elicitation slice and will not reach this block.
			if msg.ElicitationId != nil && strings.TrimSpace(*msg.ElicitationId) != "" {
				if role == "assistant" {
					pmsg.Kind = prompt.MessageKindElicitPrompt
				} else if role == "user" {
					pmsg.Kind = prompt.MessageKindElicitAnswer
				}
			} else {
				if role == "user" {
					pmsg.Kind = prompt.MessageKindChatUser
				} else if role == "assistant" {
					pmsg.Kind = prompt.MessageKindChatAssistant
				}
			}
		}
		pt.Messages = append(pt.Messages, pmsg)
		if pt.StartedAt.IsZero() || msg.CreatedAt.Before(pt.StartedAt) {
			pt.StartedAt = msg.CreatedAt
		}

		// Track the last user message per turn so deferred tool attachments
		// can be applied once a user message exists.
		if strings.EqualFold(strings.TrimSpace(pmsg.Role), "user") {
			turnID := strings.TrimSpace(pt.ID)
			if turnID != "" {
				lastUserByTurn[turnID] = pmsg
				if pending := pendingUserAttachmentsByTurn[turnID]; len(pending) > 0 {
					pmsg.Attachment = append(pmsg.Attachment, pending...)
					delete(pendingUserAttachmentsByTurn, turnID)
				}
			}
		}

		// Archive error messages once processed when applyPreview is enabled.
		if applyPreview && msg.Status != nil && strings.EqualFold(strings.TrimSpace(*msg.Status), "error") {
			if !msg.IsArchived() {
				if mm := msg.NewMutable(); mm != nil {
					archived := 1
					mm.Archived = &archived
					mm.Has.Archived = true
					if err := s.conversation.PatchMessage(ctx, (*apiconv.MutableMessage)(mm)); err != nil {
						return nil, fmt.Errorf("failed to archive error message %q: %w", msg.Id, err)
					}
				}
			}
		}
	}

	// If a turn had only control attachment messages (no parent message present
	// due to filtering), fall back to task-scoped attachments so they still
	// reach the model.
	if input != nil {
		for _, pending := range pendingUserAttachmentsByTurn {
			if len(pending) == 0 {
				continue
			}
			input.Attachments = append(input.Attachments, pending...)
		}
		for _, pending := range pendingAttachmentsByMessageID {
			if len(pending) == 0 {
				continue
			}
			input.Attachments = append(input.Attachments, pending...)
		}
	}

	// Finalize turns: drop nils, sort messages by CreatedAt, and build the
	// legacy flat Messages view for persisted history.
	for _, t := range turns {
		if t == nil || len(t.Messages) == 0 {
			continue
		}
		sort.SliceStable(t.Messages, func(i, j int) bool {
			return t.Messages[i].CreatedAt.Before(t.Messages[j].CreatedAt)
		})
		out.Past = append(out.Past, t)
		out.Messages = append(out.Messages, t.Messages...)
	}

	return &HistoryResult{History: out, Elicitation: elicitation, Overflow: overflow, MaxOverflowBytes: maxOverflowBytes}, nil
}

func (s *Service) collectNormalizedMessages(
	ctx context.Context,
	transcript apiconv.Transcript,
	applyPreview bool,
	currentTurnID string,
	currentElicitation bool,
	lastElicitationMessage *apiconv.Message,
) ([]normalizedMsg, []*prompt.Message) {
	var normalized []normalizedMsg
	var elicitation []*prompt.Message
	for ti, turn := range transcript {
		if turn == nil || turn.Message == nil {
			continue
		}
		turnStatus := strings.ToLower(strings.TrimSpace(turn.Status))
		if turnStatus == "queued" && strings.TrimSpace(turn.Id) != currentTurnID {
			continue
		}
		if turnStatus == "canceled" && strings.TrimSpace(turn.Id) != currentTurnID {
			continue
		}
		messages := turn.GetMessages()
		for _, m := range messages {
			if m == nil {
				continue
			}
			toolMsgs := s.syntheticToolMessages(ctx, m)
			baseMsg := cloneMessageWithoutToolMessages(m)
			if baseMsg == nil {
				baseMsg = m
			}
			if baseMsg.Mode != nil && strings.EqualFold(strings.TrimSpace(*baseMsg.Mode), "chain") {
				continue
			}
			if applyPreview && baseMsg.Status != nil && strings.EqualFold(strings.TrimSpace(*baseMsg.Status), "error") {
				if baseMsg.IsArchived() {
					continue
				}
				normalized = append(normalized, normalizedMsg{turnIdx: ti, msg: baseMsg})
				continue
			}
			if baseMsg.IsArchived() || baseMsg.IsInterim() {
				// Even when the base message is skipped (e.g., interim assistant
				// preamble), its tool_op children must enter the history so the
				// model can see prior tool results and continuation can work.
				for _, toolMsg := range toolMsgs {
					if body := strings.TrimSpace(toolMsg.GetContent()); body != "" {
						normalized = append(normalized, normalizedMsg{turnIdx: ti, msg: toolMsg})
					}
				}
				continue
			}
			if baseMsg.Status != nil {
				ms := strings.ToLower(strings.TrimSpace(*baseMsg.Status))
				if ms == "cancel" || ms == "canceled" {
					continue
				}
			}
			if isAttachmentCarrier(baseMsg) {
				normalized = append(normalized, normalizedMsg{turnIdx: ti, msg: baseMsg})
				for _, toolMsg := range toolMsgs {
					normalized = append(normalized, normalizedMsg{turnIdx: ti, msg: toolMsg})
				}
				continue
			}

			if baseMsg.Content != nil && *baseMsg.Content != "" {
				mtype := strings.ToLower(strings.TrimSpace(baseMsg.Type))
				isElicitationType := mtype == "elicitation_request" || mtype == "elicitation_response"
				// Steer/follow-up inputs are persisted as user task messages on the
				// active turn. They must enter prompt history for the next iteration,
				// otherwise the loop can detect late steer but the model never sees it.
				if mtype == "text" || mtype == "task" || isElicitationType {
					role := strings.ToLower(strings.TrimSpace(baseMsg.Role))
					if currentElicitation && lastElicitationMessage != nil && (role == "user" || role == "assistant") {
						if baseMsg.Id == lastElicitationMessage.Id && baseMsg.Content != nil {
							kind := prompt.MessageKindElicitAnswer
							if role == "assistant" {
								kind = prompt.MessageKindElicitPrompt
							}
							elicitation = append(elicitation, &prompt.Message{Kind: kind, Role: baseMsg.Role, Content: *baseMsg.Content, CreatedAt: baseMsg.CreatedAt})
						} else if lastElicitationMessage.CreatedAt.Before(baseMsg.CreatedAt) && baseMsg.Content != nil {
							kind := prompt.MessageKindElicitAnswer
							if role == "assistant" {
								kind = prompt.MessageKindElicitPrompt
							}
							elicitation = append(elicitation, &prompt.Message{Kind: kind, Role: baseMsg.Role, Content: *baseMsg.Content, CreatedAt: baseMsg.CreatedAt})
						} else if role == "user" || role == "assistant" {
							normalized = append(normalized, normalizedMsg{turnIdx: ti, msg: baseMsg})
						}
					} else if role == "user" || role == "assistant" {
						normalized = append(normalized, normalizedMsg{turnIdx: ti, msg: baseMsg})
					}
				}
			}

			for _, toolMsg := range toolMsgs {
				if body := strings.TrimSpace(toolMsg.GetContent()); body != "" {
					normalized = append(normalized, normalizedMsg{turnIdx: ti, msg: toolMsg})
				} else if DebugEnabled() {
					opID := ""
					toolName := ""
					if tc := messageToolCall(toolMsg); tc != nil {
						opID = strings.TrimSpace(tc.OpId)
						toolName = strings.TrimSpace(tc.ToolName)
					}
					warnf("agent.collectNormalizedMessages dropped tool_result with empty body op_id=%q tool=%q turn=%q msg_id=%q", opID, toolName, strings.TrimSpace(turn.Id), strings.TrimSpace(toolMsg.Id))
				}
			}
		}
	}
	return normalized, elicitation
}

// appendCurrentMessages appends messages to History.Current ensuring
// CreatedAt is set and non-decreasing within the current turn. It does
// not modify Past timestamps.
func appendCurrentMessages(h *prompt.History, msgs ...*prompt.Message) {
	if h == nil || len(msgs) == 0 {
		return
	}
	if h.Current == nil {
		h.Current = &prompt.Turn{ID: h.CurrentTurnID}
	}
	for _, m := range msgs {
		if m == nil {
			continue
		}
		// Seed CreatedAt when zero.
		if m.CreatedAt.IsZero() {
			m.CreatedAt = time.Now().UTC()
		}
		// Ensure non-decreasing CreatedAt relative to the last current message.
		if n := len(h.Current.Messages); n > 0 {
			last := h.Current.Messages[n-1].CreatedAt
			if m.CreatedAt.Before(last) {
				m.CreatedAt = last.Add(time.Nanosecond)
			}
		}
		h.Current.Messages = append(h.Current.Messages, m)
	}
}

func messageToolCall(msg *apiconv.Message) *apiconv.ToolCallView {
	if msg == nil {
		return nil
	}
	for _, tm := range msg.ToolMessage {
		if tm != nil && tm.ToolCall != nil {
			return tm.ToolCall
		}
	}
	return nil
}

// turnPreviewLimit returns the preview limit for a given turn index,
// applying aging only to older turns. The newest
// PreviewSettings.AgedAfterSteps turns use Limit, older ones (when
// AgedLimit > 0) use AgedLimit. Aging is never applied to the
// synthetic Current turn.
func (s *Service) turnPreviewLimit(turnIdx, totalTurns int, applyAging bool) int {
	if s.defaults == nil || s.defaults.PreviewSettings.Limit <= 0 {
		return 0
	}
	limit := s.defaults.PreviewSettings.Limit
	if !applyAging || s.defaults.PreviewSettings.AgedAfterSteps <= 0 || s.defaults.PreviewSettings.AgedLimit <= 0 {
		return limit
	}
	w := s.defaults.PreviewSettings.AgedAfterSteps
	if totalTurns <= w {
		return limit
	}
	// Turns with index < totalTurns-w are considered aged.
	if turnIdx < totalTurns-w {
		return s.defaults.PreviewSettings.AgedLimit
	}
	return limit
}
