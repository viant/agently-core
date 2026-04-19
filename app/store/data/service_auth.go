package data

import (
	"context"
	"strings"
	"sync"

	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/datly"
)

func authorizeConversation(item *agconv.ConversationView, opts *options) error {
	if item == nil || opts == nil || opts.principal == "" || opts.isAdmin {
		return nil
	}
	if strings.EqualFold(item.Visibility, "public") {
		return nil
	}
	if item.Shareable == 1 {
		return nil
	}
	if item.CreatedByUserId != nil && *item.CreatedByUserId == opts.principal {
		return nil
	}
	return ErrPermissionDenied
}

// authCache memoises per-conversation authorisation decisions. A single
// datlyService instance is shared by concurrent HTTP handlers, so reads and
// writes of byConversationID must be serialised — otherwise the Go runtime
// will detect concurrent map access and crash the process.
type authCache struct {
	mu               sync.RWMutex
	byConversationID map[string]error
}

func newAuthCache() *authCache {
	return &authCache{byConversationID: map[string]error{}}
}

func (c *authCache) get(id string) (error, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	err, ok := c.byConversationID[id]
	return err, ok
}

func (c *authCache) set(id string, err error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byConversationID[id] = err
}

func (s *datlyService) authorizeConversationID(ctx context.Context, conversationID string, opts *options, cache *authCache) error {
	if opts == nil || opts.principal == "" || opts.isAdmin {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ErrPermissionDenied
	}
	if err, ok := cache.get(conversationID); ok {
		return err
	}
	conv, err := s.loadConversationForAuth(ctx, conversationID)
	if err != nil {
		cache.set(conversationID, err)
		return err
	}
	err = authorizeConversation(conv, opts)
	cache.set(conversationID, err)
	return err
}

func (s *datlyService) loadConversationForAuth(ctx context.Context, id string) (*agconv.ConversationView, error) {
	input := &agconv.ConversationInput{Id: id, Has: &agconv.ConversationInputHas{Id: true}}
	out := &agconv.ConversationOutput{}
	uri := strings.ReplaceAll(agconv.ConversationPathURI, "{id}", id)
	if _, err := s.dao.Operate(ctx, datly.WithURI(uri), datly.WithInput(input), datly.WithOutput(out)); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, ErrPermissionDenied
	}
	return out.Data[0], nil
}
