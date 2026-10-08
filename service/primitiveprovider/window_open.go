package service

import (
	"context"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
	"strings"
	"time"
)

// WindowOpenAdmission belongs to the trusted provider/host. Open transports
// opaque approved metadata; this callback owns selected-resource admission.
type WindowOpenDecision struct {
	Window     *types.Window
	ValidUntil time.Time
}
type WindowOpenAdmission func(context.Context, identity.ResolvedResource, *types.Window, map[string]any) (*WindowOpenDecision, error)

func (s *Service) admitWindowOpen(ctx context.Context, window *types.Window, parameters map[string]any) (*WindowOpenDecision, error) {
	if s == nil || s.cfg == nil || window == nil {
		return nil, identity.ErrResourceDenied
	}
	if window.Authorization == nil {
		if window.Resource == nil {
			return &WindowOpenDecision{Window: window}, nil
		}
		return &WindowOpenDecision{Window: window, ValidUntil: window.Resource.ValidUntil}, nil
	}
	if s.cfg.WindowOpenAdmission == nil {
		if strings.EqualFold(window.Authorization.Scope, "resource") {
			return nil, identity.ErrResourceDenied
		}
		if window.Resource == nil {
			return &WindowOpenDecision{Window: window}, nil
		}
		return &WindowOpenDecision{Window: window, ValidUntil: window.Resource.ValidUntil}, nil
	}
	if window.Resource == nil {
		return nil, identity.ErrResourceDenied
	}
	decision, err := s.cfg.WindowOpenAdmission(ctx, *window.Resource, window, parameters)
	if err != nil {
		return nil, err
	}
	if decision == nil || decision.Window == nil || !decision.ValidUntil.After(time.Now()) || !window.Resource.ValidUntil.After(time.Now()) {
		return nil, identity.ErrResourceDenied
	}
	if window.Resource.ValidUntil.Before(decision.ValidUntil) {
		decision.ValidUntil = window.Resource.ValidUntil
	}
	decision.Window.Resource = window.Resource
	decision.Window.ResourceTarget = window.ResourceTarget
	return decision, nil
}

// ConfigureWindowOpenAdmission is startup-only, like ConfigureWindowCatalog.
func (s *Service) ConfigureWindowOpenAdmission(admit WindowOpenAdmission) {
	if s != nil && s.cfg != nil {
		s.cfg.WindowOpenAdmission = admit
	}
}

func (s *Service) AdmitWindowOpen(ctx context.Context, window *types.Window, parameters map[string]any) (*types.Window, error) {
	decision, err := s.admitWindowOpen(ctx, window, parameters)
	if err != nil {
		return nil, err
	}
	return decision.Window, nil
}

func (s *Service) AdmitWindowOpenDecision(ctx context.Context, window *types.Window, parameters map[string]any) (*WindowOpenDecision, error) {
	return s.admitWindowOpen(ctx, window, parameters)
}

func (s *Service) HasWindowOpenAdmission() bool {
	return s != nil && s.cfg != nil && s.cfg.WindowOpenAdmission != nil
}
