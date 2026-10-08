package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/reporting/registry"
)

const canonicalSharedResourceEnvelopeKey = "__agentlyCoreSharedResource"
const canonicalSharedResourceEnvelopeFormat = "agently-core.shared-report-pin.v1"

type sharedReportActorScope struct {
	Subject   string `json:"subject"`
	Issuer    string `json:"issuer"`
	TenantID  string `json:"tenantId"`
	AccountID string `json:"accountId"`
}

type persistedSharedResourcePin struct {
	URI               string                     `json:"uri"`
	ResourceCandidate identity.ResourceCandidate `json:"candidate"`
	AuthorityBinding  string                     `json:"authorityBinding,omitempty"`
}

type sharedResourceMetadataEnvelope struct {
	Format   string                      `json:"format"`
	Metadata json.RawMessage             `json:"metadata,omitempty"`
	Resource *persistedSharedResourcePin `json:"resource,omitempty"`
	Actor    *sharedReportActorScope     `json:"actor,omitempty"`
}

func isCanonicalReportURI(value string) bool {
	uri, err := identity.ParseResourceURI(strings.TrimSpace(value))
	return err == nil && uri.Kind == "report"
}

func (s *Service) requiresCanonicalSharedReport(artifact *SharedArtifact) bool {
	if artifact == nil {
		return false
	}
	if artifact.Resource != nil {
		return true
	}
	if s == nil || s.resourceResolver == nil {
		return false
	}
	kind := strings.TrimSpace(artifact.Kind)
	return kind == savedReportArtifactKind || kind == savedViewArtifactKind || kind == "reportBuilder.publishedSnapshot"
}

func (s *Service) shareCanonicalReport(ctx context.Context, request *ShareArtifactRequest) (*SharedArtifact, error) {
	if s == nil || s.resourceResolver == nil || request == nil {
		return nil, identity.ErrResourceDenied
	}
	if len(strings.TrimSpace(string(request.ReportDocument))) > 0 || request.ReportExportRequest != nil {
		return nil, identity.ErrResourceDenied
	}
	if len(request.Metadata) > 0 && !json.Valid(request.Metadata) || len(request.SavedViewOverlay) > 0 && !json.Valid(request.SavedViewOverlay) {
		return nil, identity.ErrResource
	}
	normalized, err := normalizeShareArtifactRequest(request)
	if err != nil {
		return nil, err
	}
	ref := identity.ResourceRef{}
	if normalized.Resource != nil {
		ref = *normalized.Resource
	}
	if normalized.ResolvedResource != nil {
		pin := normalized.ResolvedResource
		if ref.URI != "" && (ref.URI != pin.URI || ref.Revision != "" && ref.Revision != pin.Selector()) {
			return nil, identity.ErrResourceDenied
		}
		ref = identity.ResourceRef{URI: pin.URI, Revision: pin.Selector()}
	}
	ref.URI = strings.TrimSpace(ref.URI)
	if !isCanonicalReportURI(ref.URI) || normalized.ArtifactRef != ref.URI {
		return nil, identity.ErrResourceDenied
	}
	resolver, err := s.resourceResolver(ctx, "report.retrieve")
	if err != nil || resolver == nil {
		return nil, identity.ErrResourceDenied
	}
	actor, err := s.reportActorScope(ctx)
	if err != nil {
		return nil, err
	}
	pin, raw, err := resolveSharedReport(ctx, resolver, ref, normalized.ResolvedResource)
	if err != nil {
		if errors.Is(err, identity.ErrResourceStale) {
			return nil, err
		}
		return nil, identity.ErrResourceDenied
	}
	var envelope registry.ReportEnvelope
	if json.Unmarshal(raw, &envelope) != nil || envelope.SchemaVersion != 1 || len(envelope.ReportDocument) == 0 {
		return nil, identity.ErrResource
	}
	if err := s.authorizeAction(ctx, "report.retrieve", pin.URI); err != nil {
		return nil, err
	}
	ownerID := effectiveActorID(ctx)
	if ownerID == "" || ownerID != actor.Subject {
		return nil, identity.ErrResourceDenied
	}
	existing, err := s.findVisibleSharedArtifactByRef(ctx, pin.URI)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if existing != nil {
		if existing.Resource == nil || existing.Kind != savedViewArtifactKind || existing.BaseArtifactRef != pin.URI || existing.Resource.ResourceCandidate != pin.ResourceCandidate {
			return nil, identity.ErrResourceDenied
		}
		refreshed, _, err := s.reauthorizeSharedReport(ctx, existing)
		if err != nil {
			return nil, identity.ErrResourceDenied
		}
		existing.Resource = refreshed
		return cloneSharedArtifact(existing), nil
	}
	if err := s.authorizeAction(withReportCreateKind(ctx, savedViewArtifactKind), "report.create", pin.URI); err != nil {
		return nil, err
	}
	persistedPin := cloneResolvedResource(pin)
	persistedPin.ValidUntil = pin.ValidUntil
	artifact := &SharedArtifact{
		Resource:         persistedPin,
		ArtifactID:       s.newID(),
		ArtifactRef:      pin.URI,
		OwnerID:          ownerID,
		OwnerRef:         buildOwnerRef(ownerID),
		Kind:             savedViewArtifactKind,
		Lifecycle:        normalized.Lifecycle,
		Version:          resolveSharedArtifactVersion(normalized.Version, 0),
		ReportID:         pin.URI,
		Title:            jsonObjectText(envelope.ReportDocument, "title"),
		SourceArtifactID: pin.URI,
		BaseArtifactRef:  pin.URI,
		SavedViewOverlay: cloneJSON(normalized.SavedViewOverlay),
		Metadata:         cloneJSON(normalized.Metadata),
		CreatedAt:        s.now().UTC(),
		sharedActorScope: cloneSharedReportActorScope(actor),
	}
	if err := s.store.CreateSharedArtifact(ctx, artifact); err != nil {
		return nil, err
	}
	refreshed, _, err := s.reauthorizeSharedReport(ctx, artifact)
	if err != nil {
		return nil, err
	}
	artifact.Resource = refreshed
	return cloneSharedArtifact(artifact), nil
}

