package view

import (
	"context"
	"fmt"
	"strings"

	viewproto "github.com/viant/agently-core/protocol/ui/view"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
)

// loadRequested resolves a direct request without first filtering it through
// list's omitted revision selection. An allowed older stamp can be read/opened
// even when policy admits no omitted candidate.
func (s *Service) loadRequested(ctx context.Context, id string, ref *identity.ResourceRef, targets ...*types.WindowTarget) (*ListItem, error) {
	var target *types.WindowTarget
	if len(targets) > 0 {
		target = targets[0]
	}
	canonical := s != nil && s.bridge != nil && s.bridge.UsesWindowResourceResolution()
	if !canonical {
		if ref != nil {
			return nil, fmt.Errorf("canonical window resource resolution is not configured")
		}
		return s.loadOne(ctx, id)
	}
	if s.repo == nil {
		return nil, fmt.Errorf("ui view repository not configured")
	}
	if strings.Contains(id, "://") {
		if ref != nil && ref.URI != id {
			return nil, fmt.Errorf("window resource request conflicts")
		}
		if ref == nil {
			ref = &identity.ResourceRef{URI: id}
		}
		id = ""
	}
	if id == "" && ref == nil {
		return nil, fmt.Errorf("id or resource is required")
	}
	if ref != nil {
		uri, err := identity.ParseResourceURI(ref.URI)
		if err != nil || uri.Kind != "window" {
			return nil, fmt.Errorf("invalid window resource")
		}
	}
	all, err := s.repo.LoadAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, spec := range all {
		if spec == nil || id != "" && !strings.EqualFold(strings.TrimSpace(spec.ID), id) {
			continue
		}
		catalogKey, mapped, err := s.canonicalCatalogReference(ctx, spec)
		if err != nil {
			continue
		}
		if ref != nil {
			if mapped.URI != ref.URI {
				continue
			}
		}
		allowed, err := s.authorizeView(ctx, spec.ID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		definition, err := s.bridge.WindowDefinitionGet(ctx, &forgeuisvc.WindowDefinitionGetInput{WindowID: catalogKey, Resource: ref, Target: target})
		if err != nil {
			return nil, err
		}
		if definition == nil || definition.Definition == nil || definition.Definition.Resource == nil {
			return nil, fmt.Errorf("window resource resolution unavailable")
		}
		item := itemFromSpec(spec)
		item.WindowKey = catalogKey
		pin := *definition.Definition.Resource
		item.Resource = &pin
		item.Target = definition.Definition.ResourceTarget
		if err := s.enrichItem(ctx, &item); err != nil {
			return nil, err
		}
		return &item, nil
	}
	return nil, &viewNotFoundError{id: id}
}

func (s *Service) canonicalCatalogReference(ctx context.Context, spec *viewproto.Spec) (string, identity.ResourceRef, error) {
	// A configured catalogue identity owns its resource even if the underlying
	// browser activation key is a shared renderer such as reportBuilder.
	for _, key := range []string{strings.TrimSpace(spec.ID), strings.TrimSpace(spec.WindowKey)} {
		if key == "" || !s.bridge.WindowResourceConfigured(key) {
			continue
		}
		ref, err := s.bridge.WindowResourceReference(ctx, key)
		return key, ref, err
	}
	return "", identity.ResourceRef{}, identity.ErrResourceDenied
}
func itemFromSpec(spec *viewproto.Spec) ListItem {
	return ListItem{ID: strings.TrimSpace(spec.ID), Title: strings.TrimSpace(spec.Title), Description: strings.TrimSpace(spec.Description), WindowKey: strings.TrimSpace(spec.WindowKey), Presentation: strings.TrimSpace(spec.Presentation), Region: strings.TrimSpace(spec.Region), OpenMode: strings.TrimSpace(spec.OpenMode), IdentityScope: strings.TrimSpace(spec.IdentityScope), QuickSearch: spec.QuickSearch, IdentityParameters: append([]string(nil), spec.IdentityParameters...), WorkspaceSharePct: spec.WorkspaceSharePct, WorkspaceMinHeight: spec.WorkspaceMinHeight, RefreshOnOpen: spec.RefreshOnOpen, ReportBuilderRef: strings.TrimSpace(spec.ReportBuilderRef), Parameters: append([]viewproto.Parameter(nil), spec.Parameters...), ReportPresets: append([]viewproto.ReportPreset(nil), spec.ReportPresets...), Capabilities: spec.Capabilities, Navigation: spec.Navigation}
}
func (s *Service) enrichItem(ctx context.Context, item *ListItem) error {
	if item.Presentation == "" {
		item.Presentation = "hosted"
	}
	if strings.EqualFold(item.Presentation, "hosted") && item.Region == "" {
		item.Region = "chat.top"
	}
	if item.OpenMode == "" && strings.EqualFold(item.Presentation, "hosted") {
		item.OpenMode = "append"
	}
	if s.itemEnricher != nil {
		return s.itemEnricher(ctx, item)
	}
	return nil
}
