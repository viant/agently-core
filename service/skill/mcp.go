package skill

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sync/errgroup"
	"sort"
	"strings"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	mcpauth "github.com/viant/agently-core/internal/auth/mcpauth"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	skillproto "github.com/viant/agently-core/protocol/skill"
	runtimectx "github.com/viant/agently-core/runtime/requestctx"
	skillformat "github.com/viant/mcp-protocol/extension/skills"
	schema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

var errSkillCapabilityUnavailable = errors.New("MCP skills extension not advertised")

// Published native MCP definitions participate on demand without copying skill
// names into consumer configuration. Explicit discovery settings retain their
// opt-in/opt-out semantics. Capability negotiation still belongs to the client.
func ordinarySkillDiscovery(config *mcpcfg.MCPClient) *mcpcfg.MCPClient {
	if config == nil || config.SkillDiscovery != nil {
		return config
	}
	if config.ResolvedIdentity == nil || !strings.HasPrefix(config.ResolvedIdentity.ResourceURI, "mcp://") {
		return config
	}
	copy := *config
	copy.SkillDiscovery = &mcpcfg.SkillDiscovery{Enabled: true, CatalogID: fmt.Sprintf("mcp-%x", sha256.Sum256([]byte(config.ResolvedIdentity.ResourceURI)))}
	return &copy
}

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
	if s.delivery != nil {
		if source, ok := s.mcpSource.(interface{ HasPublishedMetadata() bool }); ok && source.HasPublishedMetadata() {
			return true
		}
	}
	ctx := context.Background()
	names, err := s.mcpSource.Names(ctx)
	if err != nil {
		return false
	}
	for _, name := range names {
		cfg, err := s.mcpSource.Options(ctx, name)
		cfg = ordinarySkillDiscovery(cfg)
		if err == nil && cfg != nil && (cfg.SkillDiscovery != nil && cfg.SkillDiscovery.Enabled || s.delivery != nil && cfg.PrimitiveProviderIdentity != "") {
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
	return visibleRemoteResource(agent, ref, "")
}

func visibleRemoteResource(agent *agentmdl.Agent, ref skillproto.Ref, uri string) bool {
	if agent == nil {
		return false
	}
	selected := false
	for _, pattern := range agent.Skills {
		if strings.HasPrefix(pattern, "!") {
			continue
		}
		if (uri != "" && skillPatternMatch(uri, pattern)) || skillPatternMatch(ref.URI(), pattern) || skillPatternMatch(ref.ServerID+"/"+ref.Name, pattern) || skillPatternMatch(ref.Name, pattern) {
			selected = true
		}
	}
	for _, pattern := range agent.Skills {
		if !strings.HasPrefix(pattern, "!") {
			continue
		}
		pattern = strings.TrimPrefix(pattern, "!")
		if (uri != "" && skillPatternMatch(uri, pattern)) || skillPatternMatch(ref.URI(), pattern) || skillPatternMatch(ref.ServerID+"/"+ref.Name, pattern) || skillPatternMatch(ref.Name, pattern) {
			return false
		}
	}
	return selected
}

// Remote federation requires an authenticated principal even for public lists.
// It intentionally has no process-wide cache and never runs on startup.
func (s *Service) remoteEntries(ctx context.Context, agent *agentmdl.Agent, only string) ([]remoteEntry, error) {
	return s.remoteEntriesInNamespace(ctx, agent, only, "")
}

func (s *Service) remoteEntriesInNamespace(ctx context.Context, agent *agentmdl.Agent, only, namespace string) ([]remoteEntry, error) {
	if s.mcpSource == nil || agent == nil || len(agent.Skills) == 0 {
		return nil, nil
	}
	if authctx.EffectiveUserID(ctx) == "" {
		return nil, nil
	}
	names, err := s.mcpSource.Names(ctx)
	if err != nil {
		traceDiscoveryFailure(ctx, "names", "", false, only, err)
		return nil, err
	}
	sort.Strings(names)
	// Prepare source configuration in inventory order, then overlap independent
	// metadata I/O. Results and failures are still reduced in source order.
	type catalogResult struct {
		server, id string
		config     *mcpcfg.MCPClient
		automatic  bool
		entries    []remoteEntry
		err        error
	}
	var catalogs []catalogResult
	var preflightErr error
	var preflightServer, preflightStage string
	ids := map[string]bool{}
	for _, server := range names {
		if err := ctx.Err(); err != nil {
			preflightErr = err
			break
		}
		cfg, err := s.mcpSource.Options(ctx, server)
		if err != nil {
			preflightErr, preflightServer, preflightStage = err, server, "options"
			break
		}
		if namespace != "" {
			if declared := resolvedSkillNamespace(cfg); declared != "" && declared != namespace {
				continue
			}
		}
		automatic := cfg != nil && cfg.SkillDiscovery == nil
		cfg = ordinarySkillDiscovery(cfg)
		if cfg == nil || cfg.SkillDiscovery == nil || !cfg.SkillDiscovery.Enabled {
			continue
		}
		id := cfg.SkillDiscovery.CatalogID
		if id == "" {
			id = server
		}
		if !skillproto.ValidCatalogSegment(id) || ids[id] {
			preflightErr = fmt.Errorf("invalid or duplicate MCP skill catalog ID")
			break
		}
		ids[id] = true
		if only != "" && only != id {
			continue
		}
		catalogs = append(catalogs, catalogResult{server: server, id: id, config: cfg, automatic: automatic})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	group := new(errgroup.Group)
	// Match the framework's bounded tool/namespace catalog discovery pattern.
	group.SetLimit(4)
	for i := range catalogs {
		group.Go(func() error {
			if err := ctx.Err(); err != nil {
				catalogs[i].err = err
				return nil
			}
			item := &catalogs[i]
			item.entries, item.err = s.listRemoteServer(ctx, agent, item.server, item.id, item.config)
			return nil
		})
	}
	_ = group.Wait()
	var out []remoteEntry
	for _, catalog := range catalogs {
		server, id, automatic := catalog.server, catalog.id, catalog.automatic
		entries, err := catalog.entries, catalog.err
		if err != nil {
			if ctx.Err() != nil {
				traceDiscoveryFailure(ctx, "list", server, automatic, only, err)
				return nil, ctx.Err()
			}
			if automatic && only == "" && errors.Is(err, errSkillCapabilityUnavailable) && optionalDiscoveryUnavailable(err) {
				traceSkippedDiscoveryFailure(ctx, "list", server, automatic, only, err)
				continue
			}
			if only == "" && optionalSkillLinkRequired(err) {
				// No credentials means this provider contributes no skills yet.
				// The catalog owner's credential scope changes when it is linked;
				// do not make healthy catalogs uncacheable while it stays unlinked.
				traceSkippedDiscoveryFailure(ctx, "unlinked", server, automatic, only, err)
				continue
			}
			if only == "" && automatic && optionalDiscoveryUnavailable(err) {
				traceSkippedDiscoveryFailure(ctx, "list", server, automatic, only, err)
				recordDiscoveryFailure(ctx, id)
				continue
			}
			traceDiscoveryFailure(ctx, "list", server, automatic, only, err)
			return nil, fmt.Errorf("MCP skill discovery unavailable for %s: %w", id, err)
		}
		out = append(out, entries...)
	}
	if preflightErr != nil {
		if preflightStage != "" {
			traceDiscoveryFailure(ctx, preflightStage, preflightServer, false, only, preflightErr)
		}
		return nil, preflightErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Only failures while contacting a provider are optional. Locally validated
// identities, manifests, limits and pagination invariants remain hard errors.
type discoveryUnavailable struct{ error }

func (e *discoveryUnavailable) Unwrap() error { return e.error }

func optionalDiscoveryUnavailable(err error) bool {
	return optionalDiscoveryContact(err, false)
}

func optionalDiscoveryContact(err error, contact bool) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, identity.ErrResourceDenied) || errors.Is(err, identity.ErrResourceStale) || errors.Is(err, identity.ErrResource) || errors.Is(err, identity.ErrInvalid) {
		return false
	}
	if required, ok := err.(interface{ RequiredSkillDiscoveryFailure() bool }); ok && required.RequiredSkillDiscoveryFailure() {
		return false
	}
	if _, ok := err.(*discoveryUnavailable); ok {
		contact = true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !optionalDiscoveryContact(child, contact) {
				return false
			}
		}
		return true
	}
	if child := errors.Unwrap(err); child != nil {
		return optionalDiscoveryContact(child, contact)
	}
	return contact
}

// Only a pure typed missing-link outcome is optional during aggregate
// discovery. Joined integrity, authorization or transport failures stay errors.
func optionalSkillLinkRequired(err error) bool {
	for current := err; current != nil; current = errors.Unwrap(current) {
		if _, joined := current.(interface{ Unwrap() []error }); joined {
			return false
		}
	}
	return mcpauth.IsLinkRequired(err)
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
	var initialized *schema.InitializeResult
	if negotiated, ok := cli.(interface {
		NegotiatedServer() (*schema.InitializeResult, error)
	}); ok {
		initialized, _ = negotiated.NegotiatedServer()
	}
	if initialized == nil {
		initialized, err = cli.Initialize(ctx, opts...)
	}
	if err != nil {
		return ctx, nil, nil, err
	}
	if initialized == nil {
		return ctx, nil, nil, errSkillCapabilityUnavailable
	}
	if _, advertised := initialized.Capabilities.Extensions[schema.SkillsExtension]; !advertised {
		debugtrace.Write("skill", "extension_unavailable", map[string]any{"server": server, "protocol": initialized.ProtocolVersion, "clientType": fmt.Sprintf("%T", cli), "extensionCount": len(initialized.Capabilities.Extensions), "requestTokenPresent": mcpclient.NewRequestOptions(opts).StringToken != "", "useIDToken": s.mcpSource.UseIDToken(ctx, server), "configuredAuth": cfg.ClientOptions != nil && cfg.ClientOptions.Auth != nil, "delegatedAuth": cfg.IsDelegatedAuth()})
		return ctx, nil, nil, errSkillCapabilityUnavailable
	}
	if _, ok := cli.(manager.SkillsClient); !ok {
		return ctx, nil, nil, fmt.Errorf("%w: MCP client lacks skills support", errSkillCapabilityUnavailable)
	}
	return ctx, cli, opts, nil
}

func (s *Service) listRemoteServer(ctx context.Context, agent *agentmdl.Agent, server, id string, cfg *mcpcfg.MCPClient) ([]remoteEntry, error) {
	limits := cfg.SkillDiscovery
	ctx, cancel := context.WithTimeout(ctx, time.Duration(positive(limits.TimeoutSec, 10))*time.Second)
	defer cancel()
	ctx, cli, opts, err := s.remoteClient(ctx, server, cfg)
	if err != nil {
		return nil, &discoveryUnavailable{err}
	}
	var out []remoteEntry
	var cursor *string
	seen := map[string]bool{}
	uris := map[string]bool{}
	scanned, bytes := 0, 0
	for page := 0; page < positive(limits.MaxPages, 20); page++ {
		res, err := cli.(manager.SkillsClient).ListSkills(ctx, cursor, opts...)
		if err != nil {
			return nil, &discoveryUnavailable{err}
		}
		if res == nil || res.ResultType != "complete" {
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
			cacheScope := "user"
			if res.CacheScope == "public" && cfg.ToolsListVisibility != mcpcfg.ToolsListVisibilityPrivate && (cfg.ClientOptions == nil || cfg.Auth == nil && cfg.CookieJar == nil) {
				cacheScope = "public"
			}
			frontMetadata, _ := r.Frontmatter["metadata"].(map[string]any)
			meta := skillproto.Metadata{AlwaysInclude: (skillproto.Frontmatter{Metadata: frontMetadata}).AlwaysIncluded(), DiscoveryCacheScope: cacheScope, Name: name, URI: ref.URI(), QualifiedName: id + "/" + name, ServerID: server, Description: description}
			// Published MCP resources already carry host-resolved namespace ownership.
			// Native skill URLs may use opaque storage identities; retain the exact
			// URL/revision and use the service provenance only for discovery grouping.
			meta.Namespace = resolvedSkillNamespace(cfg)
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

// HasVisible decides whether the agent needs skill controls. One admitted
// local or published entry suffices, so unrelated MCPs need no discovery.
func (s *Service) HasVisible(ctx context.Context, agent *agentmdl.Agent) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if agent == nil || len(agent.Skills) == 0 {
		return false, nil
	}
	if len(s.visibleSkills(agent)) > 0 {
		return true, nil
	}
	if !s.HasMCPSource() {
		return false, nil
	}
	if len(agent.Skills) == 1 && strings.HasPrefix(agent.Skills[0], "skill://") && !strings.ContainsAny(agent.Skills[0], "*?[]") {
		item, err := s.GetVisible(ctx, agent, agent.Skills[0])
		return item != nil, err
	}
	rows, err := s.primitiveMetadata(ctx, agent)
	if err != nil || len(rows) > 0 {
		return len(rows) > 0, err
	}
	entries, err := s.remoteEntries(ctx, agent, "")
	return len(entries) > 0, err
}

// ListVisible is the only federated model catalog. Visible remains local-only
// for prompt binding so ordinary turns cause no remote discovery.
func (s *Service) ListVisible(ctx context.Context, agent *agentmdl.Agent) ([]skillproto.Metadata, error) {
	return s.listVisibleInNamespace(ctx, agent, "")
}

func (s *Service) listVisibleInNamespace(ctx context.Context, agent *agentmdl.Agent, namespace string) ([]skillproto.Metadata, error) {
	local, _ := s.Visible(agent)
	if s.delivery != nil {
		local = nil
	}
	started := time.Now()
	entries, err := s.remoteEntriesInNamespace(ctx, agent, "", namespace)
	debugtrace.Write("skill", "list_visible_stage", map[string]any{"stage": "remote_entries", "count": len(entries), "elapsedMs": float64(time.Since(started)) / float64(time.Millisecond)})
	if err != nil {
		logSkillCatalogFailure(ctx, "remote_entries", err)
		return nil, err
	}
	started = time.Now()
	generic, err := s.primitiveMetadata(ctx, agent)
	debugtrace.Write("skill", "list_visible_stage", map[string]any{"stage": "primitive_metadata", "count": len(generic), "elapsedMs": float64(time.Since(started)) / float64(time.Millisecond)})
	if err != nil {
		logSkillCatalogFailure(ctx, "primitive_metadata", err)
		return nil, err
	}
	// A native producer can publish the same skill through both namespace
	// resources and the skills extension. Prefer its native resource identity;
	// identical names or URIs from different authenticated connections stay apart.
	native := make(map[string]bool, len(generic))
	remoteMetadata := make(map[string]skillproto.Metadata, len(entries))
	for _, entry := range entries {
		remoteMetadata[entry.server+"\x00"+entry.uri] = entry.metadata
	}
	for i, entry := range generic {
		key := entry.ServerID + "\x00" + entry.URI
		native[key] = true
		if source, ok := remoteMetadata[key]; ok {
			generic[i].AlwaysInclude = generic[i].AlwaysInclude || source.AlwaysInclude
			generic[i].DiscoveryCacheScope = source.DiscoveryCacheScope
		}
	}
	for _, entry := range entries {
		if !native[entry.server+"\x00"+entry.uri] {
			local = append(local, entry.metadata)
		}
	}
	started = time.Now()
	local = append(local, generic...)
	for i := range local {
		local[i].AlwaysInclude = local[i].AlwaysInclude || configuredSkillRetention(agent, local[i])
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
	debugtrace.Write("skill", "list_visible_stage", map[string]any{"stage": "final_merge_filter", "count": len(local), "elapsedMs": float64(time.Since(started)) / float64(time.Millisecond)})
	return local, nil
}

func logSkillCatalogFailure(ctx context.Context, stage string, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	code, status := skillDiagnosticRPCFields(err)
	turn, _ := runtimectx.TurnMetaFromContext(ctx)
	logx.OperationalWarnf("resource", "skill.catalog.failed stage=%q convo=%q turn=%q errorType=%T rpcCode=%d rpcStatus=%d", stage, runtimectx.ConversationIDFromContext(ctx), turn.TurnID, err, code, status)
}

// GetVisible always re-resolves remote identity using the caller's server list.
// A URI is a reference, never an authorization grant or an upstream locator.
func (s *Service) GetVisible(ctx context.Context, agent *agentmdl.Agent, name string) (*skillproto.Skill, error) {
	ctx = manager.WithNamesMemo(ctx)
	if state, ok := RuntimeStateFromContext(ctx); ok && !state.allowsSkillRead(name) {
		return nil, fmt.Errorf("cross-origin skill read requires approval")
	}
	if !strings.Contains(name, "/") && !strings.Contains(name, ":") {
		if local, err := s.findVisibleSkill(agent, name); err == nil {
			return s.getNativeLocal(ctx, local)
		}
		return nil, fmt.Errorf("skill not available; use a qualified reference for MCP skills")
	}
	if item, handled, err := s.getPrimitive(ctx, agent, name); handled {
		return item, err
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
	if err != nil || fresh == nil || fresh.ResultType != "complete" || fresh.Skill.Uri != e.uri {
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
	item, err := ReadManifestDocument(ctx, fresh, e.uri, e.server, e.config.SkillDiscovery.MaxSkillBytes, func(ctx context.Context, uri string) (*schema.ReadResourceResult, error) {
		if reader, ok := cli.(interface {
			ReadSkillResource(context.Context, *schema.GetSkillResult, string, ...mcpclient.RequestOption) (*schema.ReadResourceResult, error)
		}); ok {
			return reader.ReadSkillResource(ctx, fresh, uri, opts...)
		}
		return cli.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: uri}, opts...)
	})
	if err != nil {
		return nil, err
	}
	if item.Frontmatter.Name != e.metadata.Name {
		return nil, fmt.Errorf("skill name mismatch")
	}
	item.CatalogURI = e.metadata.URI
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

// resolvedSkillNamespace is host-resolved provenance, not a connection alias or
// a caller-supplied authority. Unknown legacy providers retain broad discovery.
func resolvedSkillNamespace(config *mcpcfg.MCPClient) string {
	if config == nil || config.ResolvedIdentity == nil || config.ResolvedIdentity.ProviderIdentity == "" {
		return ""
	}
	source, err := identity.ParseResourceURI(config.ResolvedIdentity.ResourceURI)
	if err != nil || source.Kind != "mcp" {
		return ""
	}
	return source.Namespace
}
