package user

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	iauth "github.com/viant/agently-core/internal/auth"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	useruri "github.com/viant/agently-core/protocol/resource/user"
	"github.com/viant/agently-core/service/reporting"
	producer "github.com/viant/agently-core/service/resource/provider"
	sharedengine "github.com/viant/forge/backend/reporting/engine"
	"github.com/viant/forge/backend/reporting/registry"
)

var ErrUserReportOperationUnsupported = fmt.Errorf("user report store operation is unsupported: %w", errors.ErrUnsupported)

// Reports is the managing source for the existing persisted user
// report records. It adds no store, index, consumer authorization or history.
type Reports struct {
	store    reporting.Store
	provider string
	reader   reporting.ResourceReaderFactory
	validate func(context.Context, string, json.RawMessage) error
}

func NewReports(store reporting.Store, provider string, readers ...reporting.ResourceReaderFactory) (*Reports, error) {
	if store == nil || strings.TrimSpace(provider) == "" {
		return nil, producer.ErrUnavailable
	}
	if len(readers) > 1 {
		return nil, ErrUserReportOperationUnsupported
	}
	var reader reporting.ResourceReaderFactory
	if len(readers) == 1 {
		reader = readers[0]
	}
	engine := sharedengine.New(sharedengine.Options{Compiler: sharedengine.NewReportSpecCompiler(nil)})
	return &Reports{store: store, provider: provider, reader: reader, validate: engine.ValidateDefinition}, nil
}
func (c *Reports) Namespaces() []primitive.NamespaceCapabilities {
	return []primitive.NamespaceCapabilities{{ProviderIdentity: c.provider, Namespace: "users", Kinds: []primitive.KindSupport{{Kind: "report", FormatVersions: []int64{1, 2}, Operations: []string{"list", "get", "create", "update", "archive", "clone"}}}}}
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
	row, err := c.getStored(ctx, uri)
	if err != nil {
		return nil, err
	}
	if row.Lifecycle == "archived" {
		return nil, producer.ErrCatalogNotFound
	}
	return row, nil
}

