// Package adapter wires the datasource service to the production tool
// dispatch path in internal/tool/registry. It is deliberately kept in its own
// package so that service/datasource stays free of runtime-package imports
// and remains trivially unit-testable with an in-memory ToolExecutor.
//
// Typical wiring at process startup:
//
//	reg := registry.New(...)
//	dsSvc := datasource.New(datasource.Options{
//	    Store:    dsStore,
//	    Executor: adapter.FromRegistry(reg),
//	})
//
// The adapter preserves ctx end-to-end — auth propagates via
// protocol/mcp/manager.WithAuthTokenContext inside Registry.Execute, same as
// every other tool call.
package adapter

import (
	"context"

	toolreg "github.com/viant/agently-core/internal/tool/registry"
)

// ToolRegistry is the subset of *registry.Registry we depend on. Defining the
// interface here lets tests substitute a stub without pulling in the full
// runtime registry.
type ToolRegistry interface {
	Execute(ctx context.Context, name string, args map[string]interface{}) (string, error)
}

// FromRegistry wraps any ToolRegistry (typically *registry.Registry) so it
// satisfies datasource.ToolExecutor. Returns nil when reg is nil so callers
// can pass the result directly into datasource.Options.Executor — the
// datasource service rejects nil at Fetch time with a clear error.
func FromRegistry(reg ToolRegistry) ToolRegistry { return reg }

// Assert compile-time compatibility: *registry.Registry already satisfies
// ToolRegistry by method set. This keeps a failed drift (signature change
// upstream) loud at build time rather than at call time.
var _ ToolRegistry = (*toolreg.Registry)(nil)
