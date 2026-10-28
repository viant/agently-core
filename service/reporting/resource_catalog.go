package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/primitive"
	useruri "github.com/viant/agently-core/protocol/resource/user"
	"github.com/viant/agently-core/service/reporting/catalog"
	publishedidentity "github.com/viant/forge/backend/resource"
	"strings"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	sharedengine "github.com/viant/forge/backend/reporting/engine"
	"github.com/viant/forge/backend/reporting/registry"
)

func (s *Service) SetReportCatalog(catalog catalog.Provider) {
	if s != nil {
		s.reportCatalog = catalog
		if s.execution != nil {
			s.execution.SetReportCatalog(catalog)
		}
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
	if ref.URI == "" && input.Resource == nil && input.ResolvedResource == nil && input.ArtifactID != "" {
		ref.URI = strings.TrimSpace(input.ArtifactID)
		if !strings.Contains(ref.URI, "://") {
			uri, err := useruri.NewURI("report", iauth.CanonicalUserID(ctx), ref.URI)
			if err != nil {
				return nil, err
			}
			ref.URI = uri.String()
		}
	}
	if input.Resource != nil {
		ref = *input.Resource
	} else if input.ResolvedResource != nil {
		ref.URI = input.ResolvedResource.URI
		ref.Revision = input.ResolvedResource.Selector()
	}
	// A bare saved-report ID is a relative name in the authenticated user's
	// namespace. Absolute references keep their provider-defined identity.
	if ref.URI != "" && !strings.Contains(ref.URI, "://") {
		uri, err := useruri.NewURI("report", iauth.CanonicalUserID(ctx), ref.URI)
		if err != nil {
			return nil, err
		}
		ref.URI = uri.String()
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
	var raw json.RawMessage
	var state *primitive.ResourceState
	if input.RefreshMetadata && (pin == nil || pin.URI != ref.URI || ref.Revision != pin.Selector() || !pin.ResourceCandidate.Valid()) {
		return nil, identity.ErrResourceDenied
	}
	var readDefinition func(context.Context, identity.ResourceRef, *identity.ResolvedResource) (*primitive.GetResult, error)
	if input.RefreshMetadata {
		if reader, ok := resolver.(ResourceFreshDefinitionReader); ok {
			readDefinition = reader.ReadDefinitionFresh
		} else if local, ok := resolver.(*identity.ResourceResolver); ok && local.ProviderIdentity == pin.ProviderIdentity {
			// ResourceResolver has no cross-request metadata cache. A fresh
			// unpinned selection invokes its ordinary source/policy path.
			pin = nil
		} else {
			return nil, fmt.Errorf("fresh report metadata is unavailable")
		}
	} else if reader, ok := resolver.(ResourceDefinitionReader); ok {
		readDefinition = reader.ReadDefinition
	}
	if readDefinition != nil {
		got, readErr := readDefinition(ctx, ref, pin)
		if readErr != nil {
			return nil, readErr
		}
		if got == nil || got.Resource == nil || got.ResolvedResource == nil {
			return nil, identity.ErrResource
		}
		state, pin = got.Resource, got.ResolvedResource
		raw = append(json.RawMessage(nil), state.DefinitionBytes...)
	} else {
		raw, pin, err = selectResourceRead(ctx, resolver, ref, pin)
		if err != nil {
			return nil, err
		}
	}
	if pin == nil || pin.URI != ref.URI || ref.Revision != "" && ref.Revision != pin.Selector() {
		return nil, identity.ErrResourceDenied
	}
	var definition registry.ReportEnvelope
	if json.Unmarshal(raw, &definition) != nil || len(definition.ReportDocument) == 0 && len(definition.ReportSpec) == 0 {
		return nil, identity.ErrResource
	}
	if definition.SchemaVersion != 1 {
		verifier, ok := resolver.(sharedengine.PublishedResourceVerifier)
		if definition.SchemaVersion != 2 || definition.Format != registry.ResourceReportFormat || pin.Kind != publishedidentity.PublishedCandidate || !ok {
			return nil, identity.ErrResource
		}
		if err = verifier.VerifyPublishedResource(ctx, *pin); err != nil {
			return nil, err
		}
	}
	report := &SharedArtifact{ArtifactID: pin.URI, Resource: pin, ResourceDefinition: cloneJSON(raw), ArtifactRef: pin.URI, ReportID: pin.URI, Kind: savedReportArtifactKind, Document: cloneJSON(definition.ReportDocument), ReportSpec: cloneJSON(definition.ReportSpec), ReportFill: cloneJSON(definition.ReportFill), ReportPrint: cloneJSON(definition.ReportPrint), CompileState: cloneJSON(definition.CompileState), Metadata: cloneJSON(definition.Metadata), Title: jsonObjectText(definition.ReportDocument, "title")}
	if state != nil {
		report.RowEtag, report.DraftRevision, report.LatestStamp = state.RowEtag, state.DraftRevision, state.LatestStamp
		report.WorkspaceID, report.OwnerID, report.Lifecycle = state.WorkspaceID, state.OwnerID, state.Lifecycle
		if state.Title != "" {
			report.Title = state.Title
		}
	} else if s.reportResourceService != nil {
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
