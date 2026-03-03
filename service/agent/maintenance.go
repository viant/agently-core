package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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
	if s.cancelReg != nil {
		s.cancelReg.CancelTurn(conversationID)
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
