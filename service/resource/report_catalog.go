package resource

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/reporting"
	catalog "github.com/viant/agently-core/service/reporting/catalog"
	"github.com/viant/authz"
	"github.com/viant/forge/backend/reporting/registry"
)

// ReportAdmission intersects gateway delivery with the host's existing report
// operation policy. It must authorize the exact pin/definition for the supplied
// operation; gateway visibility is never compile/run/export authorization.
type ReportAdmission func(context.Context, string, identity.ResolvedResource, json.RawMessage) error

// ReportGatewayCatalog connects ordinary reporting service list/get/compile
// paths to the same gateway as windows. It never rewrites provider authority,
// fingerprints, revision or lease, and never grants mutation capabilities.
type ReportGatewayCatalog struct {
	Gateway           *Gateway
	Admission         ReportAdmission
	BeginMetadataRead func(context.Context) (context.Context, func() error, error)
	BuilderWindows    map[string]string
	Capabilities      func(context.Context, identity.VerifiedActor, catalog.ReportCatalogCandidate, identity.ResolvedResource) (catalog.ReportCapabilities, error)
}

func (c *ReportGatewayCatalog) CurrentActor(ctx context.Context) (identity.VerifiedActor, error) {
	if c == nil || c.Gateway == nil {
		return identity.VerifiedActor{}, identity.ErrResourceDenied
	}
	return c.Gateway.actor(ctx)
}
func (c *ReportGatewayCatalog) MetadataRead(ctx context.Context) (context.Context, func() error, error) {
	if c == nil || c.Gateway == nil || c.Admission == nil {
		return nil, nil, identity.ErrResourceDenied
	}
	if c.BeginMetadataRead != nil {
		return c.BeginMetadataRead(ctx)
	}
	actor, err := c.Gateway.actor(ctx)
	if err != nil {
		return nil, nil, err
	}
	return ctx, func() error { return c.Gateway.final(ctx, actor) }, nil
}
func (c *ReportGatewayCatalog) Reader(ctx context.Context, operation string) (reporting.ResourceReader, error) {
	if c == nil || c.Gateway == nil || c.Admission == nil || operation == "" {
		return nil, identity.ErrResourceDenied
	}
	if _, err := c.Gateway.actor(ctx); err != nil {
		return nil, err
	}
	return &gatewayReportReader{catalog: c, operation: operation}, nil
}
func (c *ReportGatewayCatalog) Read(ctx context.Context, ref identity.ResourceRef, pin *identity.ResolvedResource) ([]byte, *identity.ResolvedResource, error) {
	reader, err := c.Reader(ctx, "report.retrieve")
	if err != nil {
		return nil, nil, err
	}
	if pin == nil {
		pin, err = reader.Resolve(ctx, ref)
		if err != nil {
			return nil, nil, err
		}
	}
	if pin.URI != ref.URI || ref.Revision != "" && ref.Revision != pin.Selector() {
		return nil, nil, identity.ErrResourceDenied
	}
	return reader.ReadResolved(ctx, *pin)
}

type gatewayReportReader struct {
	catalog   *ReportGatewayCatalog
	operation string
}

