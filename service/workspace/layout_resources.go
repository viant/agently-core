package workspace

import (
	"context"
	"fmt"
	resources "github.com/viant/agently-core/service/resource"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/policy"

	forgeservice "github.com/viant/agently-core/service/primitiveprovider"
)

// WindowResourceCatalog is the host's authorized definition catalog. Navigation
// uses the same visibility/revision path as list/get/open, never a second map of
// raw workspace filenames.
type WindowResourceCatalog interface {
	UsesWindowResourceResolution() bool
	MetadataScope() forgeservice.MetadataReadScope
	WindowDefinitionsList(context.Context, *forgeservice.WindowDefinitionListInput) (*forgeservice.WindowDefinitionListOutput, error)
}

func (h *MetadataHandler) SetWindowResourceCatalog(catalog WindowResourceCatalog) {
	if h != nil {
		h.windowResourceCatalog = catalog
	}
}

func catalogWindowIDs(ctx context.Context, catalog WindowResourceCatalog) (map[string]bool, error) {
	allowed := map[string]bool{}
	for offset := 0; offset < 10000; {
		page, err := catalog.WindowDefinitionsList(ctx, &forgeservice.WindowDefinitionListInput{Offset: offset, Limit: 100})
		if err != nil {
			return nil, err
		}
		if page == nil {
			return nil, fmt.Errorf("window catalog returned no page")
		}
		for _, entry := range page.Windows {
			if entry.WindowID == "" || allowed[entry.WindowID] {
				return nil, fmt.Errorf("invalid or duplicate catalog window identity")
			}
			allowed[entry.WindowID] = true
			if entry.ResourceURI != "" {
				allowed[entry.ResourceURI] = true
			}
		}
		if !page.HasMore {
			return allowed, nil
		}
		if len(page.Windows) == 0 {
			return nil, fmt.Errorf("window catalog pagination made no progress")
		}
		offset += len(page.Windows)
	}
	return nil, fmt.Errorf("window catalog exceeds navigation limit")
}

// Logical keys come only from the catalog's configured routing capability.
// A canonical visible inventory entry is required; routing alone grants nothing.
func addVisibleLayoutAliases(ctx context.Context, catalog WindowResourceCatalog, allowed map[string]bool, candidates []policy.Candidate) error {
	mapper, ok := catalog.(interface {
		WindowResourceReference(context.Context, string) (identity.ResourceRef, error)
	})
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate.ID] {
			continue
		}
		seen[candidate.ID] = true
		if allowed[candidate.ID] {
			continue
		}
		ref, err := mapper.WindowResourceReference(ctx, candidate.ID)
		if err != nil {
			if err == identity.ErrResource || err == identity.ErrResourceDenied {
				continue
			}
			return err
		}
		uri, err := identity.ParseResourceURI(ref.URI)
		if err != nil || uri.Kind != "window" {
			return fmt.Errorf("window catalog returned invalid resource reference")
		}
		if allowed[ref.URI] {
			allowed[candidate.ID] = true
		}
	}
	return nil
}

type scopedWindowDefinitionLister interface {
	ListFrom(context.Context, []resources.Connection, *forgeservice.WindowDefinitionListInput) (*forgeservice.WindowDefinitionListOutput, error)
}

// ScopedLayoutWindowCatalog retains the host catalog and scopes only the
// default managing menu's internal window inventory to explicit trusted roots.
func ScopedLayoutWindowCatalog(base WindowResourceCatalog, source scopedWindowDefinitionLister, roots []resources.Connection) WindowResourceCatalog {
	return &scopedLayoutWindowCatalog{WindowResourceCatalog: base, source: source, roots: append([]resources.Connection(nil), roots...)}
}

type scopedLayoutWindowCatalog struct {
	WindowResourceCatalog
	source scopedWindowDefinitionLister
	roots  []resources.Connection
}

func (c *scopedLayoutWindowCatalog) WindowDefinitionsList(ctx context.Context, input *forgeservice.WindowDefinitionListInput) (*forgeservice.WindowDefinitionListOutput, error) {
	return c.source.ListFrom(ctx, c.roots, input)
}
func (c *scopedLayoutWindowCatalog) WindowResourceReference(ctx context.Context, key string) (identity.ResourceRef, error) {
	mapper, ok := c.WindowResourceCatalog.(interface {
		WindowResourceReference(context.Context, string) (identity.ResourceRef, error)
	})
	if !ok {
		return identity.ResourceRef{}, identity.ErrResourceDenied
	}
	return mapper.WindowResourceReference(ctx, key)
}
func layoutWindowCatalog(catalog WindowResourceCatalog, layout *Layout, managed bool) WindowResourceCatalog {
	scoped, ok := catalog.(*scopedLayoutWindowCatalog)
	if !ok {
		return catalog
	}
	if !managed || !internalNavigation(layout) {
		return scoped.WindowResourceCatalog
	}
	return scoped
}
func internalNavigation(layout *Layout) bool {
	var local func([]LayoutMenu) bool
	local = func(menus []LayoutMenu) bool {
		for _, menu := range menus {
			if action := menu.Action; action != nil && action.Type == "window" {
				if action.Provider != "" && action.Provider != "workspace" || strings.Contains(action.WindowKey, ":") {
					return false
				}
			}
			if !local(menu.Children) {
				return false
			}
		}
		return true
	}
	for _, app := range layout.Applications {
		if app.WindowCatalog != nil || !local(app.Menus) {
			return false
		}
	}
	return layout.Topbar == nil || local(layout.Topbar.Actions)
}
