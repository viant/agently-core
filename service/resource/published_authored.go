package resource

import (
	"context"
	"encoding/json"
	intent "github.com/viant/agently-core/protocol/intent"
	shared "github.com/viant/forge/backend/resourcehost"

	identity "github.com/viant/agently-core/protocol/resource"
	primitive "github.com/viant/forge/backend/provider/contract"
)

// Published playbooks retain authoring text beside the producer's materialized
// profile. Native workspace definitions keep their original flat profile shape.
func authoredProfileBytes(raw json.RawMessage) (json.RawMessage, error) {
	var envelope primitive.PlaybookEnvelope[json.RawMessage]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if envelope.SchemaVersion == 0 {
		return raw, nil
	}
	if envelope.SchemaVersion != 1 || len(envelope.Profile) == 0 || string(envelope.Profile) == "null" {
		return nil, identity.ErrResource
	}
	return envelope.Profile, nil
}

func publishedProfile(ctx context.Context, g *Gateway, uri string) (*intent.Profile, bool, error) {
	if g == nil || !g.OwnsPublishedMetadata(uri) {
		return nil, false, nil
	}
	parsed, err := identity.ParseResourceURI(uri)
	if err != nil || (parsed.Kind != "playbook" && parsed.Kind != "intent") {
		return nil, true, identity.ErrResourceDenied
	}
	loaded, err := shared.Load[intent.Profile](ctx, g, uri, shared.PublishedCodec[intent.Profile]{ID: "profile-envelope.v1", Decode: func(_ context.Context, _ string, raw json.RawMessage) (intent.Profile, error) {
		return DecodeIntentDefinition(raw)
	}, Clone: clonePublishedAI[intent.Profile]})
	if err != nil {
		return nil, true, err
	}
	value := loaded.Value
	value.ResourceURI = uri
	value.ResourceProviderIdentity = loaded.Resource.ProviderIdentity
	value.ResourceFingerprint = loaded.Resource.ContentFingerprint
	for _, message := range value.Messages {
		if message.URI != "" {
			return nil, true, identity.ErrResource
		}
	}
	return &value, true, nil
}
