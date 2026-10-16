package workspace

import (
	"context"
	"encoding/json"
	"sync"

	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
)

// GatewayMenuSource is trusted menu-consumer wiring, separate from a transport
// URL or any action's window key. The managing provider owns menu visibility.
type GatewayMenuSource struct {
	Resource         identity.ResourceRef
	ProviderIdentity string
}

func NewGatewayLayoutMenuReader(gateway *resources.Gateway, sources []GatewayMenuSource) LayoutMenuReader {
	configured := append([]GatewayMenuSource(nil), sources...)
	return func(ctx context.Context) (*LayoutMenuRead, error) {
		if gateway == nil || len(configured) == 0 {
			return nil, identity.ErrResourceDenied
		}
		scoped, finish, err := gateway.BeginReadSequence(ctx, nil)
		if err != nil {
			return nil, err
		}
		ctx = scoped
		var once sync.Once
		var endErr error
		closeRead := func() error { once.Do(func() { endErr = finish() }); return endErr }
		success := false
		defer func() {
			if !success {
				_ = closeRead()
			}
		}()
		type held struct {
			connection resources.Connection
			pin        identity.ResolvedResource
			projection string
		}
		selected := make([]held, 0, len(configured))
		applications := make([]json.RawMessage, 0)
		revisions := make([]string, 0, len(configured))
		for _, source := range configured {
			uri, err := identity.ParseResourceURI(source.Resource.URI)
			if err != nil || uri.Kind != "navigation" || source.ProviderIdentity == "" {
				return nil, identity.ErrResourceDenied
			}
			connection, err := gateway.ConnectionForResource(ctx, uri, source.ProviderIdentity)
			if err != nil {
				return nil, err
			}
			got, err := resources.Get[json.RawMessage](ctx, gateway, connection, source.Resource, nil)
			if err != nil {
				return nil, err
			}
			if got.ResolvedResource == nil || got.MenuProjection == nil {
				return nil, identity.ErrResourceDenied
			}
			var envelope struct {
				Applications []json.RawMessage `json:"applications"`
			}
			if json.Unmarshal(got.Definition, &envelope) != nil || envelope.Applications == nil {
				return nil, identity.ErrResourceDenied
			}
			applications = append(applications, envelope.Applications...)
			pin := *got.ResolvedResource
			selected = append(selected, held{connection, pin, got.MenuProjection.ContentFingerprint})
			revisions = append(revisions, pin.ProviderIdentity+"\x00"+pin.URI+"\x00"+pin.Selector()+"\x00"+pin.ContentFingerprint+"\x00"+got.MenuProjection.ContentFingerprint)
		}
		raw, err := json.Marshal(applications)
		if err != nil {
			return nil, err
		}
		parts, err := json.Marshal(revisions)
		if err != nil {
			return nil, err
		}
		success = true
		return &LayoutMenuRead{Applications: raw, Revision: identity.ContentFingerprint(parts), Close: closeRead, Revalidate: func(release context.Context) error {
			if release == nil || release.Err() != nil {
				return identity.ErrResourceDenied
			}
			for _, hold := range selected {
				got, err := resources.Get[json.RawMessage](ctx, gateway, hold.connection, identity.ResourceRef{URI: hold.pin.URI, Revision: hold.pin.Selector()}, &hold.pin)
				if err != nil {
					return err
				}
				if got.MenuProjection == nil || got.MenuProjection.ContentFingerprint != hold.projection {
					return identity.ErrResourceStale
				}
			}
			return closeRead()
		}}, nil
	}
}