func (r *gatewayReportReader) read(ctx context.Context, ref identity.ResourceRef, pin *identity.ResolvedResource, ownerHint string) (json.RawMessage, *identity.ResolvedResource, error) {
	uri, err := identity.ParseResourceURI(ref.URI)
	if err != nil || uri.Kind != "report" {
		return nil, nil, identity.ErrResourceDenied
	}
	captured, err := r.catalog.Gateway.actor(ctx)
	if err != nil {
		return nil, nil, err
	}
	owner := ownerHint
	if pin != nil {
		owner = pin.ProviderIdentity
		if pin.URI != ref.URI || owner == "" {
			return nil, nil, identity.ErrResourceDenied
		}
	}
	connection, err := r.catalog.Gateway.ConnectionForResource(ctx, uri, owner)
	if err != nil {
		return nil, nil, err
	}
	got, err := r.catalog.Gateway.Get(ctx, connection, ref, pin)
	if err != nil {
		return nil, nil, err
	}
	raw := append(json.RawMessage(nil), got.Resource.DefinitionBytes...)
	if err = r.catalog.Admission(ctx, r.operation, *got.ResolvedResource, append(json.RawMessage(nil), raw...)); err != nil {
		return nil, nil, normalizeReportAdmissionError(err)
	}
	// Revalidate buffered output after host admission, which can observe a new
	// policy/content/identity state or take longer than the provider lease.
	got, err = r.catalog.Gateway.Get(ctx, connection, identity.ResourceRef{URI: ref.URI, Revision: got.ResolvedResource.Selector()}, got.ResolvedResource)
	if err != nil {
		return nil, nil, err
	}
	raw = append(json.RawMessage(nil), got.Resource.DefinitionBytes...)
	if err = r.catalog.Admission(ctx, r.operation, *got.ResolvedResource, append(json.RawMessage(nil), raw...)); err != nil {
		return nil, nil, normalizeReportAdmissionError(err)
	}
	got, err = r.catalog.Gateway.Get(ctx, connection, identity.ResourceRef{URI: ref.URI, Revision: got.ResolvedResource.Selector()}, got.ResolvedResource)
	if err != nil {
		return nil, nil, err
	}
	raw = append(json.RawMessage(nil), got.Resource.DefinitionBytes...)
	fresh, err := r.catalog.Gateway.actor(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !sameLocalActor(captured, fresh) || !got.ResolvedResource.ValidUntil.After(r.catalog.Gateway.now()) {
		return nil, nil, identity.ErrResourceDenied
	}
	if err = r.catalog.Gateway.final(ctx, captured); err != nil {
		return nil, nil, err
	}
	if ctx.Err() != nil || !got.ResolvedResource.ValidUntil.After(r.catalog.Gateway.now()) {
		return nil, nil, identity.ErrResourceDenied
	}
	pinCopy := *got.ResolvedResource
	return raw, &pinCopy, nil
}
func (r *gatewayReportReader) Resolve(ctx context.Context, ref identity.ResourceRef) (*identity.ResolvedResource, error) {
	_, pin, err := r.read(ctx, ref, nil, "")
	return pin, err
}
func (r *gatewayReportReader) ReadResolved(ctx context.Context, pin identity.ResolvedResource) (json.RawMessage, *identity.ResolvedResource, error) {
	return r.read(ctx, identity.ResourceRef{URI: pin.URI, Revision: pin.Selector()}, &pin, "")
}
func (r *gatewayReportReader) ResolveForProvider(ctx context.Context, ref identity.ResourceRef, provider string) (*identity.ResolvedResource, error) {
	if provider == "" {
		return nil, identity.ErrResourceDenied
	}
	_, pin, err := r.read(ctx, ref, nil, provider)
	return pin, err
}
func (c *ReportGatewayCatalog) List(ctx context.Context, input catalog.ReportCatalogQuery) (out *catalog.ReportCatalogResult, failure error) {
	if input.Limit < 0 || input.Limit > 1000 {
		return nil, identity.ErrResource
	}
	scoped, finish, err := c.MetadataRead(ctx)
	if err != nil {
		return nil, err
	}
	if scoped == nil || finish == nil {
		return nil, identity.ErrResourceDenied
	}
	defer func() {
		if err := finish(); err != nil {
			out = nil
			failure = err
		}
		if out != nil {
			if ctx.Err() != nil {
				out = nil
				failure = identity.ErrResourceDenied
				return
			}
			for _, entry := range out.Reports {
				if entry.Resource == nil || !entry.Resource.ValidUntil.After(c.Gateway.now()) {
					out = nil
					failure = identity.ErrResourceDenied
					return
				}
			}
		}
	}()
	ctx = scoped
	actor, err := c.CurrentActor(ctx)
	if err != nil {
		return nil, err
	}
	after := ""
	if input.Cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != input.Cursor {
			return nil, identity.ErrResource
		}
		after = string(decoded)
		uri, err := identity.ParseResourceURI(after)
		if err != nil || uri.Kind != "report" {
			return nil, identity.ErrResource
		}
	}
	rows, err := c.Gateway.List(ctx, "report", input.Namespace)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Resource.URI < rows[j].Resource.URI })
	reader := &gatewayReportReader{catalog: c, operation: "report.retrieve"}
	result := &catalog.ReportCatalogResult{Reports: []catalog.ReportCatalogEntry{}}
	limit := input.Limit
	if limit == 0 {
		limit = 1000
	}
	seen := map[string]bool{}
	for _, row := range rows {
		resource := row.Resource
		if seen[resource.URI] {
			return nil, ErrCollision
		}
		seen[resource.URI] = true
		if resource.URI <= after || input.CurrentUserOnly && resource.OwnerID != actor.Subject {
			continue
		}
		raw, pin, err := reader.read(ctx, identity.ResourceRef{URI: resource.URI}, nil, "")
		if errors.Is(err, identity.ErrResourceDenied) && !reportIdentityRejected(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if pin.ContentFingerprint != resource.ContentFingerprint {
			return nil, identity.ErrResourceStale
		}
		var envelope registry.ReportEnvelope
		if json.Unmarshal(raw, &envelope) != nil {
			return nil, identity.ErrResourceDenied
		}
		candidate := catalog.ReportCatalogCandidate{URI: resource.URI, Title: resource.Title, OwnerID: resource.OwnerID, BuilderRef: envelope.BuilderRef, BuilderWindow: c.BuilderWindows[envelope.BuilderRef], ReportID: resource.URI}
		capabilities := catalog.ReportCapabilities{}
		if c.Capabilities != nil {
			capabilities, err = c.Capabilities(ctx, actor, candidate, *pin)
			if err != nil {
				return nil, err
			}
		}
		_, pin, err = reader.ReadResolved(ctx, *pin)
		if err != nil {
			return nil, err
		}
		if !pin.ValidUntil.After(c.Gateway.now()) {
			return nil, identity.ErrResourceDenied
		}
		if len(result.Reports) == limit {
			result.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(result.Reports[len(result.Reports)-1].URI))
			break
		}
		result.Reports = append(result.Reports, catalog.ReportCatalogEntry{ReportCatalogCandidate: candidate, Namespace: resource.Namespace, Name: resource.Name, OwnedByCurrentUser: resource.OwnerID != "" && resource.OwnerID == actor.Subject, Resource: pin, Capabilities: capabilities})
	}
	return result, nil
}

var _ catalog.Provider = (*ReportGatewayCatalog)(nil)

func reportIdentityRejected(err error) bool {
	return errors.Is(err, policy.ErrIdentityRejected) || errors.Is(err, authz.ErrIdentityDenied)
}
func normalizeReportAdmissionError(err error) error {
	if err != nil && !reportIdentityRejected(err) && (errors.Is(err, policy.ErrDenied) || errors.Is(err, authz.ErrDenied)) {
		return errors.Join(identity.ErrResourceDenied, err)
	}
	return err
}
