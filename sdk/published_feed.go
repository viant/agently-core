package sdk

import (
	"context"
	"encoding/json"

	identity "github.com/viant/agently-core/protocol/resource"
	shared "github.com/viant/forge/backend/resourcehost"
)

func (c *backendClient) publishedFeed(ctx context.Context, uri string) (*FeedSpec, bool, error) {
	if c.primitiveProviders == nil || !c.primitiveProviders.OwnsPublishedMetadata(uri) {
		return nil, false, nil
	}
	parsed, err := identity.ParseResourceURI(uri)
	if err != nil || parsed.Kind != "toolfeed" {
		return nil, true, identity.ErrResourceDenied
	}
	loaded, err := shared.Load[FeedSpec](ctx, c.primitiveProviders, uri, shared.PublishedCodec[FeedSpec]{
		ID: "toolfeed.v1", Decode: func(_ context.Context, _ string, raw json.RawMessage) (FeedSpec, error) { return decodeFeedSpec(raw) },
		Clone: func(value FeedSpec) (FeedSpec, error) {
			raw, err := json.Marshal(value)
			if err != nil {
				return FeedSpec{}, err
			}
			return decodeFeedSpec(raw)
		},
	})
	if err != nil {
		return nil, true, err
	}
	value := loaded.Value
	value.URI = uri
	value.ProviderIdentity = loaded.Resource.ProviderIdentity
	value.DefinitionFingerprint = loaded.Resource.ContentFingerprint
	value.DefinitionRevision = ""
	normalizeFeedSpec(&value)
	return &value, true, nil
}
