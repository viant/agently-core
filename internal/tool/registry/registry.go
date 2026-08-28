package tool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/viant/afs"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/auth/mcpauth"
	"github.com/viant/agently-core/internal/logx"
	tmatch "github.com/viant/agently-core/internal/tool/matcher"
	transform "github.com/viant/agently-core/internal/transform"
	mcpnames "github.com/viant/agently-core/pkg/mcpname"
	"github.com/viant/agently-core/protocol/agent"
	asynccfg "github.com/viant/agently-core/protocol/async"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	runtimediscovery "github.com/viant/agently-core/runtime/discovery"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	mcprepo "github.com/viant/agently-core/workspace/repository/mcp"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"

	localmcp "github.com/viant/agently-core/protocol/mcp/localclient"
	mcpproxy "github.com/viant/agently-core/protocol/mcp/proxy"
	toolprotection "github.com/viant/agently-core/protocol/tool/protection"
	svc "github.com/viant/agently-core/protocol/tool/service"
	orchplan "github.com/viant/agently-core/protocol/tool/service/orchestration/plan"
	toolScratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	toolAsync "github.com/viant/agently-core/protocol/tool/service/system/async"
	toolExec "github.com/viant/agently-core/protocol/tool/service/system/exec"
	toolImage "github.com/viant/agently-core/protocol/tool/service/system/image"
	toolOS "github.com/viant/agently-core/protocol/tool/service/system/os"
	toolPatch "github.com/viant/agently-core/protocol/tool/service/system/patch"
)

type mcpManager interface {
	Get(ctx context.Context, convID, serverName string) (mcpclient.Interface, error)
	Reconnect(ctx context.Context, convID, serverName string) (mcpclient.Interface, error)
	Touch(convID, serverName string)
	Options(ctx context.Context, serverName string) (*mcpcfg.MCPClient, error)
	UseIDToken(ctx context.Context, serverName string) bool
	WithAuthTokenContext(ctx context.Context, serverName string) context.Context
}

// delegatedAuthReporter is optionally implemented by managers that own
// delegated (auth.mode=oauth) credential resolution.
type delegatedAuthReporter interface {
	IsDelegatedAuth(ctx context.Context, serverName string) bool
}

// isDelegatedAuthServer reports whether serverName uses manager-owned
// delegated OAuth. For those servers the registry must not independently
// select or attach a workspace token: initialization, discovery, execution
// and reconnect all use the manager's single resolution.
func (r *Registry) isDelegatedAuthServer(ctx context.Context, serverName string) bool {
	if r == nil || r.mgr == nil {
		return false
	}
	if reporter, ok := r.mgr.(delegatedAuthReporter); ok {
		return reporter.IsDelegatedAuth(ctx, serverName)
	}
	if cfg, err := r.mgr.Options(ctx, serverName); err == nil && cfg != nil {
		return cfg.IsDelegatedAuth()
	}
	return false
}

// Registry bridges per-server MCP tools and internal services to the generic
// tool.Registry interface so that callers can use dependency injection.
type Registry struct {
	debugWriter io.Writer

	// virtual tool overlay (id → definition)
	virtualDefs map[string]llm.ToolDefinition
	virtualExec map[string]Handler

	// Optional per-conversation MCP client manager. When set, Execute will
	// inject the appropriate client and auth token into context so that the
	// underlying proxy can use them.
	mgr mcpManager

	// in-memory MCP clients for internal services (server name -> client)
	internal              map[string]mcpclient.Interface
	internalTimeout       map[string]time.Duration
	internalMethodTimeout map[string]map[string]time.Duration
	internalMethodRetry   map[string]map[string]bool
	asyncByTool           map[string]*asynccfg.Config
	internalCacheable     map[string]map[string]bool // server name -> method name -> cacheable
	internalMethods       map[string]map[string]svc.Signature

	// cache: tool name → entry
	cache map[string]*toolCacheEntry

	// timeout support flags for virtual tools (name -> support)
	virtualTimeout map[string]timeoutSupport

	// guards concurrent access to cache, warnings, and virtual maps
	mu sync.RWMutex

	warnings []string

	// recentResults memoizes identical tool calls per conversation for a short TTL
	recentMu       sync.Mutex
	recentResults  map[string]map[string]recentItem  // convID -> key -> item
	recentInFlight map[string]map[string]*recentCall // convID -> key -> call
	recentTTL      time.Duration

	executionProtection toolprotection.Guard

	// background refresh configuration
	refreshEvery time.Duration // successful refresh cadence

	// scoped discovery client diagnostics for manager-backed list_tools path.
	discoveryShared         map[string]discoveryIdentity
	discoveryWarnAt         map[string]time.Time
	discoveryFailUntil      map[string]time.Time
	discoveryFailErr        map[string]string
	discoveryWarnEvery      time.Duration
	discoveryWaitEvery      time.Duration
	discoveryTimeout        time.Duration
	discoverySurfaceTimeout time.Duration
	discoveryStrictTTL      time.Duration
	discoveryFailTTL        time.Duration
	discoveryScopeSeq       uint64
}

type toolCacheEntry struct {
	def    llm.ToolDefinition
	mcpDef mcpschema.Tool
	exec   Handler
	// timeoutSupport tracks whether the original schema natively supports timeoutMs
	timeoutSupport timeoutSupport
}

type timeoutSupport struct {
	native   bool
	injected bool
}

type recentItem struct {
	when time.Time
	out  string
}

type recentCall struct {
	done chan struct{}
	out  string
	err  error
}

type discoveryIdentity struct {
	userID  string
	tokenFP string
	useID   bool
	seenAt  time.Time
}

const (
	timeoutMsField = "timeoutMs"
	maxTimeoutMs   = int64(2 * time.Hour / time.Millisecond)
)

// Handler executes a tool call and returns its textual result.
type Handler func(ctx context.Context, args map[string]interface{}) (string, error)

// NewWithManager creates a registry backed by an MCP client manager.
func NewWithManager(mgr *manager.Manager) (*Registry, error) {
	if mgr == nil {
		return nil, fmt.Errorf("adapter/tool: nil mcp manager passed to NewWithManager")
	}
	r := &Registry{
		virtualDefs:           map[string]llm.ToolDefinition{},
		virtualExec:           map[string]Handler{},
		mgr:                   mgr,
		cache:                 map[string]*toolCacheEntry{},
		internal:              map[string]mcpclient.Interface{},
		internalTimeout:       map[string]time.Duration{},
		internalMethodTimeout: map[string]map[string]time.Duration{},
		internalMethodRetry:   map[string]map[string]bool{},
		asyncByTool:           map[string]*asynccfg.Config{},
		internalMethods:       map[string]map[string]svc.Signature{},
		recentResults:         map[string]map[string]recentItem{},
		recentInFlight:        map[string]map[string]*recentCall{},
		recentTTL:             5 * time.Second,
		refreshEvery:          30 * time.Second,
		virtualTimeout:        map[string]timeoutSupport{},
		discoveryShared:       map[string]discoveryIdentity{},
		discoveryWarnAt:       map[string]time.Time{},
		discoveryFailUntil:    map[string]time.Time{},
		discoveryFailErr:      map[string]string{},
		// Cap duplicate warning noise while preserving first signal quickly.
		discoveryWarnEvery:      30 * time.Second,
		discoveryWaitEvery:      30 * time.Second,
		discoveryTimeout:        15 * time.Second,
		discoverySurfaceTimeout: 3 * time.Second,
		discoveryStrictTTL:      30 * time.Second,
		discoveryFailTTL:        30 * time.Second,
	}
	// Internal MCP services are app-owned plugins; registries start empty.
	return r, nil
}

// SetExecutionProtection installs the default registry's pre-dispatch guard.
func (r *Registry) SetExecutionProtection(guard toolprotection.Guard) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.executionProtection = guard
	r.mu.Unlock()
}

// debugf emits a formatted debug line to the configured debugWriter when present.
func (r *Registry) debugf(format string, args ...interface{}) {
	if r == nil || r.debugWriter == nil {
		return
	}
	_, _ = fmt.Fprintf(r.debugWriter, "[tools] "+format+"\n", args...)
}

func debugMCPExecf(format string, args ...interface{}) {
	if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_MCP_EXEC")) == "" {
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "[mcp-exec] "+format+"\n", args...)
}

// WithManager attaches a per-conversation MCP manager used to inject the
// appropriate client and auth token into the context at call-time.
func (r *Registry) WithManager(m *manager.Manager) *Registry { r.mgr = m; return r }

// InjectVirtualAgentTools registers synthetic tool definitions that delegate
// execution to another agent. It must be called once during bootstrap *after*
// the agent catalogue is loaded. Domain can be empty to expose all.
func (r *Registry) InjectVirtualAgentTools(agents []*agent.Agent, domain string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ag := range agents {
		if ag == nil {
			continue
		}
		// Prefer Profile.Publish to drive exposure; fallback to legacy Directory.Enabled
		if ag.Profile == nil || !ag.Profile.Publish {
			continue
		}

		// Service/method: default to historical values for compatibility
		service := "agentExec"
		method := ag.ID

		toolID := fmt.Sprintf("%s/%s", service, method)

		// Build parameter schema once
		params := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"objective": map[string]interface{}{
					"type":        "string",
					"description": "Concise goal for the agent",
				},
				"context": map[string]interface{}{
					"type":        "object",
					"description": "Optional shared context",
				},
			},
			"required": []string{"objective"},
		}

		dispName := strings.TrimSpace(ag.Name)
		if strings.TrimSpace(ag.Profile.Name) != "" {
			dispName = strings.TrimSpace(ag.Profile.Name)
		}
		desc := strings.TrimSpace(ag.Description)
		if strings.TrimSpace(ag.Profile.Description) != "" {
			desc = strings.TrimSpace(ag.Profile.Description)
		}

		def := llm.ToolDefinition{
			Name:        toolID,
			Description: fmt.Sprintf("Executes the \"%s\" agent – %s", dispName, desc),
			Parameters:  params,
			Required:    []string{"objective"},
			OutputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"answer": map[string]interface{}{"type": "string"},
				},
			},
		}

		// If agent declares an elicitation block, append an autogenerated
		// section to Description to reuse existing calling conventions.
		if ag.ContextInputs != nil && ag.ContextInputs.Enabled {
			var b strings.Builder
			base := strings.TrimSpace(def.Description)
			if base != "" {
				b.WriteString(base)
			}
			b.WriteString("\n\nWhen calling this agent, include the following fields in args.context (auxiliary inputs):\n")
			reqSet := map[string]struct{}{}
			for _, r := range ag.ContextInputs.RequestedSchema.Required {
				reqSet[r] = struct{}{}
			}
			// Render properties in a stable order
			names := make([]string, 0, len(ag.ContextInputs.RequestedSchema.Properties))
			for n := range ag.ContextInputs.RequestedSchema.Properties {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, name := range names {
				typ := ""
				descText := ""
				if m, ok := ag.ContextInputs.RequestedSchema.Properties[name].(map[string]interface{}); ok {
					if v, ok := m["type"].(string); ok {
						typ = strings.TrimSpace(v)
					}
					if v, ok := m["description"].(string); ok {
						descText = strings.TrimSpace(v)
					}
				}
				if typ == "" {
					typ = "any"
				}
				_, isReq := reqSet[name]
				// Format: - context.<name> (type, required): description
				b.WriteString("- context.")
				b.WriteString(name)
				b.WriteString(" (" + typ)
				if isReq {
					b.WriteString(", required")
				}
				b.WriteString(")")
				if descText != "" {
					b.WriteString(": ")
					b.WriteString(descText)
				}
				b.WriteString("\n")
			}
			def.Description = b.String()
		}

		// Handler closure captures agent pointer
		handler := func(ctx context.Context, args map[string]interface{}) (string, error) {
			// Merge agentId into args for downstream executor
			if args == nil {
				args = map[string]interface{}{}
			}
			args["agentId"] = ag.ID

			// Execute via MCP-backed registry
			result, err := r.Execute(ctx, "llm/agents:run", args)
			return result, err
		}

		r.virtualDefs[toolID] = def
		r.virtualExec[toolID] = handler
		if r.virtualTimeout == nil {
			r.virtualTimeout = map[string]timeoutSupport{}
		}
		r.virtualTimeout[toolID] = detectTimeoutSupport(&def)
	}
}

