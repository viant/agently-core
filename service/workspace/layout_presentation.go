package workspace

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/viant/agently-core/service/ui/layoutgate"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/authz"
)

// SetLayoutPresentationResolver binds the host's verified display facts source.
// The existing resource and tool admission paths retain their own owners.
func (h *MetadataHandler) SetLayoutPresentationResolver(resolver permittedview.Resolver) {
	if h != nil {
		h.presentationResolver = resolver
	}
}

func (h *MetadataHandler) layoutPresentationPrincipal(ctx context.Context, layout *Layout) (*permittedview.Snapshot, error) {
	if h.presentationResolver == nil || !layoutHasPresentationGuards(layout) {
		return nil, nil
	}
	snapshot, err := h.presentationResolver.Resolve(ctx, &permittedview.Request{IncludePrincipal: true})
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.AuthorizationVersion == "" || !snapshot.ExpiresAt.After(time.Now()) {
		return nil, layoutgate.ErrFactsUnavailable
	}
	return snapshot, nil
}

func layoutHasPresentationGuards(layout *Layout) bool {
	var menusHaveGuards func([]LayoutMenu) bool
	menusHaveGuards = func(menus []LayoutMenu) bool {
		for _, menu := range menus {
			if menu.VisibleWhen != nil || menu.HiddenWhen != nil || menu.DisabledWhen != nil || menu.ReadOnlyWhen != nil || menusHaveGuards(menu.Children) {
				return true
			}
		}
		return false
	}
	for _, app := range layout.Applications {
		if app.VisibleWhen != nil || app.HiddenWhen != nil || app.DisabledWhen != nil || app.ReadOnlyWhen != nil || menusHaveGuards(app.Menus) {
			return true
		}
	}
	return layout.Topbar != nil && menusHaveGuards(layout.Topbar.Actions)
}

func layoutPresentationFailure(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return metadataFailure(http.StatusRequestTimeout, "layout request canceled", err)
	case errors.Is(err, context.DeadlineExceeded):
		return metadataFailure(http.StatusGatewayTimeout, "layout presentation timed out", err)
	case errors.Is(err, authz.ErrIdentityDenied):
		return metadataFailure(http.StatusUnauthorized, "authentication required", err)
	case errors.Is(err, authz.ErrDenied):
		return metadataFailure(http.StatusForbidden, "layout presentation denied", err)
	default:
		return metadataFailure(http.StatusServiceUnavailable, "layout presentation unavailable", err)
	}
}
