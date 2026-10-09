// Package datasource implements Fetch over a declarative protocol/datasource
// DataSource. It composes four pluggable concerns:
//
//  1. Backend   — how rows are obtained (MCP, resource, feed, or inline adapters).
//  2. Projection — forge selectors project the backend result into rows.
//  3. Cache     — per-user/conversation/global memoisation with TTL.
//  4. Identity  — carried in ctx; never a method arg.
//
// All public entry points (HTTP handler, internal MCP tool, scheduler) go
// through Fetch. There are no per-datasource or per-MCP-server code paths.
package datasource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	internalAuth "github.com/viant/agently-core/internal/auth"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	windowprotocol "github.com/viant/agently-core/protocol/window"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/forge/backend/types"
)

// ToolExecutor is the seam to invoke an MCP tool. In production this is wired
// to internal/tool/registry.Registry.Execute which dispatches under the
// caller's ctx identity. Tests can supply an in-memory stub.
type ToolExecutor interface {
	// Execute calls an MCP tool by fully qualified name "service:method"
	// with args and returns the raw JSON string result (the registry's
	// documented return type). ctx carries identity (auth token is
	// attached by the registry via WithAuthTokenContext).
	Execute(ctx context.Context, name string, args map[string]interface{}) (string, error)
}

// IdentityFunc extracts a cache-key identity (user, conversation) from ctx.
// Tests and non-HTTP callers can supply their own; the default reads well-known
// keys from ctx.
type IdentityFunc func(ctx context.Context) Identity

// Identity is the cache-key identity extracted from ctx. It is never used for
// auth decisions — auth is already attached to ctx by the time Fetch runs.
type Identity struct {
	User         string
	Conversation string
}

// Store is the workspace-scoped registry of loaded datasources.
type Store interface {
	Get(id string) (*dsproto.DataSource, bool)
}

// Service is the public entry point. Construct with New, then call Fetch.
type Service struct {
	executionContext    func(context.Context) context.Context
	permissions         permittedview.Resolver
	components          windowprotocol.ComponentDispatcher
	providerExecute     ProviderExecutor
	resolveResource     ResourceRevalidator
	authorizeDefinition DefinitionAuthorizer
	resolveDefinition   ResourceDefinitionResolver
	store               Store
	executor            ToolExecutor
	identity            IdentityFunc
	cache               *memoryCache
	feedRef             FeedRefResolver // optional — nil means feed_ref kind is unsupported in this build
	now                 func() time.Time
	authorize           func(context.Context, string, map[string]interface{}) error
	disableCache        bool
}

// FeedRefResolver resolves a feed_ref backend to its already-emitted payload.
// Implementation lives in sdk layer (feeds); service/datasource keeps the
// interface here so the core has no import cycle.
type FeedRefResolver interface {
	ResolveFeed(ctx context.Context, feedID string) (interface{}, error)
}

// Options are the construction options for New.
// DefinitionAuthorizer binds execution to a resolved resource's exact backend
// descriptor. It runs before dispatch/cache access and before returning rows.
type ResourceRevalidator func(context.Context, identity.ResolvedResource) (*identity.ResolvedResource, error)

type DefinitionAuthorizer func(context.Context, *dsproto.DataSource, map[string]interface{}) error
type ResourceDefinitionResolver func(context.Context, identity.ResolvedResource, *types.WindowTarget, string) (*dsproto.DataSource, error)

type ProviderExecutor func(context.Context, *dsproto.DataSource, map[string]interface{}) (json.RawMessage, error)

type Options struct {
	ProviderExecute     ProviderExecutor
	ExecutionContext    func(context.Context) context.Context
	ResolveDefinition   ResourceDefinitionResolver
	PermissionResolver  permittedview.Resolver
	ComponentDispatcher windowprotocol.ComponentDispatcher
	ResolveResource     ResourceRevalidator
	AuthorizeDefinition DefinitionAuthorizer
	Store               Store
	Executor            ToolExecutor
	Identity            IdentityFunc // if nil, uses defaultIdentity (reads generic ctx keys)
	FeedRef             FeedRefResolver
	Now                 func() time.Time // for tests; defaults to time.Now
	// Authorize is checked on every fetch, including cache hits. The host
	// resolves datasource identity and exact inputs from trusted mappings.
	Authorize func(context.Context, string, map[string]interface{}) error
	// DisableCache prevents a prior account's result from being reused while
	// account-bound authorization is active and no account-aware cache key exists.
	DisableCache bool
}