func ensureTimeoutMs(def *llm.ToolDefinition) timeoutSupport {
	if def == nil {
		return timeoutSupport{}
	}
	props := toolProperties(def)
	if _, ok := props[timeoutMsField]; ok {
		return timeoutSupport{native: true}
	}
	if _, ok := props["timeoutSec"]; ok {
		return timeoutSupport{}
	}
	if _, ok := props["timeout"]; ok {
		return timeoutSupport{}
	}
	props[timeoutMsField] = map[string]interface{}{
		"type":        "integer",
		"description": "Optional maximum execution time in milliseconds. If omitted or 0, default timeout applies. Capped at 2 hours.",
		"minimum":     0,
		"maximum":     maxTimeoutMs,
	}
	return timeoutSupport{injected: true}
}

func toolProperties(def *llm.ToolDefinition) map[string]interface{} {
	if def.Parameters == nil {
		def.Parameters = map[string]interface{}{}
	}
	if def.Parameters["type"] == nil {
		def.Parameters["type"] = "object"
	}
	props := def.Parameters["properties"]
	switch p := props.(type) {
	case map[string]interface{}:
		return p
	case map[string]map[string]interface{}:
		coerced := make(map[string]interface{}, len(p))
		for k, v := range p {
			coerced[k] = v
		}
		def.Parameters["properties"] = coerced
		return coerced
	case map[interface{}]interface{}:
		coerced := make(map[string]interface{}, len(p))
		for k, v := range p {
			ks, ok := k.(string)
			if !ok {
				continue
			}
			coerced[ks] = v
		}
		def.Parameters["properties"] = coerced
		return coerced
	case mcpschema.ToolInputSchemaProperties:
		coerced := make(map[string]interface{}, len(p))
		for k, v := range p {
			coerced[k] = v
		}
		def.Parameters["properties"] = coerced
		return coerced
	default:
		empty := map[string]interface{}{}
		def.Parameters["properties"] = empty
		return empty
	}
}

func detectTimeoutSupport(def *llm.ToolDefinition) timeoutSupport {
	if def == nil {
		return timeoutSupport{}
	}
	props := toolProperties(def)
	if _, ok := props[timeoutMsField]; ok {
		return timeoutSupport{native: true}
	}
	return timeoutSupport{}
}

func maybeInjectTimeoutMs(def *llm.ToolDefinition, inject bool) timeoutSupport {
	if !inject {
		return detectTimeoutSupport(def)
	}
	return ensureTimeoutMs(def)
}

func (r *Registry) shouldInjectTimeoutMs(server string) bool {
	if r == nil {
		return false
	}
	if strings.TrimSpace(server) == "" {
		return false
	}
	r.mu.RLock()
	_, ok := r.internal[server]
	r.mu.RUnlock()
	return !ok
}

func newToolCacheEntry(def *llm.ToolDefinition, mcpDef mcpschema.Tool, inject bool) *toolCacheEntry {
	if def == nil {
		return nil
	}
	support := maybeInjectTimeoutMs(def, inject)
	return &toolCacheEntry{def: *def, mcpDef: mcpDef, timeoutSupport: support}
}

// ---------------------------------------------------------------------------
// tool.Registry interface implementation
// ---------------------------------------------------------------------------

func (r *Registry) Definitions() []llm.ToolDefinition {
	return r.DefinitionsWithContext(context.Background())
}

func (r *Registry) DefinitionsWithContext(ctx context.Context) []llm.ToolDefinition {
	ctx = r.discoveryLookupContext(ctx)
	var defs []llm.ToolDefinition
	// Always include virtual tools.
	r.mu.RLock()
	for _, def := range r.virtualDefs {
		defs = append(defs, def)
	}
	r.mu.RUnlock()

	// Build a set of entries we've already included to avoid duplicates.
	seen := map[string]struct{}{}

	// Include any cached MCP tools so they remain visible even if servers are offline.
	r.mu.RLock()
	for _, e := range r.cache {
		// Display using service:method for consistency
		svc, method := splitToolName(e.def.Name)
		if svc == "" || method == "" {
			continue
		}
		disp := svc + ":" + method
		if _, ok := seen[disp]; ok {
			continue
		}
		def := e.def
		def.Name = disp
		defs = append(defs, def)
		seen[disp] = struct{}{}
	}
	r.mu.RUnlock()

	// Try to aggregate current server tools; merge with cache, but never remove on failure.
	discoveryCtx, cancel := r.withDiscoveryTimeout(ctx)
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()
	servers, err := r.listServers(discoveryCtx)
	if err != nil {
		r.warnf("tools: list servers failed: %v", err)
		return defs
	}
	for _, s := range servers {
		injectTimeoutMs := r.shouldInjectTimeoutMs(s)
		tools, err := r.listServerTools(discoveryCtx, s)
		if err != nil {
			// Keep cached entries; just warn on failure.
			r.warnf("tools: list %s failed: %v", s, err)
			continue
		}
		for _, t := range tools {
			disp := displayToolName(s, strings.TrimSpace(t.Name))
			if _, ok := seen[disp]; ok {
				continue
			}
			if def := llm.ToolDefinitionFromMcpTool(&t); def != nil {
				def.Name = disp
				_ = maybeInjectTimeoutMs(def, injectTimeoutMs)
				r.applyCacheableOverride(def, s)
				defs = append(defs, *def)
				seen[disp] = struct{}{}
				// Update cache for lookup by display name
				r.mu.Lock()
				if entry := newToolCacheEntry(def, t, injectTimeoutMs); entry != nil {
					r.cache[disp] = entry
				}
				r.mu.Unlock()
			}
		}
	}
	return defs
}

func (r *Registry) MatchDefinition(pattern string) []*llm.ToolDefinition {
	return r.MatchDefinitionWithContext(context.Background(), pattern)
}

func (r *Registry) MatchDefinitionWithContext(ctx context.Context, pattern string) []*llm.ToolDefinition {
	ctx = r.discoveryLookupContext(ctx)
	ctx, cancel := r.withDiscoveryTimeout(ctx)
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()
	var result []*llm.ToolDefinition
	seen := map[string]struct{}{}

	// removed noisy debug logging
	// Strip suffix selector (e.g., "|root=...;") when present
	if i := strings.Index(pattern, "|"); i != -1 {
		pattern = strings.TrimSpace(pattern[:i])
	}
	// Virtual first: support exact, wildcard, and service-only (no colon) patterns.
	r.mu.RLock()
	for id, def := range r.virtualDefs {
		if tmatch.Match(pattern, id) {
			copyDef := def
			result = append(result, &copyDef)
			seen[mcpnames.Canonical(strings.TrimSpace(copyDef.Name))] = struct{}{}
		}
	}
	r.mu.RUnlock()
	// Cached MCP definitions remain authoritative for tools we've already
	// resolved. If the backing MCP server is temporarily unavailable, callers
	// should still be able to match those concrete definitions without forcing
	// another discovery round.
	r.mu.RLock()
	for alias, entry := range r.cache {
		if entry == nil || !tmatch.Match(pattern, alias) {
			continue
		}
		key := mcpnames.Canonical(strings.TrimSpace(entry.def.Name))
		if key == "" {
			key = mcpnames.Canonical(strings.TrimSpace(alias))
		}
		if _, ok := seen[key]; ok {
			continue
		}
		defCopy := entry.def
		result = append(result, &defCopy)
		seen[key] = struct{}{}
	}
	r.mu.RUnlock()
	if len(result) > 0 {
		if isExplicitPattern(pattern) {
			return result
		}
		if svc := serverFromPattern(pattern); svc != "" {
			return result
		}
	}
	// When an explicit tool id already matches a virtual definition,
	// or a cached MCP definition, do not probe MCP discovery for that
	// service. This avoids spurious warnings for internal tools such as
	// llm/agents:list where no MCP config file is expected, and preserves
	// already-known remote tool definitions when a server is temporarily down.
	// Discover matching server tools when pattern specifies an MCP service prefix.
	if svc := serverFromPattern(pattern); svc != "" {
		injectTimeoutMs := r.shouldInjectTimeoutMs(svc)
		tools, err := r.listServerTools(ctx, svc)
		if err != nil {
			if !shouldSuppressMissingMCPConfigWarning(svc, err) {
				r.warnf("list tools failed for %s: %v", svc, err)
			}
		}
		for _, t := range tools {
			full := qualifiedToolName(svc, strings.TrimSpace(t.Name))
			if tmatch.Match(pattern, full) {
				def := llm.ToolDefinitionFromMcpTool(&t)
				if def == nil {
					continue
				}
				def.Name = full
				_ = maybeInjectTimeoutMs(def, injectTimeoutMs)
				r.applyCacheableOverride(def, svc)
				entry := newToolCacheEntry(def, t, injectTimeoutMs)
				if entry == nil {
					continue
				}
				defCopy := entry.def
				key := mcpnames.Canonical(strings.TrimSpace(defCopy.Name))
				if _, ok := seen[key]; !ok {
					result = append(result, &defCopy)
					seen[key] = struct{}{}
				}
				r.mu.Lock()
				if _, ok := r.cache[def.Name]; !ok {
					cacheToolAliases(r.cache, entry, def.Name)
				}
				r.mu.Unlock()
			}
		}
	}
	return result
}

func isExplicitPattern(pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if strings.ContainsAny(pattern, "*?[]") {
		return false
	}
	return strings.Contains(pattern, ":")
}

func (r *Registry) GetDefinition(name string) (*llm.ToolDefinition, bool) {
	return r.GetDefinitionWithContext(context.Background(), name)
}

func (r *Registry) GetDefinitionWithContext(ctx context.Context, name string) (*llm.ToolDefinition, bool) {
	// Lightweight debug hook to trace how tool definitions are resolved.
	r.mu.RLock()
	if def, ok := r.virtualDefs[name]; ok {
		r.mu.RUnlock()
		return &def, true
	}
	// cache hit?
	if e, ok := r.cache[name]; ok {
		def := e.def
		r.mu.RUnlock()
		return &def, true
	}
	r.mu.RUnlock()
	svc := serverFromName(name)
	if svc == "" {
		return nil, false
	}
	injectTimeoutMs := r.shouldInjectTimeoutMs(svc)
	ctx = r.discoveryLookupContext(ctx)
	discoveryCtx, cancel := r.withDiscoveryTimeout(ctx)
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()
	tools, err := r.listServerTools(discoveryCtx, svc)
	if err != nil {
		r.warnf("list tools failed for %s: %v", svc, err)
		return nil, false
	}
	// Compare by method part; add both aliases on hit
	_, method := splitToolName(name)
	for _, t := range tools {
		if strings.TrimSpace(t.Name) == strings.TrimSpace(method) {
			tool := llm.ToolDefinitionFromMcpTool(&t)
			if tool != nil {
				r.mu.Lock()
				fullSlash := qualifiedToolName(svc, strings.TrimSpace(t.Name))
				methods := r.internalCacheable[svc]
				tool.Name = fullSlash
				_ = maybeInjectTimeoutMs(tool, injectTimeoutMs)
				r.applyCacheableOverrideWithMethods(tool, svc, methods)
				entry := newToolCacheEntry(tool, t, injectTimeoutMs)
				// cache both aliases and the exact name used
				if entry != nil {
					cacheToolAliases(r.cache, entry, fullSlash)
					r.cache[strings.TrimSpace(name)] = entry
				}
				r.mu.Unlock()
			}
			return tool, true
		}
	}
	return nil, false
}

func (r *Registry) discoveryLookupContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx)); convID != "" {
		return ctx
	}
	if runtimediscovery.BackgroundFromContext(ctx) {
		return ctx
	}
	return runtimediscovery.WithBackground(ctx)
}

