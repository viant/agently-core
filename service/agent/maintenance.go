package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agconvlist "github.com/viant/agently-core/pkg/agently/conversation/list"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	agrunactive "github.com/viant/agently-core/pkg/agently/run/active"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
)

// maintenanceGuards prevents concurrent maintenance operations on the same conversation.
var maintenanceGuards = &guardMap{m: make(map[string]*int32)}

type guardMap struct {
	mu sync.Mutex
	m  map[string]*int32
}

func (g *guardMap) acquire(convID string) bool {
	g.mu.Lock()
	v, ok := g.m[convID]
	if !ok {
		v = new(int32)
		g.m[convID] = v
	}
	g.mu.Unlock()
	return atomic.CompareAndSwapInt32(v, 0, 1)
}

func (g *guardMap) release(convID string) {
	g.mu.Lock()
	if v, ok := g.m[convID]; ok {
		atomic.StoreInt32(v, 0)
	}
	g.mu.Unlock()
}

// Terminate cancels all active turns for a conversation and marks it as canceled.
func (s *Service) Terminate(ctx context.Context, conversationID string) error {
	if conversationID == "" {
		return fmt.Errorf("conversation ID is required")
	}
	return s.terminateConversationTree(ctx, conversationID, map[string]struct{}{})
}

func (s *Service) terminateConversationTree(ctx context.Context, conversationID string, visited map[string]struct{}) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}
	if _, ok := visited[conversationID]; ok {
		return nil
	}
	visited[conversationID] = struct{}{}

	if s.cancelReg != nil {
		s.cancelReg.CancelConversation(conversationID)
	}

	if s.conversation == nil {
		return nil
	}

	patchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.conversation.PatchConversations(patchCtx, convw.NewConversationStatus(conversationID, "canceled")); err != nil {
		return err
	}

	conv, err := s.conversation.GetConversation(patchCtx, conversationID,
		apiconv.WithIncludeTranscript(true),
		apiconv.WithIncludeToolCall(true),
		apiconv.WithIncludeModelCall(true),
	)
	if err != nil || conv == nil {
		return err
	}

	for _, turn := range conv.GetTranscript() {
		for _, msg := range turn.Message {
			if msg == nil {
				continue
			}
			if childID := strings.TrimSpace(pointerString(msg.LinkedConversationId)); childID != "" {
				_ = s.terminateConversationTree(patchCtx, childID, visited)
			}
		}
	}
	return nil
}

