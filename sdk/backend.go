package sdk

import (
	"errors"

	"github.com/viant/agently-core/app/executor"
)

// Deprecated: prefer internal/sdkbackend.FromRuntime for server/bootstrap wiring
// and NewLocalHTTPFromRuntime for SDK callers.
//
// NewBackendFromRuntime builds the in-process backend implementation used by
// local handlers and server wiring.
func NewBackendFromRuntime(rt *executor.Runtime) (Backend, error) {
	if rt == nil {
		return nil, errors.New("runtime was nil")
	}
	return newBackendFromRuntime(rt)
}