// New constructs a Service. Store + Executor are required; the rest are
// optional.
func New(opts Options) *Service {
	id := opts.Identity
	if id == nil {
		id = defaultIdentity
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &Service{providerExecute: opts.ProviderExecute, executionContext: opts.ExecutionContext, permissions: opts.PermissionResolver, components: opts.ComponentDispatcher, resolveResource: opts.ResolveResource, authorizeDefinition: opts.AuthorizeDefinition, resolveDefinition: opts.ResolveDefinition,
		store:        opts.Store,
		executor:     opts.Executor,
		identity:     id,
		cache:        newMemoryCache(),
		feedRef:      opts.FeedRef,
		now:          nowFn,
		authorize:    opts.Authorize,
		disableCache: opts.DisableCache,
	}
}

// FetchOptions are per-call overrides carried on the wire.
type FetchOptions struct {
	Target   *types.WindowTarget
	Resource *identity.ResolvedResource
	// BypassCache forces a fresh backend call and writes the result back
	// into cache.
	BypassCache bool

	// WriteThrough (with BypassCache=false) means: on hit, still kick off
	// a background refresh if stale-while-revalidate is set. When used as
	// a prewarm, set BypassCache=true.
	WriteThrough bool
}

// Fetch resolves the datasource by id and returns a projected result. The
// caller's identity is read from ctx by the configured IdentityFunc; auth to
// the upstream MCP server is already attached to ctx by the tool registry.
func (s *Service) Fetch(ctx context.Context, id string, inputs map[string]interface{}, opts FetchOptions) (result *dsproto.FetchResult, resultErr error) {
	if s == nil {
		return nil, fmt.Errorf("datasource service: nil receiver")
	}
	if s.executionContext != nil {
		ctx = s.executionContext(ctx)
	}
	if opts.Resource != nil {
		if s.resolveResource == nil {
			return nil, identity.ErrResourceDenied
		}
		pin, err := s.resolveResource(ctx, *opts.Resource)
		if err != nil {
			return nil, err
		}
		if pin == nil {
			return nil, identity.ErrResourceDenied
		}
		ctx = runtimerequestctx.WithResolvedResource(ctx, *pin)
	}
	if opts.Target != nil {
		if _, err := opts.Target.Normalize(); err != nil {
			return nil, err
		}
		ctx = runtimerequestctx.WithWindowTarget(ctx, opts.Target)
	}
	var ds *dsproto.DataSource
	if pin, hasPin := runtimerequestctx.ResolvedResourceFromContext(ctx); hasPin && strings.HasPrefix(pin.URI, "window://") {
		if s.resolveDefinition == nil {
			return nil, identity.ErrResourceDenied
		}
		target, _ := runtimerequestctx.WindowTargetFromContext(ctx)
		var err error
		ds, err = s.resolveDefinition(ctx, *pin, target, id)
		if err != nil {
			return nil, err
		}
		approved, err := json.Marshal(ds)
		if err != nil {
			return nil, err
		}
		defer func() {
			if resultErr == nil && result != nil {
				fresh, e := s.resolveDefinition(ctx, *pin, target, id)
				if e == nil {
					encoded, encodeErr := json.Marshal(fresh)
					e = encodeErr
					if e == nil && !bytes.Equal(approved, encoded) {
						e = identity.ErrResourceStale
					}
				}
				if e != nil {
					result = nil
					resultErr = e
				}
			}
		}()
	} else if s.store != nil {
		ds, _ = s.store.Get(id)
	}
	if ds == nil {
		return nil, fmt.Errorf("datasource %q not found", id)
	}

	if err := s.validateComponentSource(ctx, ds); err != nil {
		return nil, err
	}
	componentFinalChecked := false
	defer func() {
		if resultErr == nil && result != nil && !componentFinalChecked {
			if err := s.validateComponentSource(ctx, ds); err != nil {
				result = nil
				resultErr = err
			}
		}
	}()
	if ds.Backend != nil && ds.Backend.Kind == dsproto.BackendAuthorization {
		observation := &authorizationObservation{}
		ctx = context.WithValue(ctx, authorizationObservationKey{}, observation)
		defer func() {
			if resultErr == nil && result != nil && (ctx.Err() != nil || !observation.validUntil.After(s.now())) {
				result = nil
				resultErr = fmt.Errorf("datasource authorization expired before release")
			}
		}()
	}
	if s.authorizeDefinition != nil {
		if err := s.authorizeDefinition(ctx, ds, inputs); err != nil {
			return nil, err
		}
		defer func() {
			if resultErr == nil && result != nil {
				if err := s.authorizeDefinition(ctx, ds, inputs); err != nil {
					result = nil
					resultErr = err
				}
			}
		}()
	}
	if ds.Backend == nil {
		return nil, fmt.Errorf("datasource %q has no backend", id)
	}
	if s.authorize != nil {
		if err := s.authorize(ctx, ds.ID, inputs); err != nil {
			return nil, err
		}
	}
	aliases, err := prepareResponseAliases(ds.ResponseAliases)
	if err != nil {
		return nil, fmt.Errorf("datasource %q: %w", ds.ID, err)
	}
	policy := dsproto.CachePolicyOrDefault(ds.Cache)
	cacheEnabled := ds.Backend.Kind != dsproto.BackendAuthorization && ds.Backend.Kind != dsproto.BackendDatly && !s.disableCache && (policy.Enabled == nil || *policy.Enabled)
	scopeID := s.scopeID(ctx, policy.Scope)
	normalizedInputs := normalizeFilterSemantics(inputs, &ds.DataSource)
	mergedArgs := expandNestedArgs(mergeArgs(normalizedInputs, ds.Backend.Pinned))
	cacheKey := responseAliasCacheKey(buildCacheKey(scopeID, ds.ID, policy.Key, mergedArgs), aliases)
	if ds.Backend.Component != nil {
		raw, _ := json.Marshal(ds.Backend.Component)
		sum := sha256.Sum256(raw)
		cacheKey += "|component:" + hex.EncodeToString(sum[:])
	} else if ds.Backend.ServerVersion != "" {
		cacheKey += "|server:" + ds.Backend.ServerVersion
	}

	if cacheEnabled && !opts.BypassCache {
		if entry, ok := s.cache.get(cacheKey); ok {
			age := s.now().Sub(entry.fetchedAt)
			if age <= policy.TTL {
				res := cloneResult(entry.result)
				res.Cache = &dsproto.CacheMeta{
					Hit:        true,
					Stale:      false,
					FetchedAt:  entry.fetchedAt,
					TTLSeconds: int(policy.TTL.Seconds()),
				}
				return res, nil
			}
			// Expired. For stale-while-revalidate we could return stale
			// immediately and refresh in background; keep that for phase 4.
		}
	}

	// Miss — execute backend.
	raw, err := s.runBackend(ctx, ds, mergedArgs)
	if err != nil {
		return nil, err
	}
	rows, dataInfo, metrics := project(raw, &ds.DataSource)
	rows, err = applyResponseAliases(rows, aliases)
	if err != nil {
		return nil, fmt.Errorf("datasource %q: %w", ds.ID, err)
	}
	rows, dataInfo = applyPaging(rows, dataInfo, &ds.DataSource, mergedArgs)
	result = &dsproto.FetchResult{Rows: rows, DataInfo: dataInfo, Metrics: metrics}
	if err := s.validateComponentSource(ctx, ds); err != nil {
		return nil, err
	}
	componentFinalChecked = true

	if cacheEnabled {
		s.cache.put(cacheKey, cacheEntry{
			result:    cloneResult(result),
			fetchedAt: s.now(),
		}, policy.MaxEntries)

		result.Cache = &dsproto.CacheMeta{
			Hit:        false,
			FetchedAt:  s.now(),
			TTLSeconds: int(policy.TTL.Seconds()),
		}
	}
	return result, nil
}

// InvalidateCache drops all entries for a datasource in the caller's scope.
// When inputsHash is non-empty, only the entry matching that hash is dropped.
func (s *Service) InvalidateCache(ctx context.Context, id, inputsHash string) error {
	if s.disableCache {
		return nil
	}
	ds, ok := s.store.Get(id)
	if !ok {
		return fmt.Errorf("datasource %q not found", id)
	}
	policy := dsproto.CachePolicyOrDefault(ds.Cache)
	if policy.Enabled != nil && !*policy.Enabled {
		return nil
	}
	scopeID := s.scopeID(ctx, policy.Scope)
	prefix := scopeID + "|" + ds.ID + "|"
	if inputsHash == "" {
		s.cache.dropPrefix(prefix)
		return nil
	}
	s.cache.drop(prefix + inputsHash)
	s.cache.dropPrefix(prefix + inputsHash + "|response-aliases-v1:")
	return nil
}

func (s *Service) runBackend(ctx context.Context, ds *dsproto.DataSource, args map[string]interface{}) (interface{}, error) {
	switch ds.Backend.Kind {
	case dsproto.BackendDatly:
		if s.providerExecute == nil || ds.Backend.Ownership != "provider" {
			return nil, identity.ErrResourceDenied
		}
		raw, err := s.providerExecute(ctx, ds, args)
		if err != nil {
			return nil, err
		}
		var value interface{}
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return value, nil
	case dsproto.BackendAuthorization:
		return s.resolveAuthorization(ctx, ds.Backend, args)
	case dsproto.BackendInline:
		if ds.Backend.Rows == nil {
			return []map[string]interface{}{}, nil
		}
		rows := make([]map[string]interface{}, 0, len(ds.Backend.Rows))
		rows = append(rows, ds.Backend.Rows...)
		return filterInlineRows(rows, ds.Backend.InlineFilters, args)

	case dsproto.BackendMCPTool:
		if ds.Backend.Component != nil {
			raw, err := s.components.ExecuteComponent(ctx, ds.Backend.Service, ds.Backend.Method, *ds.Backend.Component, transportArguments(args, ds.Backend.RequestMetadata))
			if err != nil {
				return nil, err
			}
			var parsed interface{}
			if json.Unmarshal(raw, &parsed) != nil {
				return nil, fmt.Errorf("component producer returned invalid JSON")
			}
			return parsed, nil
		}

		if s.executor == nil {
			return nil, fmt.Errorf("datasource %q: mcp_tool backend but no executor configured", ds.ID)
		}
		if ds.Backend.Service == "" || ds.Backend.Method == "" {
			return nil, fmt.Errorf("datasource %q: mcp_tool backend missing service/method", ds.ID)
		}
		name := ds.Backend.Service + ":" + ds.Backend.Method
		raw, err := s.executor.Execute(ctx, name, transportArguments(args, ds.Backend.RequestMetadata))
		if err != nil {
			return nil, err
		}
		var parsed interface{}
		if raw == "" {
			return map[string]interface{}{}, nil
		}
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			// Tool returned a non-JSON string; surface as a single-field row.
			return map[string]interface{}{"text": raw}, nil
		}
		return parsed, nil

	case dsproto.BackendMCPTools:
		return s.runMCPTools(ctx, ds.Backend, args)

	case dsproto.BackendMCPFanout:
		return s.runMCPFanout(ctx, ds.Backend, args)

	case dsproto.BackendFeedRef:
		if s.feedRef == nil {
			return nil, fmt.Errorf("datasource %q: feed_ref backend but no resolver configured", ds.ID)
		}
		return s.feedRef.ResolveFeed(ctx, ds.Backend.Feed)

	case dsproto.BackendMCPResource:
		// v1 stub — parity with feed_ref; wire to resources/read later.
		return nil, fmt.Errorf("datasource %q: mcp_resource backend not yet implemented", ds.ID)

	default:
		return nil, fmt.Errorf("datasource %q: unknown backend kind %q", ds.ID, ds.Backend.Kind)
	}
}