func (r *Registry) MustHaveTools(patterns []string) ([]llm.Tool, error) {
	var ret []llm.Tool
	var missing []string
	for _, n := range patterns {
		matchedTools := r.MatchDefinition(n)
		if len(matchedTools) == 0 {
			missing = append(missing, n)
		}
		for _, matchedTool := range matchedTools {
			ret = append(ret, llm.Tool{Type: "function", Definition: *matchedTool})
		}
	}
	if len(missing) > 0 {
		return ret, fmt.Errorf("tools not found: %s", strings.Join(missing, ", "))
	}
	return ret, nil
}

func (r *Registry) Execute(ctx context.Context, name string, args map[string]interface{}) (result string, retErr error) {
	execStart := time.Now()
	// Handle selector suffix and base tool name
	var selector string
	baseName := strings.TrimSpace(name)
	if i := strings.Index(name, "|"); i != -1 {
		baseName = strings.TrimSpace(name[:i])
		selector = strings.TrimSpace(name[i+1:])
	}
	callArgs := args
	debugMCPExecf("registry execute start name=%s base=%s selector=%q argsKeys=%d", name, baseName, selector, len(args))
	if r != nil {
		var cancel context.CancelFunc
		ctx, cancel, callArgs = r.applyTimeoutMs(ctx, baseName, callArgs)
		if cancel != nil {
			defer cancel()
		}
	}
	protected := false
	protectionAmbiguous := false
	r.mu.RLock()
	guard := r.executionProtection
	r.mu.RUnlock()
	if guard != nil {
		claim, err := guard.Claim(ctx, baseName, callArgs)
		if err != nil {
			return "", err
		}
		protected = claim.Protected
		if claim.Protected && !claim.Acquired {
			duplicate, _ := json.Marshal(struct {
				Status         string `json:"status"`
				ProviderCalled bool   `json:"providerCalled"`
				RuleID         string `json:"ruleId"`
			}{Status: "duplicate_suppressed", ProviderCalled: false, RuleID: claim.RuleID})
			return string(duplicate), nil
		}
		if claim.Protected {
			defer func() {
				state := toolprotection.StateCompleted
				if retErr != nil {
					state = toolprotection.StateFailed
				}
				if protectionAmbiguous || errors.Is(retErr, context.Canceled) || errors.Is(retErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
					state = toolprotection.StateUnknown
				}
				finishBase := context.WithoutCancel(ctx)
				go func() {
					finishCtx, finishCancel := context.WithTimeout(finishBase, 5*time.Second)
					defer finishCancel()
					guard.Finish(finishCtx, claim, state)
				}()
			}()
		}
	}
	convID := runtimerequestctx.ConversationIDFromContext(ctx)
	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))

	// virtual tool?
	r.mu.RLock()
	h, ok := r.virtualExec[baseName]
	r.mu.RUnlock()
	if ok {
		out, err := h(ctx, callArgs)
		if err != nil || selector == "" {
			return out, err
		}
		// Post-filter output when possible (JSON expected)
		return r.applySelector(out, selector)
	}
	serviceName, _ := splitToolName(baseName)
	hasInternalClient := false
	if serviceName != "" {
		r.mu.RLock()
		_, hasInternalClient = r.internal[serviceName]
		r.mu.RUnlock()
	}
	// cached executable?
	if !hasInternalClient {
		r.mu.RLock()
		if e, ok := r.cache[baseName]; ok && e.exec != nil {
			r.mu.RUnlock()
			out, err := e.exec(ctx, callArgs)
			if err != nil || selector == "" {
				return out, err
			}
			return r.applySelector(out, selector)
		}
		r.mu.RUnlock()
	}

	server := serviceName
	if server == "" {
		r.debugf("Execute: invalid tool name (no server): %s", baseName)
		return "", fmt.Errorf("invalid tool name: %s", name)
	}
	if r.isBlockedInternalMethodExecution(ctx, server, baseName) {
		return "", fmt.Errorf("tool not found: %s", baseName)
	}
	var options []mcpclient.RequestOption
	if r.mgr != nil {
		ctx = r.mgr.WithAuthTokenContext(ctx, server)
	}
	if r.isDelegatedAuthServer(ctx, server) {
		// Delegated servers resolve credentials through the manager-installed
		// viant/mcp CredentialResolver; the registry must not independently
		// select or attach a workspace token for them.
		if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_MCP_AUTH")) != "" {
			fmt.Fprintf(os.Stderr, "[mcp-auth] server=%s src=delegated_resolver\n", strings.TrimSpace(server))
		}
	} else {
		useID := false
		if r.mgr != nil {
			useID = r.mgr.UseIDToken(ctx, server)
		}
		if tok := authctx.MCPAuthToken(ctx, useID); tok != "" {
			debugPrintMCPAuthToken(server, useID, tok, ctx)
			options = append(options, mcpclient.WithAuthToken(tok))
		} else {
			// Debug-only: emit a line when no token is available so auth propagation issues are visible.
			if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_MCP_AUTH")) != "" {
				fmt.Fprintf(os.Stderr, "[mcp-auth] server=%s useID=%v src=none\n", strings.TrimSpace(server), useID)
			}
		}
	}
	// Request ID ownership belongs to the downstream layer: the MCP Adapter for
	// in-process clients and the JSON-RPC transport for remote clients. Assigning
	// IDs here would create a second independent sequence and possible collisions.
	// Acquire appropriate client: internal or per-conversation via manager.
	var cli mcpclient.Interface
	var err error
	if c, ok := r.internal[server]; ok && c != nil {
		cli = c
		debugMCPExecf("registry client internal server=%s base=%s", server, baseName)
	} else {
		debugMCPExecf("registry client manager get start server=%s convID=%s base=%s", server, convID, baseName)
		cli, err = r.mgr.Get(ctx, convID, server)
	}
	if err != nil {
		debugMCPExecf("registry client get error server=%s base=%s elapsed=%s err=%v", server, baseName, time.Since(execStart).Round(time.Millisecond), err)
		return "", mcpauth.WrapError(err)
	}
	debugMCPExecf("registry client ready server=%s base=%s elapsed=%s", server, baseName, time.Since(execStart).Round(time.Millisecond))
	if r.mgr != nil {
		defer r.mgr.Touch(convID, server)
	}

	// Respect context deadline when present; default a generous timeout.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
	}

	// Deduplicate rapid identical calls per conversation (memoization)
	// Key uses fully qualified tool name and a stable JSON for args.
	keyArgs, _ := json.Marshal(callArgs)
	recentKey := userID + "|" + baseName + "|" + selector + "|" + string(keyArgs)
	var activeRecent *recentCall
	if !protected && r.recentTTL > 0 {
		call, owner, out, err, handled := r.beginRecentCall(ctx, convID, recentKey)
		if handled {
			return out, err
		}
		if owner {
			activeRecent = call
			defer func() {
				r.finishRecentCall(convID, recentKey, activeRecent, result, retErr)
			}()
		}
	}

	// Use proxy to normalize tool name and execute with reconnect-aware retry.
	px, _ := mcpproxy.NewProxy(ctx, server, cli)
	if px == nil {
		err := fmt.Errorf("mcp proxy is nil for server %s", server)
		debugMCPExecf("registry proxy error server=%s base=%s elapsed=%s err=%v", server, baseName, time.Since(execStart).Round(time.Millisecond), err)
		return "", err
	}
	maxAttempts := 3 // initial + 2 retries
	if protected {
		maxAttempts = 1
	}
	var res *mcpschema.CallToolResult
	for attempt := 0; attempt < maxAttempts; attempt++ {
		attemptStart := time.Now()
		debugMCPExecf("registry calltool start server=%s base=%s attempt=%d argsBytes=%d", server, baseName, attempt+1, len(keyArgs))
		res, err = px.CallTool(ctx, baseName, callArgs, options...)
		debugMCPExecf("registry calltool done server=%s base=%s attempt=%d elapsed=%s err=%v nilResult=%v", server, baseName, attempt+1, time.Since(attemptStart).Round(time.Millisecond), err, res == nil)
		if err == nil {
			if res == nil {
				break
			}
			if res.ResultType == mcpschema.ResultTypeInputRequired {
				return "", &InputRequiredError{InputRequests: res.InputRequests, RequestState: res.RequestState}
			}
			if res.IsError != nil && *res.IsError {
				terr := toolError(res)
				debugMCPExecf("registry calltool tool-error server=%s base=%s attempt=%d err=%v content=%d structured=%d", server, baseName, attempt+1, terr, len(res.Content), structuredContentSize(res.StructuredContent))
				if r.mgr != nil && isReconnectableError(terr) && attempt < maxAttempts-1 {
					// reconnect and retry
					log.Printf("[warn][mcp-client-pool] reconnect requested server=%q scope_type=conversation conv_id=%q reason=tool_error attempt=%d err=%v",
						server, convID, attempt+1, terr)
					if _, rerr := r.mgr.Reconnect(ctx, convID, server); rerr == nil {
						if ncli, gerr := r.mgr.Get(ctx, convID, server); gerr == nil {
							px, _ = mcpproxy.NewProxy(ctx, server, ncli)
							continue
						} else {
							log.Printf("[warn][mcp-client-pool] reconnect get failed server=%q scope_type=conversation conv_id=%q attempt=%d err=%v",
								server, convID, attempt+1, gerr)
						}
					} else {
						log.Printf("[warn][mcp-client-pool] reconnect failed server=%q scope_type=conversation conv_id=%q attempt=%d err=%v",
							server, convID, attempt+1, rerr)
					}
				}
				return "", terr
			}
			break
		}
		// Transport/client-level error
		if err != nil && protected {
			protectionAmbiguous = true
		}
		if r.mgr != nil && isReconnectableError(err) && attempt < maxAttempts-1 {
			log.Printf("[warn][mcp-client-pool] reconnect requested server=%q scope_type=conversation conv_id=%q reason=transport_error attempt=%d err=%v",
				server, convID, attempt+1, err)
			if _, rerr := r.mgr.Reconnect(ctx, convID, server); rerr == nil {
				if ncli, gerr := r.mgr.Get(ctx, convID, server); gerr == nil {
					px, _ = mcpproxy.NewProxy(ctx, server, ncli)
					continue
				} else {
					log.Printf("[warn][mcp-client-pool] reconnect get failed server=%q scope_type=conversation conv_id=%q attempt=%d err=%v",
						server, convID, attempt+1, gerr)
				}
			} else {
				log.Printf("[warn][mcp-client-pool] reconnect failed server=%q scope_type=conversation conv_id=%q attempt=%d err=%v",
					server, convID, attempt+1, rerr)
			}
		}
		// Non-reconnectable or reconnect failed
		debugMCPExecf("registry calltool returning transport error server=%s base=%s elapsed=%s err=%v", server, baseName, time.Since(execStart).Round(time.Millisecond), err)
		return "", mcpauth.WrapError(err)
	}
	if res == nil {
		if protected {
			protectionAmbiguous = true
		}
		err := fmt.Errorf("mcp tool %s returned nil result", baseName)
		debugMCPExecf("registry nil result server=%s base=%s elapsed=%s", server, baseName, time.Since(execStart).Round(time.Millisecond))
		return "", err
	}
	debugMCPExecf("registry compose start server=%s base=%s elapsed=%s content=%d structured=%d isError=%v", server, baseName, time.Since(execStart).Round(time.Millisecond), len(res.Content), structuredContentSize(res.StructuredContent), res.IsError != nil && *res.IsError)
	// Compose textual result prioritising text content, then structured content.
	for _, c := range res.Content {
		if text := strings.TrimSpace(callToolContentText(c)); text != "" {
			debugMCPExecf("registry compose text server=%s base=%s elapsed=%s resultBytes=%d", server, baseName, time.Since(execStart).Round(time.Millisecond), len(text))
			if selector != "" {
				return r.applySelector(text, selector)
			}
			if !protected && r.recentTTL > 0 {
				r.recentMu.Lock()
				if r.recentResults[convID] == nil {
					r.recentResults[convID] = map[string]recentItem{}
				}
				r.recentResults[convID][recentKey] = recentItem{when: time.Now(), out: text}
				r.recentMu.Unlock()
			}
			return text, nil
		}
	}
	if res.StructuredContent != nil {
		if out, ok := structuredContentString(res.StructuredContent); ok {
			debugMCPExecf("registry compose structured-string server=%s base=%s elapsed=%s resultBytes=%d", server, baseName, time.Since(execStart).Round(time.Millisecond), len(out))
			if selector != "" {
				return r.applySelector(out, selector)
			}
			if !protected && r.recentTTL > 0 {
				r.recentMu.Lock()
				if r.recentResults[convID] == nil {
					r.recentResults[convID] = map[string]recentItem{}
				}
				r.recentResults[convID][recentKey] = recentItem{when: time.Now(), out: out}
				r.recentMu.Unlock()
			}
			return out, nil
		}
		if data, err := json.Marshal(res.StructuredContent); err == nil {
			out := string(data)
			debugMCPExecf("registry compose structured-json server=%s base=%s elapsed=%s resultBytes=%d", server, baseName, time.Since(execStart).Round(time.Millisecond), len(out))
			if selector != "" {
				return r.applySelector(out, selector)
			}
			if !protected && r.recentTTL > 0 {
				r.recentMu.Lock()
				if r.recentResults[convID] == nil {
					r.recentResults[convID] = map[string]recentItem{}
				}
				r.recentResults[convID][recentKey] = recentItem{when: time.Now(), out: out}
				r.recentMu.Unlock()
			}
			return out, nil
		}
	}
	for _, c := range res.Content {
		if text := strings.TrimSpace(callToolContentText(c)); text != "" {
			debugMCPExecf("registry compose fallback-text server=%s base=%s elapsed=%s resultBytes=%d", server, baseName, time.Since(execStart).Round(time.Millisecond), len(text))
			if selector != "" {
				return r.applySelector(text, selector)
			}
			if !protected && r.recentTTL > 0 {
				r.recentMu.Lock()
				if r.recentResults[convID] == nil {
					r.recentResults[convID] = map[string]recentItem{}
				}
				r.recentResults[convID][recentKey] = recentItem{when: time.Now(), out: text}
				r.recentMu.Unlock()
			}
			return text, nil
		}
	}
	// Fallback to raw JSON of first element or empty
	if len(res.Content) > 0 {
		raw, _ := json.Marshal(res.Content[0])
		out := string(raw)
		debugMCPExecf("registry compose raw-content server=%s base=%s elapsed=%s resultBytes=%d", server, baseName, time.Since(execStart).Round(time.Millisecond), len(out))
		if selector != "" {
			return r.applySelector(out, selector)
		}
		return out, nil
	}
	debugMCPExecf("registry compose empty server=%s base=%s elapsed=%s", server, baseName, time.Since(execStart).Round(time.Millisecond))
	return "", nil
}

