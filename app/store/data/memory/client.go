package memory

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	gfread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
	gfwrite "github.com/viant/agently-core/pkg/agently/generatedfile/write"
	payloadread "github.com/viant/agently-core/pkg/agently/payload/read"
	queueread "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	queuew "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
)

// Client is an in-memory implementation of conversation.Client.
// It is intended for tests and local runs without SQL/Datly.
type Client struct {
	mu            sync.RWMutex
	conversations map[string]*agconv.ConversationView
	// Indexes for fast lookup
	messages       map[string]*agconv.MessageView
	toolApprovals  map[string]*queuew.ToolApprovalQueue
	payloads       map[string]*payloadread.PayloadView
	generatedFiles map[string]*gfread.GeneratedFileView
}

func New() *Client {
	return &Client{
		conversations:  map[string]*agconv.ConversationView{},
		messages:       map[string]*agconv.MessageView{},
		toolApprovals:  map[string]*queuew.ToolApprovalQueue{},
		payloads:       map[string]*payloadread.PayloadView{},
		generatedFiles: map[string]*gfread.GeneratedFileView{},
	}
}

// DeleteConversation removes a conversation and its dependent messages in-memory.
func (c *Client) DeleteConversation(_ context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Remove messages belonging to the conversation
	for mid, mv := range c.messages {
		if mv != nil && mv.ConversationId == id {
			delete(c.messages, mid)
		}
	}
	for fid, fv := range c.generatedFiles {
		if fv != nil && fv.ConversationID == id {
			delete(c.generatedFiles, fid)
		}
	}
	// Remove conversation entry
	delete(c.conversations, id)
	return nil
}

// DeleteMessage removes a message by id from indexes and the conversation transcript.
func (c *Client) DeleteMessage(_ context.Context, conversationID, messageID string) error {
	if strings.TrimSpace(messageID) == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.messages, messageID)
	for fid, fv := range c.generatedFiles {
		if fv == nil || fv.MessageID == nil {
			continue
		}
		if *fv.MessageID == messageID {
			delete(c.generatedFiles, fid)
		}
	}
	if conv, ok := c.conversations[conversationID]; ok && conv != nil && conv.Transcript != nil {
		for _, t := range conv.Transcript {
			if t == nil || t.Message == nil {
				continue
			}
			kept := t.Message[:0]
			for _, m := range t.Message {
				if m == nil || m.Id == messageID {
					continue
				}
				kept = append(kept, m)
			}
			t.Message = kept
		}
	}
	return nil
}

