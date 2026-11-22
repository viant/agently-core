package ui

import (
	"context"
	"log"

	"github.com/viant/agently-core/protocol/primitive/observe"
	"github.com/viant/agently-core/service/ui/permittedview"
	forgetypes "github.com/viant/forge/backend/types"
)

// Both managed delivery paths use the same configured display-facts projection.
// Source admission and datasource authorization remain with their existing owners.
func projectManagedWindowPresentation(ctx context.Context, window *forgetypes.Window, provider WindowResourceProvider, capabilities *permittedview.Runtime, useDefaults bool) (*forgetypes.Window, error) {
	span := observe.Start(ctx, "http.window.project", observe.Labels{Kind: "window"})
	var projected *permittedview.Result
	var err error
	configured, hasConfigured := provider.(interface{ HasWindowUIProjection() bool })
	project, hasProjector := provider.(interface {
		ProjectWindowWithInput(context.Context, *permittedview.BoundView) (*forgetypes.Window, error)
	})
	if window.AuthorizationSnapshot == nil && hasConfigured && configured.HasWindowUIProjection() && hasProjector {
		var next *forgetypes.Window
		next, err = project.ProjectWindowWithInput(ctx, &permittedview.BoundView{Window: window})
		projected = &permittedview.Result{Window: next}
	} else {
		runtime := capabilities
		if runtime == nil && useDefaults {
			runtime = permittedview.DefaultRuntime()
		}
		projected, err = runtime.ProjectPrincipal(ctx, window)
	}
	span.End(observe.OutcomeFor(err), observe.CacheUnknown)
	if err != nil || projected == nil {
		return nil, err
	}
	for _, diagnostic := range projected.Diagnostics {
		log.Printf("window presentation: %s", diagnostic.Code)
	}
	return projected.Window, nil
}
