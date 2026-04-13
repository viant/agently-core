package resources

import (
	"context"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/url"
	"github.com/viant/agently-core/internal/logx"
	agmodel "github.com/viant/agently-core/protocol/agent"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpuri "github.com/viant/agently-core/protocol/mcp/uri"
	mcpfs "github.com/viant/agently-core/service/augmenter/mcpfs"
)

func (s *Service) agentResources(ctx context.Context, ag *agmodel.Agent) []*agmodel.Resource {
	if ag == nil || len(ag.Resources) == 0 {
		return nil
	}
	var out []*agmodel.Resource
	seen := map[string]struct{}{}
	for _, r := range ag.Resources {
		if r == nil {
			continue
		}
		if strings.TrimSpace(r.URI) == "" && strings.TrimSpace(r.MCP) != "" {
			for _, expanded := range s.expandMCPResources(ctx, r) {
				if expanded == nil || strings.TrimSpace(expanded.URI) == "" {
					continue
				}
				key := normalizeRootURI(expanded.URI)
				if key == "" {
					continue
				}
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, expanded)
			}
			continue
		}
		if strings.TrimSpace(r.URI) == "" {
			continue
		}
		key := normalizeRootURI(r.URI)
		if key != "" {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, r)
	}
	return out
}

func (s *Service) expandMCPResources(ctx context.Context, base *agmodel.Resource) []*agmodel.Resource {
	if s == nil || s.mcpMgr == nil || base == nil {
		return nil
	}
	server := strings.TrimSpace(base.MCP)
	if server == "" {
		return nil
	}
	opts, err := s.mcpMgr.Options(ctx, server)
	if err != nil || opts == nil {
		logx.Debugf("resources", "mcp include server=%q err=%v", server, err)
		return nil
	}
	roots := mcpcfg.ResourceRoots(opts.Metadata)
	if len(roots) == 0 {
		logx.Debugf("resources", "mcp include server=%q no roots", server)
		return nil
	}
	selectors := make([]string, 0, len(base.Roots))
	for _, r := range base.Roots {
		if v := strings.TrimSpace(r); v != "" {
			selectors = append(selectors, v)
		}
	}
	matchAll := len(selectors) == 0
	for _, sel := range selectors {
		if sel == "*" || strings.EqualFold(sel, "all") {
			matchAll = true
			break
		}
	}
	var out []*agmodel.Resource
	for _, root := range roots {
		uri := strings.TrimRight(strings.TrimSpace(root.URI), "/")
		if uri == "" {
			continue
		}
		if !matchAll && !matchesMCPSelector(selectors, root) {
			continue
		}
		if mcpuri.Is(uri) {
			uri = normalizeMCPURI(uri)
		}
		rootID := strings.TrimSpace(root.ID)
		if rootID == "" {
			rootID = uri
		}
		role := strings.TrimSpace(base.Role)
		if role == "" {
			role = "user"
		}
		res := &agmodel.Resource{
			ID:          rootID,
			URI:         uri,
			Role:        role,
			Binding:     base.Binding,
			MaxFiles:    base.MaxFiles,
			TrimPath:    base.TrimPath,
			Match:       mergeMatchOptions(base.Match, root.Include, root.Exclude, root.MaxSizeBytes),
			MinScore:    base.MinScore,
			Description: strings.TrimSpace(root.Description),
		}
		if strings.TrimSpace(base.Description) != "" {
			res.Description = strings.TrimSpace(base.Description)
		}
		if base.AllowSemanticMatch != nil {
			res.AllowSemanticMatch = base.AllowSemanticMatch
		} else {
			allowed := root.Vectorize && root.Snapshot
			res.AllowSemanticMatch = &allowed
		}
		if base.AllowGrep != nil {
			res.AllowGrep = base.AllowGrep
		} else {
			allowed := root.AllowGrep && root.Snapshot
			res.AllowGrep = &allowed
		}
		out = append(out, res)
	}
	return out
}

func (s *Service) tryDescribe(ctx context.Context, uri, kind string) string {
	order := s.defaults.SummaryFiles
	if len(order) == 0 {
		order = []string{".summary", ".summary.md", "README.md"}
	}
	if kind == "mcp" {
		mfs, err := s.mcpFS(ctx)
		if err != nil {
			return ""
		}
		for _, name := range order {
			p := url.Join(uri, name)
			data, err := mfs.Download(ctx, mcpfs.NewObjectFromURI(p))
			if err == nil && len(data) > 0 {
				return summarizeText(string(boundBytes(data, 4096)))
			}
		}
		return ""
	}
	// file or workspace:// → map to file for reading
	fs := afs.New()
	if strings.HasPrefix(uri, "workspace://") {
		uri = workspaceToFile(uri)
	}
	for _, name := range order {
		p := url.Join(uri, name)
		data, err := fs.DownloadWithURL(ctx, p)
		if err == nil && len(data) > 0 {
			return summarizeText(string(boundBytes(data, 4096)))
		}
	}
	return ""
}

func (s *Service) describeCached(ctx context.Context, uri, kind string) string {
	key := kind + "|" + normalizeWorkspaceKey(uri)
	if key == "" {
		return ""
	}
	s.descMu.RLock()
	if s.descCache != nil {
		if val, ok := s.descCache[key]; ok {
			s.descMu.RUnlock()
			return val
		}
	}
	s.descMu.RUnlock()

	desc := s.tryDescribe(ctx, uri, kind)
	s.descMu.Lock()
	if s.descCache == nil {
		s.descCache = map[string]string{}
	}
	s.descCache[key] = desc
	s.descMu.Unlock()
	return desc
}

func summarizeText(s string) string {
	txt := strings.TrimSpace(s)
	if txt == "" {
		return ""
	}
	if i := strings.Index(txt, "\n\n"); i != -1 {
		txt = txt[:i]
	}
	if len(txt) > 512 {
		txt = txt[:512]
	}
	return strings.TrimSpace(txt)
}

func boundBytes(b []byte, n int) []byte {
	if n <= 0 || len(b) <= n {
		return b
	}
	return b[:n]
}