// GetConversations returns all conversations without transcript for summary.
func (c *Client) GetConversations(ctx context.Context, input *convcli.Input) ([]*convcli.Conversation, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// When a user is present in auth context, restrict listing to that user.
	// Otherwise, include all conversations.
	var userID string
	if ui := authctx.User(ctx); ui != nil {
		userID = strings.TrimSpace(ui.Subject)
		if userID == "" {
			userID = strings.TrimSpace(ui.Email)
		}
	}
	out := make([]*convcli.Conversation, 0, len(c.conversations))
	for _, v := range c.conversations {
		if v == nil {
			continue
		}
		if userID != "" {
			if v.CreatedByUserId == nil || strings.TrimSpace(*v.CreatedByUserId) != userID {
				continue
			}
		}
		cp := cloneConversationView(v)
		// Compute aggregated usage across entire conversation (not filtered)
		cp.Usage = c.aggregateUsage(v.Id)
		// Remove transcript for list view
		cp.Transcript = nil
		out = append(out, toClientConversation(cp))
	}
	// Stable order by CreatedAt asc then Id
	sort.Slice(out, func(i, j int) bool {
		if out[i] == nil || out[j] == nil {
			return false
		}
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].Id < out[j].Id
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// GetConversation returns a single conversation with optional filtering.
func (c *Client) GetConversation(ctx context.Context, id string, options ...convcli.Option) (*convcli.Conversation, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	conv, ok := c.conversations[id]
	if !ok {
		return nil, nil
	}
	// When a user is present in auth context, only the owner may view.
	var userID string
	if ui := authctx.User(ctx); ui != nil {
		userID = strings.TrimSpace(ui.Subject)
		if userID == "" {
			userID = strings.TrimSpace(ui.Email)
		}
	}
	if userID != "" {
		if conv.CreatedByUserId == nil || strings.TrimSpace(*conv.CreatedByUserId) != userID {
			return nil, nil
		}
	}

	// Build input from options
	in := buildInput(id, options)

	// Clone first; aggregate usage against full conversation (not subject to since filter)
	cp := cloneConversationView(conv)
	cp.Usage = c.aggregateUsage(id)
	applySinceFilter(cp, &in)
	applyIncludeFlags(cp, &in)
	cp.OnRelation(ctx)

	return toClientConversation(cp), nil
}

func (c *Client) GetGeneratedFiles(_ context.Context, input *gfread.Input) ([]*gfread.GeneratedFileView, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*gfread.GeneratedFileView, 0, len(c.generatedFiles))
	for _, item := range c.generatedFiles {
		if item == nil {
			continue
		}
		if input != nil {
			if strings.TrimSpace(input.ID) != "" && item.ID != input.ID {
				continue
			}
			if strings.TrimSpace(input.ConversationID) != "" && item.ConversationID != input.ConversationID {
				continue
			}
			if strings.TrimSpace(input.TurnID) != "" {
				if item.TurnID == nil || strings.TrimSpace(*item.TurnID) != strings.TrimSpace(input.TurnID) {
					continue
				}
			}
			if strings.TrimSpace(input.MessageID) != "" {
				if item.MessageID == nil || strings.TrimSpace(*item.MessageID) != strings.TrimSpace(input.MessageID) {
					continue
				}
			}
			if strings.TrimSpace(input.Provider) != "" && !strings.EqualFold(strings.TrimSpace(item.Provider), strings.TrimSpace(input.Provider)) {
				continue
			}
			if strings.TrimSpace(input.Status) != "" && !strings.EqualFold(strings.TrimSpace(item.Status), strings.TrimSpace(input.Status)) {
				continue
			}
			if input.Since != nil && item.CreatedAt != nil && item.CreatedAt.Before(*input.Since) {
				continue
			}
		}
		cp := *item
		out = append(out, &cp)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := out[i].CreatedAt, out[j].CreatedAt
		if ti == nil || tj == nil {
			return out[i].ID < out[j].ID
		}
		if ti.Equal(*tj) {
			return out[i].ID < out[j].ID
		}
		return ti.Before(*tj)
	})
	return out, nil
}