func (s *Service) scopeID(ctx context.Context, scope dsproto.CacheScope) string {
	id := s.identity(ctx)
	switch scope {
	case dsproto.ScopeUser:
		if id.User == "" {
			return "u:anonymous"
		}
		return "u:" + id.User
	case dsproto.ScopeConversation:
		if id.Conversation == "" {
			return "c:-"
		}
		return "c:" + id.Conversation
	case dsproto.ScopeGlobal:
		return "g"
	default:
		// Default to user scope for unknown values — safer than global.
		if id.User == "" {
			return "u:anonymous"
		}
		return "u:" + id.User
	}
}

// mergeArgs combines caller inputs with pinned args. Pinned wins on conflict.
func mergeArgs(caller, pinned map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(caller)+len(pinned))
	for k, v := range caller {
		out[k] = v
	}
	for k, v := range pinned {
		out[k] = v
	}
	return out
}

func expandNestedArgs(args map[string]interface{}) map[string]interface{} {
	if len(args) == 0 {
		return args
	}
	out := map[string]interface{}{}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		leftDepth := strings.Count(keys[i], ".")
		rightDepth := strings.Count(keys[j], ".")
		if leftDepth == rightDepth {
			return keys[i] < keys[j]
		}
		return leftDepth < rightDepth
	})
	for _, key := range keys {
		value := args[key]
		assignNestedArg(out, key, value)
	}
	return out
}

