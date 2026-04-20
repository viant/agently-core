package resources

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agmodel "github.com/viant/agently-core/protocol/agent"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	svc "github.com/viant/agently-core/protocol/tool/service"
	aug "github.com/viant/agently-core/service/augmenter"
	mcpfs "github.com/viant/agently-core/service/augmenter/mcpfs"
	skillsvc "github.com/viant/agently-core/service/skill"
)

// Name identifies the resources tool service namespace
const Name = "resources"

// Service exposes resource roots, listing, reading and semantic match over filesystem and MCP
type Service struct {
	augmenter *aug.Service
	mcpMgr    *mcpmgr.Manager
	defaults  ResourcesDefaults
	conv      apiconv.Client
	aFinder   agmodel.Finder
	// defaultEmbedder is used when MatchInput.Embedder/Model is not provided.
	defaultEmbedder string
	skillSvc        *skillsvc.Service

	augmentDocsOverride func(ctx context.Context, input *aug.AugmentDocsInput, output *aug.AugmentDocsOutput) error

	descMu    sync.RWMutex
	descCache map[string]string
	mfsMu     sync.Mutex
	mfs       *mcpfs.Service
}

// New returns a resources service using a shared augmenter instance.
func New(augmenter *aug.Service, opts ...func(*Service)) *Service {
	s := &Service{augmenter: augmenter}
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	return s
}

// WithMCPManager attaches an MCP manager for listing/downloading MCP
func WithMCPManager(m *mcpmgr.Manager) func(*Service) { return func(s *Service) { s.mcpMgr = m } }

// WithConversationClient attaches a conversation client for context-aware filtering.
func WithConversationClient(c apiconv.Client) func(*Service) { return func(s *Service) { s.conv = c } }

// WithAgentFinder attaches an agent finder to resolve agent resources in context.
func WithAgentFinder(f agmodel.Finder) func(*Service) { return func(s *Service) { s.aFinder = f } }

// WithDefaultEmbedder specifies a default embedder ID to use when the caller
// does not provide one. This typically comes from executor config defaults.
func WithDefaultEmbedder(id string) func(*Service) {
	return func(s *Service) { s.defaultEmbedder = strings.TrimSpace(id) }
}

func WithSkillService(skillSvc *skillsvc.Service) func(*Service) {
	return func(s *Service) { s.skillSvc = skillSvc }
}

func (s *Service) mcpFS(ctx context.Context) (*mcpfs.Service, error) {
	if s.mcpMgr == nil {
		return nil, fmt.Errorf("mcp manager not configured (resources/mcpfs)")
	}
	s.mfsMu.Lock()
	defer s.mfsMu.Unlock()
	if s.mfs == nil {
		opts := []mcpfs.Option{}
		if strings.TrimSpace(s.defaults.SnapshotPath) != "" {
			opts = append(opts, mcpfs.WithSnapshotCacheRoot(s.defaults.SnapshotPath))
		}
		s.mfs = mcpfs.New(s.mcpMgr, opts...)
	}
	resolver := s.mcpSnapshotResolver(ctx)
	if resolver != nil {
		s.mfs.SetSnapshotResolver(resolver)
	}
	manifestResolver := s.mcpSnapshotManifestResolver(ctx)
	if manifestResolver != nil {
		s.mfs.SetSnapshotManifestResolver(manifestResolver)
	}
	return s.mfs, nil
}

// Name returns service name
func (s *Service) Name() string { return Name }

// ToolTimeout suggests a longer timeout for resources tools that may index large roots.
func (s *Service) ToolTimeout() time.Duration { return 15 * time.Minute }

// CacheableMethods declares which methods produce cacheable outputs.
func (s *Service) CacheableMethods() map[string]bool {
	return map[string]bool{
		"roots":          true,
		"list":           true,
		"read":           true,
		"readImage":      true,
		"match":          true,
		"matchDocuments": true,
		"grepFiles":      true,
	}
}

// Methods declares available tool methods
func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "roots", Description: "Discover configured resource roots with optional descriptions", Input: reflect.TypeOf(&RootsInput{}), Output: reflect.TypeOf(&RootsOutput{})},
		{Name: "list", Description: "List resources under a root (file or MCP)", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "read", Description: "Read a single resource under a root. For large files, prefer byteRange and page in chunks (<= 8KB).", Input: reflect.TypeOf(&ReadInput{}), Output: reflect.TypeOf(&ReadOutput{})},
		{Name: "readImage", Description: "Read an image under a root and return a base64 payload suitable for attaching as a vision input. Defaults to resizing to fit 2048x768.", Input: reflect.TypeOf(&ReadImageInput{}), Output: reflect.TypeOf(&ReadImageOutput{})},
		{Name: "match", Description: "Semantic match search over one or more roots; use `match.exclusions` to block specific  paths.", Input: reflect.TypeOf(&MatchInput{}), Output: reflect.TypeOf(&MatchOutput{})},
		{Name: "matchDocuments", Description: "Rank semantic matches and return distinct URIs with score + root metadata for transcript promotion. Example: {\"rootIds\":[\"workspace://localhost/knowledge/bidder\"],\"query\":\"performance\"}. Output fields: documents[].uri, documents[].score, documents[].rootId.", Input: reflect.TypeOf(&MatchDocumentsInput{}), Output: reflect.TypeOf(&MatchDocumentsOutput{})},
		{Name: "grepFiles", Description: "Search text patterns in files under a root and return per-file snippets.", Input: reflect.TypeOf(&GrepInput{}), Output: reflect.TypeOf(&GrepOutput{})},
	}
}

// Method resolves an executable method by name
func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(name) {
	case "roots":
		return s.roots, nil
	case "list":
		return s.list, nil
	case "read":
		return s.read, nil
	case "readimage":
		return s.readImage, nil
	case "match":
		return s.match, nil
	case "matchdocuments":
		return s.matchDocuments, nil
	case "grepfiles":
		return s.grepFiles, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

func (s *Service) runAugmentDocs(ctx context.Context, input *aug.AugmentDocsInput, output *aug.AugmentDocsOutput) error {
	if s.augmentDocsOverride != nil {
		return s.augmentDocsOverride(ctx, input, output)
	}
	if s.augmenter == nil {
		return fmt.Errorf("augmenter service is not configured")
	}
	return s.augmenter.AugmentDocs(ctx, input, output)
}