func (c *Client) PatchGeneratedFile(_ context.Context, generatedFile *gfwrite.GeneratedFile) error {
	if generatedFile == nil || strings.TrimSpace(generatedFile.ID) == "" {
		return errors.New("generated file id required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cur := c.generatedFiles[generatedFile.ID]
	if cur == nil {
		cur = &gfread.GeneratedFileView{ID: generatedFile.ID}
		now := time.Now()
		cur.CreatedAt = &now
		c.generatedFiles[generatedFile.ID] = cur
	}
	if generatedFile.Has == nil {
		generatedFile.Has = &gfwrite.GeneratedFileHas{}
	}
	if generatedFile.Has.ConversationID {
		cur.ConversationID = generatedFile.ConversationID
	}
	if generatedFile.Has.TurnID {
		cur.TurnID = generatedFile.TurnID
	}
	if generatedFile.Has.MessageID {
		cur.MessageID = generatedFile.MessageID
	}
	if generatedFile.Has.Provider {
		cur.Provider = generatedFile.Provider
	}
	if generatedFile.Has.Mode {
		cur.Mode = generatedFile.Mode
	}
	if generatedFile.Has.CopyMode {
		cur.CopyMode = generatedFile.CopyMode
	}
	if generatedFile.Has.Status {
		cur.Status = generatedFile.Status
	}
	if generatedFile.Has.PayloadID {
		cur.PayloadID = generatedFile.PayloadID
	}
	if generatedFile.Has.ContainerID {
		cur.ContainerID = generatedFile.ContainerID
	}
	if generatedFile.Has.ProviderFileID {
		cur.ProviderFileID = generatedFile.ProviderFileID
	}
	if generatedFile.Has.Filename {
		cur.Filename = generatedFile.Filename
	}
	if generatedFile.Has.MimeType {
		cur.MimeType = generatedFile.MimeType
	}
	if generatedFile.Has.SizeBytes {
		cur.SizeBytes = generatedFile.SizeBytes
	}
	if generatedFile.Has.Checksum {
		cur.Checksum = generatedFile.Checksum
	}
	if generatedFile.Has.ErrorMessage {
		cur.ErrorMessage = generatedFile.ErrorMessage
	}
	if generatedFile.Has.ExpiresAt {
		cur.ExpiresAt = generatedFile.ExpiresAt
	}
	if generatedFile.Has.CreatedAt {
		cur.CreatedAt = generatedFile.CreatedAt
	}
	if generatedFile.Has.UpdatedAt {
		cur.UpdatedAt = generatedFile.UpdatedAt
	} else {
		now := time.Now()
		cur.UpdatedAt = &now
	}
	return nil
}

// aggregateUsage builds a UsageView equivalent to SQL aggregation for a conversation.
func (c *Client) aggregateUsage(conversationID string) *agconv.UsageView {
	if strings.TrimSpace(conversationID) == "" {
		return nil
	}
	// Accumulate totals and per-model stats
	type acc struct {
		pt, pct, pat, ct, crt, cat, capt, crpt, tt int
	}
	totals := acc{}
	byModel := map[string]*acc{}

	// Walk message index for matching conversation
	for _, m := range c.messages {
		if m == nil || m.ConversationId != conversationID || m.ModelCall == nil {
			continue
		}
		mc := m.ModelCall
		// Helper: fetch or create model accumulator
		get := func(model string) *acc {
			if v, ok := byModel[model]; ok {
				return v
			}
			v := &acc{}
			byModel[model] = v
			return v
		}
		mac := get(mc.Model)

		v := func(p *int) int {
			if p != nil {
				return *p
			}
			return 0
		}

		// Prompt
		x := v(mc.PromptTokens)
		totals.pt += x
		mac.pt += x
		x = v(mc.PromptCachedTokens)
		totals.pct += x
		mac.pct += x
		x = v(mc.PromptAudioTokens)
		totals.pat += x
		mac.pat += x

		// Completion
		x = v(mc.CompletionTokens)
		totals.ct += x
		mac.ct += x
		x = v(mc.CompletionReasoningTokens)
		totals.crt += x
		mac.crt += x
		x = v(mc.CompletionAudioTokens)
		totals.cat += x
		mac.cat += x
		x = v(mc.CompletionAcceptedPredictionTokens)
		totals.capt += x
		mac.capt += x
		x = v(mc.CompletionRejectedPredictionTokens)
		totals.crpt += x
		mac.crpt += x

		// Total
		x = v(mc.TotalTokens)
		totals.tt += x
		mac.tt += x
	}

	// No model calls → mirror SQL behavior (no row)
	if len(byModel) == 0 && totals.pt == 0 && totals.ct == 0 && totals.tt == 0 && totals.pct == 0 && totals.pat == 0 && totals.crt == 0 && totals.cat == 0 && totals.capt == 0 && totals.crpt == 0 {
		return nil
	}

	pint := func(i int) *int { v := i; return &v }

	// Build usage view with per-model breakdown (stable order by model)
	models := make([]*agconv.ModelView, 0, len(byModel))
	keys := make([]string, 0, len(byModel))
	for k := range byModel {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := byModel[k]
		models = append(models, &agconv.ModelView{
			ConversationId:                     conversationID,
			Model:                              k,
			PromptTokens:                       pint(a.pt),
			PromptCachedTokens:                 pint(a.pct),
			PromptAudioTokens:                  pint(a.pat),
			CompletionTokens:                   pint(a.ct),
			CompletionReasoningTokens:          pint(a.crt),
			CompletionAudioTokens:              pint(a.cat),
			CompletionAcceptedPredictionTokens: pint(a.capt),
			CompletionRejectedPredictionTokens: pint(a.crpt),
			TotalTokens:                        pint(a.tt),
		})
	}

	return &agconv.UsageView{
		ConversationId:                     conversationID,
		PromptTokens:                       pint(totals.pt),
		PromptCachedTokens:                 pint(totals.pct),
		PromptAudioTokens:                  pint(totals.pat),
		CompletionTokens:                   pint(totals.ct),
		CompletionReasoningTokens:          pint(totals.crt),
		CompletionAudioTokens:              pint(totals.cat),
		CompletionAcceptedPredictionTokens: pint(totals.capt),
		CompletionRejectedPredictionTokens: pint(totals.crpt),
		TotalTokens:                        pint(totals.tt),
		Model:                              models,
	}
}

// PatchConversations upserts conversations and merges fields according to Has flags.
func (c *Client) PatchConversations(ctx context.Context, in *convcli.MutableConversation) error {
	if in == nil || in.Has == nil || !in.Has.Id {
		return errors.New("missing conversation id")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	cur, ok := c.conversations[in.Id]
	if !ok {
		// Create minimal conversation
		cur = &agconv.ConversationView{Id: in.Id, Stage: "", CreatedAt: time.Now()}
		// Default to private visibility and set owner when available
		cur.Visibility = "private"
		if ui := authctx.User(ctx); ui != nil {
			userID := strings.TrimSpace(ui.Subject)
			if userID == "" {
				userID = strings.TrimSpace(ui.Email)
			}
			if userID != "" {
				cur.CreatedByUserId = &userID
			}
		}
		c.conversations[in.Id] = cur
	}
	applyConversationPatch(cur, in)
	return nil
}

// GetPayload returns a payload by id.
func (c *Client) GetPayload(_ context.Context, id string) (*convcli.Payload, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.payloads[id]
	if !ok {
		return nil, nil
	}
	cp := copyPayload((*convcli.Payload)(p))
	return cp, nil
}

// PatchPayload upserts a payload.
func (c *Client) PatchPayload(_ context.Context, p *convcli.MutablePayload) error {
	if p == nil || p.Has == nil || !p.Has.Id {
		return errors.New("missing payload id")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	existing, ok := c.payloads[p.Id]
	if !ok {
		c.payloads[p.Id] = &payloadread.PayloadView{Id: p.Id}
		existing = c.payloads[p.Id]
	}
	applyPayloadPatch((*convcli.Payload)(existing), p)
	return nil
}

// GetMessage returns a message by id.
func (c *Client) GetMessage(_ context.Context, id string, _ ...convcli.Option) (*convcli.Message, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.messages[id]
	if !ok {
		return nil, nil
	}
	return toClientMessage(copyMessage(m)), nil
}

// GetMessageByElicitation returns a message matching the given conversation and elicitation IDs.
func (c *Client) GetMessageByElicitation(_ context.Context, conversationID, elicitationID string) (*convcli.Message, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	conv, ok := c.conversations[conversationID]
	if !ok || conv == nil || conv.Transcript == nil {
		return nil, nil
	}
	for _, t := range conv.Transcript {
		if t == nil || t.Message == nil {
			continue
		}
		for _, m := range t.Message {
			if m == nil || m.ElicitationId == nil {
				continue
			}
			if *m.ElicitationId == elicitationID {
				return toClientMessage(copyMessage(m)), nil
			}
		}
	}
	return nil, nil
}

// PatchMessage upserts a message and places it into its conversation/turn transcript.
func (c *Client) PatchMessage(_ context.Context, in *convcli.MutableMessage) error {
	if in == nil || in.Has == nil || !in.Has.Id {
		return errors.New("missing message id")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	var (
		convID string
		cur    *agconv.MessageView
	)
	if existing, ok := c.messages[in.Id]; ok && existing != nil {
		cur = existing
		convID = strings.TrimSpace(existing.ConversationId)
	}
	if in.Has.ConversationID && strings.TrimSpace(in.ConversationID) != "" {
		convID = strings.TrimSpace(in.ConversationID)
	}
	if convID == "" {
		return errors.New("missing conversation id")
	}
	conv, ok := c.conversations[convID]
	if !ok || conv == nil {
		return errors.New("conversation not found")
	}

	// Ensure turn exists if provided
	var targetTurn *agconv.TranscriptView
	if in.Has.TurnID && in.TurnID != nil {
		targetTurn = findOrCreateTurn(conv, *in.TurnID)
	} else if cur != nil && cur.TurnId != nil && strings.TrimSpace(*cur.TurnId) != "" {
		targetTurn = findOrCreateTurn(conv, *cur.TurnId)
	} else {
		// If no turn provided, attach to a default synthetic turn per conversation
		targetTurn = findOrCreateTurn(conv, defaultTurnID(conv.Id))
	}

	// Upsert message in index and in turn
	if cur == nil {
		cur = &agconv.MessageView{Id: in.Id, ConversationId: convID, Role: in.Role, Type: in.Type, CreatedAt: time.Now()}
		c.messages[in.Id] = cur
		// Place into turn if not present
		if !messageInTurn(targetTurn, in.Id) {
			targetTurn.Message = append(targetTurn.Message, cur)
		}
	}
	applyMessagePatch(cur, in)
	return nil
}

// PatchModelCall upserts/attaches model-call to a message.
func (c *Client) PatchModelCall(_ context.Context, in *convcli.MutableModelCall) error {
	if in == nil || in.Has == nil || !in.Has.MessageID {
		return errors.New("missing model call message id")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	msg, ok := c.messages[in.MessageID]
	if !ok {
		return errors.New("message not found")
	}
	if msg.ModelCall == nil {
		msg.ModelCall = &agconv.ModelCallView{MessageId: in.MessageID}
	}
	applyModelCallPatch(msg.ModelCall, in)
	return nil
}

// PatchToolCall upserts/attaches tool-call to a message.
func (c *Client) PatchToolCall(_ context.Context, in *convcli.MutableToolCall) error {
	if in == nil || in.Has == nil || !in.Has.MessageID || !in.Has.OpID {
		return errors.New("missing tool call identifiers")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	msg, ok := c.messages[in.MessageID]
	if !ok {
		return errors.New("message not found")
	}
	if msg.ToolMessage == nil {
		msg.ToolMessage = []*agconv.ToolMessageView{}
	}
	var target *agconv.ToolMessageView
	for _, item := range msg.ToolMessage {
		if item != nil && strings.TrimSpace(item.Id) == strings.TrimSpace(in.MessageID) {
			target = item
			break
		}
	}
	if target == nil {
		target = &agconv.ToolMessageView{
			Id:              in.MessageID,
			ParentMessageId: msg.ParentMessageId,
			CreatedAt:       msg.CreatedAt,
			Type:            msg.Type,
			Content:         msg.Content,
		}
		msg.ToolMessage = append(msg.ToolMessage, target)
	}
	if target.ToolCall == nil {
		target.ToolCall = &agconv.ToolCallView{
			MessageId: in.MessageID,
			OpId:      in.OpID,
			Attempt:   in.Attempt,
			ToolName:  in.ToolName,
			ToolKind:  in.ToolKind,
		}
	}
	applyToolCallPatch(target.ToolCall, in)
	if strings.TrimSpace(target.ToolCall.ToolName) != "" {
		name := target.ToolCall.ToolName
		target.ToolName = &name
	}
	return nil
}

// PatchTurn upserts a turn and attaches to a conversation.
func (c *Client) PatchTurn(_ context.Context, in *convcli.MutableTurn) error {
	if in == nil || in.Has == nil || !in.Has.Id {
		return errors.New("missing turn id")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	convID := ""
	if in.Has.ConversationID && strings.TrimSpace(in.ConversationID) != "" {
		convID = strings.TrimSpace(in.ConversationID)
	}
	if convID == "" {
		for _, conv := range c.conversations {
			if conv == nil {
				continue
			}
			for _, t := range conv.Transcript {
				if t != nil && strings.TrimSpace(t.Id) == strings.TrimSpace(in.Id) {
					convID = strings.TrimSpace(conv.Id)
					break
				}
			}
			if convID != "" {
				break
			}
		}
	}
	if convID == "" {
		return errors.New("missing conversation id")
	}
	conv, ok := c.conversations[convID]
	if !ok || conv == nil {
		return errors.New("conversation not found")
	}
	t := findOrCreateTurn(conv, in.Id)
	applyTurnPatch(t, in)
	return nil
}

// Helpers

// Internal helpers operating on agconv views

// Bootstrap helpers used by tests

// EnsureConversation creates a conversation if it does not exist.
func (c *Client) EnsureConversation(id string, opts ...func(*convcli.MutableConversation)) error {
	mc := convcli.NewConversation()
	mc.SetId(id)
	for _, o := range opts {
		if o != nil {
			o(mc)
		}
	}
	return c.PatchConversations(context.Background(), mc)
}

func (c *Client) PatchToolApprovalQueue(_ context.Context, in *queuew.ToolApprovalQueue) error {
	if in == nil || strings.TrimSpace(in.Id) == "" {
		return errors.New("queue id is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cur := c.toolApprovals[in.Id]
	if cur == nil {
		cp := *in
		c.toolApprovals[in.Id] = &cp
		return nil
	}
	if in.Has == nil {
		cp := *in
		c.toolApprovals[in.Id] = &cp
		return nil
	}
	if in.Has.UserId {
		cur.UserId = in.UserId
	}
	if in.Has.ConversationId {
		cur.ConversationId = in.ConversationId
	}
	if in.Has.TurnId {
		cur.TurnId = in.TurnId
	}
	if in.Has.MessageId {
		cur.MessageId = in.MessageId
	}
	if in.Has.ToolName {
		cur.ToolName = in.ToolName
	}
	if in.Has.Title {
		cur.Title = in.Title
	}
	if in.Has.Arguments {
		cur.Arguments = append([]byte(nil), in.Arguments...)
	}
	if in.Has.Metadata {
		cur.Metadata = in.Metadata
	}
	if in.Has.Status {
		cur.Status = in.Status
	}
	if in.Has.Decision {
		cur.Decision = in.Decision
	}
	if in.Has.ApprovedByUserId {
		cur.ApprovedByUserId = in.ApprovedByUserId
	}
	if in.Has.ApprovedAt {
		cur.ApprovedAt = in.ApprovedAt
	}
	if in.Has.ExecutedAt {
		cur.ExecutedAt = in.ExecutedAt
	}
	if in.Has.ErrorMessage {
		cur.ErrorMessage = in.ErrorMessage
	}
	if in.Has.CreatedAt {
		cur.CreatedAt = in.CreatedAt
	}
	if in.Has.UpdatedAt {
		cur.UpdatedAt = in.UpdatedAt
	}
	return nil
}

func (c *Client) ListToolApprovalQueues(_ context.Context, in *queueread.QueueRowsInput) ([]*queueread.QueueRowView, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []*queueread.QueueRowView
	for _, q := range c.toolApprovals {
		if q == nil {
			continue
		}
		if in != nil {
			if strings.TrimSpace(in.Id) != "" && !strings.EqualFold(strings.TrimSpace(in.Id), strings.TrimSpace(q.Id)) {
				continue
			}
			if strings.TrimSpace(in.UserId) != "" && !strings.EqualFold(strings.TrimSpace(in.UserId), strings.TrimSpace(q.UserId)) {
				continue
			}
			if strings.TrimSpace(in.ConversationId) != "" && (q.ConversationId == nil || !strings.EqualFold(strings.TrimSpace(in.ConversationId), strings.TrimSpace(*q.ConversationId))) {
				continue
			}
			if strings.TrimSpace(in.TurnId) != "" && (q.TurnId == nil || !strings.EqualFold(strings.TrimSpace(in.TurnId), strings.TrimSpace(*q.TurnId))) {
				continue
			}
			if strings.TrimSpace(in.MessageId) != "" && (q.MessageId == nil || !strings.EqualFold(strings.TrimSpace(in.MessageId), strings.TrimSpace(*q.MessageId))) {
				continue
			}
			if strings.TrimSpace(in.ToolName) != "" && !strings.EqualFold(strings.TrimSpace(in.ToolName), strings.TrimSpace(q.ToolName)) {
				continue
			}
			if strings.TrimSpace(in.QueueStatus) != "" && !strings.EqualFold(strings.TrimSpace(in.QueueStatus), strings.TrimSpace(q.Status)) {
				continue
			}
		}
		row := &queueread.QueueRowView{
			Id:        q.Id,
			UserId:    q.UserId,
			ToolName:  q.ToolName,
			Arguments: append([]byte(nil), q.Arguments...),
			Status:    q.Status,
		}
		if q.CreatedAt != nil {
			row.CreatedAt = *q.CreatedAt
		}
		if q.ConversationId != nil {
			v := *q.ConversationId
			row.ConversationId = &v
		}
		if q.TurnId != nil {
			v := *q.TurnId
			row.TurnId = &v
		}
		if q.MessageId != nil {
			v := *q.MessageId
			row.MessageId = &v
		}
		if q.Title != nil {
			v := *q.Title
			row.Title = &v
		}
		if q.Metadata != nil {
			row.Metadata = q.Metadata
		}
		if q.Decision != nil {
			v := *q.Decision
			row.Decision = &v
		}
		if q.ApprovedByUserId != nil {
			v := *q.ApprovedByUserId
			row.ApprovedByUserId = &v
		}
		if q.ApprovedAt != nil {
			v := *q.ApprovedAt
			row.ApprovedAt = &v
		}
		if q.ExecutedAt != nil {
			v := *q.ExecutedAt
			row.ExecutedAt = &v
		}
		if q.ErrorMessage != nil {
			v := *q.ErrorMessage
			row.ErrorMessage = &v
		}
		if q.UpdatedAt != nil {
			v := *q.UpdatedAt
			row.UpdatedAt = &v
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].Id < out[j].Id
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}
