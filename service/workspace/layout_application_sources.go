package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/viant/agently-core/protocol/mcp/auth/integrate"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"

	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"gopkg.in/yaml.v3"
)

// LayoutApplicationSource selects presentation order, never grants access.
// Names, icons and menu content remain owned by the managing MCP.
type LayoutGlobalStyleSource struct {
	Connection string `yaml:"connection"`
	Resource   string `yaml:"resource"`
}

type LayoutApplicationSource struct {
	AllowStyleOverrides bool   `yaml:"allowStyleOverrides,omitempty"`
	Connection          string `yaml:"connection"`
	Resource            string `yaml:"resource"`
	ApplicationID       string `yaml:"applicationId"`
}

// ApplicationMenuReader uses the same cached primitive gateway as windows and
// reports. Only explicitly configured connections participate in discovery.
func ApplicationMenuReader(gateway *resources.Gateway, raw []byte) (LayoutMenuReader, error) {
	var config struct {
		GlobalStyle     *LayoutGlobalStyleSource  `yaml:"globalStyleSource"`
		Sources         []LayoutApplicationSource `yaml:"applicationSources"`
		IncludeUnlisted bool                      `yaml:"includeUnlistedApplications"`
	}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	if len(config.Sources) == 0 && config.GlobalStyle == nil {
		return nil, nil
	}
	if config.GlobalStyle != nil {
		uri, err := identity.ParseResourceURI(config.GlobalStyle.Resource)
		if err != nil || uri.Kind != "style" || config.GlobalStyle.Connection == "" {
			return nil, fmt.Errorf("invalid global stylesheet source")
		}
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
	menuReader := newGatewayLayoutMenuReader(gateway, func(ctx context.Context) ([]GatewayMenuSource, error) {
		available := map[string]resources.Located[json.RawMessage]{}
		var ordered []resources.Located[json.RawMessage]
		for _, root := range roots {
			// Optional application discovery is isolated per owning provider.
			// Keep each provider's cursor scoped to the ordinary gateway catalog.
			var providerRows []resources.Located[json.RawMessage]
			for cursor := ""; ; {
				page, err := resources.ListPageFrom[json.RawMessage](ctx, gateway, []resources.Connection{root}, "navigation", resources.CatalogQuery{Limit: 100, Cursor: cursor}, false)
				if err != nil {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					if !optionalApplicationDenied(err) {
						logOptionalMenuFailure("source_list", markLayoutMenuFailure("source_list", root.Name, "", err))
					}
					break
				}
				providerRows = append(providerRows, page.Items...)
				if page.NextCursor == "" {
					for _, row := range providerRows {
						available[row.Connection.Name+"\x00"+row.Resource.URI] = row
						ordered = append(ordered, row)
					}
					break
				}
				if page.NextCursor == cursor {
					logOptionalMenuFailure("source_list", markLayoutMenuFailure("source_list", root.Name, "", fmt.Errorf("navigation pagination made no progress")))
					break
				}
				cursor = page.NextCursor
			}
		}
		sort.SliceStable(ordered, func(i, j int) bool {
			if strings.ToLower(ordered[i].Resource.Name) != strings.ToLower(ordered[j].Resource.Name) {
				return strings.ToLower(ordered[i].Resource.Name) < strings.ToLower(ordered[j].Resource.Name)
			}
			if ordered[i].Resource.Name != ordered[j].Resource.Name {
				return ordered[i].Resource.Name < ordered[j].Resource.Name
			}
			if ordered[i].Resource.URI == ordered[j].Resource.URI {
				return ordered[i].Connection.ProviderIdentity < ordered[j].Connection.ProviderIdentity
			}
			return ordered[i].Resource.URI < ordered[j].Resource.URI
		})

		out := []GatewayMenuSource{}
		selected := map[string]bool{}
		for _, source := range config.Sources {
			key := source.Connection + "\x00" + source.Resource
			if row, ok := available[key]; ok {
				out = append(out, GatewayMenuSource{Resource: identity.ResourceRef{URI: source.Resource}, ProviderIdentity: row.Connection.ProviderIdentity, ApplicationID: source.ApplicationID, AllowStyleOverrides: source.AllowStyleOverrides, connection: row.Connection})
				selected[key] = true
			}
		}
		if config.IncludeUnlisted {
			for _, row := range ordered {
				if !selected[row.Connection.Name+"\x00"+row.Resource.URI] {
					out = append(out, GatewayMenuSource{Resource: identity.ResourceRef{URI: row.Resource.URI}, ProviderIdentity: row.Connection.ProviderIdentity, connection: row.Connection})
				}
			}
		}
		return out, nil
	}, true)
	if config.GlobalStyle == nil {
		return menuReader, nil
	}
	return func(ctx context.Context) (*LayoutMenuRead, error) {
		read, err := menuReader(ctx)
		if err != nil {
			return nil, err
		}
		read.PreserveApplications = len(config.Sources) == 0
		source := config.GlobalStyle
		uri, _ := identity.ParseResourceURI(source.Resource)
		var selected *LayoutGlobalStyle
		for cursor := ""; ; {
			page, err := resources.ListPageFrom[json.RawMessage](ctx, gateway, []resources.Connection{{Name: source.Connection}}, "style", resources.CatalogQuery{Namespace: uri.Namespace, NameTerms: []string{uri.Name}, Limit: 100, Cursor: cursor}, false)
			if err != nil {
				_ = read.Close()
				return nil, markLayoutMenuFailure("global_style_discovery", source.Connection, source.Resource, err)
			}
			for _, row := range page.Items {
				if row.Resource.URI == source.Resource {
					metadata, marshalErr := json.Marshal(row.LocatedResource)
					if marshalErr != nil {
						_ = read.Close()
						return nil, markLayoutMenuFailure("global_style_metadata", source.Connection, source.Resource, marshalErr)
					}
					revision := identity.ContentFingerprint(append(metadata, []byte(fmt.Sprintf(":%v:%v", gateway.Manager.ResourceCatalogGeneration(ctx, row.Connection.Name), gateway.Manager.ResourceContentGeneration(ctx, row.Connection.Name)))...))
					selected = &LayoutGlobalStyle{ResourceURI: source.Resource, ResourceConnection: row.Connection.Name, ProviderIdentity: row.Connection.ProviderIdentity, StyleRevision: revision}
					break
				}
			}
			if selected != nil || page.NextCursor == "" {
				break
			}
			if page.NextCursor == cursor {
				_ = read.Close()
				return nil, fmt.Errorf("global stylesheet pagination made no progress")
			}
			cursor = page.NextCursor
		}
		read.GlobalStyle = selected
		styleProjection, _ := json.Marshal(selected)
		read.Revision = identity.ContentFingerprint(append([]byte(read.Revision), styleProjection...))
		revalidate := read.Revalidate
		read.Revalidate = func(ctx context.Context) error {
			if err := revalidate(ctx); err != nil {
				return err
			}
			read.Revision = identity.ContentFingerprint(append([]byte(read.Revision), styleProjection...))
			return nil
		}
		return read, nil
	}, nil
}

// Classify only typed authorization outcomes. Mixed failures, cancellation,
// transport outages and invalid publications must remain visible to the host.
func optionalApplicationDenied(err error) bool {
	for current := err; current != nil; current = errors.Unwrap(current) {
		if _, joined := current.(interface{ Unwrap() []error }); joined {
			return false
		}
	}
	if errors.Is(err, integrate.ErrHeadlessAuthRequired) {
		return true
	}
	if _, required := manager.LinkRequired(err); required {
		return true
	}
	var unauthorized *jsonrpc.UnauthorizedError
	if errors.As(err, &unauthorized) {
		return unauthorized.StatusCode == http.StatusUnauthorized || unauthorized.StatusCode == http.StatusForbidden
	}
	var remote *jsonrpc.Error
	if errors.As(err, &remote) && remote.Code == schema.Unauthorized {
		return true
	}
	var status interface{ HTTPStatusCode() int }
	if errors.As(err, &status) {
		return status.HTTPStatusCode() == http.StatusUnauthorized || status.HTTPStatusCode() == http.StatusForbidden
	}
	return false
}
