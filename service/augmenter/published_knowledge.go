package augmenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	iofs "io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/viant/afs/file"
	"github.com/viant/afs/object"
	"github.com/viant/afs/storage"
	afsurl "github.com/viant/afs/url"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/embedius/document"
	"github.com/viant/embedius/indexer"
	"github.com/viant/embedius/indexer/cache"
	embfs "github.com/viant/embedius/indexer/fs"
	"github.com/viant/embedius/metadata"
	"github.com/viant/embedius/schema"
	primitive "github.com/viant/forge/backend/provider/contract"
	identity "github.com/viant/forge/backend/resource"
	shared "github.com/viant/forge/backend/resourcehost"
)

type knowledgeSelectionsKey struct{}
type knowledgeIndexLocationKey struct{}
type knowledgeSelection struct {
	uri   string
	value *shared.PublishedValue[primitive.KnowledgeEnvelope]
}

// SetKnowledgeGateway binds the existing native publication owner at startup.
// It must be called before the augmenter serves requests.
func (s *Service) SetKnowledgeGateway(gateway *resources.Gateway) { s.knowledgeGateway = gateway }

// ReadPublishedKnowledgeFile reuses the runtime's existing Knowledge owner.
func (s *Service) ReadPublishedKnowledgeFile(ctx context.Context, root, child string) ([]byte, error) {
	return resources.ReadKnowledgeFile(ctx, s.knowledgeGateway, root, child)
}

// PublishedKnowledgeClaims reports a registered namespace, never a read grant.
func (s *Service) PublishedKnowledgeClaims(root string) bool {
	return s != nil && s.knowledgeGateway != nil && s.knowledgeGateway.OwnsPublishedMetadata(root)
}

// PublishedKnowledgeRoots pages the existing local cohort metadata catalog.
func (s *Service) PublishedKnowledgeRoots(ctx context.Context, query resources.CatalogQuery) (*primitive.CatalogPage[resources.Located[json.RawMessage]], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.knowledgeGateway == nil {
		return &primitive.CatalogPage[resources.Located[json.RawMessage]]{Complete: true}, nil
	}
	connections := s.knowledgeGateway.PublishedMetadataConnections()
	if len(connections) == 0 {
		return &primitive.CatalogPage[resources.Located[json.RawMessage]]{Complete: true}, nil
	}
	return resources.ListPageFrom[json.RawMessage](ctx, s.knowledgeGateway, connections, primitive.KnowledgeKind, query, false)
}

func knowledgeURI(uri string) bool { return strings.HasPrefix(strings.TrimSpace(uri), "knowledge://") }
func selectedKnowledge(ctx context.Context, uri string) *knowledgeSelection {
	selected, _ := ctx.Value(knowledgeSelectionsKey{}).(map[string]*knowledgeSelection)
	var best *knowledgeSelection
	for root, value := range selected {
		if uri == root || strings.HasPrefix(uri, root+"/") {
			if best == nil || len(root) > len(best.uri) {
				best = value
			}
		}
	}
	return best
}

