package skill

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	skillproto "github.com/viant/agently-core/protocol/skill"
	runtimectx "github.com/viant/agently-core/runtime/requestctx"
	skillformat "github.com/viant/mcp-protocol/extension/skills"
	schema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

// MCPSource uses the manager's authenticated, principal-isolated connections.
// Implementations must resolve configuration locally in Names and Options.
type MCPSource interface {
	Names(context.Context) ([]string, error)
	Options(context.Context, string) (*mcpcfg.MCPClient, error)
	Get(context.Context, string, string) (mcpclient.Interface, error)
	WithAuthTokenContext(context.Context, string) context.Context
	UseIDToken(context.Context, string) bool
}

func (s *Service) SetMCPSource(source MCPSource) { s.mcpSource = source }

func (s *Service) HasMCPSource() bool {
	if s == nil || s.mcpSource == nil {
		return false
	}
	ctx := context.Background()
	names, err := s.mcpSource.Names(ctx)
	if err != nil {
		return false
	}
	for _, name := range names {
		cfg, err := s.mcpSource.Options(ctx, name)
		if err == nil && cfg != nil && cfg.SkillDiscovery != nil && cfg.SkillDiscovery.Enabled {
			return true
		}
	}
	return false
}

// VisibleSkillsWithContext restores identities under the current caller.
func (s *Service) VisibleSkillsWithContext(ctx context.Context, agent *agentmdl.Agent, names []string) []*skillproto.Skill {
	var out []*skillproto.Skill
	for _, name := range names {
		if item, err := s.GetVisible(ctx, agent, name); err == nil {
			out = append(out, item)
		}
	}
	return out
}

type remoteEntry struct {
	revision string
	entry    schema.Skill
	metadata skillproto.Metadata
	server   string
	uri      string
	config   *mcpcfg.MCPClient
}