func resolveSharedReport(ctx context.Context, resolver *identity.ResourceResolver, ref identity.ResourceRef, supplied *identity.ResolvedResource) (*identity.ResolvedResource, []byte, error) {
	if supplied != nil {
		if ref.URI != supplied.URI || ref.Revision != supplied.Selector() {
			return nil, nil, identity.ErrResourceDenied
		}
	}
	pin, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	if supplied != nil && (pin.URI != supplied.URI || pin.ResourceCandidate != supplied.ResourceCandidate) {
		return nil, nil, identity.ErrResourceDenied
	}
	raw, fresh, err := resolver.ReadResolved(ctx, *pin)
	if err != nil {
		return nil, nil, err
	}
	return fresh, raw, nil
}

func (s *Service) reportActorScope(ctx context.Context) (*sharedReportActorScope, error) {
	if s == nil || s.reportCatalog == nil {
		return nil, identity.ErrResourceDenied
	}
	actor, err := s.reportCatalog.CurrentActor(ctx)
	if err != nil || !actor.Valid(s.now()) || actor.Subject == "" || actor.Issuer == "" || actor.TenantID == "" || actor.AccountID == "" {
		return nil, identity.ErrResourceDenied
	}
	return &sharedReportActorScope{Subject: actor.Subject, Issuer: actor.Issuer, TenantID: actor.TenantID, AccountID: actor.AccountID}, nil
}

func sameSharedReportActorScope(left, right *sharedReportActorScope) bool {
	return left != nil && right != nil && left.Subject != "" && left.Subject == right.Subject && left.Issuer == right.Issuer && left.TenantID == right.TenantID && left.AccountID == right.AccountID
}

func cloneSharedReportActorScope(input *sharedReportActorScope) *sharedReportActorScope {
	if input == nil {
		return nil
	}
	output := *input
	return &output
}

