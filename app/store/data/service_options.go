package data

import (
	"github.com/viant/datly"
	hstate "github.com/viant/xdatly/handler/state"
)

type options struct {
	selectors []*hstate.NamedQuerySelector
	principal string
	isAdmin   bool
}

// Option customizes read execution without changing generated input DTOs.
type Option func(*options)

// WithQuerySelector delegates pagination/projection/order constraints to Datly selectors.
func WithQuerySelector(selectors ...*hstate.NamedQuerySelector) Option {
	return func(o *options) {
		o.selectors = append(o.selectors, selectors...)
	}
}

// WithPrincipal enforces conversation visibility rules for reads.
func WithPrincipal(userID string) Option {
	return func(o *options) {
		o.principal = userID
	}
}

// WithAdminPrincipal bypasses visibility restrictions.
func WithAdminPrincipal(userID string) Option {
	return func(o *options) {
		o.principal = userID
		o.isAdmin = true
	}
}

func toOperateOptions(opts []Option) []datly.OperateOption {
	callOpts := collectOptions(opts)
	if len(callOpts.selectors) == 0 {
		return nil
	}
	return []datly.OperateOption{
		datly.WithSessionOptions(datly.WithQuerySelectors(callOpts.selectors...)),
	}
}

func collectOptions(opts []Option) *options {
	if len(opts) == 0 {
		return &options{}
	}
	callOpts := &options{}
	for _, opt := range opts {
		if opt != nil {
			opt(callOpts)
		}
	}
	return callOpts
}
