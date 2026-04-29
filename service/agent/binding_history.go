package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/internal/logx"
	"sort"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/protocol/binding"
	runtimeprojection "github.com/viant/agently-core/runtime/projection"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

func (s *Service) BuildHistory(ctx context.Context, transcript apiconv.Transcript, binding *binding.Binding) error {
	hist, err := s.buildHistory(ctx, transcript)
	if err != nil {
		return err
	}
	binding.History = hist
	return nil
}

func (s *Service) buildTaskBinding(input *QueryInput) binding.Task {
	return binding.Task{Prompt: input.Query, Attachments: input.Attachments}
}

// buildHistory derives history from a provided conversation transcript.
// It maps transcript turns and messages into prompt history without
// applying any overflow preview logic.
func (s *Service) buildHistory(ctx context.Context, transcript apiconv.Transcript) (binding.History, error) {
	result, err := s.buildChronologicalHistory(ctx, transcript, nil, false)
	if err != nil {
		return binding.History{}, err
	}
	return result.History, nil
}

// HistoryResult holds the combined result of building prompt history with
// overflow preview and elicitation extraction.
type HistoryResult struct {
	History          binding.History
	Elicitation      []*binding.Message
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
		return s.buildChronologicalHistory(ctx, transcript, input, false)
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
	var out binding.History
	var elicitation []*binding.Message
	// Empty transcript yields empty history.
	if len(transcript) == 0 {
		return &HistoryResult{History: out}, nil
	}

	// Skip queued turns so future user prompts do not get merged into a single
	// LLM request when the chat queue is used. Always keep the current turn
	// (by TurnMeta) even if it is still marked queued due to eventual
	// consistency or ordering.
	currentTurnID := ""
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
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

	projection, _ := runtimeprojection.SnapshotFromContext(ctx)
	scope := string(resolveToolCallExposure(input))
	if state, ok := runtimeprojection.StateFromContext(ctx); ok {
		state.SetScope(scope)
		s.applyRelevanceProjection(ctx, transcript, input, currentTurnID, scope)
		projection = state.Snapshot()
		if expanded := expandHiddenTurnMessageIDs(transcript, projection.HiddenTurnIDs); len(expanded) > 0 {
			state.HideMessages(expanded...)
		}
		projection = state.Snapshot()
	} else {
		projection.Scope = scope
		projection.HiddenMessageIDs = appendUniqueProjectionIDs(projection.HiddenMessageIDs, expandHiddenTurnMessageIDs(transcript, projection.HiddenTurnIDs)...)
	}
	normalized, elicitation := s.collectNormalizedMessages(ctx, transcript, applyPreview, currentTurnID, currentElicitation, lastElicitationMessage, projection)

	// Dedupe current-turn user task messages that are effectively the
	// same instruction expressed twice (e.g., raw input and a later
	// Task: wrapper). The transcript remains unchanged for UI/summary;
	// this is only a prompt-history optimization for the LLM.
	if len(normalized) > 0 {
		if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok && strings.TrimSpace(tm.TurnID) != "" {
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
	// Apply cacheable tool-call supersession by updating projection state, then
	// filtering normalized messages through the projection snapshot.
	if s.defaults != nil && s.registry != nil && strings.EqualFold(strings.TrimSpace(projection.Scope), "conversation") {
		currentIdx := -1
		if currentTurnID != "" {
			for ti, turn := range transcript {
				if turn != nil && strings.TrimSpace(turn.Id) == currentTurnID {
					currentIdx = ti
					break
				}
			}
		}
		hidden, freed := collectToolCallSupersessionHiddenMessageIDs(normalized, currentIdx, s.registry, &s.defaults.Projection)
		if len(hidden) > 0 {
			if state, ok := runtimeprojection.StateFromContext(ctx); ok {
				state.HideMessages(hidden...)
				state.AddReason("tool call supersession")
				state.AddTokensFreed(freed)
				projection = state.Snapshot()
			} else {
				projection.HiddenMessageIDs = appendUniqueProjectionIDs(projection.HiddenMessageIDs, hidden...)
				if projection.Reason == "" {
					projection.Reason = "tool call supersession"
				} else if !strings.Contains(projection.Reason, "tool call supersession") {
					projection.Reason += "; tool call supersession"
				}
				projection.TokensFreed += freed
			}
			normalized = applyProjectionToNormalized(normalized, projection)
		}
	}

	overflow := false
	maxOverflowBytes := 0
	turns := make([]*binding.Turn, len(transcript))
	totalTurns := len(transcript)
	lastUserByTurn := map[string]*binding.Message{}
	pendingUserAttachmentsByTurn := map[string][]*binding.Attachment{}
	promptByMessageID := map[string]*binding.Message{}
	pendingAttachmentsByMessageID := map[string][]*binding.Attachment{}
	payloadAttachmentCache := map[string]*binding.Attachment{}

	// Second pass: map normalized messages into prompt turns with optional preview.
	for _, item := range normalized {
		msg := item.msg
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		orig := ""
		if isConcreteToolResultMessage(msg) {
			orig = strings.TrimSpace(msg.GetContentPreferContent())
		} else if msg.Content != nil {
			orig = *msg.Content
		}
		text := orig
		if applyPreview && orig != "" {
			limit := s.messagePreviewLimit(item.turnIdx, totalTurns, true, isConcreteToolResultMessage(msg))
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
			pt = &binding.Turn{ID: transcript[turnIdx].Id}
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
			// so the binaries still reach the model with the user binding.
			if input != nil {
				input.Attachments = append(input.Attachments, attachments...)
			}
			continue
		}

		pmsgRole := role
		// Normalize tool-call messages to assistant role for history so
		// they are rendered as assistant context rather than tool role
		// messages, which require a preceding tool_calls message.
		if isConcreteToolResultMessage(msg) {
			pmsgRole = "assistant"
		}

		pmsg := &binding.Message{
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
		if tc := messageToolCall(msg); tc != nil && isConcreteToolResultMessage(msg) {
			pmsg.Kind = binding.MessageKindToolResult
			pmsg.ToolOpID = tc.OpId
			pmsg.ToolName = tc.ToolName
			pmsg.ToolArgs = msg.ToolCallArguments()
			if tc.TraceId != nil {
				pmsg.ToolTraceID = strings.TrimSpace(*tc.TraceId)
			}
			logx.Infof("conversation", "agent.buildHistory tool_result msg=%q tool=%q len=%d gzip=%t", strings.TrimSpace(msg.Id), strings.TrimSpace(tc.ToolName), len(pmsg.Content), strings.HasPrefix(pmsg.Content, "\x1f\x8b"))
		} else {
			// Classify chat and elicitation messages. For past elicitation
			// flows, ElicitationId will be set on assistant/user messages;
			// current-turn elicitation has already been extracted into the
			// separate elicitation slice and will not reach this block.
			if msg.ElicitationId != nil && strings.TrimSpace(*msg.ElicitationId) != "" {
				if role == "assistant" {
					pmsg.Kind = binding.MessageKindElicitPrompt
				} else if role == "user" {
					pmsg.Kind = binding.MessageKindElicitAnswer
				}
			} else {
				if role == "user" {
					pmsg.Kind = binding.MessageKindChatUser
				} else if role == "assistant" {
					pmsg.Kind = binding.MessageKindChatAssistant
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
		if currentTurnID != "" && strings.TrimSpace(t.ID) == currentTurnID {
			out.Current = t
			continue
		}
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
	projection runtimeprojection.ContextProjection,
) ([]normalizedMsg, []*binding.Message) {
	var normalized []normalizedMsg
	var elicitation []*binding.Message
	normalizedByID := map[string]int{}
	appendNormalized := func(item normalizedMsg) {
		if item.msg == nil {
			return
		}
		id := strings.TrimSpace(item.msg.Id)
		if id == "" {
			normalized = append(normalized, item)
			return
		}
		if idx, ok := normalizedByID[id]; ok {
			normalized[idx] = item
			return
		}
		normalizedByID[id] = len(normalized)
		normalized = append(normalized, item)
	}
	hiddenTurns := make(map[string]struct{}, len(projection.HiddenTurnIDs))
	for _, turnID := range projection.HiddenTurnIDs {
		turnID = strings.TrimSpace(turnID)
		if turnID == "" {
			continue
		}
		hiddenTurns[turnID] = struct{}{}
	}
	hiddenMessages := make(map[string]struct{}, len(projection.HiddenMessageIDs))
	for _, messageID := range projection.HiddenMessageIDs {
		messageID = strings.TrimSpace(messageID)
		if messageID == "" {
			continue
		}
		hiddenMessages[messageID] = struct{}{}
	}
	for ti, turn := range transcript {
		if turn == nil || turn.Message == nil {
			continue
		}
		if _, ok := hiddenTurns[strings.TrimSpace(turn.Id)]; ok {
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
		concreteToolMessageIDs := map[string]struct{}{}
		for _, candidate := range messages {
			if candidate == nil {
				continue
			}
			role := strings.ToLower(strings.TrimSpace(candidate.Role))
			mtype := strings.ToLower(strings.TrimSpace(candidate.Type))
			if (mtype == "tool_op" || role == "tool") && strings.TrimSpace(candidate.Id) != "" {
				concreteToolMessageIDs[strings.TrimSpace(candidate.Id)] = struct{}{}
			}
		}
		for _, m := range messages {
			if m == nil {
				continue
			}
			if _, ok := hiddenMessages[strings.TrimSpace(m.Id)]; ok {
				continue
			}
			toolMsgs := s.syntheticToolMessages(ctx, m)
			baseMsg := cloneMessageWithoutToolMessages(m)
			if baseMsg == nil {
				baseMsg = m
			}
			if _, ok := hiddenMessages[strings.TrimSpace(baseMsg.Id)]; ok {
				continue
			}
			if baseMsg.Mode != nil {
				switch strings.ToLower(strings.TrimSpace(*baseMsg.Mode)) {
				case "chain", "router":
					continue
				}
			}
			if applyPreview && baseMsg.Status != nil && strings.EqualFold(strings.TrimSpace(*baseMsg.Status), "error") {
				if baseMsg.IsArchived() {
					continue
				}
				appendNormalized(normalizedMsg{turnIdx: ti, msg: baseMsg})
				continue
			}
			if baseMsg.IsArchived() || baseMsg.IsInterim() {
				// Even when the base message is skipped (e.g., interim assistant
				// preamble), its tool_op children must enter the history so the
				// model can see prior tool results and continuation can work.
				for _, toolMsg := range toolMsgs {
					if body := strings.TrimSpace(toolMsg.GetContent()); body != "" {
						appendNormalized(normalizedMsg{turnIdx: ti, msg: toolMsg})
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
				appendNormalized(normalizedMsg{turnIdx: ti, msg: baseMsg})
				for _, toolMsg := range toolMsgs {
					appendNormalized(normalizedMsg{turnIdx: ti, msg: toolMsg})
				}
				continue
			}

			if baseMsg.Content != nil && *baseMsg.Content != "" {
				mtype := strings.ToLower(strings.TrimSpace(baseMsg.Type))
				isElicitationType := mtype == "elicitation_request" || mtype == "elicitation_response"
				role := strings.ToLower(strings.TrimSpace(baseMsg.Role))
				msgID := strings.TrimSpace(baseMsg.Id)
				if (mtype == "tool_op" || role == "tool") && msgID != "" {
					appendNormalized(normalizedMsg{turnIdx: ti, msg: baseMsg})
				} else if mtype == "text" || mtype == "task" || isElicitationType {
					// Steer/follow-up inputs are persisted as user task messages on the
					// active turn. They must enter prompt history for the next iteration,
					// otherwise the loop can detect late steer but the model never sees it.
					role := strings.ToLower(strings.TrimSpace(baseMsg.Role))
					if currentElicitation && lastElicitationMessage != nil && (role == "user" || role == "assistant") {
						if baseMsg.Id == lastElicitationMessage.Id && baseMsg.Content != nil {
							kind := binding.MessageKindElicitAnswer
							if role == "assistant" {
								kind = binding.MessageKindElicitPrompt
							}
							elicitation = append(elicitation, &binding.Message{Kind: kind, Role: baseMsg.Role, Content: *baseMsg.Content, CreatedAt: baseMsg.CreatedAt})
						} else if lastElicitationMessage.CreatedAt.Before(baseMsg.CreatedAt) && baseMsg.Content != nil {
							kind := binding.MessageKindElicitAnswer
							if role == "assistant" {
								kind = binding.MessageKindElicitPrompt
							}
							elicitation = append(elicitation, &binding.Message{Kind: kind, Role: baseMsg.Role, Content: *baseMsg.Content, CreatedAt: baseMsg.CreatedAt})
						} else if role == "user" || role == "assistant" {
							appendNormalized(normalizedMsg{turnIdx: ti, msg: baseMsg})
						}
					} else if role == "user" || role == "assistant" {
						appendNormalized(normalizedMsg{turnIdx: ti, msg: baseMsg})
					}
				}
			}

			for _, toolMsg := range toolMsgs {
				if _, ok := hiddenMessages[strings.TrimSpace(toolMsg.Id)]; ok {
					continue
				}
				if _, ok := concreteToolMessageIDs[strings.TrimSpace(toolMsg.Id)]; ok {
					continue
				}
				if shouldSkipInjectedDocumentToolResult(toolMsg) {
					continue
				}
				if body := strings.TrimSpace(toolMsg.GetContentPreferContent()); body != "" {
					appendNormalized(normalizedMsg{turnIdx: ti, msg: toolMsg})
				} else if logx.Enabled() {
					opID := ""
					toolName := ""
					if tc := messageToolCall(toolMsg); tc != nil {
						opID = strings.TrimSpace(tc.OpId)
						toolName = strings.TrimSpace(tc.ToolName)
					}
					logx.Warnf("conversation", "agent.collectNormalizedMessages dropped tool_result with empty body op_id=%q tool=%q turn=%q msg_id=%q", opID, toolName, strings.TrimSpace(turn.Id), strings.TrimSpace(toolMsg.Id))
				}
			}
		}
	}
	return normalized, elicitation
}

func shouldSkipInjectedDocumentToolResult(msg *apiconv.Message) bool {
	if msg == nil {
		return false
	}
	return shouldSkipInjectedDocumentToolResultBody(msg.GetContent())
}

func shouldSkipInjectedDocumentToolResultBody(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" || !strings.HasPrefix(body, "{") {
		return false
	}
	var payload struct {
		Injected         bool `json:"injected"`
		IncludedDocument bool `json:"includedDocument"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return false
	}
	return payload.Injected || payload.IncludedDocument
}

func expandHiddenTurnMessageIDs(transcript apiconv.Transcript, turnIDs []string) []string {
	if len(transcript) == 0 || len(turnIDs) == 0 {
		return nil
	}
	hiddenTurns := make(map[string]struct{}, len(turnIDs))
	for _, turnID := range turnIDs {
		turnID = strings.TrimSpace(turnID)
		if turnID == "" {
			continue
		}
		hiddenTurns[turnID] = struct{}{}
	}
	if len(hiddenTurns) == 0 {
		return nil
	}
	var result []string
	for _, turn := range transcript {
		if turn == nil {
			continue
		}
		if _, ok := hiddenTurns[strings.TrimSpace(turn.Id)]; !ok {
			continue
		}
		for _, msg := range turn.GetMessages() {
			if msg == nil {
				continue
			}
			if id := strings.TrimSpace(msg.Id); id != "" {
				result = append(result, id)
			}
			for _, tm := range msg.ToolMessage {
				if tm == nil {
					continue
				}
				if id := strings.TrimSpace(tm.Id); id != "" {
					result = append(result, id)
				}
			}
		}
	}
	return appendUniqueProjectionIDs(nil, result...)
}

func appendUniqueProjectionIDs(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, existing := range dst {
		id := strings.TrimSpace(existing)
		if id == "" {
			continue
		}
		seen[id] = struct{}{}
	}
	for _, raw := range values {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		dst = append(dst, id)
	}
	return dst
}

// appendCurrentMessages appends messages to History.Current ensuring
// CreatedAt is set and non-decreasing within the current turn. It does
// not modify Past timestamps.
func appendCurrentMessages(h *binding.History, msgs ...*binding.Message) {
	if h == nil || len(msgs) == 0 {
		return
	}
	if h.Current == nil {
		h.Current = &binding.Turn{ID: h.CurrentTurnID}
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
	if msg.MessageToolCall != nil {
		return &apiconv.ToolCallView{
			MessageSequence:   msg.MessageToolCall.MessageSequence,
			MessageId:         msg.MessageToolCall.MessageId,
			TurnId:            msg.MessageToolCall.TurnId,
			OpId:              msg.MessageToolCall.OpId,
			Attempt:           msg.MessageToolCall.Attempt,
			ToolName:          msg.MessageToolCall.ToolName,
			ToolKind:          msg.MessageToolCall.ToolKind,
			Status:            msg.MessageToolCall.Status,
			RequestHash:       msg.MessageToolCall.RequestHash,
			ErrorCode:         msg.MessageToolCall.ErrorCode,
			ErrorMessage:      msg.MessageToolCall.ErrorMessage,
			Retriable:         msg.MessageToolCall.Retriable,
			StartedAt:         msg.MessageToolCall.StartedAt,
			CompletedAt:       msg.MessageToolCall.CompletedAt,
			LatencyMs:         msg.MessageToolCall.LatencyMs,
			Cost:              msg.MessageToolCall.Cost,
			TraceId:           msg.MessageToolCall.TraceId,
			SpanId:            msg.MessageToolCall.SpanId,
			RequestPayloadId:  msg.MessageToolCall.RequestPayloadId,
			ResponsePayloadId: msg.MessageToolCall.ResponsePayloadId,
			RunId:             msg.MessageToolCall.RunId,
			Iteration:         msg.MessageToolCall.Iteration,
			RequestPayload:    msg.MessageToolCall.MessageRequestPayload,
			ResponsePayload:   msg.MessageToolCall.MessageResponsePayload,
		}
	}
	for _, tm := range msg.ToolMessage {
		if tm != nil && tm.ToolCall != nil {
			return tm.ToolCall
		}
	}
	return nil
}

func isConcreteToolResultMessage(msg *apiconv.Message) bool {
	if msg == nil {
		return false
	}
	role := strings.ToLower(strings.TrimSpace(msg.Role))
	mtype := strings.ToLower(strings.TrimSpace(msg.Type))
	return role == "tool" || mtype == "tool_op"
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

func (s *Service) messagePreviewLimit(turnIdx, totalTurns int, applyAging bool, isToolResult bool) int {
	limit := s.turnPreviewLimit(turnIdx, totalTurns, applyAging)
	if !isToolResult || s.defaults == nil || s.defaults.PreviewSettings.ToolResultLimit <= 0 {
		return limit
	}
	if limit <= 0 || s.defaults.PreviewSettings.ToolResultLimit < limit {
		return s.defaults.PreviewSettings.ToolResultLimit
	}
	return limit
}
