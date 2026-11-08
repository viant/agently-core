package resource

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	identity "github.com/viant/agently-core/protocol/resource"
	primitive "github.com/viant/forge/backend/provider/contract"
	shared "github.com/viant/forge/backend/resourcehost"
)

// ReadKnowledge reads a producer-validated corpus through the existing local
// publication loader. Every call retains that loader's admission and selection
// fences; no remote source or filesystem fallback is attempted.
func ReadKnowledge(ctx context.Context, gateway *Gateway, ref string) (*shared.PublishedValue[primitive.KnowledgeEnvelope], error) {
	uri, err := identity.ParseResourceURI(ref)
	if err != nil || uri.Kind != primitive.KnowledgeKind {
		return nil, identity.ErrResourceDenied
	}
	return shared.Load(ctx, gateway, ref, shared.PublishedCodec[primitive.KnowledgeEnvelope]{
		ID: "knowledge.v1",
		Decode: func(_ context.Context, _ string, raw json.RawMessage) (primitive.KnowledgeEnvelope, error) {
			var value primitive.KnowledgeEnvelope
			if err := json.Unmarshal(raw, &value); err != nil {
				return value, err
			}
			return value, nil
		},
		Clone: func(value primitive.KnowledgeEnvelope) (primitive.KnowledgeEnvelope, error) {
			value.Files = maps.Clone(value.Files)
			value.Metadata.Fields = maps.Clone(value.Metadata.Fields)
			value.Sources = slices.Clone(value.Sources)
			for i := range value.Sources {
				value.Sources[i].Paths = slices.Clone(value.Sources[i].Paths)
			}
			return value, nil
		},
	})
}
