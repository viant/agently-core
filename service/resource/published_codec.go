package resource

import (
	"bytes"
	"context"
	"encoding/json"

	identity "github.com/viant/agently-core/protocol/resource"
	shared "github.com/viant/forge/backend/resourcehost"
)

// The gateway owns admission, immutable source selection and the bounded typed
// cache. Consumer codecs retain only their native document-format adaptation.
func loadPublishedAI[T any](ctx context.Context, g *Gateway, uri, kind, codecID string, decode func(context.Context, string, json.RawMessage) (T, error)) (T, bool, error) {
	var zero T
	if g == nil || !g.OwnsPublishedMetadata(uri) {
		return zero, false, nil
	}
	parsed, err := identity.ParseResourceURI(uri)
	if err != nil || parsed.Kind != kind {
		return zero, true, identity.ErrResourceDenied
	}
	value, err := shared.Load[T](ctx, g, uri, shared.PublishedCodec[T]{ID: codecID, Decode: decode, Clone: clonePublishedAI[T]})
	if err != nil {
		return zero, true, err
	}
	return value.Value, true, nil
}
func clonePublishedAI[T any](value T) (T, error) {
	var copy T
	raw, err := json.Marshal(value)
	if err != nil {
		return copy, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err = decoder.Decode(&copy)
	return copy, err
}
func decodePublishedJSON[T any](_ context.Context, _ string, raw json.RawMessage) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&value)
	return value, err
}
