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
// GlobalStyleURI identifies the single producer-owned public workspace stylesheet.
const GlobalStyleURI = "style://global/workspace"

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
		Sources         []LayoutApplicationSource `yaml:"applicationSources"`
		IncludeUnlisted bool                      `yaml:"includeUnlistedApplications"`
	}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	if len(config.Sources) == 0 && (gateway == nil || len(gateway.PublishedMetadataConnections()) == 0) {
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
	if gateway == nil || len(gateway.PublishedMetadataConnections()) == 0 {
		return menuReader, nil
	}
	return func(ctx context.Context) (*LayoutMenuRead, error) {
		read, err := menuReader(ctx)
		if err != nil {
			return nil, err
		}
		read.PreserveApplications = len(config.Sources) == 0
		selected, err := readGlobalStyleMetadata(ctx, gateway)
		if err != nil {
			if ctx.Err() != nil {
				_ = read.Close()
				return nil, ctx.Err()
			}
			logLayoutMenuFailure("global_style_metadata", err)
			read.StyleDiagnostics = []string{"Managed global stylesheet is unavailable; check the producer's public global publication."}
		}
		if selected == nil && len(read.StyleDiagnostics) == 0 {
			read.StyleDiagnostics = []string{"No public global stylesheet is published."}
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

// Both shell and standalone renderer metadata reuse the same prepared source
// and gateway index. This reads no stylesheet body and adds no cache or ACL.
func readGlobalStyleMetadata(ctx context.Context, gateway *resources.Gateway) (*LayoutGlobalStyle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if gateway == nil || gateway.Manager == nil {
		return nil, fmt.Errorf("managed stylesheet catalog is unavailable")
	}
	connections := gateway.PublishedMetadataConnections("global")
	if len(connections) == 0 {
		return nil, nil
	}
	for cursor := ""; ; {
		page, err := resources.ListPageFrom[json.RawMessage](ctx, gateway, connections, "style", resources.CatalogQuery{Namespace: "global", NameTerms: []string{"workspace"}, Limit: 100, Cursor: cursor}, false)
		if err != nil {
			return nil, markLayoutMenuFailure("global_style_discovery", "", GlobalStyleURI, err)
		}
		for _, row := range page.Items {
			if row.Resource.URI == GlobalStyleURI {
				metadata, err := json.Marshal(row.LocatedResource)
				if err != nil {
					return nil, markLayoutMenuFailure("global_style_metadata", "", GlobalStyleURI, err)
				}
				revision := identity.ContentFingerprint(append(metadata, []byte(fmt.Sprintf(":%v:%v", gateway.Manager.ResourceCatalogGeneration(ctx, row.Connection.Name), gateway.Manager.ResourceContentGeneration(ctx, row.Connection.Name)))...))
				return &LayoutGlobalStyle{ResourceURI: GlobalStyleURI, ResourceConnection: row.Connection.Name, ProviderIdentity: row.Connection.ProviderIdentity, StyleRevision: revision}, nil
			}
		}
		if page.NextCursor == "" {
			return nil, nil
		}
		if page.NextCursor == cursor {
			return nil, fmt.Errorf("global stylesheet pagination made no progress")
		}
		cursor = page.NextCursor
	}
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