func (r *Registry) beginRecentCall(ctx context.Context, convID, recentKey string) (*recentCall, bool, string, error, bool) {
	r.recentMu.Lock()
	if m := r.recentResults[convID]; m != nil {
		if it, ok := m[recentKey]; ok && time.Since(it.when) <= r.recentTTL {
			r.recentMu.Unlock()
			return nil, false, it.out, nil, true
		}
	}
	if r.recentInFlight == nil {
		r.recentInFlight = map[string]map[string]*recentCall{}
	}
	if m := r.recentInFlight[convID]; m != nil {
		if call := m[recentKey]; call != nil {
			r.recentMu.Unlock()
			select {
			case <-call.done:
				return nil, false, call.out, call.err, true
			case <-ctx.Done():
				return nil, false, "", ctx.Err(), true
			}
		}
	}
	if r.recentInFlight[convID] == nil {
		r.recentInFlight[convID] = map[string]*recentCall{}
	}
	call := &recentCall{done: make(chan struct{})}
	r.recentInFlight[convID][recentKey] = call
	r.recentMu.Unlock()
	return call, true, "", nil, false
}

func (r *Registry) finishRecentCall(convID, recentKey string, call *recentCall, out string, err error) {
	if call == nil {
		return
	}
	r.recentMu.Lock()
	call.out = out
	call.err = err
	if err == nil {
		if r.recentResults == nil {
			r.recentResults = map[string]map[string]recentItem{}
		}
		if r.recentResults[convID] == nil {
			r.recentResults[convID] = map[string]recentItem{}
		}
		r.recentResults[convID][recentKey] = recentItem{when: time.Now(), out: out}
	}
	close(call.done)
	if m := r.recentInFlight[convID]; m != nil && m[recentKey] == call {
		delete(m, recentKey)
		if len(m) == 0 {
			delete(r.recentInFlight, convID)
		}
	}
	r.recentMu.Unlock()
}

func structuredContentString(structured interface{}) (string, bool) {
	if text, ok := structured.(string); ok {
		return text, true
	}
	values, ok := structured.(map[string]interface{})
	if !ok {
		return "", false
	}
	if len(values) != 1 {
		return "", false
	}
	for _, key := range []string{"result", "text"} {
		if value, ok := values[key]; ok {
			text, ok := value.(string)
			return text, ok
		}
	}
	return "", false
}

// InputRequiredError preserves the July multi-round-trip payload for the host
// orchestration layer. Error intentionally omits the opaque request state so it
// cannot leak through ordinary logs.
type InputRequiredError struct {
	InputRequests map[string]interface{}
	RequestState  *string
}

func (e *InputRequiredError) Error() string {
	return "MCP operation requires additional input"
}

func structuredContentSize(structured interface{}) int {
	switch value := structured.(type) {
	case nil:
		return 0
	case map[string]interface{}:
		return len(value)
	case []interface{}:
		return len(value)
	default:
		return 1
	}
}

func debugPrintMCPAuthToken(server string, useID bool, token string, ctx context.Context) {
	if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_MCP_AUTH")) == "" {
		return
	}
	fp := tokenFingerprint(token)
	src := "legacy"
	if tb := authctx.TokensFromContext(ctx); tb != nil {
		switch {
		case useID && strings.TrimSpace(tb.IDToken) != "":
			src = "bundle:id"
		case !useID && strings.TrimSpace(tb.AccessToken) != "":
			src = "bundle:access"
		default:
			src = "bundle"
		}
	}
	fmt.Fprintf(os.Stderr, "[mcp-auth] server=%s useID=%v src=%s tokLen=%d sha256=%s\n", strings.TrimSpace(server), useID, src, len(token), fp)
}

func tokenFingerprint(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}

func (r *Registry) applySelector(out, selector string) (string, error) {
	spec := transform.ParseSuffix(selector)
	if spec == nil {
		return out, nil
	}
	data := []byte(out)
	filtered, err := spec.Apply(data)
	if err != nil {
		r.warnf("selector apply failed: %v", err)
		return out, nil // fallback to original
	}
	return string(filtered), nil
}

func (r *Registry) applyTimeoutMs(ctx context.Context, name string, args map[string]interface{}) (context.Context, context.CancelFunc, map[string]interface{}) {
	timeoutMs, present, valid := extractTimeoutMs(args)
	if !present {
		return ctx, nil, args
	}
	support, ok := r.timeoutSupportFor(name)
	if !valid || timeoutMs <= 0 {
		return ctx, nil, stripTimeoutMs(args)
	}
	if !ok || !support.native {
		args = stripTimeoutMs(args)
	}
	if timeoutMs > maxTimeoutMs {
		timeoutMs = maxTimeoutMs
	}
	nctx, cancel := withTimeoutMs(ctx, timeoutMs)
	return nctx, cancel, args
}

func (r *Registry) timeoutSupportFor(name string) (timeoutSupport, bool) {
	if r == nil {
		return timeoutSupport{}, false
	}
	if s, ok := r.lookupTimeoutSupport(name); ok {
		return s, true
	}
	if _, ok := r.GetDefinition(name); ok {
		if s, ok := r.lookupTimeoutSupport(name); ok {
			return s, true
		}
	}
	return timeoutSupport{}, false
}

func (r *Registry) lookupTimeoutSupport(name string) (timeoutSupport, bool) {
	r.mu.RLock()
	if s, ok := r.virtualTimeout[name]; ok {
		r.mu.RUnlock()
		return s, true
	}
	if e, ok := r.cache[name]; ok {
		r.mu.RUnlock()
		return e.timeoutSupport, true
	}
	r.mu.RUnlock()

	svc, method := splitToolName(name)
	if svc == "" || method == "" {
		return timeoutSupport{}, false
	}
	slash := svc + "/" + method
	colon := svc + ":" + method
	r.mu.RLock()
	if e, ok := r.cache[slash]; ok {
		r.mu.RUnlock()
		return e.timeoutSupport, true
	}
	if e, ok := r.cache[colon]; ok {
		r.mu.RUnlock()
		return e.timeoutSupport, true
	}
	r.mu.RUnlock()
	return timeoutSupport{}, false
}

func extractTimeoutMs(args map[string]interface{}) (int64, bool, bool) {
	if args == nil {
		return 0, false, false
	}
	raw, ok := args[timeoutMsField]
	if !ok {
		return 0, false, false
	}
	switch v := raw.(type) {
	case int:
		return int64(v), true, true
	case int64:
		return v, true, true
	case int32:
		return int64(v), true, true
	case float64:
		return int64(v), true, true
	case float32:
		return int64(v), true, true
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, true, false
		}
		return i, true, true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, true, false
		}
		return i, true, true
	default:
		return 0, true, false
	}
}

func stripTimeoutMs(args map[string]interface{}) map[string]interface{} {
	if args == nil {
		return nil
	}
	if _, ok := args[timeoutMsField]; !ok {
		return args
	}
	out := make(map[string]interface{}, len(args)-1)
	for k, v := range args {
		if k == timeoutMsField {
			continue
		}
		out[k] = v
	}
	return out
}

func withTimeoutMs(ctx context.Context, timeoutMs int64) (context.Context, context.CancelFunc) {
	if timeoutMs <= 0 {
		return ctx, nil
	}
	d := time.Duration(timeoutMs) * time.Millisecond
	if d <= 0 {
		return ctx, nil
	}
	if deadline, ok := ctx.Deadline(); ok {
		if time.Until(deadline) <= d {
			return ctx, nil
		}
	}
	return context.WithTimeout(ctx, d)
}

func (r *Registry) SetDebugLogger(w io.Writer) { r.debugWriter = w }

// AddInternalService registers a service.Service as an in-memory MCP client under its Service.Name().
func (r *Registry) AddInternalService(s svc.Service) error {
	if s == nil {
		return fmt.Errorf("nil service")
	}
	cli, err := localmcp.NewServiceClient(context.Background(), s)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.internal == nil {
		r.internal = map[string]mcpclient.Interface{}
	}
	if r.internalTimeout == nil {
		r.internalTimeout = map[string]time.Duration{}
	}
	if r.internalMethodTimeout == nil {
		r.internalMethodTimeout = map[string]map[string]time.Duration{}
	}
	if r.internalMethodRetry == nil {
		r.internalMethodRetry = map[string]map[string]bool{}
	}
	if r.asyncByTool == nil {
		r.asyncByTool = map[string]*asynccfg.Config{}
	}
	if r.internalMethods == nil {
		r.internalMethods = map[string]map[string]svc.Signature{}
	}
	r.internal[s.Name()] = cli
	methods := make(map[string]svc.Signature, len(s.Methods()))
	for _, sig := range s.Methods() {
		methods[strings.TrimSpace(sig.Name)] = sig
	}
	r.internalMethods[s.Name()] = methods
	r.captureInternalTimeoutsLocked(s)
	r.captureInternalRetryPolicyLocked(s)
	if ac, ok := any(s).(svc.AsyncConfigurer); ok {
		for _, cfg := range ac.AsyncConfigs() {
			cacheAsyncConfigAliases(r.asyncByTool, cfg)
		}
	}
	if cp, ok := any(s).(svc.CacheableProvider); ok {
		if methods := cp.CacheableMethods(); len(methods) > 0 {
			if r.internalCacheable == nil {
				r.internalCacheable = map[string]map[string]bool{}
			}
			r.internalCacheable[s.Name()] = methods
		}
	}
	r.mu.Unlock()
	return nil
}

