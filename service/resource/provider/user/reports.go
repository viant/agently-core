package user

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	iauth "github.com/viant/agently-core/internal/auth"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	useruri "github.com/viant/agently-core/protocol/resource/user"
	"github.com/viant/agently-core/service/reporting"
	producer "github.com/viant/agently-core/service/resource/provider"
)

var ErrUserReportOperationUnsupported = errors.New("user report store operation is unsupported")

// Reports is the managing source for the existing persisted user
// report records. It adds no store, index, consumer authorization or history.
type Reports struct {
	store    reporting.Store
	provider string
}

func NewReports(store reporting.Store, provider string) (*Reports, error) {
	if store == nil || strings.TrimSpace(provider) == "" {
		return nil, producer.ErrUnavailable
	}
	return &Reports{store: store, provider: provider}, nil
}
func (c *Reports) Namespaces() []primitive.NamespaceCapabilities {
	return []primitive.NamespaceCapabilities{{ProviderIdentity: c.provider, Namespace: "users", Kinds: []primitive.KindSupport{{Kind: "report", FormatVersions: []int64{1}, Operations: []string{"list", "get", "create", "update", "archive"}}}}}
}
func (c *Reports) ownURI(ctx context.Context, uri identity.ResourceURI) (string, string, error) {
	if uri.Kind != "report" {
		return "", "", identity.ErrResourceDenied
	}
	return OwnURI(ctx, uri)
}
func userReportOwned(ctx context.Context, owner string, row *reporting.SharedArtifact) bool {
	// Both identities come from the same verified ingress context, never a URI
	// or report payload. Existing legacy owners are not migrated on update.
	return row != nil && (row.OwnerID == owner || row.OwnerID == iauth.EffectiveUserID(ctx))
}
func userReportKind(row *reporting.SharedArtifact) bool {
	return row != nil && (row.Kind == "reportBuilder.savedReportPayload" || row.Kind == "reportBuilder.publishedSnapshot")
}
func userReportStorageID(uri identity.ResourceURI) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(uri.String())).String()
}
func (c *Reports) get(ctx context.Context, uri identity.ResourceURI) (*reporting.SharedArtifact, error) {
	owner, path, err := c.ownURI(ctx, uri)
	if err != nil {
		return nil, err
	}
	storage, err := canonicalStorageContext(ctx)
	if err != nil {
		return nil, err
	}
	row, err := c.store.GetSharedArtifact(storage, userReportStorageID(uri))
	if err == nil {
		if !userReportOwned(ctx, owner, row) {
			return nil, identity.ErrResourceDenied
		}
		if row.ArtifactRef != uri.String() {
			return nil, producer.ErrCatalogCollision
		}
	} else {
		if !errors.Is(err, reporting.ErrNotFound) {
			return nil, err
		}
		row, err = c.store.GetSharedArtifact(ctx, path)
		if errors.Is(err, reporting.ErrNotFound) {
			return nil, producer.ErrCatalogNotFound
		}
		if err != nil {
			return nil, err
		}
		if !userReportOwned(ctx, owner, row) {
			return nil, identity.ErrResourceDenied
		}
		if stored, parseErr := identity.ParseResourceURI(row.ArtifactRef); parseErr == nil && stored.Namespace == "users" && stored.String() != uri.String() {
			return nil, identity.ErrResourceDenied
		}
	}
	if !userReportKind(row) {
		return nil, producer.ErrCatalogNotFound
	}
	return row, nil
}
func (c *Reports) binding(ctx context.Context, owner string, row *reporting.SharedArtifact) (producer.LocalResourceBinding, error) {
	uri, uriErr := useruri.NewURI("report", owner, row.ArtifactID)
	if stored, err := identity.ParseResourceURI(row.ArtifactRef); err == nil && stored.Namespace == "users" {
		if _, _, err = c.ownURI(ctx, stored); err != nil {
			return producer.LocalResourceBinding{}, err
		}
		uri, uriErr = stored, nil
	}
	if uriErr != nil {
		return producer.LocalResourceBinding{}, identity.ErrResource
	}

	if _, err := userReportDefinition(row); err != nil {
		return producer.LocalResourceBinding{}, err
	}
	return producer.LocalResourceBinding{URI: uri.String(), OwnerID: owner, Title: row.Title, FormatVersion: 1, Resolver: func(ctx context.Context, actor identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
		if _, _, err := c.ownURI(ctx, uri); err != nil {
			return nil, err
		}
		return &identity.ResourceResolver{ProviderIdentity: c.provider, Source: &identity.LocalResource{URI: uri, Load: func(ctx context.Context) (json.RawMessage, error) {
			fresh, err := c.get(ctx, uri)
			if err != nil {
				return nil, err
			}
			return userReportDefinition(fresh)
		}}, Policy: userReportWorkingPolicy{uri: uri.String(), actor: actor}}, nil
	}}, nil
}

