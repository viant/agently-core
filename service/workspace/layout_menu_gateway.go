package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
)

// GatewayMenuSource is trusted menu-consumer wiring, separate from a transport
// URL or any action's window key. The managing provider owns menu visibility.
type GatewayMenuSource struct {
	Resource         identity.ResourceRef
	ProviderIdentity string
	ApplicationID    string
}

func NewGatewayLayoutMenuReader(gateway *resources.Gateway, sources []GatewayMenuSource) LayoutMenuReader {
	configured := append([]GatewayMenuSource(nil), sources...)
	return newGatewayLayoutMenuReader(gateway, func(context.Context) ([]GatewayMenuSource, error) { return configured, nil }, false)
}

func newGatewayLayoutMenuReader(gateway *resources.Gateway, sources func(context.Context) ([]GatewayMenuSource, error), providerOwned bool) LayoutMenuReader {
	return func(ctx context.Context) (*LayoutMenuRead, error) {
		if gateway == nil {
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
		configured, err := sources(ctx)
		if err != nil {
			return nil, err
		}
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
				return nil, fmt.Errorf("navigation connection %s: %w", source.Resource.URI, err)
			}
			got, err := resources.GetWithCodec[json.RawMessage](ctx, gateway, connection, source.Resource, nil, resources.DefinitionCodec[json.RawMessage]{ID: "agently.navigation.layout.v1", Parse: validateNavigationDefinition})
			if err != nil {
				return nil, fmt.Errorf("navigation read %s: %w", source.Resource.URI, err)
			}
			if got.ResolvedResource == nil || len(got.Definition) == 0 {
				return nil, fmt.Errorf("navigation projection %s: %w", source.Resource.URI, identity.ErrResourceDenied)
			}
			// Typed Get already verifies legacy projections. Standard MCP reads
			// deliver provider-authorized content directly, without that envelope.
			projection := identity.ContentFingerprint(got.Definition)
			var envelope struct {
				Applications []json.RawMessage `json:"applications"`
			}
			if json.Unmarshal(got.Definition, &envelope) != nil || envelope.Applications == nil {
				return nil, identity.ErrResourceDenied
			}
			for _, raw := range envelope.Applications {
				if source.ApplicationID != "" {
					var app struct {
						ID string `json:"id"`
					}
					if err := json.Unmarshal(raw, &app); err != nil {
						return nil, err
					}
					if app.ID != source.ApplicationID {
						continue
					}
				}
				bound, err := bindNavigationOwner(raw, uri.Namespace, connection.ProviderIdentity, connection.Name)
				if err != nil {
					return nil, err
				}
				applications = append(applications, bound)
			}
			pin := *got.ResolvedResource
			selected = append(selected, held{connection, pin, projection})
			revisions = append(revisions, pin.ProviderIdentity+"\x00"+pin.URI+"\x00"+pin.Selector()+"\x00"+pin.ContentFingerprint+"\x00"+projection+"\x00"+source.ApplicationID)
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
		return &LayoutMenuRead{Applications: raw, Revision: identity.ContentFingerprint(parts), Close: closeRead, ProviderOwnedApplications: providerOwned, Revalidate: func(release context.Context) error {
			if release == nil || release.Err() != nil {
				return identity.ErrResourceDenied
			}
			for _, hold := range selected {
				got, err := resources.Get[json.RawMessage](ctx, gateway, hold.connection, identity.ResourceRef{URI: hold.pin.URI, Revision: hold.pin.Selector()}, &hold.pin)
				if err != nil {
					return err
				}
				if identity.ContentFingerprint(got.Definition) != hold.projection {
					return identity.ErrResourceStale
				}
			}
			return closeRead()
		}}, nil
	}
}

// Carry the discovered owner for same-namespace destinations. This is routing
// provenance, not an authorization grant; reads still go to the owning MCP.
func bindNavigationOwner(raw json.RawMessage, namespace, owner string, connection ...string) (json.RawMessage, error) {
	var app map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&app); err != nil {
		return nil, err
	}
	app["providerIdentity"] = owner
	app["resourceNamespace"] = namespace
	if len(connection) > 0 {
		app["resourceConnection"] = connection[0]
	}
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				visit(child)
			}
		case map[string]any:
			if action, ok := node["action"].(map[string]any); ok {
				if resource, ok := action["resourceUri"].(string); ok {
					uri, err := identity.ParseResourceURI(resource)
					if err == nil && uri.Kind == "window" && uri.Namespace == namespace {
						action["providerIdentity"] = owner
					}
				}
			}
			for _, key := range []string{"menus", "children", "items"} {
				visit(node[key])
			}
		}
	}
	visit(app)
	return json.Marshal(app)
}

// Validate the portable shape inside the existing representation cache. A bad
// publication must be evicted so a corrected source can recover without an
// application restart. Provider references are checked in the composed layout.
func validateNavigationDefinition(raw json.RawMessage) (json.RawMessage, error) {
	var document struct {
		Applications []LayoutApplication `json:"applications"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	if document.Applications == nil {
		return nil, fmt.Errorf("navigation applications are required")
	}
	seen := map[string]bool{}
	for _, app := range document.Applications {
		if app.ID == "" || app.Title == "" || seen[app.ID] {
			return nil, fmt.Errorf("invalid or duplicate application %q", app.ID)
		}
		seen[app.ID] = true
		if err := validateMenus(app.Menus, map[string]bool{}, nil); err != nil {
			return nil, fmt.Errorf("application %s: %w", app.ID, err)
		}
	}
	return append(json.RawMessage(nil), raw...), nil
}
