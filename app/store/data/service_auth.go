package data

import (
	"context"
	"strings"

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

type authCache struct {
	byConversationID map[string]error
}

func newAuthCache() *authCache {
	return &authCache{byConversationID: map[string]error{}}
}

func (s *datlyService) authorizeConversationID(ctx context.Context, conversationID string, opts *options, cache *authCache) error {
	if opts == nil || opts.principal == "" || opts.isAdmin {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ErrPermissionDenied
	}
	if cache != nil {
		if err, ok := cache.byConversationID[conversationID]; ok {
			return err
		}
	}
	conv, err := s.loadConversationForAuth(ctx, conversationID)
	if err != nil {
		if cache != nil {
			cache.byConversationID[conversationID] = err
		}
		return err
	}
	err = authorizeConversation(conv, opts)
	if cache != nil {
		cache.byConversationID[conversationID] = err
	}
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
