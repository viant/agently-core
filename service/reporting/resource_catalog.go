package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/viant/agently-core/service/reporting/catalog"
	"strings"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/reporting/registry"
)

func (s *Service) SetReportCatalog(catalog catalog.Provider) {
	if s != nil {
		s.reportCatalog = catalog
	}
}

func (s *Service) listResourceReports(ctx context.Context, input *ListReportsInput) (*ListReportsResult, error) {
	if input == nil {
		input = &ListReportsInput{}
	}
	result, err := s.reportCatalog.List(ctx, catalog.ReportCatalogQuery{Namespace: input.Namespace, CurrentUserOnly: input.CurrentUserOnly, Cursor: input.Cursor, Limit: input.Limit})
	if err != nil {
		return nil, err
	}
	out := &ListReportsResult{Reports: []*ReportSummary{}, NextCursor: result.NextCursor}
	for _, entry := range result.Reports {
		if input.ArtifactRef != "" && input.ArtifactRef != entry.URI || input.ReportID != "" && input.ReportID != entry.URI && input.ReportID != entry.ReportID {
			continue
		}
		created, _ := time.Parse(time.RFC3339, entry.CreatedAt)
		summary := &ReportSummary{Resource: cloneResolvedResource(entry.Resource), ResourceURI: entry.URI, Namespace: entry.Namespace, Name: entry.Name, OwnedByCurrentUser: entry.OwnedByCurrentUser, Capabilities: &entry.Capabilities, ArtifactID: entry.ArtifactID, ArtifactRef: entry.URI, ReportID: entry.URI, Title: entry.Title, OwnerID: entry.OwnerID, ReportType: entry.ReportType, BuilderRef: entry.BuilderRef, OrderIDs: append([]string(nil), entry.OrderIDs...), CreatedAt: created}
		if updated, err := time.Parse(time.RFC3339, entry.UpdatedAt); err == nil {
			summary.UpdatedAt = &updated
		}
		if reportSummaryMatchesOrder(summary, input.OrderID) {
			out.Reports = append(out.Reports, summary)
		}
	}
	// A total is unknown while the shared catalog has further pages.
	if result.NextCursor == "" {
		out.TotalCount = len(out.Reports)
	}
	return out, nil
}

func (s *Service) getResourceReport(ctx context.Context, input *GetReportInput) (out *SharedArtifact, resultErr error) {
	if s != nil && s.reportCatalog != nil {
		scoped, finish, err := s.reportCatalog.MetadataRead(ctx)
		if err != nil {
			return nil, err
		}
		if finish == nil {
			return nil, identity.ErrResourceDenied
		}
		defer func() {
			if err := finish(); err != nil {
				out = nil
				resultErr = errors.Join(resultErr, err)
			}
		}()
		if scoped == nil {
			return nil, identity.ErrResourceDenied
		}
		ctx = scoped
	}
	return s.getResourceReportUnscoped(ctx, input)
}

func (s *Service) getResourceReportUnscoped(ctx context.Context, input *GetReportInput) (*SharedArtifact, error) {
	if !s.hasResourceReader() || input == nil {
		return nil, identity.ErrResourceDenied
	}
	ref := identity.ResourceRef{URI: strings.TrimSpace(input.ArtifactRef)}
	if ref.URI == "" {
		ref.URI = strings.TrimSpace(input.ReportID)
	}
	if input.Resource != nil {
		ref = *input.Resource
	} else if input.ResolvedResource != nil {
		ref.URI = input.ResolvedResource.URI
		ref.Revision = input.ResolvedResource.Selector()
	}
	uri, err := identity.ParseResourceURI(ref.URI)
	if err != nil || uri.Kind != "report" {
		return nil, identity.ErrResourceDenied
	}
	resolver, err := s.resourceReaderFor(ctx, "report.retrieve")
	if err != nil {
		return nil, err
	}
	if resolver == nil {
		return nil, identity.ErrResourceDenied
	}
	pin := cloneResolvedResource(input.ResolvedResource)
	if pin == nil {
		pin, err = resolver.Resolve(ctx, ref)
		if err != nil {
			return nil, err
		}
	}
	if pin.URI != ref.URI || ref.Revision != "" && ref.Revision != pin.Selector() {
		return nil, identity.ErrResourceDenied
	}
	raw, pin, err := resolver.ReadResolved(ctx, *pin)
	if err != nil {
		return nil, err
	}
	var definition registry.ReportEnvelope
	if json.Unmarshal(raw, &definition) != nil || definition.SchemaVersion != 1 || len(definition.ReportDocument) == 0 {
		return nil, identity.ErrResource
	}
	report := &SharedArtifact{Resource: pin, ResourceDefinition: cloneJSON(raw), ArtifactRef: pin.URI, ReportID: pin.URI, Kind: savedReportArtifactKind, Document: cloneJSON(definition.ReportDocument), ReportSpec: cloneJSON(definition.ReportSpec), Title: jsonObjectText(definition.ReportDocument, "title")}
	if s.reportResourceService != nil {
		header, err := s.reportResourceService.GetResource(ctx, pin.URI)
		if err != nil {
			return nil, err
		}
		report.RowEtag = header.RowEtag
		report.DraftRevision = header.DraftRevision
		report.LatestStamp = header.LatestStamp
		report.WorkspaceID = header.WorkspaceID
		report.OwnerID = header.OwnerID
		report.Title = header.Title
		report.Lifecycle = header.Lifecycle
		// Header reads cannot release a definition after policy or content changed.
		if _, refreshed, err := resolver.ReadResolved(ctx, *pin); err != nil {
			return nil, err
		} else {
			report.Resource = refreshed
		}
	}
	return report, nil
}
