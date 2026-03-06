package resources

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/url"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpuri "github.com/viant/agently-core/protocol/mcp/uri"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/workspace"
	mcprepo "github.com/viant/agently-core/workspace/repository/mcp"
	embopt "github.com/viant/embedius/matching/option"
)

type ResourcesDefaults struct {
	Locations    []string
	TrimPath     string
	SummaryFiles []string
	DescribeMCP  bool
	SnapshotPath string
}

// WithDefaults configures default roots and presentation hints.
func WithDefaults(d ResourcesDefaults) func(*Service) { return func(s *Service) { s.defaults = d } }

type RootsInput struct {
	MaxRoots int `json:"maxRoots,omitempty"`
}

type Root struct {
	// ID is a stable identifier for this root when available. When the
	// underlying agent resource entry defines an explicit id, it is surfaced
	// here. Otherwise, the normalized URI is used as a fallback id so callers
	// can still use rootId as an alias for the URI.
	ID string `json:"id"`

	URI         string `json:"uri"`
	Description string `json:"description,omitempty"`
	// UpstreamRef is an internal-only reference used to resolve local upstream sync.
	UpstreamRef string `json:"-"`
	// DB is an optional embedius sqlite database path override for this root.
	DB string `json:"-"`
	// Match carries per-root match options (include/exclude/max file size).
	Match *embopt.Options `json:"match,omitempty"`
	// AllowedSemanticSearch reports whether semantic match (match)
	// is permitted for this root in the current agent configuration.
	AllowedSemanticSearch bool `json:"allowedSemanticSearch"`
	// AllowedGrepSearch reports whether lexical grep (grepFiles)
	// is permitted for this root in the current agent configuration.
	AllowedGrepSearch bool   `json:"allowedGrepSearch"`
	Role              string `json:"role,omitempty"`
}

type RootsOutput struct {
	Roots []Root `json:"roots"`
}

type rootCollection struct {
	user   []Root
	system []Root
}

func (c *rootCollection) all() []Root {
	if c == nil {
		return nil
	}
	out := make([]Root, 0, len(c.user)+len(c.system))
	out = append(out, c.user...)
	out = append(out, c.system...)
	return out
}

func (s *Service) collectRoots(ctx context.Context) (*rootCollection, error) {
	locs := s.agentAllowed(ctx)
	if len(locs) == 0 {
		locs = append([]string(nil), s.defaults.Locations...)
	}
	if len(locs) == 0 {
		return &rootCollection{}, nil
	}
	curAgent := s.currentAgent(ctx)
	seen := map[string]bool{}
	var userRoots []Root
	var systemRoots []Root
	for _, loc := range locs {
		root := strings.TrimSpace(loc)
		if root == "" {
			continue
		}
		wsRoot, kind, err := s.normalizeUserRoot(ctx, root)
		if err != nil || wsRoot == "" {
			continue
		}
		if seen[wsRoot] {
			continue
		}
		seen[wsRoot] = true
		desc := ""
		role := "user"
		rootID := wsRoot
		upstreamRef := ""
		rootDB := ""
		var rootMatch *embopt.Options
		if curAgent != nil {
			for _, r := range s.agentResources(ctx, curAgent) {
				if r == nil || strings.TrimSpace(r.URI) == "" {
					continue
				}
				normRes, _, err := s.normalizeUserRoot(ctx, r.URI)
				if err != nil || strings.TrimSpace(normRes) == "" {
					continue
				}
				if normalizeWorkspaceKey(normRes) == normalizeWorkspaceKey(wsRoot) {
					if strings.EqualFold(strings.TrimSpace(r.Role), "system") {
						role = "system"
					}
					if strings.TrimSpace(r.ID) != "" {
						rootID = strings.TrimSpace(r.ID)
					}
					if strings.TrimSpace(r.Description) != "" {
						desc = strings.TrimSpace(r.Description)
					}
					if strings.TrimSpace(r.UpstreamRef) != "" {
						upstreamRef = strings.TrimSpace(r.UpstreamRef)
					}
					if strings.TrimSpace(r.DB) != "" {
						rootDB = strings.TrimSpace(r.DB)
					}
					if r.Match != nil {
						rootMatch = r.Match
					}
					break
				}
			}
		}
		if desc == "" && (kind != "mcp" || s.defaults.DescribeMCP) {
			desc = s.describeCached(ctx, wsRoot, kind)
		}
		semAllowed := s.semanticAllowedForAgent(ctx, curAgent, wsRoot)
		grepAllowed := s.grepAllowedForAgent(ctx, curAgent, wsRoot)
		rootEntry := Root{
			ID:                    rootID,
			URI:                   wsRoot,
			Description:           desc,
			UpstreamRef:           upstreamRef,
			DB:                    rootDB,
			AllowedSemanticSearch: semAllowed,
			AllowedGrepSearch:     grepAllowed,
			Role:                  role,
		}
		if semAllowed && rootMatch != nil {
			rootEntry.Match = rootMatch
		}
		if role == "system" {
			systemRoots = append(systemRoots, rootEntry)
			continue
		}
		userRoots = append(userRoots, rootEntry)
	}
	return &rootCollection{user: userRoots, system: systemRoots}, nil
}