func pointerString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// ReconcileRunningConversationStatuses repairs persisted conversation.status
// rows that still say "running" even though execution is fully terminal.
// This is explicit maintenance for historical drift; it is intentionally
// separate from stale-run recovery watchdog logic.
func (s *Service) ReconcileRunningConversationStatuses(ctx context.Context, limit int) error {
	if s == nil || s.dataService == nil || s.conversation == nil {
		return nil
	}
	if limit <= 0 {
		limit = 200
	}
	page, err := s.dataService.ListConversations(ctx, &agconvlist.ConversationRowsInput{
		StatusFilter:     "running",
		DefaultPredicate: "1",
		ParentId:         "",
		Has: &agconvlist.ConversationRowsInputHas{
			StatusFilter:     true,
			DefaultPredicate: true,
			ParentId:         true,
		},
	}, &data.PageInput{Limit: limit, Direction: data.DirectionLatest}, data.WithAdminPrincipal("maintenance"))
	if err != nil {
		return fmt.Errorf("list running conversations: %w", err)
	}
	for _, row := range page.Rows {
		if row == nil {
			continue
		}
		if err := s.reconcileConversationStatus(ctx, strings.TrimSpace(row.Id)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileConversationStatus(ctx context.Context, conversationID string) error {
	if s == nil || s.dataService == nil || s.conversation == nil {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}
	activeRun, err := s.dataService.GetActiveRun(ctx, &agrunactive.ActiveRunsInput{
		ConversationId: conversationID,
		Has:            &agrunactive.ActiveRunsInputHas{ConversationId: true},
	}, data.WithAdminPrincipal("maintenance"))
	if err != nil {
		return fmt.Errorf("load active run for %s: %w", conversationID, err)
	}
	if activeRun != nil && strings.TrimSpace(activeRun.Id) != "" {
		return nil
	}
	activeTurn, err := s.dataService.GetActiveTurn(ctx, &agturnactive.ActiveTurnsInput{
		ConversationID: conversationID,
		Has:            &agturnactive.ActiveTurnsInputHas{ConversationID: true},
	}, data.WithAdminPrincipal("maintenance"))
	if err != nil {
		return fmt.Errorf("load active turn for %s: %w", conversationID, err)
	}
	if activeTurn != nil && strings.TrimSpace(activeTurn.Id) != "" {
		activeTurnStatus := strings.TrimSpace(strings.ToLower(activeTurn.Status))
		if activeTurnStatus == "waiting_for_user" || activeTurnStatus == "blocked" {
			return nil
		}
		upd := apiconv.NewTurn()
		upd.SetId(strings.TrimSpace(activeTurn.Id))
		upd.SetStatus("failed")
		upd.SetErrorMessage("orphan active turn without active run")
		if err := s.conversation.PatchTurn(ctx, upd); err != nil {
			return fmt.Errorf("patch orphan active turn %s: %w", activeTurn.Id, err)
		}
		if err := s.patchConversationStatus(ctx, conversationID, "failed"); err != nil {
			return fmt.Errorf("patch orphan active conversation %s: %w", conversationID, err)
		}
		s.triggerQueueDrain(conversationID)
		return nil
	}
	queuedCount, err := s.dataService.CountQueuedTurns(ctx, &agturncount.QueuedTotalInput{
		ConversationID: conversationID,
		Has:            &agturncount.QueuedTotalInputHas{ConversationID: true},
	}, data.WithAdminPrincipal("maintenance"))
	if err != nil {
		return fmt.Errorf("count queued turns for %s: %w", conversationID, err)
	}
	if queuedCount > 0 {
		return nil
	}
	conv, err := s.dataService.GetConversation(ctx, conversationID, &agconv.ConversationInput{
		Id:                conversationID,
		IncludeTranscript: true,
		Has: &agconv.ConversationInputHas{
			Id:                true,
			IncludeTranscript: true,
		},
	}, data.WithAdminPrincipal("maintenance"))
	if err != nil {
		return fmt.Errorf("load conversation %s: %w", conversationID, err)
	}
	if conv == nil || conv.Status == nil {
		return nil
	}
	status := strings.TrimSpace(*conv.Status)
	if status == "" || strings.EqualFold(status, "running") {
		return nil
	}
	if err := s.patchConversationStatus(ctx, conversationID, status); err != nil {
		return fmt.Errorf("patch conversation %s to %q: %w", conversationID, status, err)
	}
	return nil
}

// Compact generates an LLM summary of the conversation history, archiving old
// messages and replacing them with the summary. Uses an atomic guard to prevent
// concurrent compaction on the same conversation.
func (s *Service) Compact(ctx context.Context, conversationID string) error {
	if conversationID == "" {
		return fmt.Errorf("conversation ID is required")
	}
	if !maintenanceGuards.acquire(conversationID) {
		return fmt.Errorf("maintenance operation already in progress for conversation %s", conversationID)
	}
	defer maintenanceGuards.release(conversationID)

	// Placeholder: full implementation would:
	// 1. Set conversation status to "compacting"
	// 2. Load conversation history
	// 3. Generate LLM summary using prompts/compact.md
	// 4. Archive old messages
	// 5. Insert summary as a new system message
	// 6. Set conversation status to "compacted"
	return nil
}

// Prune uses an LLM to select low-value messages for removal from the
// conversation history. Uses an atomic guard to prevent concurrent operations.
func (s *Service) Prune(ctx context.Context, conversationID string) error {
	if conversationID == "" {
		return fmt.Errorf("conversation ID is required")
	}
	if !maintenanceGuards.acquire(conversationID) {
		return fmt.Errorf("maintenance operation already in progress for conversation %s", conversationID)
	}
	defer maintenanceGuards.release(conversationID)

	// Placeholder: full implementation would:
	// 1. Set conversation status to "pruning"
	// 2. Load conversation history
	// 3. Use LLM with prompts/prune_prompt.md to identify messages for removal
	// 4. Archive selected messages
	// 5. Set conversation status to "pruned"
	return nil
}