// captureInternalTimeoutsLocked replaces all cached timeout metadata for s.
// The caller must hold r.mu for writing.
func (r *Registry) captureInternalTimeoutsLocked(s svc.Service) {
	if s == nil {
		return
	}
	if r.internalTimeout == nil {
		r.internalTimeout = map[string]time.Duration{}
	}
	if r.internalMethodTimeout == nil {
		r.internalMethodTimeout = map[string]map[string]time.Duration{}
	}
	name := strings.TrimSpace(s.Name())
	delete(r.internalTimeout, name)
	delete(r.internalMethodTimeout, name)
	if tt, ok := any(s).(svc.HasToolTimeout); ok {
		if d := tt.ToolTimeout(); d > 0 {
			r.internalTimeout[name] = d
		}
	}
	tt, ok := any(s).(svc.HasMethodToolTimeout)
	if !ok {
		return
	}
	timeouts := make(map[string]time.Duration)
	for _, sig := range s.Methods() {
		method := strings.ToLower(strings.TrimSpace(sig.Name))
		if method == "" {
			continue
		}
		if d := tt.MethodToolTimeout(sig.Name); d > 0 {
			timeouts[method] = d
		}
	}
	if len(timeouts) > 0 {
		r.internalMethodTimeout[name] = timeouts
	}
}

// captureInternalRetryPolicyLocked replaces all cached retry metadata for s.
// The caller must hold r.mu for writing.
func (r *Registry) captureInternalRetryPolicyLocked(s svc.Service) {
	if s == nil {
		return
	}
	if r.internalMethodRetry == nil {
		r.internalMethodRetry = map[string]map[string]bool{}
	}
	name := strings.TrimSpace(s.Name())
	delete(r.internalMethodRetry, name)
	provider, ok := any(s).(svc.HasMethodRetryPolicy)
	if !ok {
		return
	}
	policies := make(map[string]bool)
	for _, sig := range s.Methods() {
		method := strings.ToLower(strings.TrimSpace(sig.Name))
		if method == "" {
			continue
		}
		if retryable, configured := provider.MethodRetryable(sig.Name); configured {
			policies[method] = retryable
		}
	}
	if len(policies) > 0 {
		r.internalMethodRetry[name] = policies
	}
}

func (r *Registry) isBlockedInternalMethodExecution(ctx context.Context, server, baseName string) bool {
	if r == nil {
		return false
	}
	planMode := strings.EqualFold(strings.TrimSpace(runtimerequestctx.RequestModeFromContext(ctx)), "plan")
	if planMode {
		return false
	}
	_, method := splitToolName(baseName)
	if method == "" {
		return false
	}
	r.mu.RLock()
	methods := r.internalMethods[strings.TrimSpace(server)]
	sig, ok := methods[strings.TrimSpace(method)]
	r.mu.RUnlock()
	return ok && sig.Internal
}

func (r *Registry) AsyncConfig(name string) (*asynccfg.Config, bool) {
	if r == nil {
		return nil, false
	}
	key := strings.TrimSpace(name)
	if key == "" {
		return nil, false
	}
	r.mu.RLock()
	cfg, ok := r.asyncByTool[key]
	if !ok {
		cfg, ok = r.asyncByTool[mcpnames.Canonical(key)]
	}
	r.mu.RUnlock()
	if ok && cfg != nil {
		return cfg, true
	}
	server := serverFromName(key)
	if server == "" || r.mgr == nil {
		return nil, false
	}
	opts, err := r.mgr.Options(context.Background(), server)
	if err != nil || opts == nil || len(opts.Async) == 0 {
		return nil, false
	}
	r.mu.Lock()
	if r.asyncByTool == nil {
		r.asyncByTool = map[string]*asynccfg.Config{}
	}
	for _, candidate := range opts.Async {
		cacheAsyncConfigAliases(r.asyncByTool, candidate)
	}
	cfg, ok = r.asyncByTool[key]
	if !ok {
		cfg, ok = r.asyncByTool[mcpnames.Canonical(key)]
	}
	r.mu.Unlock()
	return cfg, ok && cfg != nil
}

// ToolTimeout returns a suggested timeout for a given tool name.
func (r *Registry) ToolTimeout(name string) (time.Duration, bool) {
	server, method := splitToolName(name)
	if server == "" {
		return 0, false
	}
	// Internal service timeout
	r.mu.RLock()
	d, ok := r.internalMethodTimeout[server][strings.ToLower(strings.TrimSpace(method))]
	if !ok {
		d, ok = r.internalTimeout[server]
	}
	r.mu.RUnlock()
	if ok && d > 0 {
		return d, true
	}
	// MCP client config timeout
	if r.mgr != nil {
		if opts, err := r.mgr.Options(context.Background(), server); err == nil && opts != nil {
			if opts.ToolTimeoutSec > 0 {
				return time.Duration(opts.ToolTimeoutSec) * time.Second, true
			}
		}
	}
	return 0, false
}

// ToolRetryable returns an explicitly configured retry policy for an internal
// tool method. Tools without a policy retain the executor's default behavior.
func (r *Registry) ToolRetryable(name string) (bool, bool) {
	r.mu.RLock()
	guard := r.executionProtection
	r.mu.RUnlock()
	if guard != nil && guard.IsProtected(name) {
		return false, true
	}
	server, method := splitToolName(name)
	if server == "" || method == "" {
		return false, false
	}
	r.mu.RLock()
	retryable, ok := r.internalMethodRetry[server][strings.ToLower(strings.TrimSpace(method))]
	r.mu.RUnlock()
	return retryable, ok
}

// ToolExecutionProtected reports whether durable execution protection covers
// the supplied tool name.
func (r *Registry) ToolExecutionProtected(name string) bool {
	r.mu.RLock()
	guard := r.executionProtection
	r.mu.RUnlock()
	return guard != nil && guard.IsProtected(name)
}

// Initialize attempts to eagerly discover MCP servers and list their tools to
// warm the local cache. It logs warnings for unreachable servers.
func (r *Registry) Initialize(ctx context.Context) {
	if r == nil {
		return
	}
	r.addInternalMcp()
	servers, err := r.listServers(ctx)
	if err != nil {
		r.warnf("list servers failed: %v", err)
		return
	}
	var wg sync.WaitGroup
	for _, s := range servers {
		server := s
		if !r.shouldWarmServer(ctx, server) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			tools, err := r.listServerTools(runtimediscovery.WithBackground(ctx), server)
			if err != nil {
				r.warnf("list tools failed for %s: %v", server, err)
				return
			}
			r.replaceServerTools(server, tools)
		}()
	}
	wg.Wait()
	// Start background refresh monitors to auto-register tools when servers come online
	r.startAutoRefresh(ctx)

}

// startAutoRefresh launches a monitor per known server that periodically attempts
// to refresh its tool list and update the cache when connectivity is restored.
func (r *Registry) startAutoRefresh(ctx context.Context) {
	servers, err := r.listServers(ctx)
	if err != nil {
		r.warnf("refresh: list servers failed: %v", err)
		return
	}
	for _, s := range servers {
		srv := s
		if !r.shouldWarmServer(ctx, srv) {
			continue
		}
		go r.monitorServer(ctx, srv)
	}
}

func (r *Registry) shouldWarmServer(ctx context.Context, server string) bool {
	r.mu.RLock()
	_, isInternal := r.internal[server]
	r.mu.RUnlock()
	if isInternal {
		return true
	}
	if r.hasCachedServerTools(server) {
		return true
	}
	return r.isLoopbackMCPServer(server)
}

func (r *Registry) hasCachedServerTools(server string) bool {
	if r == nil {
		return false
	}
	server = strings.TrimSpace(server)
	if server == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for alias, entry := range r.cache {
		if entry == nil {
			continue
		}
		name := strings.TrimSpace(alias)
		if defName := strings.TrimSpace(entry.def.Name); defName != "" {
			name = defName
		}
		if strings.HasPrefix(name, server+"/") || strings.HasPrefix(name, server+":") {
			return true
		}
	}
	return false
}

