package executor

import (
	"context"
	"strings"

	agent "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
)

// The wrapper retains configured agent selection while routing canonical
// published documents and prompt dependencies through the shared gateway.
// Published objects never enter the legacy finder's process-wide object cache.
type gatewayAgentFinder struct {
	runtime  *Runtime
	fallback agent.Finder
}

func (f *gatewayAgentFinder) Find(ctx context.Context, key string) (*agent.Agent, error) {
	var value *agent.Agent
	var err error
	published := strings.HasPrefix(key, "agent://")
	if published {
		if f.runtime.PrimitiveProviders == nil {
			return nil, identity.ErrResourceDenied
		}
		value, err = resources.NewAgentReader(f.runtime.PrimitiveProviders).Load(ctx, key)
	} else if f.fallback != nil {
		value, err = f.fallback.Find(ctx, key)
	} else {
		return nil, identity.ErrResourceDenied
	}
	if err != nil || value == nil {
		return value, err
	}
	return f.materializePrompts(ctx, value, published)
}
func (f *gatewayAgentFinder) All() []*agent.Agent {
	if catalog, ok := f.fallback.(interface{ All() []*agent.Agent }); ok {
		return catalog.All()
	}
	return nil
}
func (f *gatewayAgentFinder) Version() int64 {
	if catalog, ok := f.fallback.(interface{ Version() int64 }); ok {
		return catalog.Version()
	}
	return 0
}
func (f *gatewayAgentFinder) materializePrompts(ctx context.Context, value *agent.Agent, closed bool) (*agent.Agent, error) {
	copy := *value
	load := func(prompt *binding.Prompt) (*binding.Prompt, error) {
		if prompt == nil {
			return nil, nil
		}
		if closed && prompt.Text != "" {
			// Producer-materialized text is part of the owning immutable agent.
			// A retained locator must not turn it into a fresh child grant/read.
			return &binding.Prompt{Text: prompt.Text, Engine: prompt.Engine}, nil
		}
		if !strings.HasPrefix(prompt.URI, "prompt://") {
			return prompt, nil
		}
		if f.runtime.PrimitiveProviders == nil {
			return nil, identity.ErrResourceDenied
		}
		return resources.NewPromptReader(f.runtime.PrimitiveProviders).Load(ctx, prompt.URI)
	}
	var err error
	if copy.Prompt, err = load(value.Prompt); err != nil {
		return nil, err
	}
	if copy.SystemPrompt, err = load(value.SystemPrompt); err != nil {
		return nil, err
	}
	if copy.InstructionPrompt, err = load(value.InstructionPrompt); err != nil {
		return nil, err
	}
	if copy.Instruction, err = load(value.Instruction); err != nil {
		return nil, err
	}
	// Follow-up nodes also carry prompts. Clone them before replacing slots.
	copy.FollowUps = make([]*agent.Chain, len(value.FollowUps))
	for index, chain := range value.FollowUps {
		if chain == nil {
			continue
		}
		cloned := *chain
		if cloned.Query, err = load(chain.Query); err != nil {
			return nil, err
		}
		if chain.When != nil {
			when := *chain.When
			if when.Query, err = load(chain.When.Query); err != nil {
				return nil, err
			}
			cloned.When = &when
		}
		copy.FollowUps[index] = &cloned
	}
	return &copy, nil
}
