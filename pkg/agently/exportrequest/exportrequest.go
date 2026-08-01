// Package exportrequest carries trusted export-operation identity outside the
// model-visible tool arguments.
package exportrequest

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

const Header = "X-Agently-Export-Request-ID"

// NewID allocates one trusted host/runtime operation ID.
func NewID() string {
	return uuid.NewString()
}

type contextKey struct{}

// WithID binds one trusted transport operation ID to ctx. Callers must reuse
// the same ID when retrying the same submission and allocate a new ID for a
// new user command or click.
func WithID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, id)
}

// ID returns the trusted export-operation ID, if one was injected by the
// runtime or host boundary.
func ID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(contextKey{}).(string)
	return strings.TrimSpace(id)
}