func (s *Service) reauthorizeSharedReport(ctx context.Context, artifact *SharedArtifact) (*identity.ResolvedResource, []byte, error) {
	if s == nil || artifact == nil || artifact.Resource == nil || artifact.sharedActorScope == nil || s.resourceResolver == nil {
		return nil, nil, identity.ErrResourceDenied
	}
	stored := artifact.Resource
	uri, err := identity.ParseResourceURI(stored.URI)
	if err != nil || uri.Kind != "report" || artifact.BaseArtifactRef != stored.URI || artifact.ReportID != stored.URI || artifact.ArtifactRef != stored.URI {
		return nil, nil, identity.ErrResourceDenied
	}
	actor, err := s.reportActorScope(ctx)
	if err != nil || !sameSharedReportActorScope(actor, artifact.sharedActorScope) {
		return nil, nil, identity.ErrResourceDenied
	}
	resolver, err := s.resourceResolver(ctx, "report.retrieve")
	if err != nil || resolver == nil {
		return nil, nil, identity.ErrResourceDenied
	}
	fresh, err := resolver.Resolve(ctx, identity.ResourceRef{URI: stored.URI, Revision: stored.Selector()})
	if err != nil || fresh.URI != stored.URI || fresh.ResourceCandidate != stored.ResourceCandidate {
		return nil, nil, identity.ErrResourceDenied
	}
	raw, readFresh, err := resolver.ReadResolved(ctx, *fresh)
	if err != nil || readFresh.URI != stored.URI || readFresh.ResourceCandidate != stored.ResourceCandidate {
		return nil, nil, identity.ErrResourceDenied
	}
	var envelope registry.ReportEnvelope
	if json.Unmarshal(raw, &envelope) != nil || envelope.SchemaVersion != 1 || len(envelope.ReportDocument) == 0 {
		return nil, nil, identity.ErrResource
	}
	finalActor, err := s.reportActorScope(ctx)
	if err != nil || !sameSharedReportActorScope(finalActor, artifact.sharedActorScope) {
		return nil, nil, identity.ErrResourceDenied
	}
	return readFresh, raw, nil
}

func encodeSharedArtifactMetadata(metadata json.RawMessage, resource *identity.ResolvedResource, actor *sharedReportActorScope) json.RawMessage {
	var top map[string]json.RawMessage
	if json.Unmarshal(metadata, &top) == nil {
		if _, reserved := top[canonicalSharedResourceEnvelopeKey]; !reserved && resource == nil && actor == nil {
			return cloneJSON(metadata)
		}
	} else if resource == nil && actor == nil {
		return cloneJSON(metadata)
	}
	envelope := sharedResourceMetadataEnvelope{Format: canonicalSharedResourceEnvelopeFormat, Metadata: cloneJSON(metadata)}
	if resource != nil {
		envelope.Resource = &persistedSharedResourcePin{URI: resource.URI, ResourceCandidate: resource.ResourceCandidate, AuthorityBinding: resource.AuthorityBinding}
	}
	envelope.Actor = cloneSharedReportActorScope(actor)
	encoded, err := json.Marshal(map[string]interface{}{canonicalSharedResourceEnvelopeKey: envelope})
	if err != nil {
		return cloneJSON(metadata)
	}
	return encoded
}

func decodeSharedArtifactMetadata(metadata json.RawMessage) (json.RawMessage, *identity.ResolvedResource, *sharedReportActorScope, error) {
	var top map[string]json.RawMessage
	if json.Unmarshal(metadata, &top) != nil || top == nil {
		return cloneJSON(metadata), nil, nil, nil
	}
	raw, ok := top[canonicalSharedResourceEnvelopeKey]
	if !ok {
		return cloneJSON(metadata), nil, nil, nil
	}
	var envelope sharedResourceMetadataEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Format != canonicalSharedResourceEnvelopeFormat {
		return nil, nil, nil, fmt.Errorf("invalid shared report metadata envelope")
	}
	var pin *identity.ResolvedResource
	if envelope.Resource != nil {
		if !isCanonicalReportURI(envelope.Resource.URI) || !envelope.Resource.ResourceCandidate.Valid() || strings.TrimSpace(envelope.Resource.AuthorityBinding) == "" {
			return nil, nil, nil, fmt.Errorf("invalid persisted shared report pin")
		}
		pin = &identity.ResolvedResource{URI: envelope.Resource.URI, ResourceCandidate: envelope.Resource.ResourceCandidate, AuthorityBinding: envelope.Resource.AuthorityBinding}
	}
	return cloneJSON(envelope.Metadata), pin, cloneSharedReportActorScope(envelope.Actor), nil
}
