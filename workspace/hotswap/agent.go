package hotswap

import (
	"context"

	"github.com/viant/agently-core/protocol/agent"
	agentfinder "github.com/viant/agently-core/protocol/agent/finder"
)

// NewAgentAdaptor creates a Reloadable that reloads agents on workspace changes.
func NewAgentAdaptor(loader agent.Loader, finder *agentfinder.Finder) Reloadable {
	return NewAdaptor[*agent.Agent](
		func(ctx context.Context, name string) (*agent.Agent, error) {
			return loader.Load(ctx, name)
		},
		func(name string, a *agent.Agent) { finder.Add(name, a) },
		func(name string) { finder.Remove(name) },
	)
}
