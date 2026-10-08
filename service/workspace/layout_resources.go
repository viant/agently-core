package workspace

import (
	"context"
	"fmt"

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