func (r *Registry) monitorServer(ctx context.Context, server string) {
	// Exponential backoff on errors; steady cadence on success.
	backoff := time.Second
	maxBackoff := 60 * time.Second
	jitter := time.Millisecond * 200
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		// Attempt refresh
		if err := r.refreshServerTools(ctx, server); err != nil {
			// wait with backoff + jitter
			d := backoff
			if d < time.Second {
				d = time.Second
			}
			if d > maxBackoff {
				d = maxBackoff
			}
			// add small jitter
			d += time.Duration(time.Now().UnixNano() % int64(jitter))
			timer := time.NewTimer(d)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			// increase backoff up to cap
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		// success: reset backoff and wait for steady refresh interval
		backoff = time.Second
		interval := r.refreshEvery
		if interval <= 0 {
			interval = 30 * time.Second
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// refreshServerTools lists tools for a server and atomically replaces its cache entries.
func (r *Registry) refreshServerTools(ctx context.Context, server string) error {
	ctx = runtimediscovery.WithBackground(ctx)
	tools, err := r.listServerTools(ctx, server)
	if err != nil {
		return err
	}
	r.replaceServerTools(server, tools)
	return nil
}

// replaceServerTools atomically replaces cache entries for a given server.
func (r *Registry) replaceServerTools(server string, tools []mcpschema.Tool) {
	// Build new map for server
	newEntries := make(map[string]*toolCacheEntry, len(tools)*2)
	injectTimeoutMs := r.shouldInjectTimeoutMs(server)
	for _, t := range tools {
		full := server + "/" + t.Name
		if def := llm.ToolDefinitionFromMcpTool(&t); def != nil {
			def.Name = full
			_ = maybeInjectTimeoutMs(def, injectTimeoutMs)
			r.applyCacheableOverride(def, server)
			if entry := newToolCacheEntry(def, t, injectTimeoutMs); entry != nil {
				newEntries[full] = entry
				// also colon alias
				colon := server + ":" + t.Name
				newEntries[colon] = entry
			}
		}
	}
	// If refresh returns no tools, retain previous cache for this server.
	if len(newEntries) == 0 {
		r.warnf("refresh: %s returned no tools; retaining previous cache", server)
		return
	}
	// Swap entries under lock: remove old for server, then add new
	r.mu.Lock()
	for k := range r.cache {
		if serverFromName(k) == server {
			delete(r.cache, k)
		}
	}
	for k, v := range newEntries {
		r.cache[k] = v
	}
	r.mu.Unlock()
}

func (r *Registry) warnf(format string, args ...interface{}) {
	r.mu.Lock()
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
	r.mu.Unlock()
}

// LastWarnings returns any accumulated non-fatal warnings and does not clear them.
func (r *Registry) LastWarnings() []string {
	r.mu.RLock()
	if len(r.warnings) == 0 {
		r.mu.RUnlock()
		return nil
	}
	out := make([]string, len(r.warnings))
	copy(out, r.warnings)
	r.mu.RUnlock()
	return out
}

// ClearWarnings clears accumulated warnings.
func (r *Registry) ClearWarnings() { r.mu.Lock(); r.warnings = nil; r.mu.Unlock() }

// ---------------------- helpers ----------------------

// serverFromName extracts the service prefix from a tool name (service/method).
func serverFromName(name string) string { svc, _ := splitToolName(name); return svc }

// serverFromPattern returns service prefix when pattern contains it.
func serverFromPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return ""
	}
	if svc, method := splitToolName(pattern); method != "" {
		return svc
	}
	if strings.Contains(pattern, "*") {
		return strings.TrimSuffix(pattern, "*")
	}
	return pattern
}

// splitToolName returns service path and method given a name like "service/path:method".
func splitToolName(name string) (service, method string) {
	can := mcpnames.Canonical(name)
	n := mcpnames.Name(can)
	return n.Service(), n.Method()
}

func qualifiedToolName(server, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if svc, method := splitToolName(raw); svc != "" && method != "" {
		return raw
	}
	server = strings.TrimSpace(server)
	if server == "" {
		return raw
	}
	return server + "/" + raw
}

func displayToolName(server, raw string) string {
	qualified := qualifiedToolName(server, raw)
	svc, method := splitToolName(qualified)
	if svc == "" || method == "" {
		return strings.TrimSpace(qualified)
	}
	return svc + ":" + method
}

func cacheToolAliases(cache map[string]*toolCacheEntry, entry *toolCacheEntry, name string) {
	if cache == nil || entry == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	cache[name] = entry
	svc, method := splitToolName(name)
	if svc == "" || method == "" {
		return
	}
	cache[svc+"/"+method] = entry
	cache[svc+":"+method] = entry
	cache[mcpnames.Canonical(name)] = entry
}

func cacheAsyncConfigAliases(cache map[string]*asynccfg.Config, cfg *asynccfg.Config) {
	if cache == nil || cfg == nil {
		return
	}
	tools := []string{cfg.Run.Tool, cfg.Status.Tool}
	if cfg.Cancel != nil {
		tools = append(tools, cfg.Cancel.Tool)
	}
	for _, name := range tools {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		cache[name] = cfg
		if svc, method := splitToolName(name); svc != "" && method != "" {
			cache[svc+"/"+method] = cfg
			cache[svc+":"+method] = cfg
		}
		cache[mcpnames.Canonical(name)] = cfg
	}
}

// listServerTools queries the server tool registry via MCP ListTools.
func (r *Registry) listServerTools(ctx context.Context, server string) ([]mcpschema.Tool, error) {
	var cancel context.CancelFunc
	ctx, cancel = r.withDiscoveryTimeout(ctx)
	if cancel != nil {
		defer cancel()
	}
	// Prefer internal client if present
	r.mu.RLock()
	c, ok := r.internal[server]
	r.mu.RUnlock()
	if ok && c != nil {
		px, _ := mcpproxy.NewProxy(ctx, server, c)
		var opts []mcpclient.RequestOption
		if r.mgr != nil {
			ctx = r.mgr.WithAuthTokenContext(ctx, server)
		}
		useID := false
		if r.mgr != nil {
			useID = r.mgr.UseIDToken(ctx, server)
		}
		if tok := authctx.MCPAuthToken(ctx, useID); tok != "" && !r.isDelegatedAuthServer(ctx, server) {
			opts = append(opts, mcpclient.WithAuthToken(tok))
		}
		var tools []mcpschema.Tool
		err := r.waitDiscoveryStage(ctx, server, "list_tools_internal", func(callCtx context.Context) error {
			var listErr error
			tools, listErr = px.ListAllTools(callCtx, opts...)
			return listErr
		})
		if err != nil {
			return nil, err
		}
		return tools, nil
	}
	if r.mgr == nil {
		return nil, errors.New("mcp manager not configured")
	}
	ctx = r.mgr.WithAuthTokenContext(ctx, server)
	useID := r.mgr.UseIDToken(ctx, server)
	token := authctx.MCPAuthToken(ctx, useID)
	if r.isDelegatedAuthServer(ctx, server) {
		// Delegated servers authenticate through the manager-installed
		// resolver; never forward the workspace token to them.
		token = ""
	}
	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	scope := r.discoveryClientScope(ctx, server)
	r.observeSharedDiscoveryIdentity(server, scope, userID, token, useID)
	if err := r.discoveryFailureFor(server, scope); err != nil {
		if r.shouldBypassDiscoveryCooldown(server, scope) {
			r.clearDiscoveryFailure(server, scope)
		} else if bestEffortToolSurfaceDiscovery(ctx) {
			return nil, nil
		} else {
			log.Printf("[warn][mcp-discovery] cooldown active server=%q scope=%q scope_type=%s err=%v",
				server, scope, discoveryScopeType(scope, server), err)
			r.warnDiscoveryListIssue(server, scope, "cooldown", err, userID, useID, tokenFingerprint(token))
			return nil, err
		}
	}
	var cli mcpclient.Interface
	err := r.waitDiscoveryStage(ctx, server, "manager_get", func(callCtx context.Context) error {
		var getErr error
		cli, getErr = r.mgr.Get(callCtx, scope, server)
		return getErr
	})
	if err != nil {
		r.noteDiscoveryFailure(server, scope, err)
		if shouldSkipBestEffortToolSurfaceDiscoveryError(ctx, err) {
			return nil, nil
		}
		r.warnDiscoveryListIssue(server, scope, "manager_get", err, userID, useID, tokenFingerprint(token))
		return nil, err
	}
	r.clearDiscoveryFailure(server, scope)
	px, _ := mcpproxy.NewProxy(ctx, server, cli)
	var opts []mcpclient.RequestOption
	if tok := strings.TrimSpace(token); tok != "" {
		opts = append(opts, mcpclient.WithAuthToken(tok))
	}
	var tools []mcpschema.Tool
	err = r.waitDiscoveryStage(ctx, server, "list_tools", func(callCtx context.Context) error {
		var listErr error
		tools, listErr = px.ListAllTools(callCtx, opts...)
		return listErr
	})
	if err != nil {
		if isReconnectableError(err) {
			if retried, retryErr := r.retrySharedDiscoveryListTools(ctx, scope, server, opts); retryErr == nil {
				r.clearDiscoveryFailure(server, scope)
				return retried, nil
			} else {
				err = retryErr
			}
		}
		r.noteDiscoveryFailure(server, scope, err)
		if shouldSkipBestEffortToolSurfaceDiscoveryError(ctx, err) {
			return nil, nil
		}
		r.warnDiscoveryListIssue(server, scope, "list_tools", err, userID, useID, tokenFingerprint(token))
		return nil, err
	}
	r.clearDiscoveryFailure(server, scope)
	return tools, nil
}

func (r *Registry) retrySharedDiscoveryListTools(ctx context.Context, scope, server string, opts []mcpclient.RequestOption) ([]mcpschema.Tool, error) {
	if r == nil || r.mgr == nil {
		return nil, errors.New("mcp manager not configured")
	}
	log.Printf("[warn][mcp-discovery] reconnect requested server=%q scope=%q scope_type=%s reason=list_tools_retry",
		server, scope, discoveryScopeType(scope, server))
	if _, err := r.mgr.Reconnect(ctx, scope, server); err != nil {
		r.noteDiscoveryFailure(server, scope, err)
		log.Printf("[warn][mcp-discovery] reconnect failed server=%q scope=%q scope_type=%s err=%v",
			server, scope, discoveryScopeType(scope, server), err)
		return nil, err
	}
	var cli mcpclient.Interface
	err := r.waitDiscoveryStage(ctx, server, "manager_get_retry", func(callCtx context.Context) error {
		var getErr error
		cli, getErr = r.mgr.Get(callCtx, scope, server)
		return getErr
	})
	if err != nil {
		r.noteDiscoveryFailure(server, scope, err)
		log.Printf("[warn][mcp-discovery] reconnect get failed server=%q scope=%q scope_type=%s err=%v",
			server, scope, discoveryScopeType(scope, server), err)
		return nil, err
	}
	px, _ := mcpproxy.NewProxy(ctx, server, cli)
	var tools []mcpschema.Tool
	err = r.waitDiscoveryStage(ctx, server, "list_tools_retry", func(callCtx context.Context) error {
		var listErr error
		tools, listErr = px.ListAllTools(callCtx, opts...)
		return listErr
	})
	if err != nil {
		r.noteDiscoveryFailure(server, scope, err)
		return nil, err
	}
	r.clearDiscoveryFailure(server, scope)
	log.Printf("[info][mcp-discovery] reconnect retry succeeded server=%q scope=%q scope_type=%s tools=%d",
		server, scope, discoveryScopeType(scope, server), len(tools))
	return tools, nil
}

func (r *Registry) discoveryFailureFor(server, scope string) error {
	if r == nil {
		return nil
	}
	key := r.discoveryFailureKey(server, scope)
	now := time.Now()
	r.mu.RLock()
	until := r.discoveryFailUntil[key]
	msg := r.discoveryFailErr[key]
	r.mu.RUnlock()
	if until.IsZero() || !until.After(now) {
		return nil
	}
	if strings.TrimSpace(msg) == "" {
		msg = "discovery temporarily unavailable"
	}
	return fmt.Errorf("mcp discovery cooldown active until %s: %s", until.Format(time.RFC3339), msg)
}

func (r *Registry) shouldBypassDiscoveryCooldown(server, scope string) bool {
	if !r.isLoopbackMCPServer(server) {
		return false
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return true
	}
	return strings.HasPrefix(scope, fmt.Sprintf("mcp-discovery:%s:", strings.TrimSpace(server)))
}

func (r *Registry) noteDiscoveryFailure(server, scope string, err error) {
	if r == nil || err == nil {
		return
	}
	if classifyDiscoveryError(err) != "transport" {
		return
	}
	ttl := r.discoveryFailTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	key := r.discoveryFailureKey(server, scope)
	if r.isLoopbackMCPServer(server) && ttl < 5*time.Minute {
		ttl = 5 * time.Minute
	}
	r.mu.Lock()
	if r.discoveryFailUntil == nil {
		r.discoveryFailUntil = map[string]time.Time{}
	}
	if r.discoveryFailErr == nil {
		r.discoveryFailErr = map[string]string{}
	}
	r.discoveryFailUntil[key] = time.Now().Add(ttl)
	r.discoveryFailErr[key] = err.Error()
	r.mu.Unlock()
}

func (r *Registry) clearDiscoveryFailure(server, scope string) {
	if r == nil {
		return
	}
	key := r.discoveryFailureKey(server, scope)
	r.mu.Lock()
	delete(r.discoveryFailUntil, key)
	delete(r.discoveryFailErr, key)
	r.mu.Unlock()
}

// EvictDelegatedAuthState clears the discovery failure/cooldown state for a
// server across all scopes. Called after a delegated credential change (link,
// disconnect) so the next discovery retries immediately with the new
// credential instead of waiting out a cooldown earned by the old one.
func (r *Registry) EvictDelegatedAuthState(server string) {
	server = strings.TrimSpace(server)
	if r == nil || server == "" {
		return
	}
	prefix := server + "|"
	r.mu.Lock()
	for key := range r.discoveryFailUntil {
		if key == server || strings.HasPrefix(key, prefix) {
			delete(r.discoveryFailUntil, key)
			delete(r.discoveryFailErr, key)
		}
	}
	for key := range r.discoveryFailErr {
		if key == server || strings.HasPrefix(key, prefix) {
			delete(r.discoveryFailErr, key)
		}
	}
	r.mu.Unlock()
}

func (r *Registry) withDiscoveryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, nil
	}
	timeout := r.discoveryTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if mode, ok := runtimediscovery.ModeFromContext(ctx); ok {
		if mode.Strict {
			if r.discoveryStrictTTL > 0 {
				timeout = r.discoveryStrictTTL
			}
		} else if mode.ToolSurface && r.discoverySurfaceTimeout > 0 {
			timeout = r.discoverySurfaceTimeout
		}
	}
	return context.WithTimeout(ctx, timeout)
}

func bestEffortToolSurfaceDiscovery(ctx context.Context) bool {
	mode, ok := runtimediscovery.ModeFromContext(ctx)
	return ok && mode.ToolSurface && !mode.Strict
}

func shouldSkipBestEffortToolSurfaceDiscoveryError(ctx context.Context, err error) bool {
	return bestEffortToolSurfaceDiscovery(ctx) && classifyDiscoveryError(err) == "transport"
}

func (r *Registry) discoveryClientScope(ctx context.Context, server string) string {
	if convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx)); convID != "" {
		return convID
	}
	server = strings.TrimSpace(server)
	if server == "" {
		server = "unknown"
	}
	if runtimediscovery.BackgroundFromContext(ctx) {
		scope := fmt.Sprintf("mcp-discovery:%s:background", server)
		return scope
	}
	seq := atomic.AddUint64(&r.discoveryScopeSeq, 1)
	scope := fmt.Sprintf("mcp-discovery:%s:%d", server, seq)
	log.Printf("[warn][mcp-discovery] synthetic discovery scope fallback server=%q scope=%q seq=%d reason=%q",
		server, scope, seq, "no background mode and no conversation id in context")
	return scope
}

func discoveryScopeType(scope, server string) string {
	scope = strings.TrimSpace(scope)
	server = strings.TrimSpace(server)
	if scope == "" {
		return "other"
	}
	if strings.EqualFold(scope, "mcp-discovery:"+server+":background") {
		return "background"
	}
	if strings.HasPrefix(scope, "mcp-discovery:"+server+":") {
		return "synthetic_seq"
	}
	return "conversation"
}

