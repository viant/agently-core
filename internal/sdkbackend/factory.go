package sdkbackend

import (
	"errors"

	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/sdk"
	agentsvc "github.com/viant/agently-core/service/agent"
)

// New builds the internal in-process backend used by local handlers and
// server/bootstrap wiring.
func New(agent *agentsvc.Service, conv conversation.Client) (sdk.Backend, error) {
	return sdk.NewEmbedded(agent, conv)
}

// FromRuntime builds the internal in-process backend from a runtime.
func FromRuntime(rt *executor.Runtime) (sdk.Backend, error) {
	if rt == nil {
		return nil, errors.New("runtime was nil")
	}
	return sdk.NewEmbeddedFromRuntime(rt)
}
