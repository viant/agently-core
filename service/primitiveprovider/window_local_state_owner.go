package service

import (
	"context"
	"github.com/viant/forge/backend/types"
	"github.com/viant/forge/backend/uicontext"
)

// ConfigureLocalStateOwner installs the trusted host resolver before serving
// requests. Like the other startup configuration hooks, it is not concurrent.
func (s *Service) ConfigureLocalStateOwner(resolve uicontext.LocalStateOwnerResolver) {
	if s != nil && s.cfg != nil {
		s.cfg.LocalStateOwnerResolver = resolve
		s.hub.localStateOwner = resolve
	}
}
func (s *Service) AttachLocalStateOwner(ctx context.Context, window *types.Window) (*types.Window, error) {
	var resolve uicontext.LocalStateOwnerResolver
	if s != nil && s.cfg != nil {
		resolve = s.cfg.LocalStateOwnerResolver
	}
	return uicontext.AttachLocalStateOwner(ctx, window, resolve)
}