func (r *Registry) waitDiscoveryStage(ctx context.Context, server, stage string, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Default mode: execute stage directly with no wait-wrapper diagnostics.
	// Diagnostic wait logging/dumps are enabled only when AGENTLY_DEBUG is on.
	if !logx.Enabled() {
		return fn(ctx)
	}
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	waitEvery := 30 * time.Second
	if r != nil && r.discoveryWaitEvery > 0 {
		waitEvery = r.discoveryWaitEvery
	}
	started := time.Now()
	result := make(chan error, 1)
	go func() {
		result <- fn(callCtx)
	}()

	ticker := time.NewTicker(waitEvery)
	defer ticker.Stop()
	logEvery := 5 * time.Minute
	dumpEvery := 30 * time.Minute
	lastLogged := time.Time{}
	nextDumpAt := started.Add(dumpEvery)

	for {
		select {
		case err := <-result:
			return err
		case <-ctx.Done():
			cancel()
			return ctx.Err()
		case <-ticker.C:
			now := time.Now()
			shouldLog := lastLogged.IsZero() || now.Sub(lastLogged) >= logEvery
			shouldDump := !now.Before(nextDumpAt)
			if !shouldLog && !shouldDump {
				continue
			}
			waited := time.Since(started).Round(time.Second)
			diag := r.discoveryWaitDiagnostics(ctx, server)
			if shouldLog {
				lastLogged = now
				logDiscoveryWait("heartbeat", server, stage, waited, diag)
			}
			if shouldDump {
				for !now.Before(nextDumpAt) {
					nextDumpAt = nextDumpAt.Add(dumpEvery)
				}
				logDiscoveryWait("wait_dump_30m", server, stage, waited, diag)
				logDiscoveryGoroutineDump(server, stage, waited)
			}
		}
	}
}

func logDiscoveryWait(event, server, stage string, waited time.Duration, diag discoveryWaitDiag) {
	log.Printf(
		"[warn][mcp-discovery] shared discovery still waiting event=%s server=%q conv_id=%q conv_present=%v stage=%s wait=%s mode_guess=%s mode_basis=%s user=%q user_present=%v scheduler=%v strict=%v discovery_mode_present=%v schedule_id=%q schedule_id_present=%v schedule_run_id=%q schedule_run_id_present=%v has_deadline=%v deadline_in=%s token_selected=%s token_access=%s token_id=%s token_bearer=%s token_id_legacy=%s",
		strings.TrimSpace(event),
		strings.TrimSpace(server),
		diag.convID,
		diag.hasConvID,
		strings.TrimSpace(stage),
		waited,
		diag.modeGuess,
		diag.modeBasis,
		diag.userID,
		diag.hasUserID,
		diag.scheduler,
		diag.strict,
		diag.modePresent,
		diag.scheduleID,
		diag.hasScheduleID,
		diag.scheduleRunID,
		diag.hasScheduleRunID,
		diag.hasDeadline,
		diag.deadlineIn,
		diag.selectedTokenFP,
		diag.accessTokenFP,
		diag.idTokenFP,
		diag.bearerFP,
		diag.legacyIDTokenFP,
	)
}

func logDiscoveryGoroutineDump(server, stage string, waited time.Duration) {
	var buf bytes.Buffer
	if p := pprof.Lookup("goroutine"); p != nil {
		if err := p.WriteTo(&buf, 2); err != nil {
			log.Printf("[warn][mcp-discovery] goroutine dump failed server=%q stage=%s wait=%s err=%v", strings.TrimSpace(server), strings.TrimSpace(stage), waited, err)
			return
		}
	} else {
		log.Printf("[warn][mcp-discovery] goroutine dump unavailable server=%q stage=%s wait=%s", strings.TrimSpace(server), strings.TrimSpace(stage), waited)
		return
	}
	log.Printf("[warn][mcp-discovery] goroutine dump begin server=%q stage=%s wait=%s", strings.TrimSpace(server), strings.TrimSpace(stage), waited)
	log.Printf("%s", buf.String())
	log.Printf("[warn][mcp-discovery] goroutine dump end server=%q stage=%s wait=%s", strings.TrimSpace(server), strings.TrimSpace(stage), waited)
}

type discoveryWaitDiag struct {
	convID           string
	hasConvID        bool
	userID           string
	hasUserID        bool
	scheduler        bool
	strict           bool
	modePresent      bool
	scheduleID       string
	hasScheduleID    bool
	scheduleRunID    string
	hasScheduleRunID bool
	hasDeadline      bool
	deadlineIn       string
	selectedTokenFP  string
	accessTokenFP    string
	idTokenFP        string
	bearerFP         string
	legacyIDTokenFP  string
	modeGuess        string
	modeBasis        string
}

const (
	discoveryCtxMissing = "<ctx-missing>"
	discoveryCtxEmpty   = "<ctx-empty>"
)

func (r *Registry) discoveryWaitDiagnostics(ctx context.Context, server string) discoveryWaitDiag {
	convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	hasConvID := convID != ""
	if !hasConvID {
		convID = discoveryCtxMissing
	}

	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	hasUserID := userID != ""
	if !hasUserID {
		// EffectiveUserID accessor cannot distinguish absent key vs empty payload.
		userID = discoveryCtxMissing
	}

	diag := discoveryWaitDiag{
		convID:          convID,
		hasConvID:       hasConvID,
		userID:          userID,
		hasUserID:       hasUserID,
		deadlineIn:      "none",
		selectedTokenFP: "none",
		accessTokenFP:   "none",
		idTokenFP:       "none",
		bearerFP:        "none",
		legacyIDTokenFP: "none",
		modeGuess:       "unknown",
		modeBasis:       "insufficient_signal",
	}

	if mode, ok := runtimediscovery.ModeFromContext(ctx); ok {
		diag.modePresent = true
		diag.scheduler = mode.Scheduler
		diag.strict = mode.Strict
		diag.scheduleID = strings.TrimSpace(mode.ScheduleID)
		diag.hasScheduleID = diag.scheduleID != ""
		if !diag.hasScheduleID {
			diag.scheduleID = discoveryCtxEmpty
		}
		diag.scheduleRunID = strings.TrimSpace(mode.ScheduleRunID)
		diag.hasScheduleRunID = diag.scheduleRunID != ""
		if !diag.hasScheduleRunID {
			diag.scheduleRunID = discoveryCtxEmpty
		}
	} else {
		diag.scheduleID = discoveryCtxMissing
		diag.scheduleRunID = discoveryCtxMissing
	}

	if deadline, ok := ctx.Deadline(); ok {
		diag.hasDeadline = true
		remaining := time.Until(deadline).Round(time.Second)
		diag.deadlineIn = remaining.String()
	}

	if tb := authctx.TokensFromContext(ctx); tb != nil {
		diag.accessTokenFP = tokenFingerprint(tb.AccessToken)
		diag.idTokenFP = tokenFingerprint(tb.IDToken)
	}
	diag.bearerFP = tokenFingerprint(authctx.Bearer(ctx))
	diag.legacyIDTokenFP = tokenFingerprint(authctx.IDToken(ctx))

	useID := false
	if r != nil && r.mgr != nil {
		useID = r.mgr.UseIDToken(ctx, strings.TrimSpace(server))
	}
	diag.selectedTokenFP = tokenFingerprint(authctx.MCPAuthToken(ctx, useID))

	diag.modeGuess, diag.modeBasis = guessDiscoveryMode(diag)
	return diag
}

func guessDiscoveryMode(diag discoveryWaitDiag) (string, string) {
	hasToken := diag.selectedTokenFP != "none" ||
		diag.accessTokenFP != "none" ||
		diag.idTokenFP != "none" ||
		diag.bearerFP != "none" ||
		diag.legacyIDTokenFP != "none"

	if diag.scheduler {
		if hasToken {
			return "manual_or_schedule_cred", "scheduler_ctx_with_token"
		}
		if diag.hasUserID {
			return "auto_like", "scheduler_ctx_user_without_token"
		}
		return "auto_like", "scheduler_ctx_without_user_or_token"
	}

	if hasToken {
		return "manual_like", "non_scheduler_ctx_with_token"
	}
	if diag.hasUserID {
		return "unknown", "non_scheduler_ctx_user_without_token"
	}
	return "unknown", "non_scheduler_ctx_without_user_or_token"
}

func (r *Registry) observeSharedDiscoveryIdentity(server, scope, userID, token string, useID bool) {
	if r == nil {
		return
	}
	server = strings.TrimSpace(server)
	if server == "" {
		return
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = discoveryCtxMissing
	}
	cur := discoveryIdentity{
		userID:  strings.TrimSpace(userID),
		tokenFP: tokenFingerprint(token),
		useID:   useID,
		seenAt:  time.Now(),
	}
	identityKey := discoveryIdentityKey(server, scope)

	r.mu.Lock()
	prev, ok := r.discoveryShared[identityKey]
	if r.discoveryShared == nil {
		r.discoveryShared = map[string]discoveryIdentity{}
	}
	r.discoveryShared[identityKey] = cur
	r.mu.Unlock()

	if !ok {
		return
	}
	if prev.useID != cur.useID {
		key := fmt.Sprintf("discovery_identity:%s:%s:useid:%v->%v", server, scope, prev.useID, cur.useID)
		r.warnDiscoveryf(key, "discovery client identity drift server=%q scope=%q reason=use_id_token_changed prev=%v curr=%v user_prev=%q user_curr=%q token_prev=%s token_curr=%s", server, scope, prev.useID, cur.useID, prev.userID, cur.userID, prev.tokenFP, cur.tokenFP)
	}
	if prev.userID != "" && cur.userID != "" && prev.userID != cur.userID {
		key := fmt.Sprintf("discovery_identity:%s:%s:user:%s->%s", server, scope, prev.userID, cur.userID)
		r.warnDiscoveryf(key, "discovery client identity drift server=%q scope=%q reason=user_changed prev=%q curr=%q use_id_token=%v token_prev=%s token_curr=%s", server, scope, prev.userID, cur.userID, cur.useID, prev.tokenFP, cur.tokenFP)
	}
	if prev.tokenFP != "none" && cur.tokenFP != "none" && prev.tokenFP != cur.tokenFP {
		key := fmt.Sprintf("discovery_identity:%s:%s:token:%s->%s", server, scope, prev.tokenFP, cur.tokenFP)
		r.warnDiscoveryf(key, "discovery client identity drift server=%q scope=%q reason=token_changed user_prev=%q user_curr=%q use_id_token=%v token_prev=%s token_curr=%s", server, scope, prev.userID, cur.userID, cur.useID, prev.tokenFP, cur.tokenFP)
	}
}

func (r *Registry) warnDiscoveryListIssue(server, scope, stage string, err error, userID string, useID bool, tokenFP string) {
	if err == nil {
		return
	}
	if shouldSuppressMissingMCPConfigWarning(server, err) {
		return
	}
	server = strings.TrimSpace(server)
	if server == "" {
		server = "unknown"
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = discoveryCtxMissing
	}
	kind := classifyDiscoveryError(err)
	if kind == "transport" && r.isLoopbackMCPServer(server) && strings.EqualFold(strings.TrimSpace(stage), "cooldown") {
		return
	}
	warnScope := scope
	if kind == "transport" && r.isLoopbackMCPServer(server) {
		warnScope = "loopback"
	}
	key := fmt.Sprintf("discovery_issue:%s:%s:%s:%s:%s:%v", server, warnScope, strings.TrimSpace(stage), kind, strings.TrimSpace(userID), useID)
	r.warnDiscoveryf(key, "discovery client issue server=%q scope=%q stage=%s kind=%s user=%q use_id_token=%v token=%s err=%v", server, scope, stage, kind, strings.TrimSpace(userID), useID, tokenFP, err)
}

func discoveryIdentityKey(server, scope string) string {
	return strings.TrimSpace(server) + "|" + strings.TrimSpace(scope)
}

func (r *Registry) discoveryFailureKey(server, scope string) string {
	if r != nil && r.isLoopbackMCPServer(server) {
		return strings.TrimSpace(server)
	}
	return discoveryIdentityKey(server, scope)
}