type userReportWorkingPolicy struct {
	uri   string
	actor identity.VerifiedActor
}

func (p userReportWorkingPolicy) SelectRevision(ctx context.Context, ref identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if ref.URI != p.uri || ref.Revision != "" && ref.Revision != identity.WorkingCandidate || len(c) != 1 || c[0].Kind != identity.WorkingCandidate {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	binding, err := userAuthority(ctx, p.uri)
	if err != nil {
		return identity.ResourceDecision{}, err
	}
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: binding, ValidUntil: p.actor.ValidUntil}, nil
}
func (c *Reports) List(ctx context.Context, actor identity.VerifiedActor, kind, namespace string) ([]producer.LocalResourceBinding, error) {
	owner, err := CanonicalOwner(ctx)
	if err != nil {
		return nil, err
	}
	if kind != "report" || namespace != "" && namespace != "users" {
		return nil, nil
	}
	storage, err := canonicalStorageContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := c.store.ListSharedArtifacts(storage)
	if err != nil {
		return nil, err
	}
	if iauth.EffectiveUserID(ctx) != owner {
		legacy, err := c.store.ListSharedArtifacts(ctx)
		if err != nil {
			return nil, err
		}
		rows = append(rows, legacy...)
	}
	seen := map[string]bool{}
	var bindings []producer.LocalResourceBinding
	for _, row := range rows {
		if !userReportOwned(ctx, owner, row) || !userReportKind(row) {
			continue
		}
		if seen[row.ArtifactID] {
			continue
		}
		seen[row.ArtifactID] = true
		binding, err := c.binding(ctx, owner, row)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}
func (c *Reports) Lookup(ctx context.Context, actor identity.VerifiedActor, uri identity.ResourceURI) (producer.LocalResourceBinding, error) {
	owner, _, err := c.ownURI(ctx, uri)
	if err != nil {
		return producer.LocalResourceBinding{}, err
	}
	row, err := c.get(ctx, uri)
	if err != nil {
		return producer.LocalResourceBinding{}, err
	}
	return c.binding(ctx, owner, row)
}
func (c *Reports) GetResource(ctx context.Context, value string) (*primitive.ResourceDescriptor, error) {
	uri, err := identity.ParseResourceURI(value)
	if err != nil {
		return nil, err
	}
	row, err := c.get(ctx, uri)
	if err != nil {
		return nil, err
	}
	raw, err := userReportDefinition(row)
	if err != nil {
		return nil, err
	}
	owner, _, _ := c.ownURI(ctx, uri)
	return &primitive.ResourceDescriptor{ResourceResult: primitive.ResourceResult{URI: value, ContentFingerprint: identity.ContentFingerprint(raw)}, WorkspaceID: row.WorkspaceID, ResourceKind: "report", Namespace: "users", Name: uri.Name, OwnerID: owner, Title: row.Title, Lifecycle: "working"}, nil
}
func rejectUserReportConstraints(etag, revision int64, fingerprint, key string) error {
	if etag != 0 || revision != 0 || fingerprint != "" || key != "" {
		return ErrUserReportOperationUnsupported
	}
	return nil
}
func (c *Reports) CreateResource(ctx context.Context, input primitive.CreateResourceInput) (*primitive.ResourceResult, error) {
	uri, err := identity.ParseResourceURI(input.URI)
	if err != nil {
		return nil, err
	}
	owner, _, err := c.ownURI(ctx, uri)
	if err != nil {
		return nil, err
	}
	if input.FormatVersion != 1 || input.GroupID != "" || input.ACLMode != "" {
		return nil, ErrUserReportOperationUnsupported
	}
	if err = rejectUserReportConstraints(0, 0, "", input.IdempotencyKey); err != nil {
		return nil, err
	}
	envelope, err := decodeUserReport(input.Definition)
	if err != nil {
		return nil, err
	}
	row := &reporting.SharedArtifact{ArtifactID: userReportStorageID(uri), OwnerID: owner, Kind: "reportBuilder.savedReportPayload", Lifecycle: "draft", WorkspaceID: input.WorkspaceID, ArtifactRef: uri.String(), ReportID: uri.String(), Title: input.Title, Document: cloneReportJSON(envelope.ReportDocument), ReportSpec: cloneReportJSON(envelope.ReportSpec), ResourceDefinition: cloneReportJSON(input.Definition), CreatedAt: time.Now()}
	storage, err := canonicalStorageContext(ctx)
	if err != nil {
		return nil, err
	}
	if err = c.store.CreateSharedArtifact(storage, row); err != nil {
		return nil, err
	}
	return &primitive.ResourceResult{URI: uri.String(), ContentFingerprint: identity.ContentFingerprint(input.Definition)}, nil
}
func (c *Reports) UpdateDraft(ctx context.Context, value string, input primitive.UpdateDraftInput) (*primitive.ResourceResult, error) {
	uri, err := identity.ParseResourceURI(value)
	if err != nil {
		return nil, err
	}
	row, err := c.get(ctx, uri)
	if err != nil {
		return nil, err
	}
	if input.FormatVersion != 1 {
		return nil, ErrUserReportOperationUnsupported
	}
	if err = rejectUserReportConstraints(input.ExpectedRowEtag, input.ExpectedDraftRevision, input.ExpectedContentFingerprint, input.IdempotencyKey); err != nil {
		return nil, err
	}
	envelope, err := decodeUserReport(input.Definition)
	if err != nil {
		return nil, err
	}
	updated := cloneReportRow(row)
	updated.ResourceDefinition = cloneReportJSON(input.Definition)
	updated.Document = cloneReportJSON(envelope.ReportDocument)
	updated.ReportSpec = cloneReportJSON(envelope.ReportSpec)
	if input.Title != nil {
		updated.Title = *input.Title
	}
	now := time.Now()
	updated.UpdatedAt = &now
	storage, err := rowStorageContext(ctx, updated.OwnerID)
	if err != nil {
		return nil, err
	}
	if err = c.store.UpdateSharedArtifact(storage, updated); err != nil {
		return nil, err
	}
	return &primitive.ResourceResult{URI: value, ContentFingerprint: identity.ContentFingerprint(input.Definition)}, nil
}
func (c *Reports) ArchiveResource(ctx context.Context, value string, input primitive.ArchiveResourceInput) (*primitive.ResourceResult, error) {
	uri, err := identity.ParseResourceURI(value)
	if err != nil {
		return nil, err
	}
	row, err := c.get(ctx, uri)
	if err != nil {
		return nil, err
	}
	if err = rejectUserReportConstraints(0, input.ExpectedHeadRevision, "", input.IdempotencyKey); err != nil {
		return nil, err
	}
	storage, err := rowStorageContext(ctx, row.OwnerID)
	if err != nil {
		return nil, err
	}
	if err = c.store.DeleteSharedArtifact(storage, row.ArtifactID); err != nil {
		return nil, err
	}
	return &primitive.ResourceResult{URI: value}, nil
}
func (*Reports) StampResource(context.Context, string, primitive.StampResourceInput) (*primitive.ResourceResult, error) {
	return nil, ErrUserReportOperationUnsupported
}
func (*Reports) CloneResource(context.Context, primitive.CloneResourceInput) (*primitive.ResourceResult, error) {
	return nil, ErrUserReportOperationUnsupported
}

var _ producer.LocalCatalog = (*Reports)(nil)
var _ primitive.ResourceAuthoring = (*Reports)(nil)

func cloneReportJSON(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}
func cloneReportRow(row *reporting.SharedArtifact) *reporting.SharedArtifact {
	if row == nil {
		return nil
	}
	raw, _ := json.Marshal(row)
	var copy reporting.SharedArtifact
	_ = json.Unmarshal(raw, &copy)
	return &copy
}