func (s *Service) roots(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*RootsInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*RootsOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	max := input.MaxRoots
	if max < 0 {
		max = 0
	}

	collected, err := s.collectRoots(ctx)
	if err != nil {
		return err
	}
	mcpRoots := s.filterMCPRootsByAllowed(ctx, s.collectMCPRoots(ctx))
	var roots []Root
	seen := map[string]bool{}
	appendWithLimit := func(source []Root) {
		for _, r := range source {
			if max > 0 && len(roots) >= max {
				return
			}
			key := strings.TrimRight(strings.TrimSpace(r.URI), "/")
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			roots = append(roots, r)
		}
	}
	appendWithLimit(collected.user)
	appendWithLimit(collected.system)
	appendWithLimit(mcpRoots)
	output.Roots = roots
	return nil
}

func (s *Service) collectMCPRoots(ctx context.Context) []Root {
	if s == nil || s.mcpMgr == nil {
		return nil
	}
	repo := mcprepo.New(afs.New())
	names, err := repo.List(ctx)
	if err != nil || len(names) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var roots []Root
	for _, name := range names {
		opts, err := s.mcpMgr.Options(ctx, name)
		if err != nil || opts == nil {
			continue
		}
		for _, root := range mcpcfg.ResourceRoots(opts.Metadata) {
			uri := strings.TrimRight(strings.TrimSpace(root.URI), "/")
			if uri == "" || seen[uri] {
				continue
			}
			seen[uri] = true
			rootID := strings.TrimSpace(root.ID)
			if rootID == "" {
				rootID = uri
			}
			semAllowed := root.Vectorize && root.Snapshot
			match := mergeMatchOptions(nil, root.Include, root.Exclude, root.MaxSizeBytes)
			rootEntry := Root{
				ID:                    rootID,
				URI:                   uri,
				Description:           strings.TrimSpace(root.Description),
				AllowedSemanticSearch: root.Vectorize && root.Snapshot,
				AllowedGrepSearch:     root.AllowGrep && root.Snapshot,
				Role:                  "system",
			}
			if semAllowed && match != nil {
				rootEntry.Match = match
			}
			roots = append(roots, rootEntry)
		}
	}
	return roots
}

func (s *Service) filterMCPRootsByAllowed(ctx context.Context, roots []Root) []Root {
	allowed := s.agentAllowed(ctx)
	if len(allowed) == 0 || len(roots) == 0 {
		return roots
	}
	allowedSet := map[string]struct{}{}
	for _, loc := range allowed {
		if key := normalizeRootURI(loc); key != "" {
			allowedSet[key] = struct{}{}
		}
	}
	if len(allowedSet) == 0 {
		return nil
	}
	out := make([]Root, 0, len(roots))
	for _, root := range roots {
		if _, ok := allowedSet[normalizeRootURI(root.URI)]; !ok {
			continue
		}
		out = append(out, root)
	}
	return out
}

// normalizeLocation was unused; removed to reduce file size and duplication.

// resolveRootID maps a logical root id to its normalized root URI within the
// current agent context. It prefers explicit ids declared on agent resources
// and, for backward compatibility, falls back to interpreting the id as a
// URI only when it already looks like a URI (contains a scheme).
func (s *Service) resolveRootID(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("rootId is empty")
	}
	curAgent := s.currentAgent(ctx)

	// If the context was canceled/deadlined and we couldn't resolve the agent,
	// retry once with a background context that preserves identity and conversation id.
	if curAgent == nil && (errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
		bg := context.Background()
		if userID != "" {
			bg = authctx.WithUserInfo(bg, &authctx.UserInfo{Subject: userID})
		}
		if convID := strings.TrimSpace(memory.ConversationIDFromContext(ctx)); convID != "" {
			bg = memory.WithConversationID(bg, convID)
		}
		curAgent = s.currentAgent(bg)
	}

	if curAgent != nil {
		for _, r := range s.agentResources(ctx, curAgent) {
			if r == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(r.ID), id) {
				norm, _, err := s.normalizeUserRoot(ctx, r.URI)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(norm) == "" {
					break
				}
				return norm, nil
			}
		}
	}
	// Check MCP roots defined in MCP client metadata (allows simple IDs like "mediator").
	if s.mcpMgr != nil {
		allowed := s.agentAllowed(ctx)
		allowedSet := map[string]struct{}{}
		if len(allowed) > 0 {
			for _, loc := range allowed {
				if key := normalizeRootURI(loc); key != "" {
					allowedSet[key] = struct{}{}
				}
			}
		}
		for _, root := range s.collectMCPRoots(ctx) {
			if len(allowedSet) > 0 {
				if _, ok := allowedSet[normalizeRootURI(root.URI)]; !ok {
					continue
				}
			}
			if normalizeRootID(root.ID) != normalizeRootID(id) {
				continue
			}
			norm, _, err := s.normalizeUserRoot(ctx, root.URI)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(norm) == "" {
				break
			}
			return norm, nil
		}
	}
	// Fallback: only treat id as a URI when it already looks like one
	// (e.g., workspace://..., file://..., s3://..., mcp:...). This preserves
	// legacy configurations that used raw URIs as ids, while avoiding
	// accidentally mapping human-friendly ids like "local" into workspace
	// roots (e.g., workspace://localhost/local).
	if strings.Contains(id, "://") || mcpuri.Is(id) || strings.HasPrefix(id, "workspace://") || strings.HasPrefix(id, "file://") {
		norm, _, err := s.normalizeUserRoot(ctx, id)
		if err != nil {
			return "", fmt.Errorf("unknown rootId %s: %w", id, err)
		}
		if strings.TrimSpace(norm) == "" {
			return "", fmt.Errorf("unknown rootId: %s", id)
		}
		return norm, nil
	}
	return "", fmt.Errorf("unknown rootId: %s", id)
}