// Published URI roots retain their namespace; filesystem path-only matching
// would let an unrelated configured namespace shadow the owning corpus.
func (s *Service) knowledgeRoot(ctx context.Context, location string) string {
	best := ""
	consider := func(uri string) {
		uri = strings.TrimRight(uri, "/")
		if knowledgeURI(uri) && (location == uri || strings.HasPrefix(location, uri+"/")) && len(uri) > len(best) {
			best = uri
		}
	}
	for _, root := range s.localRoots {
		consider(root.URI)
	}
	for _, root := range localRootsFromContext(ctx) {
		consider(root.URI)
	}
	if best != "" {
		return best
	}
	return strings.TrimRight(location, "/")
}
func (s *Service) prepareKnowledge(ctx context.Context, locations []string) (context.Context, error) {
	selected := map[string]*knowledgeSelection{}
	for _, location := range locations {
		if !knowledgeURI(location) {
			continue
		}
		root := s.knowledgeRoot(ctx, location)
		parsed, err := identity.ParseResourceURI(root)
		if err != nil || parsed.Kind != primitive.KnowledgeKind {
			return ctx, identity.ErrResourceDenied
		}
		if selected[root] != nil {
			continue
		}
		if s.knowledgeGateway == nil {
			return ctx, identity.ErrResourceDenied
		}
		value, err := resources.ReadKnowledge(ctx, s.knowledgeGateway, root)
		if err != nil {
			return ctx, err
		}
		selected[root] = &knowledgeSelection{uri: root, value: value}
	}
	if len(selected) == 0 {
		return ctx, nil
	}
	return context.WithValue(ctx, knowledgeSelectionsKey{}, selected), nil
}
func (s *Service) verifyKnowledge(ctx context.Context) error {
	selected, _ := ctx.Value(knowledgeSelectionsKey{}).(map[string]*knowledgeSelection)
	for _, selection := range selected {
		if err := s.knowledgeGateway.VerifyPublishedMetadata(ctx, *selection.value.Resource); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) knowledgeNamespace(ctx context.Context, uri, selectionKey string) (string, bool, error) {
	if !knowledgeURI(uri) {
		return "", false, nil
	}
	selection := selectedKnowledge(ctx, uri)
	if selection == nil || s.knowledgeGateway == nil {
		return "", true, identity.ErrResourceDenied
	}
	if err := s.knowledgeGateway.VerifyPublishedMetadata(ctx, *selection.value.Resource); err != nil {
		return "", true, err
	}
	pin := selection.value.Resource
	digest := sha256.Sum256([]byte(selectionKey + "\x00" + pin.ProviderIdentity + "\x00" + uri + "\x00" + pin.ContentFingerprint))
	return fmt.Sprintf("knowledge_%x", digest), true, nil
}
func (s *Service) knowledgeMetadata(ctx context.Context, uri string) metadata.Config {
	location, _ := ctx.Value(knowledgeIndexLocationKey{}).(string)
	if !knowledgeURI(location) {
		location = uri
	}
	if selection := selectedKnowledge(ctx, location); selection != nil {
		value := selection.value.Value.Metadata
		return metadata.Config{Extractor: value.Extractor, Fields: value.Fields}
	}
	return s.resolveMetadataConfig(ctx, uri)
}

// knowledgeFS reads immutable bytes admitted for the invocation. The native
// index namespace check fences indexing, and the augmenter verifies the exact
// selection again before delivery. Reading each file needs no owner round trip.
type knowledgeFS struct{ fallback embfs.Service }

func (f *knowledgeFS) selection(ctx context.Context, uri string) (*knowledgeSelection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selected := selectedKnowledge(ctx, uri)
	if selected == nil {
		return nil, identity.ErrResourceDenied
	}
	return selected, nil
}
func (f *knowledgeFS) List(ctx context.Context, location string) ([]storage.Object, error) {
	if !knowledgeURI(location) {
		return f.fallback.List(ctx, location)
	}
	selected, err := f.selection(ctx, location)
	if err != nil {
		return nil, err
	}
	prefix := strings.TrimPrefix(strings.TrimRight(location, "/"), selected.uri)
	prefix = strings.TrimPrefix(prefix, "/")
	if prefix != "" && !iofs.ValidPath(prefix) {
		return nil, identity.ErrResourceDenied
	}
	names := make([]string, 0, len(selected.value.Value.Files))
	for name := range selected.value.Value.Files {
		if prefix == "" || name == prefix || strings.HasPrefix(name, prefix+"/") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	result := make([]storage.Object, 0, len(names))
	for _, name := range names {
		data := selected.value.Value.Files[name]
		result = append(result, object.New(selected.uri+"/"+name, file.NewInfo(path.Base(name), int64(len(data)), 0400, time.Unix(0, 0), false), nil))
	}
	return result, nil
}
func (f *knowledgeFS) Download(ctx context.Context, source storage.Object) ([]byte, error) {
	if source == nil {
		return nil, nil
	}
	if !knowledgeURI(source.URL()) {
		return f.fallback.Download(ctx, source)
	}
	selected, err := f.selection(ctx, source.URL())
	if err != nil {
		return nil, err
	}
	name := strings.TrimPrefix(source.URL(), selected.uri+"/")
	if !iofs.ValidPath(name) {
		return nil, identity.ErrResourceDenied
	}
	body, ok := selected.value.Value.Files[name]
	if !ok {
		return nil, identity.ErrResourceDenied
	}
	return []byte(body), nil
}

// The native FS indexer uses path-only document IDs internally. Keep those
// cache IDs intact and restore the admitted source URI only at delivery.
type knowledgeIndexer struct{ base indexer.Indexer }

func (i *knowledgeIndexer) Namespace(ctx context.Context, uri string) (string, error) {
	return i.base.Namespace(ctx, uri)
}
func (i *knowledgeIndexer) Index(ctx context.Context, uri string, entries *cache.Map[string, document.Entry]) ([]schema.Document, []string, error) {
	return i.base.Index(context.WithValue(ctx, knowledgeIndexLocationKey{}, uri), uri, entries)
}
func (i *knowledgeIndexer) ConsumeMetadataChanges() bool {
	reporter, ok := i.base.(metadataChangeReporter)
	return ok && reporter.ConsumeMetadataChanges()
}
func projectKnowledgeDocuments(ctx context.Context, location string, documents []schema.Document) error {
	if !knowledgeURI(location) {
		return nil
	}
	selection := selectedKnowledge(ctx, location)
	if selection == nil {
		return identity.ErrResourceDenied
	}
	prefix := afsurl.Path(selection.uri)
	for index := range documents {
		metadata := make(map[string]interface{}, len(documents[index].Metadata))
		for key, value := range documents[index].Metadata {
			metadata[key] = value
		}
		for _, key := range []string{"path", "docId", "fragmentId"} {
			value, ok := metadata[key].(string)
			if !ok || value == "" {
				continue
			}
			if !strings.HasPrefix(value, prefix+"/") {
				return identity.ErrResourceDenied
			}
			metadata[key] = selection.uri + strings.TrimPrefix(value, prefix)
		}
		documents[index].Metadata = metadata
	}
	return nil
}
