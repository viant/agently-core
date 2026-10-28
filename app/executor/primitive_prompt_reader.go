package executor

import (
	"context"

	"github.com/viant/agently-core/protocol/binding"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
)

type runtimePromptReader struct{ runtime *Runtime }

func (r *runtimePromptReader) Load(ctx context.Context, uri string) (*binding.Prompt, error) {
	if r.runtime.PrimitiveProviders == nil {
		return nil, identity.ErrResourceDenied
	}
	return resources.NewPromptReader(r.runtime.PrimitiveProviders).Load(ctx, uri)
}
