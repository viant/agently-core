package resource

import (
	"context"
	"encoding/json"

	"github.com/viant/agently-core/protocol/evaluation"
	identity "github.com/viant/agently-core/protocol/resource"
)

type EvalReader struct{ Gateway *Gateway }

func NewEvalReader(g *Gateway) *EvalReader { return &EvalReader{Gateway: g} }
func (r *EvalReader) Load(ctx context.Context, uri string) (*evaluation.Definition, error) {
	if value, owned, err := loadPublishedAI[evaluation.Definition](ctx, r.Gateway, uri, "eval", "eval-envelope.v1", decodePublishedJSON[evaluation.Definition]); owned {
		if err != nil {
			return nil, err
		}
		return &value, nil
	}

	got, err := ResolveDefinition[evaluation.Definition](ctx, r.Gateway, "eval", uri, nil)
	if err != nil {
		return nil, err
	}
	if got.Definition.SchemaVersion != 1 {
		return nil, identity.ErrResource
	}
	return &got.Definition, nil
}
func (r *EvalReader) LoadAll(ctx context.Context) ([]*evaluation.Definition, error) {
	rows, err := authoredPages[json.RawMessage](ctx, r.Gateway, "eval", false)
	if err != nil {
		return nil, err
	}
	result := make([]*evaluation.Definition, 0, len(rows))
	for _, row := range rows {
		value, err := r.Load(ctx, row.Resource.URI)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}
