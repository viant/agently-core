package permittedview

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/viant/agently-core/service/policy"
	"github.com/viant/authz"
	"strings"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/forge/backend/types"
)

// OpenBootstrap must execute a host-selected approved datasource through its
// protected service. Parameters are hints, never verified resource data.
type OpenBootstrap func(context.Context, identity.ResolvedResource, *types.Window, map[string]any) (map[string]any, error)

// OpenSelectionCheck binds the requested object to the returned bootstrap row.
// It is explicit: the runtime never guesses datasource or input field names.
type OpenSelectionCheck func(context.Context, *types.Window, map[string]any, map[string]any) error

type OpenAdmission struct {
	Runtime        *Runtime
	Bootstrap      OpenBootstrap
	MatchSelection OpenSelectionCheck
}

// Apply authorizes a complete approved window. Only resource read is mandatory;
// independent optional capabilities remain false in the rendering projection.
func (a *OpenAdmission) ApplyDecision(ctx context.Context, pin identity.ResolvedResource, window *types.Window, parameters map[string]any) (*Result, error) {
	if ctx == nil || ctx.Err() != nil || window == nil || !pin.ValidUntil.After(time.Now()) {
		return nil, identity.ErrResourceDenied
	}
	if window.Authorization == nil {
		return &Result{Window: window, ExpiresAt: pin.ValidUntil}, nil
	}
	if a == nil || a.Runtime == nil || a.Runtime.Resolver == nil {
		return nil, identity.ErrResourceDenied
	}
	ctx = requestctx.WithResolvedResource(ctx, pin)
	ctx = requestctx.WithWindowTarget(ctx, window.ResourceTarget)
	var resource map[string]any
	spec := window.Authorization
	if spec.Resource != nil && strings.EqualFold(spec.Resource.ID.Source, "resource") {
		if a.Bootstrap == nil || a.MatchSelection == nil {
			return nil, identity.ErrResourceDenied
		}
		var err error
		resource, err = a.Bootstrap(ctx, pin, window, parameters)
		if err != nil {
			if errors.Is(err, authz.ErrDenied) || errors.Is(err, policy.ErrDenied) || errors.Is(err, identity.ErrResourceDenied) {
				return nil, identity.ErrResourceDenied
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return nil, err
			}
			return nil, ErrUnavailable
		}
		if len(resource) == 0 {
			return nil, identity.ErrResourceDenied
		}
		if err = a.MatchSelection(ctx, window, parameters, resource); err != nil {
			return nil, identity.ErrResourceDenied
		}
	}
	bound, err := BindResource(window, window.WindowKey, "", parameters, resource)
	if err != nil {
		return nil, identity.ErrResource
	}
	if strings.EqualFold(spec.Scope, "resource") && bound.ResourceID <= 0 && bound.ResourceIDString == "" {
		return nil, identity.ErrResourceDenied
	}
	result, err := a.Runtime.Apply(ctx, bound)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Denied || result.Window == nil || result.Authorization == nil || !result.Authorization.ExpiresAt.After(time.Now()) || !pin.ValidUntil.After(time.Now()) {
		return nil, identity.ErrResourceDenied
	}
	raw, err := json.Marshal(result.Authorization)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &result.Window.AuthorizationSnapshot); err != nil {
		return nil, err
	}
	result.Window.Resource = &pin
	result.Window.ResourceTarget = window.ResourceTarget
	return result, nil
}

func (a *OpenAdmission) Apply(ctx context.Context, pin identity.ResolvedResource, window *types.Window, parameters map[string]any) (*types.Window, error) {
	result, err := a.ApplyDecision(ctx, pin, window, parameters)
	if err != nil {
		return nil, err
	}
	return result.Window, nil
}