func (r *Registry) isLoopbackMCPServer(server string) bool {
	if r == nil || r.mgr == nil {
		return false
	}
	opts, err := r.mgr.Options(context.Background(), strings.TrimSpace(server))
	if err != nil || opts == nil || opts.ClientOptions == nil {
		return false
	}
	rawURL := strings.TrimSpace(opts.ClientOptions.Transport.URL)
	if rawURL == "" {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func shouldSuppressMissingMCPConfigWarning(server string, err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "no mcp servers configured in test" {
		return true
	}
	if strings.TrimSpace(server) != "llm/agents" {
		return false
	}
	return strings.Contains(msg, "mcp/llm/agents.yaml") &&
		strings.Contains(msg, "no such file or directory")
}

func classifyDiscoveryError(err error) string {
	if err == nil {
		return "none"
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "401") ||
		strings.Contains(msg, "403") ||
		strings.Contains(msg, "419") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "unauthenticated") ||
		strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "invalid token") ||
		strings.Contains(msg, "token expired") ||
		strings.Contains(msg, "id token") ||
		strings.Contains(msg, "access token") ||
		strings.Contains(msg, "jwt") ||
		strings.Contains(msg, "authorization") {
		return "auth"
	}
	if isReconnectableError(err) ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no route to host") ||
		strings.Contains(msg, "tls handshake timeout") {
		return "transport"
	}
	return "other"
}

func (r *Registry) warnDiscoveryf(key, format string, args ...interface{}) {
	if r == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	emit := true
	now := time.Now()

	r.mu.Lock()
	if r.discoveryWarnAt == nil {
		r.discoveryWarnAt = map[string]time.Time{}
	}
	if key != "" && r.discoveryWarnEvery > 0 {
		if at, ok := r.discoveryWarnAt[key]; ok && now.Sub(at) < r.discoveryWarnEvery {
			emit = false
		}
	}
	if emit {
		r.discoveryWarnAt[key] = now
		r.warnings = append(r.warnings, msg)
	}
	r.mu.Unlock()

	if emit {
		log.Printf("[warn][mcp-discovery] %s", msg)
	}
}

// listServers returns MCP client names from the workspace repository.
func (r *Registry) listServers(ctx context.Context) ([]string, error) {
	repo := mcprepo.New(afs.New())
	names, _ := repo.List(ctx)
	// Filter out malformed MCP configs to avoid nil pointer panics downstream.
	validNames := make([]string, 0, len(names))
	for _, n := range names {
		cfgPath, pathErr := repo.ResolveFilename(ctx, n)
		if pathErr != nil {
			r.warnf("mcp config path resolution failed: %s: %v", n, pathErr)
			continue
		}
		cfg, err := repo.Load(ctx, n)
		if err != nil {
			r.warnf("mcp config load failed: %s: %v", cfgPath, err)
			continue
		}
		if cfg == nil || cfg.ClientOptions == nil || strings.TrimSpace(cfg.Name) == "" {
			r.warnf("mcp config invalid (missing name/transport): %s", cfgPath)
			continue
		}
		validNames = append(validNames, n)
	}
	names = validNames
	// Optional override to force discovery of specific servers (comma-separated)
	if extra := strings.TrimSpace(os.Getenv("AGENTLY_MCP_SERVERS")); extra != "" {
		for _, s := range strings.Split(extra, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			names = append(names, s)
		}
	}
	// Merge with internal client names
	seen := map[string]struct{}{}
	var out []string
	for _, n := range names {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	r.mu.RLock()
	internalNames := make([]string, 0, len(r.internal))
	for n := range r.internal {
		internalNames = append(internalNames, n)
	}
	r.mu.RUnlock()
	for _, n := range internalNames {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out, nil
}

// toolError converts an error‑flagged MCP result into Go error.
func toolError(res *mcpschema.CallToolResult) error {
	if len(res.Content) == 0 {
		return errors.New("tool returned error without content")
	}
	if msg := callToolContentText(res.Content[0]); msg != "" {
		return errors.New(msg)
	}
	raw, _ := json.Marshal(res.Content[0])
	return errors.New(string(raw))
}

func callToolContentText(elem mcpschema.CallToolResultContentElem) string {
	switch v := elem.(type) {
	case mcpschema.TextContent:
		return v.Text
	case *mcpschema.TextContent:
		if v != nil {
			return v.Text
		}
	case mcpschema.ImageContent:
		return v.Data
	case *mcpschema.ImageContent:
		if v != nil {
			return v.Data
		}
	case mcpschema.AudioContent:
		return v.Data
	case *mcpschema.AudioContent:
		if v != nil {
			return v.Data
		}
	case mcpschema.ResourceLink:
		return v.Uri
	case *mcpschema.ResourceLink:
		if v != nil {
			return v.Uri
		}
	case mcpschema.EmbeddedResource:
		if v.Resource.Text != "" {
			return v.Resource.Text
		}
		if v.Resource.Uri != "" {
			return v.Resource.Uri
		}
		return v.Resource.Blob
	case *mcpschema.EmbeddedResource:
		if v == nil {
			return ""
		}
		if v.Resource.Text != "" {
			return v.Resource.Text
		}
		if v.Resource.Uri != "" {
			return v.Resource.Uri
		}
		return v.Resource.Blob
	case map[string]interface{}:
		if s, ok := v["text"].(string); ok {
			return s
		}
		if s, ok := v["data"].(string); ok {
			return s
		}
		if s, ok := v["uri"].(string); ok {
			return s
		}
		if s, ok := v["blob"].(string); ok {
			return s
		}
	}
	return ""
}

// isReconnectableError heuristically classifies transport/stream errors that
// are likely to be resolved by reconnecting the MCP client and retrying.
func isReconnectableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "stream error"),
		strings.Contains(msg, "internal_error; received from peer"),
		strings.Contains(msg, "handshake missing mcp-session-id header"),
		strings.Contains(msg, "clienthandler is not initialized"),
		strings.Contains(msg, "rst_stream"),
		strings.Contains(msg, "goaway"),
		strings.Contains(msg, "http2"),
		strings.Contains(msg, "trip not found"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "broken pipe"),
		strings.Contains(msg, "eof"),
		strings.Contains(msg, "failed to parse response: trip not found"),
		strings.Contains(msg, "server closed idle connection"),
		strings.Contains(msg, "no cached connection"):
		return true
	}
	return false
}

// addInternalMcp registers built-in services as in-memory MCP clients.
func (r *Registry) addInternalMcp() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.internal == nil {
		r.internal = map[string]mcpclient.Interface{}
	}
	if r.internalTimeout == nil {
		r.internalTimeout = map[string]time.Duration{}
	}
	if r.internalMethodTimeout == nil {
		r.internalMethodTimeout = map[string]map[string]time.Duration{}
	}
	if r.internalMethodRetry == nil {
		r.internalMethodRetry = map[string]map[string]bool{}
	}
	if r.asyncByTool == nil {
		r.asyncByTool = map[string]*asynccfg.Config{}
	}
	// system/exec
	{
		service := toolExec.New()
		if cli, err := localmcp.NewServiceClient(context.Background(), service); err == nil && cli != nil {
			r.internal[service.Name()] = cli
			r.captureInternalTimeoutsLocked(service)
			r.captureInternalRetryPolicyLocked(service)
			if ac, ok := any(service).(svc.AsyncConfigurer); ok {
				for _, cfg := range ac.AsyncConfigs() {
					cacheAsyncConfigAliases(r.asyncByTool, cfg)
				}
			}
		} else if err != nil {
			r.warnf("internal mcp for %s failed: %v", service.Name(), err)
		}
	}
	// system/async (list non-terminal async operations for the current conversation)
	{
		service := toolAsync.New()
		if cli, err := localmcp.NewServiceClient(context.Background(), service); err == nil && cli != nil {
			r.internal[service.Name()] = cli
			r.captureInternalTimeoutsLocked(service)
			r.captureInternalRetryPolicyLocked(service)
		} else if err != nil {
			r.warnf("internal mcp for %s failed: %v", service.Name(), err)
		}
	}
	// system/patch
	{
		service := toolPatch.New()
		if cli, err := localmcp.NewServiceClient(context.Background(), service); err == nil && cli != nil {
			r.internal[service.Name()] = cli
			r.captureInternalTimeoutsLocked(service)
			r.captureInternalRetryPolicyLocked(service)
			if ac, ok := any(service).(svc.AsyncConfigurer); ok {
				for _, cfg := range ac.AsyncConfigs() {
					cacheAsyncConfigAliases(r.asyncByTool, cfg)
				}
			}
		} else if err != nil {
			r.warnf("internal mcp for %s failed: %v", service.Name(), err)
		}
	}
	// system/os
	{
		service := toolOS.New()
		if cli, err := localmcp.NewServiceClient(context.Background(), service); err == nil && cli != nil {
			r.internal[service.Name()] = cli
			r.captureInternalTimeoutsLocked(service)
			r.captureInternalRetryPolicyLocked(service)
			if ac, ok := any(service).(svc.AsyncConfigurer); ok {
				for _, cfg := range ac.AsyncConfigs() {
					cacheAsyncConfigAliases(r.asyncByTool, cfg)
				}
			}
		} else if err != nil {
			r.warnf("internal mcp for %s failed: %v", service.Name(), err)
		}
	}
	// system/image
	{
		service := toolImage.New()
		if cli, err := localmcp.NewServiceClient(context.Background(), service); err == nil && cli != nil {
			r.internal[service.Name()] = cli
			r.captureInternalTimeoutsLocked(service)
			r.captureInternalRetryPolicyLocked(service)
			if ac, ok := any(service).(svc.AsyncConfigurer); ok {
				for _, cfg := range ac.AsyncConfigs() {
					cacheAsyncConfigAliases(r.asyncByTool, cfg)
				}
			}
		} else if err != nil {
			r.warnf("internal mcp for %s failed: %v", service.Name(), err)
		}
	}
	// orchestration/plan
	{
		s := orchplan.New()
		if cli, err := localmcp.NewServiceClient(context.Background(), s); err == nil && cli != nil {
			r.internal[s.Name()] = cli
			r.captureInternalTimeoutsLocked(s)
			r.captureInternalRetryPolicyLocked(s)
		} else if err != nil {
			r.warnf("internal mcp for %s failed: %v", s.Name(), err)
		}
	}
	// scratchpad
	{
		service := toolScratchpad.New()
		if cli, err := localmcp.NewServiceClient(context.Background(), service); err == nil && cli != nil {
			r.internal[service.Name()] = cli
			r.captureInternalTimeoutsLocked(service)
			r.captureInternalRetryPolicyLocked(service)
		} else if err != nil {
			r.warnf("internal mcp for %s failed: %v", service.Name(), err)
		}
	}

}

// applyCacheableOverride sets def.Cacheable based on internal service
// annotations or MCP server config. This is the centralized application
// point — callers should not scatter this logic elsewhere.
func (r *Registry) applyCacheableOverride(def *llm.ToolDefinition, serverName string) {
	if def == nil {
		return
	}
	// Internal service annotations (CacheableProvider)
	r.mu.RLock()
	methods := r.internalCacheable[serverName]
	r.mu.RUnlock()
	r.applyCacheableOverrideWithMethods(def, serverName, methods)
}

func (r *Registry) applyCacheableOverrideWithMethods(def *llm.ToolDefinition, serverName string, methods map[string]bool) {
	if def == nil {
		return
	}
	if len(methods) > 0 {
		_, method := splitToolName(def.Name)
		if v, ok := methods[method]; ok {
			def.Cacheable = v
			return
		}
	}
	// MCP server config
	if r.mgr == nil {
		return
	}
	opts, err := r.mgr.Options(context.Background(), serverName)
	if err != nil || opts == nil || len(opts.Cacheable) == 0 {
		return
	}
	if v, ok := opts.Cacheable[def.Name]; ok {
		def.Cacheable = v
		return
	}
	canonical := mcpnames.Canonical(def.Name)
	if v, ok := opts.Cacheable[canonical]; ok {
		def.Cacheable = v
	}
}
