package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/protocol/primitive"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/forge/backend/reporting/registry"
)

// SetReportResourceService is a pre-serving composition seam. The host supplies
// a facade bound to the same verified authority as the read resolver.
func (s *Service) SetReportResourceService(writer primitive.ResourceAuthoring) {
	s.reportResourceService = writer
}
func (s *Service) WithReportResourceService(writer primitive.ResourceAuthoring) *Service {
	s.SetReportResourceService(writer)
	return s
}
func (s *Service) usesReportResources() bool {
	return s.reportResourceService != nil || s.reportCatalog != nil || s.hasResourceReader()
}
func (s *Service) requireReportWriter() error {
	if s.reportResourceService == nil || !s.hasResourceReader() {
		return identity.ErrResourceDenied
	}
	return nil
}
func authoringRef(ref *identity.ResourceRef, pin *identity.ResolvedResource, artifactRef, reportID string) (identity.ResourceRef, error) {
	result := identity.ResourceRef{URI: strings.TrimSpace(artifactRef)}
	if result.URI == "" {
		result.URI = strings.TrimSpace(reportID)
	}
	if ref != nil {
		result = *ref
	} else if pin != nil {
		result = identity.ResourceRef{URI: pin.URI, Revision: pin.Selector()}
	}
	uri, err := identity.ParseResourceURI(result.URI)
	if err != nil || uri.Kind != "report" {
		return result, identity.ErrResourceDenied
	}
	if pin != nil && (result.URI != pin.URI || result.Revision != "" && result.Revision != pin.Selector()) {
		return result, identity.ErrResourceDenied
	}
	for _, alias := range []string{artifactRef, reportID} {
		if alias != "" && alias != result.URI {
			return result, identity.ErrResourceDenied
		}
	}
	return result, nil
}
func (s *Service) readAuthoringResource(ctx context.Context, ref identity.ResourceRef, pin *identity.ResolvedResource, operation string) (json.RawMessage, *identity.ResolvedResource, error) {
	if err := s.requireReportWriter(); err != nil {
		return nil, nil, err
	}
	resolver, err := s.resourceReaderFor(ctx, operation)
	if err != nil {
		return nil, nil, err
	}
	if resolver == nil {
		return nil, nil, identity.ErrResourceDenied
	}
	selected := cloneResolvedResource(pin)
	if selected == nil {
		selected, err = resolver.Resolve(ctx, ref)
		if err != nil {
			return nil, nil, err
		}
	}
	if selected.URI != ref.URI || ref.Revision != "" && selected.Selector() != ref.Revision {
		return nil, nil, identity.ErrResourceDenied
	}
	return resolver.ReadResolved(ctx, *selected)
}
func reportEnvelope(doc, spec, definition json.RawMessage) (json.RawMessage, error) {
	if len(definition) > 0 {
		return cloneJSON(definition), nil
	}
	if len(doc) == 0 || len(spec) == 0 {
		return nil, fmt.Errorf("report definition requires a complete envelope or document and spec")
	}
	return json.Marshal(registry.ReportEnvelope{SchemaVersion: 1, ReportDocument: normalizeToolJSONPayload(doc), ReportSpec: normalizeToolJSONPayload(spec)})
}
func (s *Service) resourceWriteResult(ctx context.Context, result *primitive.ResourceResult) (*SharedArtifact, error) {
	// The metadata read scope applies to the public get_report projection. A
	// lifecycle write's response is already bound to the write transaction and
	// must not start a second metadata-read scope.
	report, err := s.getResourceReportUnscoped(ctx, &GetReportInput{Resource: &identity.ResourceRef{URI: result.URI, Revision: "working"}})
	if err != nil {
		return nil, err
	}
	report.RowEtag = result.RowEtag
	report.DraftRevision = result.DraftRevision
	report.LatestStamp = result.LatestStamp
	return report, nil
}
func (s *Service) saveResourceReport(ctx context.Context, input *SaveReportRequest) (*SharedArtifact, error) {
	if input == nil {
		return nil, fmt.Errorf("report save input required")
	}
	if err := s.requireReportWriter(); err != nil {
		return nil, err
	}
	ref, err := authoringRef(input.Resource, input.ResolvedResource, input.ArtifactRef, input.ReportID)
	if err != nil {
		return nil, err
	}
	if ref.Revision != "" || input.ResolvedResource != nil {
		return nil, fmt.Errorf("create requires a new logical resource")
	}
	raw, err := reportEnvelope(input.ReportDocument, input.ReportSpec, input.Definition)
	if err != nil {
		return nil, err
	}
	result, err := s.reportResourceService.CreateResource(ctx, primitive.CreateResourceInput{URI: ref.URI, WorkspaceID: input.WorkspaceID, Title: input.Title, FormatVersion: 1, Definition: raw, IdempotencyKey: input.IdempotencyKey})
	if err != nil {
		return nil, err
	}
	return s.resourceWriteResult(ctx, result)
}
func (s *Service) updateResourceReport(ctx context.Context, input *UpdateReportRequest) (*SharedArtifact, error) {
	if input == nil {
		return nil, fmt.Errorf("report update input required")
	}
	ref, err := authoringRef(input.Resource, input.ResolvedResource, input.ArtifactRef, input.ReportID)
	if err != nil {
		return nil, err
	}
	raw, pin, err := s.readAuthoringResource(ctx, ref, input.ResolvedResource, "report.edit")
	if err != nil {
		return nil, err
	}
	if pin.Kind != identity.WorkingCandidate {
		return nil, fmt.Errorf("only the mutable report draft can be updated")
	}
	expectedFP := input.ExpectedContentFingerprint
	if expectedFP == "" {
		expectedFP = pin.ContentFingerprint
	}
	if expectedFP != pin.ContentFingerprint {
		return nil, identity.ErrResourceDenied
	}
	if len(input.Definition) > 0 {
		raw = cloneJSON(input.Definition)
	} else {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return nil, identity.ErrResource
		}
		if len(input.ReportDocument) > 0 {
			fields["reportDocument"] = normalizeToolJSONPayload(input.ReportDocument)
		}
		if len(input.ReportSpec) > 0 {
			if _, authored := fields["format"]; authored {
				return nil, fmt.Errorf("authored report updates require their complete authored envelope")
			}
			fields["reportSpec"] = normalizeToolJSONPayload(input.ReportSpec)
		}
		if input.Title != "" {
			var doc map[string]json.RawMessage
			if json.Unmarshal(fields["reportDocument"], &doc) != nil {
				return nil, identity.ErrResource
			}
			doc["title"], _ = json.Marshal(input.Title)
			fields["reportDocument"], _ = json.Marshal(doc)
		}
		raw, err = json.Marshal(fields)
		if err != nil {
			return nil, err
		}
	}
	var title *string
	if input.Title != "" {
		title = &input.Title
	}
	result, err := s.reportResourceService.UpdateDraft(ctx, ref.URI, primitive.UpdateDraftInput{Title: title, ExpectedRowEtag: input.ExpectedRowEtag, ExpectedDraftRevision: input.ExpectedDraftRevision, ExpectedContentFingerprint: expectedFP, FormatVersion: 1, Definition: raw, IdempotencyKey: input.IdempotencyKey})
	if err != nil {
		return nil, err
	}
	return s.resourceWriteResult(ctx, result)
}
func (s *Service) duplicateResourceReport(ctx context.Context, input *DuplicateReportRequest) (*SharedArtifact, error) {
	if input == nil {
		return nil, fmt.Errorf("report duplicate input required")
	}
	ref, err := authoringRef(input.Resource, input.ResolvedResource, input.ArtifactRef, input.ReportID)
	if err != nil {
		return nil, err
	}
	_, pin, err := s.readAuthoringResource(ctx, ref, input.ResolvedResource, "report.duplicate")
	if err != nil {
		return nil, err
	}
	target, err := identity.ParseResourceURI(input.TargetURI)
	if err != nil || target.Kind != "report" {
		return nil, identity.ErrResourceDenied
	}
	result, err := s.reportResourceService.CloneResource(ctx, primitive.CloneResourceInput{Source: identity.ResourceRef{URI: pin.URI, Revision: pin.Selector()}, ExpectedSourceFingerprint: pin.ContentFingerprint, TargetURI: input.TargetURI, TargetWorkspaceID: input.TargetWorkspaceID, Title: input.Title, IdempotencyKey: input.IdempotencyKey})
	if err != nil {
		return nil, err
	}
	return s.resourceWriteResult(ctx, result)
}
func (s *Service) deleteResourceReport(ctx context.Context, input *DeleteReportRequest) (*DeleteReportResult, error) {
	if input == nil {
		return nil, fmt.Errorf("report archive input required")
	}
	ref, err := authoringRef(input.Resource, input.ResolvedResource, input.ArtifactRef, input.ReportID)
	if err != nil {
		return nil, err
	}
	_, _, err = s.readAuthoringResource(ctx, ref, input.ResolvedResource, "report.delete")
	if err != nil {
		return nil, err
	}
	_, err = s.reportResourceService.ArchiveResource(ctx, ref.URI, primitive.ArchiveResourceInput{ExpectedHeadRevision: input.ExpectedRowEtag, IdempotencyKey: input.IdempotencyKey})
	if err != nil {
		return nil, err
	}
	return &DeleteReportResult{ArtifactID: ref.URI, ReportID: ref.URI, Deleted: true}, nil
}
func (s *Service) StampReport(ctx context.Context, input *StampReportRequest) (*primitive.ResourceResult, error) {
	if input == nil {
		return nil, fmt.Errorf("report stamp input required")
	}
	ref, err := authoringRef(input.Resource, input.ResolvedResource, "", "")
	if err != nil {
		return nil, err
	}
	_, pin, err := s.readAuthoringResource(ctx, ref, input.ResolvedResource, "report.stamp")
	if err != nil {
		return nil, err
	}
	if pin.Kind != identity.WorkingCandidate || pin.ContentFingerprint != input.ContentFingerprint {
		return nil, identity.ErrResourceDenied
	}
	return s.reportResourceService.StampResource(ctx, ref.URI, primitive.StampResourceInput{ExpectedRowEtag: input.ExpectedRowEtag, ExpectedDraftRevision: input.ExpectedDraftRevision, ContentFingerprint: input.ContentFingerprint, ExpectedDependencies: input.ExpectedDependencies, IdempotencyKey: input.IdempotencyKey})
}
func (s *Service) stampReportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*StampReportRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*primitive.ResourceResult)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	result, err := s.StampReport(ctx, input)
	if err != nil {
		return err
	}
	*output = *result
	return nil
}