func assignNestedArg(target map[string]interface{}, key string, value interface{}) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	parts := strings.Split(key, ".")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" {
			return
		}
	}
	assignNestedPath(target, parts, value)
}

func assignNestedPath(current interface{}, parts []string, value interface{}) interface{} {
	if len(parts) == 0 {
		return value
	}
	part := parts[0]
	if index, ok := numericPathIndex(part); ok {
		items, _ := current.([]interface{})
		for len(items) <= index {
			items = append(items, nil)
		}
		items[index] = assignNestedPath(items[index], parts[1:], value)
		return items
	}
	holder, _ := current.(map[string]interface{})
	if holder == nil {
		holder = map[string]interface{}{}
	}
	holder[part] = assignNestedPath(holder[part], parts[1:], value)
	return holder
}

func numericPathIndex(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	index := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
		index = index*10 + int(char-'0')
	}
	return index, true
}

// buildCacheKey produces a stable, hashed cache key. When cacheKeyPaths is
// non-empty, only those args participate; otherwise all args do. The returned
// key is prefixed by scopeID|dsID so InvalidateCache can drop whole ranges.
//
// Paths use dotted selectors to support YAML like `key: [args.q, args.parent]`.
// Interpretation:
//
//   - A leading "args." prefix is the documented forge convention for the
//     args domain; it is stripped before walking the merged args map.
//     This matches the doc/lookups.md example verbatim.
//   - The remaining path is dot-walked into nested map[string]interface{}
//     / map[string]any values (e.g. "a.b" reads args["a"]["b"]).
//   - When a path fails to resolve, the key slot gets nil — which still
//     differentiates it from another path that did resolve, because the
//     original path string is the map key in the serialized payload.
func buildCacheKey(scopeID, dsID string, cacheKeyPaths []string, args map[string]interface{}) string {
	picked := args
	if len(cacheKeyPaths) > 0 {
		picked = make(map[string]interface{}, len(cacheKeyPaths))
		for _, p := range cacheKeyPaths {
			picked[p] = selectKeyPath(p, args)
		}
	}
	payload, _ := json.Marshal(picked)
	sum := sha256.Sum256(payload)
	return scopeID + "|" + dsID + "|" + hex.EncodeToString(sum[:])
}

