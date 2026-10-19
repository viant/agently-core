package workspace

import (
	"context"
	"encoding/json"
	"fmt"

	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"gopkg.in/yaml.v3"
)

// LayoutApplicationSource selects presentation order, never grants access.
// Names, icons and menu content remain owned by the managing MCP.
type LayoutApplicationSource struct {
	Connection    string `yaml:"connection"`
	Resource      string `yaml:"resource"`
	ApplicationID string `yaml:"applicationId"`
}

// ApplicationMenuReader uses the same cached primitive gateway as windows and
// reports. Only explicitly configured connections participate in discovery.
func ApplicationMenuReader(gateway *resources.Gateway, raw []byte) (LayoutMenuReader, error) {
	var config struct {
		Sources         []LayoutApplicationSource `yaml:"applicationSources"`
		IncludeUnlisted bool                      `yaml:"includeUnlistedApplications"`
	}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	if len(config.Sources) == 0 {
		return nil, nil
	}
	roots := []resources.Connection{}
	seenRoots, seenSelections := map[string]bool{}, map[string]bool{}
	for _, source := range config.Sources {
		uri, err := identity.ParseResourceURI(source.Resource)
		key := source.Connection + "\x00" + source.Resource + "\x00" + source.ApplicationID
		if err != nil || uri.Kind != "navigation" || source.Connection == "" || source.ApplicationID == "" || seenSelections[key] {
			return nil, fmt.Errorf("invalid or duplicate application navigation source")
		}
		seenSelections[key] = true
		if !seenRoots[source.Connection] {
			roots = append(roots, resources.Connection{Name: source.Connection})
			seenRoots[source.Connection] = true
		}
	}
	return newGatewayLayoutMenuReader(gateway, func(ctx context.Context) ([]GatewayMenuSource, error) {
		available := map[string]resources.Located[json.RawMessage]{}
		var ordered []resources.Located[json.RawMessage]
		for cursor := ""; ; {
			page, err := resources.ListPageFrom[json.RawMessage](ctx, gateway, roots, "navigation", resources.CatalogQuery{Limit: 100, Cursor: cursor}, false)
			if err != nil {
				return nil, err
			}
			for _, row := range page.Items {
				available[row.Connection.Name+"\x00"+row.Resource.URI] = row
				ordered = append(ordered, row)
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor == cursor {
				return nil, fmt.Errorf("navigation pagination made no progress")
			}
			cursor = page.NextCursor
		}
		out := []GatewayMenuSource{}
		selected := map[string]bool{}
		for _, source := range config.Sources {
			key := source.Connection + "\x00" + source.Resource
			if row, ok := available[key]; ok {
				out = append(out, GatewayMenuSource{Resource: identity.ResourceRef{URI: source.Resource}, ProviderIdentity: row.Connection.ProviderIdentity, ApplicationID: source.ApplicationID})
				selected[key] = true
			}
		}
		if config.IncludeUnlisted {
			for _, row := range ordered {
				if !selected[row.Connection.Name+"\x00"+row.Resource.URI] {
					out = append(out, GatewayMenuSource{Resource: identity.ResourceRef{URI: row.Resource.URI}, ProviderIdentity: row.Connection.ProviderIdentity})
				}
			}
		}
		return out, nil
	}, true), nil
}
