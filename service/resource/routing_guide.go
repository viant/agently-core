package resource

import (
	"context"
	"encoding/json"

	identity "github.com/viant/agently-core/protocol/resource"
	primitive "github.com/viant/forge/backend/provider/contract"
	shared "github.com/viant/forge/backend/resourcehost"
)

// ReadRoutingGuide reads only from a registered, locally admitted cohort. The
// published codec caches the body by immutable selection and fences admission
// on every read, including cache hits.
func ReadRoutingGuide(ctx context.Context, gateway *Gateway, ref string) (string, error) {
	uri, err := identity.ParseResourceURI(ref)
	if err != nil || uri.Kind != primitive.RoutingKind {
		return "", identity.ErrResourceDenied
	}
	value, err := shared.Load(ctx, gateway, ref, shared.PublishedCodec[string]{
		ID:        "routing-guide.v1",
		Immutable: true,
		Decode: func(_ context.Context, _ string, raw json.RawMessage) (string, error) {
			var envelope primitive.RoutingGuideEnvelope
			if err := json.Unmarshal(raw, &envelope); err != nil {
				return "", err
			}
			guide, err := primitive.ParseRoutingGuide(envelope)
			return guide.Body, err
		},
	})
	if err != nil {
		return "", err
	}
	return value.Value, nil
}
