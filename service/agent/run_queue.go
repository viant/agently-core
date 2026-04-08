package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturnbyid "github.com/viant/agently-core/pkg/agently/turn/byId"
	agturnnext "github.com/viant/agently-core/pkg/agently/turn/nextQueued"
	agturncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
	turnqueuewrite "github.com/viant/agently-core/pkg/agently/turnqueue/write"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/shared"
)

func (s *Service) tryQueueTurn(ctx context.Context, input *QueryInput) (bool, error) {
	if s == nil || s.dataService == nil || s.conversation == nil || input == nil {
		return false, nil
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	turnID := strings.TrimSpace(input.MessageID)
	if conversationID == "" || turnID == "" {
		return false, nil
	}
	active, err := s.dataService.GetActiveTurn(ctx, &agturnactive.ActiveTurnsInput{
		ConversationID: conversationID,
		Has:            &agturnactive.ActiveTurnsInputHas{ConversationID: true},
	})
	if err != nil {
		return false, fmt.Errorf("failed to check active turn: %w", err)
	}
	if active == nil || strings.TrimSpace(active.Id) == "" || strings.TrimSpace(active.Id) == turnID {
		return false, nil
	}
	queuedCount, err := s.dataService.CountQueuedTurns(ctx, &agturncount.QueuedTotalInput{
		ConversationID: conversationID,
		Has:            &agturncount.QueuedTotalInputHas{ConversationID: true},
	})
	if err != nil {
		return false, fmt.Errorf("failed to count queued turns: %w", err)
	}
	if queuedCount >= 20 {
		return false, fmt.Errorf("turn queue limit reached for conversation %s", conversationID)
	}
	queueSeq := time.Now().UnixNano()
	now := time.Now()
	rec := apiconv.NewTurn()
	rec.SetId(turnID)
	rec.SetConversationID(conversationID)
	rec.SetStatus("queued")
	rec.SetQueueSeq(queueSeq)
	rec.SetCreatedAt(now)
	rec.SetStartedByMessageID(turnID)
	if err := s.conversation.PatchTurn(ctx, rec); err != nil {
		return false, fmt.Errorf("failed to queue turn: %w", err)
	}
	msg := apiconv.NewMessage()
	msg.SetId(turnID)
	msg.SetConversationID(conversationID)
	msg.SetTurnID(turnID)
	msg.SetRole("user")
	msg.SetType("task")
	msg.SetContent(strings.TrimSpace(input.Query))
	msg.SetRawContent(strings.TrimSpace(input.Query))
	msg.SetCreatedAt(now)
	if userID := strings.TrimSpace(input.UserId); userID != "" {
		msg.SetCreatedByUserID(userID)
	}
	if err := s.conversation.PatchMessage(ctx, msg); err != nil {
		return false, fmt.Errorf("failed to persist queued message: %w", err)
	}
	if patcher, ok := s.dataService.(interface {
		PatchTurnQueue(ctx context.Context, in *turnqueuewrite.TurnQueue) error
	}); ok {
		q := &turnqueuewrite.TurnQueue{Has: &turnqueuewrite.TurnQueueHas{}}
		q.SetId(turnID)
		q.SetConversationId(conversationID)
		q.SetTurnId(turnID)
		q.SetMessageId(turnID)
		q.SetQueueSeq(queueSeq)
		q.SetStatus("queued")
		q.SetCreatedAt(now)
		q.SetUpdatedAt(now)
		if err := patcher.PatchTurnQueue(ctx, q); err != nil {
			return false, fmt.Errorf("failed to persist turn queue: %w", err)
		}
	}
	infof("agent.Query queued convo=%q turn_id=%q active_turn=%q queue_seq=%d", conversationID, turnID, strings.TrimSpace(active.Id), queueSeq)
	s.emitTurnQueued(ctx, conversationID, turnID, queueSeq, now, strings.TrimSpace(input.Query))
	return true, nil
}

func (s *Service) emitTurnQueued(ctx context.Context, conversationID, turnID string, queueSeq int64, createdAt time.Time, query string) {
	if s.streamPub == nil {
		return
	}
	event := &streaming.Event{
		Type:               streaming.EventTypeTurnQueued,
		StreamID:           conversationID,
		ConversationID:     conversationID,
		TurnID:             turnID,
		MessageID:          turnID,
		QueueSeq:           int(queueSeq),
		StartedByMessageID: turnID,
		UserMessageID:      turnID,
		CreatedAt:          createdAt,
	}
	event.NormalizeIdentity(conversationID, turnID)
	if err := s.streamPub.Publish(ctx, event); err != nil {
		warnf("turn_queued publish error convo=%q turn=%q err=%v", conversationID, turnID, err)
	}
}

func (s *Service) registerTurnCancel(ctx context.Context, turn runtimerequestctx.TurnMeta) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	var wrappedCancel func()
	wrappedCancel = func() {
		cancel()
		if s.conversation != nil {
			upd := apiconv.NewTurn()
			upd.SetId(turn.TurnID)
			upd.SetStatus("canceled")
			if err := s.conversation.PatchTurn(context.Background(), upd); err == nil {
				s.patchStarterMessageTerminalStatus(context.Background(), turn, "canceled")
			}
		}
		warnf("agent.turn cancel convo=%q turn_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	}
	if s.cancelReg != nil {
		s.cancelReg.Register(turn.ConversationID, turn.TurnID, wrappedCancel)
		return ctx, func() {
			s.cancelReg.Complete(turn.ConversationID, turn.TurnID, wrappedCancel)
		}
	}
	return ctx, wrappedCancel
}

func (s *Service) isTurnCanceled(ctx context.Context, conversationID, turnID string) bool {
	if s == nil || s.conversation == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(turnID) == "" {
		return false
	}
	conv, err := s.conversation.GetConversation(ctx, strings.TrimSpace(conversationID), apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return false
	}
	for _, turn := range conv.GetTranscript() {
		if strings.TrimSpace(turn.Id) != strings.TrimSpace(turnID) {
			continue
		}
		return strings.EqualFold(strings.TrimSpace(turn.Status), "canceled") || strings.EqualFold(strings.TrimSpace(turn.Status), "cancelled")
	}
	return false
}

func (s *Service) triggerQueueDrain(conversationID string) {
	if s == nil || s.dataService == nil || s.conversation == nil {
		return
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return
	}
	if !queueDrainGuards.acquire(conversationID) {
		return
	}
	go func(convID string) {
		defer queueDrainGuards.release(convID)
		if err := s.drainQueuedTurns(convID); err != nil {
			warnf("agent.queueDrain error convo=%q err=%v", convID, err)
		}
	}(conversationID)
}

func (s *Service) drainQueuedTurns(conversationID string) error {
	for {
		next, err := s.dataService.GetNextQueuedTurn(context.Background(), &agturnnext.QueuedTurnInput{
			ConversationID: conversationID,
			Has:            &agturnnext.QueuedTurnInputHas{ConversationID: true},
		})
		if err != nil {
			return fmt.Errorf("failed to load next queued turn: %w", err)
		}
		if next == nil || strings.TrimSpace(next.Id) == "" {
			return nil
		}

		turnID := strings.TrimSpace(next.Id)
		starterID := strings.TrimSpace(valueOrEmpty(next.StartedByMessageId))
		if starterID == "" {
			starterID = turnID
		}
		starter, err := s.conversation.GetMessage(context.Background(), starterID)
		if err != nil || starter == nil {
			upd := apiconv.NewTurn()
			upd.SetId(turnID)
			upd.SetStatus("failed")
			upd.SetErrorMessage("queued starter message not found")
			_ = s.conversation.PatchTurn(context.Background(), upd)
			warnf("agent.queueDrain failed to load starter message convo=%q turn_id=%q starter_id=%q err=%v", conversationID, turnID, starterID, err)
			continue
		}

		queryText := strings.TrimSpace(valueOrEmpty(starter.RawContent))
		if queryText == "" {
			queryText = strings.TrimSpace(valueOrEmpty(starter.Content))
		}
		if queryText == "" {
			upd := apiconv.NewTurn()
			upd.SetId(turnID)
			upd.SetStatus("failed")
			upd.SetErrorMessage("queued starter message is empty")
			_ = s.conversation.PatchTurn(context.Background(), upd)
			s.patchQueuedStarterMessageStatus(context.Background(), conversationID, turnID, starterID, "failed")
			warnf("agent.queueDrain empty starter message convo=%q turn_id=%q starter_id=%q", conversationID, turnID, starterID)
			continue
		}

		input := &QueryInput{
			RequestTime:            time.Now(),
			ConversationID:         conversationID,
			MessageID:              turnID,
			AgentID:                strings.TrimSpace(valueOrEmpty(next.AgentIdUsed)),
			UserId:                 strings.TrimSpace(valueOrEmpty(starter.CreatedByUserId)),
			Query:                  queryText,
			ModelOverride:          strings.TrimSpace(valueOrEmpty(next.ModelOverride)),
			SkipInitialUserMessage: true,
			IsNewConversation:      false,
			ParentConversationID:   "",
		}
		out := &QueryOutput{}
		err = s.Query(context.Background(), input, out)
		if err != nil {
			warnf("agent.queueDrain query failed convo=%q turn_id=%q err=%v", conversationID, turnID, err)
		}

		refreshed, rErr := s.dataService.GetTurnByID(context.Background(), &agturnbyid.TurnLookupInput{
			ID:             turnID,
			ConversationID: conversationID,
			Has:            &agturnbyid.TurnLookupInputHas{ID: true, ConversationID: true},
		})
		if rErr == nil && refreshed != nil && strings.EqualFold(strings.TrimSpace(refreshed.Status), "queued") {
			return nil
		}
	}
}

func (s *Service) patchQueuedStarterMessageStatus(ctx context.Context, conversationID, turnID, starterID, status string) {
	if s == nil || s.conversation == nil || strings.TrimSpace(starterID) == "" || strings.TrimSpace(status) == "" {
		return
	}
	msg := apiconv.NewMessage()
	msg.SetId(strings.TrimSpace(starterID))
	msg.SetConversationID(strings.TrimSpace(conversationID))
	if strings.TrimSpace(turnID) != "" {
		msg.SetTurnID(strings.TrimSpace(turnID))
	}
	msg.SetStatus(shared.NormalizeMessageStatus(status))
	if err := s.conversation.PatchMessage(ctx, msg); err != nil {
		warnf("agent.queueDrain patch starter message failed convo=%q turn_id=%q starter_id=%q status=%q err=%v", strings.TrimSpace(conversationID), strings.TrimSpace(turnID), strings.TrimSpace(starterID), strings.TrimSpace(status), err)
	}
}

func (s *Service) patchStarterMessageTerminalStatus(ctx context.Context, turn runtimerequestctx.TurnMeta, status string) {
	normalized := shared.NormalizeMessageStatus(status)
	if normalized != "rejected" && normalized != "cancel" {
		return
	}
	starterID := strings.TrimSpace(turn.ParentMessageID)
	if starterID == "" {
		starterID = strings.TrimSpace(turn.TurnID)
	}
	s.patchQueuedStarterMessageStatus(ctx, turn.ConversationID, turn.TurnID, starterID, normalized)
}