// normalizeUserRoot enforces workspace:// or mcp: for resources tools.
// - workspace kinds (e.g., agents/...) => workspace://localhost/<input>
// - relative => agents/<agentId>/<input>, else <workspace>/<input>
// - mcp: passthrough
// - file:// absolute under workspace => mapped to workspace://
// - others => error
func (s *Service) normalizeUserRoot(ctx context.Context, in string) (string, string, error) {
	u := strings.TrimSpace(in)
	if u == "" {
		return "", "", nil
	}
	// Treat github://... as shorthand for the github MCP server.
	if strings.HasPrefix(strings.ToLower(u), "github://") {
		return mcpuri.Canonical("github", u), "mcp", nil
	}
	if mcpuri.Is(u) {
		return u, "mcp", nil
	}
	if strings.HasPrefix(u, "workspace://") {
		// Normalize agent segment casing when path starts with agents/
		rel := strings.TrimPrefix(u, "workspace://")
		rel = strings.TrimPrefix(rel, "localhost/")
		low := strings.ToLower(rel)
		if strings.HasPrefix(low, workspace.KindAgent+"/") {
			// Extract agent id segment and remainder
			seg := rel[len(workspace.KindAgent)+1:]
			agentID := seg
			rest := ""
			if i := strings.Index(seg, "/"); i != -1 {
				agentID = seg[:i]
				rest = seg[i+1:]
			}
			agentID = strings.ToLower(strings.TrimSpace(agentID))
			if rest != "" {
				return url.Join("workspace://localhost/", workspace.KindAgent, agentID, rest), "workspace", nil
			}
			return url.Join("workspace://localhost/", workspace.KindAgent, agentID), "workspace", nil
		}
		return u, "workspace", nil
	}
	if strings.HasPrefix(u, "file://") {
		// For file:// URIs, accept the value as-is and treat it as a file
		// root. When the URI happens to be under the current workspace root,
		// other helpers may still map it to workspace:// for internal use, but
		// we no longer reject file:// URIs that live outside the workspace.
		u = workspace.ResolvePathTemplate(u)
		return u, "file", nil
	}
	if filepath.IsAbs(u) || isWindowsAbsPath(u) {
		u = workspace.ResolvePathTemplate(u)
		return "file://localhost" + url.Path(u), "file", nil
	}
	// known workspace kinds
	lower := strings.ToLower(u)
	kinds := []string{
		workspace.KindAgent + "/",
		workspace.KindModel + "/",
		workspace.KindEmbedder + "/",
		workspace.KindMCP + "/",
		workspace.KindWorkflow + "/",
		workspace.KindTool + "/",
		workspace.KindOAuth + "/",
		workspace.KindFeeds + "/",
		workspace.KindA2A + "/",
	}
	for _, pfx := range kinds {
		if strings.HasPrefix(lower, pfx) {
			// Normalize prefix to canonical lowercase kind and, for agents, normalize the agent id segment
			rel := u
			if len(u) >= len(pfx) {
				rel = u[len(pfx):]
			}
			if pfx == workspace.KindAgent+"/" {
				// Ensure agent folder matches the canonical (lowercase) agent id to align with workspace layout
				agentSeg := rel
				rest := ""
				if i := strings.Index(agentSeg, "/"); i != -1 {
					rest = agentSeg[i+1:]
					agentSeg = agentSeg[:i]
				}
				agentSeg = strings.ToLower(strings.TrimSpace(agentSeg))
				if rest != "" {
					return url.Join("workspace://localhost/", workspace.KindAgent, agentSeg, rest), "workspace", nil
				}
				return url.Join("workspace://localhost/", workspace.KindAgent, agentSeg), "workspace", nil
			}
			// Other kinds: keep remainder as-is, normalize kind to canonical lowercase
			return url.Join("workspace://localhost/", pfx+rel), "workspace", nil
		}
	}
	// relative: resolve under the current workspace root
	return url.Join("workspace://localhost/", u), "workspace", nil
}

// (duplicate removed above)