// Archive uses the same owner-checked lookup while ordinary reads expose only
// active resources. Archived definitions stay in the existing reporting store.
func (c *Reports) getStored(ctx context.Context, uri identity.ResourceURI) (*reporting.SharedArtifact, error) {
	owner, path, err := c.ownURI(ctx, uri)
	if err != nil {
		return nil, err
	}
	storage, err := canonicalStorageContext(ctx)
	if err != nil {
		return nil, err
	}
	get := c.store.GetSharedArtifact
	if batch, ok := ctx.Value(catalogRowsKey{}).(*catalogRows); ok && batch.source == c && batch.owner == owner && batch.effective == iauth.EffectiveUserID(ctx) && batch.provider == iauth.Provider(ctx) {
		get = batch.get
	}
	row, err := get(storage, userReportStorageID(uri))
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
		row, err = get(ctx, path)
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
func (c *Reports) rowURI(ctx context.Context, owner string, row *reporting.SharedArtifact) (identity.ResourceURI, error) {
	uri, uriErr := useruri.NewURI("report", owner, row.ArtifactID)
	if stored, err := identity.ParseResourceURI(row.ArtifactRef); err == nil && stored.Namespace == "users" {
		if _, _, err = c.ownURI(ctx, stored); err != nil {
			return identity.ResourceURI{}, err
		}
		uri, uriErr = stored, nil
	}
	if uriErr != nil {
		return identity.ResourceURI{}, identity.ErrResource
	}

	return uri, nil
}

func (c *Reports) binding(ctx context.Context, owner string, row *reporting.SharedArtifact) (producer.LocalResourceBinding, error) {
	uri, err := c.rowURI(ctx, owner, row)
	if err != nil {
		return producer.LocalResourceBinding{}, err
	}
	_, formatVersion, err := userReportDefinitionAndVersion(row)
	if err != nil {
		return producer.LocalResourceBinding{}, err
	}
	return producer.LocalResourceBinding{URI: uri.String(), OwnerID: owner, Title: row.Title, FormatVersion: formatVersion, Resolver: func(ctx context.Context, actor identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
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

// ListMetadataPage advances through canonical and legacy owner records. The two
// queries retain their own verified storage identities. Offsets address raw
// header rows, so archived and unrelated rows cannot repeat a page.
func (c *Reports) ListMetadataPage(ctx context.Context, kind, namespace, cursor string, limit int) ([]primitive.ResourceState, string, error) {
	owner, err := CanonicalOwner(ctx)
	if err != nil {
		return nil, "", err
	}
	if kind != "report" || namespace != "" && namespace != "users" {
		return nil, "", nil
	}
	if limit < 1 {
		return nil, "", identity.ErrResource
	}
	legacy := strings.HasPrefix(cursor, "legacy-")
	offset := 0
	if cursor != "" {
		value := strings.TrimPrefix(cursor, "legacy-")
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 || strconv.Itoa(offset) != value {
			return nil, "", identity.ErrResource
		}
	}
	effective := iauth.EffectiveUserID(ctx)
	if legacy && effective == owner {
		return nil, "", identity.ErrResource
	}
	storage := ctx
	if !legacy {
		storage, err = canonicalStorageContext(ctx)
		if err != nil {
			return nil, "", err
		}
	}
	var rows []*reporting.SharedArtifact
	if source, ok := c.store.(interface {
		ListSharedArtifactHeaders(context.Context, int, int) ([]*reporting.SharedArtifact, error)
	}); ok {
		rows, err = source.ListSharedArtifactHeaders(storage, offset, limit+1)
	} else {
		rows, err = c.store.ListSharedArtifacts(storage)
		if err == nil {
			visible := make([]*reporting.SharedArtifact, 0, len(rows))
			for _, row := range rows {
				if row != nil && row.OwnerID == iauth.EffectiveUserID(storage) {
					visible = append(visible, row)
				}
			}
			rows = visible
			sort.SliceStable(rows, func(i, j int) bool {
				return rows[i].ArtifactID < rows[j].ArtifactID
			})
			start := min(offset, len(rows))
			rows = rows[start : start+min(limit+1, len(rows)-start)]
		}
	}
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = strconv.Itoa(offset + limit)
		if legacy {
			next = "legacy-" + next
		}
	} else if !legacy && owner != effective {
		next = "legacy-0"
	}
	states := make([]primitive.ResourceState, 0, len(rows))
	for _, row := range rows {
		if row == nil || row.OwnerID != iauth.EffectiveUserID(storage) || !userReportKind(row) || row.Lifecycle == "archived" {
			continue
		}
		uri, err := c.rowURI(ctx, owner, row)
		if err != nil {
			return nil, "", err
		}
		states = append(states, primitive.ResourceState{Kind: kind, Namespace: uri.Namespace, Name: uri.Name, URI: uri.String(), OwnerID: owner, Title: row.Title, Lifecycle: identity.WorkingCandidate, Revision: identity.WorkingCandidate, RowEtag: int64(row.Version)})
	}
	return states, next, nil
}

func (c *Reports) List(ctx context.Context, actor identity.VerifiedActor, kind, namespace string) ([]producer.LocalResourceBinding, error) {
	owner, rows, err := c.ownedRows(ctx, kind, namespace, c.store.ListSharedArtifacts)
	if err != nil {
		return nil, err
	}
	bindings := make([]producer.LocalResourceBinding, 0, len(rows))
	for _, row := range rows {
		binding, err := c.binding(ctx, owner, row)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

func (c *Reports) ownedRows(ctx context.Context, kind, namespace string, list func(context.Context) ([]*reporting.SharedArtifact, error)) (string, []*reporting.SharedArtifact, error) {
	owner, err := CanonicalOwner(ctx)
	if err != nil {
		return "", nil, err
	}
	if kind != "report" || namespace != "" && namespace != "users" {
		return "", nil, nil
	}
	storage, err := canonicalStorageContext(ctx)
	if err != nil {
		return "", nil, err
	}
	rows, err := list(storage)
	if err != nil {
		return "", nil, err
	}
	if iauth.EffectiveUserID(ctx) != owner {
		legacy, err := list(ctx)
		if err != nil {
			return "", nil, err
		}
		rows = append(rows, legacy...)
	}
	seen := map[string]bool{}
	var visible []*reporting.SharedArtifact
	for _, row := range rows {
		if !userReportOwned(ctx, owner, row) || !userReportKind(row) || row.Lifecycle == "archived" {
			continue
		}
		if seen[row.ArtifactID] {
			continue
		}
		seen[row.ArtifactID] = true
		visible = append(visible, row)
	}
	return owner, visible, nil
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
	return &primitive.ResourceDescriptor{ResourceResult: primitive.ResourceResult{URI: value, RowEtag: int64(row.Version), ContentFingerprint: identity.ContentFingerprint(raw)}, WorkspaceID: row.WorkspaceID, ResourceKind: "report", Namespace: "users", Name: uri.Name, OwnerID: owner, Title: row.Title, Lifecycle: "working"}, nil
}
func rejectUserReportConstraints(etag, revision int64, fingerprint, key string) error {
	if etag != 0 || revision != 0 || fingerprint != "" || key != "" {
		return ErrUserReportOperationUnsupported
	}
	return nil
}
func (c *Reports) CreateResource(ctx context.Context, input primitive.CreateResourceInput) (*primitive.ResourceResult, error) {
	return c.createReport(ctx, input, nil)
}

func (c *Reports) CloneResource(ctx context.Context, input primitive.CloneResourceInput) (*primitive.ResourceResult, error) {
	target := primitive.CreateResourceInput{URI: input.TargetURI, WorkspaceID: input.TargetWorkspaceID, Title: input.Title, IdempotencyKey: input.IdempotencyKey}
	return c.createReport(ctx, target, &input)
}

func createRequestFingerprint(input primitive.CreateResourceInput) string {
	request, _ := json.Marshal([]any{input.URI, input.WorkspaceID, input.Title, input.GroupID, input.ACLMode, input.FormatVersion, identity.ContentFingerprint(input.Definition), identity.ContentFingerprint(input.SourceManifest)})
	return identity.ContentFingerprint(request)
}

func (c *Reports) createReport(ctx context.Context, input primitive.CreateResourceInput, clone *primitive.CloneResourceInput) (*primitive.ResourceResult, error) {
	input.SourceManifest = normalizeReportSourceManifest(input.SourceManifest)
	uri, err := identity.ParseResourceURI(input.URI)
	if err != nil {
		return nil, err
	}
	owner, _, err := c.ownURI(ctx, uri)
	if err != nil {
		return nil, err
	}
	if input.GroupID != "" || input.ACLMode != "" {
		return nil, ErrUserReportOperationUnsupported
	}
	if len(input.IdempotencyKey) > 512 {
		return nil, fmt.Errorf("idempotency key too long")
	}
	var envelope registry.ReportEnvelope
	if clone == nil {
		if input.FormatVersion != 1 && input.FormatVersion != 2 {
			return nil, ErrUserReportOperationUnsupported
		}
		envelope, err = decodeUserReport(input.Definition)
		if err != nil {
			return nil, err
		}
		if int64(envelope.SchemaVersion) != input.FormatVersion {
			return nil, fmt.Errorf("report format/schema version mismatch")
		}
	}
	row := &reporting.SharedArtifact{ArtifactID: userReportStorageID(uri), OwnerID: owner, Kind: "reportBuilder.savedReportPayload", Lifecycle: "draft", Version: 1, WorkspaceID: input.WorkspaceID, ArtifactRef: uri.String(), ReportID: uri.String(), Title: input.Title, ResourceDefinition: cloneReportJSON(input.Definition), CreatedAt: time.Now()}
	storage, err := canonicalStorageContext(ctx)
	if err != nil {
		return nil, err
	}
	// An edited logical copy is a create with caller-supplied content and an
	// unpinned source. Resolve it through the same admission path as clone.
	copySource := clone
	if clone == nil && len(input.SourceManifest) != 0 {
		var manifest reporting.PersonalReportSourceManifest
		if err := json.Unmarshal(input.SourceManifest, &manifest); err != nil {
			return nil, fmt.Errorf("personal report source manifest: %w: %w", identity.ErrResource, err)
		}
		if manifest.Operation == "copy" {
			if manifest.Source == nil || manifest.Provider != "" && manifest.ExpectedSourceFingerprint == "" {
				return nil, identity.ErrResource
			}
			copySource = &primitive.CloneResourceInput{Source: *manifest.Source, SourceProvider: manifest.Provider, ExpectedSourceFingerprint: manifest.ExpectedSourceFingerprint}
		}
	}
	logicalEdit := clone == nil && copySource != nil && copySource.ExpectedSourceFingerprint == ""
	var legacyReplay *reporting.SharedArtifact
	if input.IdempotencyKey != "" {
		var fingerprint string
		if clone != nil {
			raw, _ := json.Marshal(clone)
			fingerprint = "clone:" + identity.ContentFingerprint(raw)
		} else if logicalEdit {
			fingerprint = "edit:" + createRequestFingerprint(input)
		} else {
			fingerprint = createRequestFingerprint(input)
		}
		row.Metadata, _ = json.Marshal(map[string]string{"createOperation": input.IdempotencyKey, "createRequestFingerprint": fingerprint})
		if existing, getErr := c.store.GetSharedArtifact(storage, row.ArtifactID); getErr == nil {
			var receipt struct {
				Fingerprint string `json:"createRequestFingerprint"`
				Operation   string `json:"createOperation"`
			}
			if len(existing.Metadata) > 0 {
				if err := json.Unmarshal(existing.Metadata, &receipt); err != nil {
					return nil, err
				}
			}
			legacyCreate := receipt.Fingerprint != "" && !strings.Contains(receipt.Fingerprint, ":")
			needsOriginal := receipt.Fingerprint == "" || (logicalEdit || clone != nil) && legacyCreate
			if !needsOriginal || receipt.Operation != input.IdempotencyKey || existing.Version != 1 || existing.Lifecycle == "archived" {
				return replayUserReportCreate(existing, row, input.IdempotencyKey)
			}
			legacyReplay = existing
		} else if !errors.Is(getErr, reporting.ErrNotFound) {
			return nil, getErr
		}
	}
	if copySource != nil {
		source, pin, err := reporting.PrepareUserReportClone(ctx, c.reader, c.provider, row, *copySource)
		if err != nil {
			return nil, err
		}
		if legacyReplay != nil && (logicalEdit || clone != nil) {
			// Older hosts sent the admitted definition and pin through create.
			// Rebuild that exact receipt only for those existing records.
			if clone != nil {
				sourceEnvelope, err := decodeUserReport(source)
				if err != nil {
					return nil, err
				}
				input.FormatVersion = int64(sourceEnvelope.SchemaVersion)
				input.Definition = source
			}
			input.SourceManifest, err = json.Marshal(reporting.PersonalReportSourceManifest{Operation: "copy", Source: &identity.ResourceRef{URI: pin.URI, Revision: pin.Selector()}, Provider: pin.ProviderIdentity, ExpectedSourceFingerprint: pin.ContentFingerprint})
			if err != nil {
				return nil, err
			}
			row.Metadata, _ = json.Marshal(map[string]string{"createOperation": input.IdempotencyKey, "createRequestFingerprint": createRequestFingerprint(input)})
		}
	} else if err = reporting.PrepareUserReportWrite(ctx, c.reader, c.provider, row, nil, input.SourceManifest); err != nil {
		return nil, err
	}
	if clone != nil || !bytes.Equal(input.Definition, row.ResourceDefinition) {
		envelope, err = decodeUserReport(row.ResourceDefinition)
		if err != nil {
			return nil, err
		}
		if clone == nil && int64(envelope.SchemaVersion) != input.FormatVersion {
			return nil, fmt.Errorf("report format/schema version mismatch")
		}
	}
	if err = reporting.ValidatePersonalReportCandidate(ctx, row, c.validate); err != nil {
		return nil, err
	}
	row.Document, row.ReportSpec = cloneReportJSON(envelope.ReportDocument), cloneReportJSON(envelope.ReportSpec)
	if legacyReplay != nil {
		return replayUserReportCreate(legacyReplay, row, input.IdempotencyKey)
	}
	if err = c.store.CreateSharedArtifact(storage, row); err != nil {
		if input.IdempotencyKey != "" {
			if existing, getErr := c.store.GetSharedArtifact(storage, row.ArtifactID); getErr == nil {
				return replayUserReportCreate(existing, row, input.IdempotencyKey)
			}
		}
		return nil, err
	}
	return &primitive.ResourceResult{URI: uri.String(), RowEtag: int64(row.Version), ContentFingerprint: identity.ContentFingerprint(row.ResourceDefinition)}, nil
}
func (c *Reports) UpdateDraft(ctx context.Context, value string, input primitive.UpdateDraftInput) (*primitive.ResourceResult, error) {
	input.SourceManifest = normalizeReportSourceManifest(input.SourceManifest)
	uri, err := identity.ParseResourceURI(value)
	if err != nil {
		return nil, err
	}
	row, err := c.get(ctx, uri)
	if err != nil {
		return nil, err
	}
	if input.FormatVersion != 1 && input.FormatVersion != 2 {
		return nil, ErrUserReportOperationUnsupported
	}
	if err = rejectUserReportConstraints(0, input.ExpectedDraftRevision, "", input.IdempotencyKey); err != nil {
		return nil, err
	}
	if (row.IsPersonalCopy() || len(input.SourceManifest) > 0) && input.ExpectedRowEtag == 0 && input.ExpectedContentFingerprint == "" {
		return nil, fmt.Errorf("source-derived update requires an expected row version or content fingerprint: %w", identity.ErrResource)
	}
	current, err := userReportDefinition(row)
	if err != nil {
		return nil, err
	}
	if input.ExpectedContentFingerprint != "" && input.ExpectedContentFingerprint != identity.ContentFingerprint(current) {
		return nil, identity.ErrResourceStale
	}
	if input.ExpectedRowEtag != 0 && input.ExpectedRowEtag != int64(row.Version) {
		return nil, identity.ErrResourceStale
	}
	envelope, err := decodeUserReport(input.Definition)
	if err == nil && int64(envelope.SchemaVersion) != input.FormatVersion {
		err = fmt.Errorf("report format/schema version mismatch")
	}
	if err != nil {
		return nil, err
	}
	// Copy the row by value. This operation replaces each changed blob below;
	// untouched opaque fields and private store metadata stay byte-for-byte intact.
	updated := *row
	updated.Version = row.Version + 1
	updated.ResourceDefinition = cloneReportJSON(input.Definition)
	updated.Document = cloneReportJSON(envelope.ReportDocument)
	updated.ReportSpec = cloneReportJSON(envelope.ReportSpec)
	if input.Title != nil {
		updated.Title = *input.Title
	}
	previous := *row
	previous.ResourceDefinition = current
	if err = reporting.PrepareUserReportWrite(ctx, c.reader, c.provider, &updated, &previous, input.SourceManifest); err != nil {
		return nil, err
	}
	if !bytes.Equal(input.Definition, updated.ResourceDefinition) {
		envelope, err = decodeUserReport(updated.ResourceDefinition)
		if err != nil {
			return nil, err
		}
		if int64(envelope.SchemaVersion) != input.FormatVersion {
			return nil, fmt.Errorf("report format/schema version mismatch")
		}
	}
	if err = reporting.ValidatePersonalReportCandidate(ctx, &updated, c.validate); err != nil {
		return nil, err
	}
	updated.Document, updated.ReportSpec = cloneReportJSON(envelope.ReportDocument), cloneReportJSON(envelope.ReportSpec)
	now := time.Now()
	updated.UpdatedAt = &now
	storage, err := rowStorageContext(ctx, updated.OwnerID)
	if err != nil {
		return nil, err
	}
	writer, ok := c.store.(interface {
		UpdateSharedArtifactCAS(context.Context, *reporting.SharedArtifact, int) error
	})
	if !ok {
		return nil, ErrUserReportOperationUnsupported
	}
	if err = writer.UpdateSharedArtifactCAS(storage, &updated, row.Version); err != nil {
		return nil, err
	}
	return &primitive.ResourceResult{URI: value, RowEtag: int64(updated.Version), ContentFingerprint: identity.ContentFingerprint(updated.ResourceDefinition)}, nil
}
func (c *Reports) ArchiveResource(ctx context.Context, value string, input primitive.ArchiveResourceInput) (*primitive.ResourceResult, error) {
	uri, err := identity.ParseResourceURI(value)
	if err != nil {
		return nil, err
	}
	row, err := c.getStored(ctx, uri)
	if err != nil {
		return nil, err
	}
	if err = rejectUserReportConstraints(0, 0, "", input.IdempotencyKey); err != nil {
		return nil, err
	}
	if row.Lifecycle == "archived" {
		if input.ExpectedHeadRevision != 0 && input.ExpectedHeadRevision != int64(row.Version-1) {
			return nil, identity.ErrResourceStale
		}
		return &primitive.ResourceResult{URI: value, RowEtag: int64(row.Version)}, nil
	}
	if input.ExpectedHeadRevision != 0 && input.ExpectedHeadRevision != int64(row.Version) {
		return nil, identity.ErrResourceStale
	}
	storage, err := rowStorageContext(ctx, row.OwnerID)
	if err != nil {
		return nil, err
	}
	writer, ok := c.store.(interface {
		UpdateSharedArtifactCAS(context.Context, *reporting.SharedArtifact, int) error
	})
	if !ok {
		return nil, ErrUserReportOperationUnsupported
	}
	updated := *row
	updated.Lifecycle, updated.Version = "archived", row.Version+1
	now := time.Now()
	updated.UpdatedAt = &now
	if err = writer.UpdateSharedArtifactCAS(storage, &updated, row.Version); err != nil {
		return nil, err
	}
	return &primitive.ResourceResult{URI: value, RowEtag: int64(updated.Version)}, nil
}
func (*Reports) StampResource(context.Context, string, primitive.StampResourceInput) (*primitive.ResourceResult, error) {
	return nil, ErrUserReportOperationUnsupported
}

var _ producer.LocalCatalog = (*Reports)(nil)
var _ primitive.ResourceAuthoring = (*Reports)(nil)

func cloneReportJSON(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

func replayUserReportCreate(existing, candidate *reporting.SharedArtifact, key string) (*primitive.ResourceResult, error) {
	var metadata struct {
		CreateOperation    string `json:"createOperation"`
		RequestFingerprint string `json:"createRequestFingerprint"`
	}
	if existing == nil || existing.OwnerID != candidate.OwnerID || existing.ArtifactRef != candidate.ArtifactRef {
		return nil, identity.ErrResourceDenied
	}
	if existing.Lifecycle == "archived" {
		return nil, fmt.Errorf("report is archived: %w", identity.ErrResourceStale)
	}
	if len(existing.Metadata) == 0 {
		return nil, identity.ErrResourceStale
	}
	if err := json.Unmarshal(existing.Metadata, &metadata); err != nil {
		return nil, err
	}
	var candidateReceipt struct {
		RequestFingerprint string `json:"createRequestFingerprint"`
	}
	if len(candidate.Metadata) > 0 {
		if err := json.Unmarshal(candidate.Metadata, &candidateReceipt); err != nil {
			return nil, err
		}
	}
	sameRequest := metadata.RequestFingerprint == candidateReceipt.RequestFingerprint && existing.Version == 1
	if metadata.RequestFingerprint == "" {
		sameRequest = bytes.Equal(existing.ResourceDefinition, candidate.ResourceDefinition) && existing.Title == candidate.Title && existing.Version == 1
	}
	if metadata.CreateOperation != key || !sameRequest {
		return nil, identity.ErrResourceStale
	}
	raw, err := userReportDefinition(existing)
	if err != nil {
		return nil, err
	}
	return &primitive.ResourceResult{URI: existing.ArtifactRef, RowEtag: int64(existing.Version), ContentFingerprint: identity.ContentFingerprint(raw)}, nil
}

func normalizeReportSourceManifest(raw json.RawMessage) json.RawMessage {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return nil
	}
	return raw
}

// SetDefinitionValidator reuses the host's configured compiler at composition.
func (c *Reports) SetDefinitionValidator(validate func(context.Context, string, json.RawMessage) error) {
	if validate != nil {
		c.validate = validate
	}
}