// selectKeyPath walks a dotted cache-key path against the merged args map.
// Matches the "args.q" convention used in doc/lookups.md plus any nested dotted
// path. Returns nil when the path fails to resolve.
func selectKeyPath(path string, args map[string]interface{}) interface{} {
	// Strip the forge "args." namespace prefix when present; everything
	// we cache is already in the merged args domain.
	norm := path
	if strings.HasPrefix(norm, "args.") {
		norm = norm[len("args."):]
	}
	if norm == "" {
		return nil
	}
	var cur interface{} = args
	for _, seg := range strings.Split(norm, ".") {
		switch m := cur.(type) {
		case map[string]interface{}:
			v, ok := m[seg]
			if !ok {
				return nil
			}
			cur = v
		default:
			return nil
		}
	}
	return cur
}

// defaultIdentity reads two common ctx keys. Callers can override.
type identityKey string

const (
	CtxUserKey         identityKey = "agently.identity.user"
	CtxConversationKey identityKey = "agently.identity.conversation"
)

// WithIdentity returns ctx with explicit user + conversation identifiers.
// Useful from HTTP handlers and tests that don't use the full runtime
// auth stack.
func WithIdentity(ctx context.Context, user, conversation string) context.Context {
	if user != "" {
		ctx = context.WithValue(ctx, CtxUserKey, user)
	}
	if conversation != "" {
		ctx = context.WithValue(ctx, CtxConversationKey, conversation)
	}
	return ctx
}

