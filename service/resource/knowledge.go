package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	primitive "github.com/viant/forge/backend/provider/contract"
	shared "github.com/viant/forge/backend/resourcehost"
)

// ErrKnowledgeDocumentNotFound reports a missing child of an admitted publication.
var ErrKnowledgeDocumentNotFound = fmt.Errorf("published knowledge document not found: %w", fs.ErrNotExist)

// ReadKnowledge returns an owned copy of a producer-validated corpus through
// the existing publication loader. Admission and selection belong to the gateway.
func ReadKnowledge(ctx context.Context, gateway *Gateway, ref string) (*shared.PublishedValue[primitive.KnowledgeEnvelope], error) {
	loaded, err := loadKnowledge(ctx, gateway, ref)
	if err != nil {
		return nil, err
	}
	value := loaded.Value
	value.Files = maps.Clone(value.Files)
	value.Metadata.Fields = maps.Clone(value.Metadata.Fields)
	value.Sources = slices.Clone(value.Sources)
	for i := range value.Sources {
		value.Sources[i].Paths = slices.Clone(value.Sources[i].Paths)
	}
	loaded.Value = value
	return loaded, nil
}

// ReadKnowledgeFile selects one exact child from the immutable published map.
// The caller supplies a previously admitted root and a manifest-relative path.
func ReadKnowledgeFile(ctx context.Context, gateway *Gateway, ref, child string) ([]byte, error) {
	if gateway == nil || child == "." || !fs.ValidPath(child) || strings.ContainsAny(child, "\\:\x00") {
		return nil, identity.ErrResourceDenied
	}
	loaded, err := loadKnowledge(ctx, gateway, ref)
	if err != nil {
		return nil, err
	}
	// The loader verified the exact publication pin before returning its map.
	body, found := loaded.Value.Files[child]
	if !found {
		return nil, ErrKnowledgeDocumentNotFound
	}
	return []byte(body), nil
}

// loadKnowledge keeps the shared compiled envelope immutable. Exact file reads
// reuse its path map without cloning it, while corpus readers receive a copy.
func loadKnowledge(ctx context.Context, gateway *Gateway, ref string) (*shared.PublishedValue[primitive.KnowledgeEnvelope], error) {
	uri, err := identity.ParseResourceURI(ref)
	if err != nil || uri.Kind != primitive.KnowledgeKind {
		return nil, identity.ErrResourceDenied
	}
	return shared.Load(ctx, gateway, ref, shared.PublishedCodec[primitive.KnowledgeEnvelope]{
		ID:        "knowledge.v1",
		Immutable: true,
		Decode: func(_ context.Context, _ string, raw json.RawMessage) (primitive.KnowledgeEnvelope, error) {
			var value primitive.KnowledgeEnvelope
			if err := json.Unmarshal(raw, &value); err != nil {
				return value, err
			}
			return value, nil
		},
	})
}
