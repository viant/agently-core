package datlycompat

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/viant/datly"
	"github.com/viant/datly/repository"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/datly/view"
	xhandler "github.com/viant/xdatly/handler"
)

const reportSelectionErr = "report metadata had no selectable dimensions or measures"

// AddComponent preserves normal registration, but falls back to plain component
// registration when newer datly builds try to synthesize an invalid report route.
func AddComponent(ctx context.Context, srv *datly.Service, component *repository.Component) error {
	var originalView = (*repository.Component)(nil)
	if component != nil {
		cloned := *component
		originalView = &cloned
	}

	err := srv.AddComponent(ctx, component)
	if err == nil || !strings.Contains(err.Error(), reportSelectionErr) {
		return err
	}

	if component != nil && originalView != nil {
		component.View = originalView.View
	}

	components := repository.NewComponents(ctx)
	components.Components = []*repository.Component{component}
	components.Resource = srv.Resource()
	if component != nil && component.View != nil {
		if resource := component.View.GetResource(); resource != nil {
			components.Resource = resource
		}
	}

	if fallbackErr := srv.AddComponents(ctx, components); fallbackErr != nil {
		return fmt.Errorf("component registration fallback failed after report synthesis error: %w (fallback: %v)", err, fallbackErr)
	}
	return nil
}

func AddHandler(ctx context.Context, srv *datly.Service, path *contract.Path, handler xhandler.Handler, options ...repository.ComponentOption) (*repository.Component, error) {
	options = append([]repository.ComponentOption{repository.WithHandler(handler)}, options...)
	component, err := repository.NewComponent(path, options...)
	if err != nil {
		return nil, err
	}
	if component.View.Name == "" {
		rType := reflect.TypeOf(handler)
		if rType.Kind() == reflect.Ptr {
			rType = rType.Elem()
		}
		component.View.Name = rType.Name()
	}
	component.View.Mode = view.ModeHandler
	if err := AddComponent(ctx, srv, component); err != nil {
		return nil, err
	}
	return component, nil
}