func defaultIdentity(ctx context.Context) Identity {
	id := Identity{}
	if v, ok := ctx.Value(CtxUserKey).(string); ok {
		id.User = v
	}
	if v, ok := ctx.Value(CtxConversationKey).(string); ok {
		id.Conversation = v
	}
	if id.User == "" {
		id.User = strings.TrimSpace(internalAuth.EffectiveUserID(ctx))
	}
	if id.Conversation == "" {
		id.Conversation = strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	}
	return id
}

func cloneResult(r *dsproto.FetchResult) *dsproto.FetchResult {
	if r == nil {
		return nil
	}
	out := &dsproto.FetchResult{}
	if r.Rows != nil {
		out.Rows = make([]map[string]interface{}, len(r.Rows))
		for i, row := range r.Rows {
			cp := make(map[string]interface{}, len(row))
			for k, v := range row {
				cp[k] = v
			}
			out.Rows[i] = cp
		}
	}
	if r.DataInfo != nil {
		cp := make(map[string]interface{}, len(r.DataInfo))
		for k, v := range r.DataInfo {
			cp[k] = v
		}
		out.DataInfo = cp
	}
	if r.Metrics != nil {
		cp := make(map[string]interface{}, len(r.Metrics))
		for k, v := range r.Metrics {
			cp[k] = v
		}
		out.Metrics = cp
	}
	return out
}

// memoryCache is a simple map-backed store with coarse LRU eviction.
type memoryCache struct {
	mu  sync.Mutex
	m   map[string]cacheEntry
	ord []string
}

type cacheEntry struct {
	result    *dsproto.FetchResult
	fetchedAt time.Time
}

func newMemoryCache() *memoryCache {
	return &memoryCache{m: make(map[string]cacheEntry)}
}

func (c *memoryCache) get(k string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	return e, ok
}

func (c *memoryCache) put(k string, e cacheEntry, maxEntries int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[k]; !ok {
		c.ord = append(c.ord, k)
	}
	c.m[k] = e
	if maxEntries > 0 {
		for len(c.ord) > maxEntries {
			evict := c.ord[0]
			c.ord = c.ord[1:]
			delete(c.m, evict)
		}
	}
}

func (c *memoryCache) drop(k string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
	for i, v := range c.ord {
		if v == k {
			c.ord = append(c.ord[:i], c.ord[i+1:]...)
			break
		}
	}
}

func (c *memoryCache) dropPrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := c.ord[:0]
	for _, k := range c.ord {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.m, k)
			continue
		}
		keep = append(keep, k)
	}
	c.ord = keep
}
