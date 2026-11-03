package view

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
)

// reportOpenReference recognizes an existing report resource, not an authored
// builder/view alias. The reporting catalog remains its admission authority.
func reportOpenReference(input OpenItem) (*identity.ResourceRef, bool, error) {
	ref := input.Resource
	if strings.HasPrefix(input.ID, "report://") {
		if ref != nil && ref.URI != input.ID {
			return nil, true, fmt.Errorf("report resource request conflicts")
		}
		if ref == nil {
			ref = &identity.ResourceRef{URI: input.ID}
		}
	}
	if ref == nil || !strings.HasPrefix(ref.URI, "report://") {
		return nil, false, nil
	}
	uri, err := identity.ParseResourceURI(ref.URI)
	if err != nil || uri.Kind != "report" {
		return nil, true, identity.ErrResource
	}
	if input.Target != nil || input.SearchText != "" {
		return nil, true, fmt.Errorf("report resources do not use window targets or quick search")
	}
	return ref, true, nil
}

func (s *Service) prepareReportOpen(ctx context.Context, input OpenItem, ref *identity.ResourceRef) (*preparedOpenItem, error) {
	if s.bridge == nil {
		return nil, fmt.Errorf("ui bridge not configured")
	}
	selected, err := s.bridge.ReportDefinition(ctx, &forgeuisvc.ReportDefinitionInput{Resource: *ref})
	if err != nil {
		return nil, err
	}
	if selected == nil || selected.Resource == nil || selected.Resource.URI != ref.URI {
		return nil, identity.ErrResourceDenied
	}
	var title struct {
		ReportDocument struct {
			Title string `json:"title"`
		} `json:"reportDocument"`
		ReportSpec struct {
			Title string `json:"title"`
		} `json:"reportSpec"`
	}
	if err = json.Unmarshal(selected.Definition, &title); err != nil {
		return nil, err
	}
	label := firstNonEmpty(title.ReportDocument.Title, title.ReportSpec.Title)
	if label == "" {
		uri, _ := identity.ParseResourceURI(ref.URI)
		label = uri.Name
	}
	item := &ListItem{ID: ref.URI, ResourceURI: ref.URI, Resource: selected.Resource, Title: label, WindowKey: ref.URI, Presentation: "hosted", Region: "chat.top", OpenMode: "append"}
	return &preparedOpenItem{item: item, openMode: input.OpenMode, windowParameters: cloneMap(input.Parameters)}, nil
}