func positive(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func visibleRemote(agent *agentmdl.Agent, ref skillproto.Ref) bool {
	if agent == nil {
		return false
	}
	selected := false
	for _, pattern := range agent.Skills {
		if strings.HasPrefix(pattern, "!") {
			continue
		}
		if skillPatternMatch(ref.URI(), pattern) || skillPatternMatch(ref.ServerID+"/"+ref.Name, pattern) || skillPatternMatch(ref.Name, pattern) {
			selected = true
		}
	}
	for _, pattern := range agent.Skills {
		if !strings.HasPrefix(pattern, "!") {
			continue
		}
		pattern = strings.TrimPrefix(pattern, "!")
		if skillPatternMatch(ref.URI(), pattern) || skillPatternMatch(ref.ServerID+"/"+ref.Name, pattern) || skillPatternMatch(ref.Name, pattern) {
			return false
		}
	}
	return selected
}

// Remote federation requires an authenticated principal even for public lists.
// It intentionally has no process-wide cache and never runs on startup.
func (s *Service) remoteEntries(ctx context.Context, agent *agentmdl.Agent, only string) ([]remoteEntry, error) {
	if s.mcpSource == nil || agent == nil || len(agent.Skills) == 0 {
		return nil, nil
	}
	if authctx.EffectiveUserID(ctx) == "" {
		return nil, nil
	}
	names, err := s.mcpSource.Names(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []remoteEntry
	ids := map[string]bool{}
	for _, server := range names {
		cfg, err := s.mcpSource.Options(ctx, server)
		if err != nil {
			return nil, err
		}
		if cfg == nil || cfg.SkillDiscovery == nil || !cfg.SkillDiscovery.Enabled {
			continue
		}
		id := cfg.SkillDiscovery.CatalogID
		if id == "" {
			id = server
		}
		if !skillproto.ValidCatalogSegment(id) || ids[id] {
			return nil, fmt.Errorf("invalid or duplicate MCP skill catalog ID")
		}
		ids[id] = true
		if only != "" && only != id {
			continue
		}
		entries, err := s.listRemoteServer(ctx, agent, server, id, cfg)
		if err != nil {
			return nil, fmt.Errorf("MCP skill discovery unavailable for %s: %w", id, err)
		}
		out = append(out, entries...)
	}
	return out, nil
}

func (s *Service) remoteClient(ctx context.Context, server string, cfg *mcpcfg.MCPClient) (context.Context, mcpclient.Interface, []mcpclient.RequestOption, error) {
	if authctx.EffectiveUserID(ctx) == "" {
		return ctx, nil, nil, fmt.Errorf("skill not available")
	}
	ctx = s.mcpSource.WithAuthTokenContext(ctx, server)
	scope := runtimectx.ConversationIDFromContext(ctx)
	if scope == "" {
		scope = "skill-catalog"
	} // manager also isolates by principal
	cli, err := s.mcpSource.Get(ctx, scope, server)
	if err != nil {
		return ctx, nil, nil, err
	}
	var opts []mcpclient.RequestOption
	if !cfg.IsDelegatedAuth() {
		if token := authctx.MCPAuthToken(ctx, s.mcpSource.UseIDToken(ctx, server)); token != "" {
			opts = append(opts, mcpclient.WithAuthToken(token))
		}
	}
	initialized, err := cli.Initialize(ctx, opts...)
	if err != nil {
		return ctx, nil, nil, err
	}
	bridge := func() (context.Context, mcpclient.Interface, []mcpclient.RequestOption, error) {
		tools, bridgeErr := resolveSkillToolBridge(ctx, cli, cfg, opts)
		if bridgeErr == nil {
			return ctx, &toolSkillBridge{Interface: cli, tools: tools}, opts, nil
		}
		return ctx, nil, nil, bridgeErr
	}
	if initialized != nil && initialized.ProtocolVersion != "" && initialized.ProtocolVersion < "2026-07-28" {
		return bridge()
	}
	discovery, ok := cli.(mcpclient.DiscoveryInterface)
	if !ok {
		return bridge()
	}
	info, err := discovery.Discover(ctx, opts...)
	if err != nil {
		return ctx, nil, nil, err
	}
	if info == nil || info.Capabilities.Resources == nil {
		return bridge()
	}
	if _, ok := info.Capabilities.Extensions[schema.SkillsExtension]; !ok {
		return bridge()
	}
	if _, ok := cli.(manager.SkillsClient); !ok {
		return ctx, nil, nil, fmt.Errorf("MCP client lacks skills support")
	}
	return ctx, cli, opts, nil
}

func (s *Service) listRemoteServer(ctx context.Context, agent *agentmdl.Agent, server, id string, cfg *mcpcfg.MCPClient) ([]remoteEntry, error) {
	limits := cfg.SkillDiscovery
	ctx, cancel := context.WithTimeout(ctx, time.Duration(positive(limits.TimeoutSec, 10))*time.Second)
	defer cancel()
	ctx, cli, opts, err := s.remoteClient(ctx, server, cfg)
	if err != nil {
		return nil, err
	}
	var out []remoteEntry
	var cursor *string
	seen := map[string]bool{}
	uris := map[string]bool{}
	scanned, bytes := 0, 0
	for page := 0; page < positive(limits.MaxPages, 20); page++ {
		res, err := cli.(manager.SkillsClient).ListSkills(ctx, cursor, opts...)
		if err != nil {
			return nil, err
		}
		if res == nil || res.ResultType != "complete" || res.TtlMs == nil || (res.CacheScope != "public" && res.CacheScope != "private") {
			return nil, fmt.Errorf("empty resource response")
		}
		for _, r := range res.Skills {
			scanned++
			encoded, _ := json.Marshal(r)
			bytes += len(encoded)
			if scanned > positive(limits.MaxScannedItems, 5000) || bytes > positive(limits.MaxMetadataBytes, 1<<20) {
				return nil, fmt.Errorf("resource discovery limit exceeded")
			}
			if err := skillformat.Validate(r); err != nil {
				return nil, err
			}
			if uris[r.Uri] {
				return nil, fmt.Errorf("duplicate skill URI")
			}
			uris[r.Uri] = true
			if len(uris) > positive(limits.MaxSkills, 500) {
				return nil, fmt.Errorf("skill discovery limit exceeded")
			}
			name, _ := r.Frontmatter["name"].(string)
			ref := skillproto.Ref{ServerID: id, Name: name, ResourceURI: r.Uri, Revision: manifestRevision(r)}
			if !visibleRemote(agent, ref) {
				continue
			}
			description, _ := r.Frontmatter["description"].(string)
			meta := skillproto.Metadata{Name: name, URI: ref.URI(), QualifiedName: id + "/" + name, ServerID: server, Description: description}
			out = append(out, remoteEntry{entry: r, revision: ref.Revision, metadata: meta, server: server, uri: r.Uri, config: cfg})
		}
		if res.NextCursor == nil || *res.NextCursor == "" {
			return out, nil
		}
		if seen[*res.NextCursor] {
			return nil, fmt.Errorf("repeated resource cursor")
		}
		seen[*res.NextCursor] = true
		cursor = res.NextCursor
	}
	return nil, fmt.Errorf("resource page limit exceeded")
}

func markdownMIME(s string) bool {
	s = strings.ToLower(strings.TrimSpace(strings.Split(s, ";")[0]))
	return s == "text/markdown" || s == "text/plain"
}

// ListVisible is the only federated model catalog. Visible remains local-only
// for prompt binding so ordinary turns cause no remote discovery.
func (s *Service) ListVisible(ctx context.Context, agent *agentmdl.Agent) ([]skillproto.Metadata, error) {
	local, _ := s.Visible(agent)
	entries, err := s.remoteEntries(ctx, agent, "")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		local = append(local, entry.metadata)
	}
	sort.SliceStable(local, func(i, j int) bool {
		a, b := local[i].URI, local[j].URI
		if a == "" {
			a = local[i].Name
		}
		if b == "" {
			b = local[j].Name
		}
		return a < b
	})
	return local, nil
}

// GetVisible always re-resolves remote identity using the caller's server list.
// A URI is a reference, never an authorization grant or an upstream locator.
func (s *Service) GetVisible(ctx context.Context, agent *agentmdl.Agent, name string) (*skillproto.Skill, error) {
	if state, ok := RuntimeStateFromContext(ctx); ok && !state.allowsSkillRead(name) {
		return nil, fmt.Errorf("cross-origin skill read requires approval")
	}
	if !strings.Contains(name, "/") && !strings.Contains(name, ":") {
		if local, err := s.findVisibleSkill(agent, name); err == nil {
			return local, nil
		}
		return nil, fmt.Errorf("skill not available; use a qualified reference for MCP skills")
	}
	var ref skillproto.Ref
	var err error
	if strings.Contains(name, "/") {
		ref, err = skillproto.ParseRef(name)
		if err != nil {
			return nil, err
		}
	}
	if ref.ServerID != "" && !visibleRemote(agent, ref) {
		return nil, fmt.Errorf("skill not available")
	}
	if ref.ResourceURI != "" {
		return s.getDirect(ctx, agent, ref)
	}
	entries, err := s.remoteEntries(ctx, agent, ref.ServerID)
	if err != nil {
		return nil, err
	}
	var selected *remoteEntry
	for i := range entries {
		e := &entries[i]
		if (ref.ServerID != "" && e.metadata.Name == ref.Name) || (ref.ServerID == "" && e.metadata.Name == name) {
			if selected != nil {
				return nil, fmt.Errorf("ambiguous skill; use skill://server/name")
			}
			selected = e
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("skill not available")
	}
	return s.readRemote(ctx, *selected)
}

func (s *Service) readRemote(ctx context.Context, e remoteEntry) (*skillproto.Skill, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(positive(e.config.SkillDiscovery.TimeoutSec, 10))*time.Second)
	defer cancel()
	ctx, cli, opts, err := s.remoteClient(ctx, e.server, e.config)
	if err != nil {
		return nil, err
	}
	fresh, err := cli.(manager.SkillsClient).GetSkill(ctx, e.uri, opts...)
	if err != nil || fresh == nil || fresh.ResultType != "complete" || fresh.Skill.Uri != e.uri || fresh.TtlMs == nil || (fresh.CacheScope != "public" && fresh.CacheScope != "private") {
		return nil, fmt.Errorf("skill not available")
	}
	if err := skillformat.Validate(fresh.Skill); err != nil {
		return nil, err
	}
	e.entry = fresh.Skill
	if e.revision != "" && e.revision != manifestRevision(e.entry) {
		return nil, fmt.Errorf("skill changed; list and select the updated skill")
	}
	if ref, err := skillproto.ParseRef(e.metadata.URI); err == nil {
		ref.ResourceURI = e.uri
		ref.Revision = manifestRevision(e.entry)
		e.metadata.URI = ref.URI()
	}
	if e.entry.Resources.Dynamic {
		return nil, fmt.Errorf("dynamic remote skills are unsupported: content cannot be verified")
	}
	res, err := cli.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: e.uri}, opts...)
	if err != nil {
		return nil, fmt.Errorf("skill not available")
	}
	if res == nil || len(res.Contents) != 1 {
		return nil, fmt.Errorf("skill requires exactly one text document")
	}
	c := res.Contents[0]
	if c.Uri != e.uri || c.Blob != "" || !utf8.ValidString(c.Text) || len(c.Text) > positive(e.config.SkillDiscovery.MaxSkillBytes, 16<<20) || (c.MimeType != nil && !markdownMIME(*c.MimeType)) {
		return nil, fmt.Errorf("invalid skill document")
	}
	verified := false
	for _, f := range e.entry.Resources.Files {
		if f.Uri == e.uri {
			verified = int64(len(c.Text)) == f.Size && fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(c.Text))) == f.Digest
		}
	}
	front, err := skillformat.Frontmatter([]byte(c.Text))
	if !verified || err != nil || !reflect.DeepEqual(front, e.entry.Frontmatter) {
		return nil, fmt.Errorf("skill integrity verification failed")
	}
	item, diags, err := skillproto.Parse(e.uri, "", "mcp", c.Text)
	if err != nil {
		return nil, err
	}
	for _, d := range diags {
		if d.Level == "error" {
			return nil, fmt.Errorf("invalid skill document: %s", d.Message)
		}
	}
	if item == nil || item.Frontmatter.Name != e.metadata.Name {
		return nil, fmt.Errorf("skill name mismatch")
	}
	if item.Frontmatter.PreprocessEnabled() {
		return nil, fmt.Errorf("remote skill preprocessing is unsupported")
	}
	item.CatalogURI = e.metadata.URI
	item.ServerID = e.server
	item.RemoteURI = e.uri
	item.Manifest = &e.entry
	return s.resolveRemoteTools(ctx, item)
}

