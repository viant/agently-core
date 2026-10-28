package resource

import (
	"context"

	"github.com/viant/agently-core/protocol/binding"
	identity "github.com/viant/agently-core/protocol/resource"
	primitive "github.com/viant/forge/backend/provider/contract"
)

// PromptDefinition is materialized template text and its authored engine. A
// prompt resource is distinct from a scenario/playbook profile.
type PromptDefinition = primitive.PromptEnvelope
type PromptArgument = primitive.PromptArgument
type PromptReader struct{ Gateway *Gateway }

func NewPromptReader(g *Gateway) *PromptReader { return &PromptReader{Gateway: g} }
func (r *PromptReader) Load(ctx context.Context, uri string) (*binding.Prompt, error) {
	value, err := r.Definition(ctx, uri)
	if err != nil {
		return nil, err
	}
	return &binding.Prompt{Text: value.Text, Engine: value.Engine}, nil
}

func (r *PromptReader) Definition(ctx context.Context, uri string) (*PromptDefinition, error) {
	if value, owned, err := loadPublishedAI[PromptDefinition](ctx, r.Gateway, uri, "prompt", "prompt-envelope.v1", decodePublishedJSON[PromptDefinition]); owned {
		if err != nil {
			return nil, err
		}
		return &value, nil
	}

	got, err := ResolveDefinition[PromptDefinition](ctx, r.Gateway, "prompt", uri, nil)
	if err != nil {
		return nil, err
	}
	if got.Definition.SchemaVersion != 1 {
		return nil, identity.ErrResource
	}
	return &got.Definition, nil
}