func (s *Service) resolveRemoteTools(ctx context.Context, item *skillproto.Skill) (*skillproto.Skill, error) {
	copy := *item
	var names []string
	for _, token := range skillproto.ParseAllowedTools(item.Frontmatter.AllowedTools) {
		if token.BashCommand != "" {
			names = append(names, token.Raw)
			continue
		}
		pattern := token.ToolPattern
		if !strings.ContainsAny(pattern, ":/") {
			pattern = item.ServerID + ":" + pattern
		}
		if pattern == "system/exec" || strings.HasPrefix(pattern, "system/exec:") {
			names = append(names, pattern)
			continue
		}
		if !strings.HasPrefix(pattern, item.ServerID+":") && !strings.HasPrefix(pattern, item.ServerID+"/") {
			names = append(names, pattern)
			continue
		}
		matches := matchKnownSkillTool(ctx, s.toolRegistry, pattern)
		if len(matches) == 0 {
			names = append(names, pattern)
			continue
		}
		for _, def := range matches {
			names = append(names, def.Name)
		}
	}
	copy.RequestedTools = strings.Join(names, " ")
	// SEP-2640: remote allowed-tools is not a grant. Approval integration can
	// later authorize these requested names, bound to the complete manifest.
	copy.Frontmatter.AllowedTools = ""
	if len(names) > 0 {
		copy.Body += "\n\nQualified tool references (no permission grant): " + strings.Join(names, ", ")
	}
	return &copy, nil
}
