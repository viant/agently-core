// Package datasource implements Fetch over a declarative protocol/datasource
// DataSource. It composes four pluggable concerns:
//
//  1. Backend   — how rows are obtained (mcp_tool | mcp_resource | feed_ref | inline).
//  2. Projection — forge selectors project the backend result into rows.
//  3. Cache     — per-user/conversation/global memoisation with TTL.
//  4. Identity  — carried in ctx; never a method arg.
//
// All public entry points (HTTP handler, internal MCP tool, scheduler) go
// through Fetch. There are no per-datasource or per-MCP-server code paths.
package datasource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	dsproto "github.com/viant/agently-core/protocol/datasource"
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
	store    Store
	executor ToolExecutor
	identity IdentityFunc
	cache    *memoryCache
	feedRef  FeedRefResolver // optional — nil means feed_ref kind is unsupported in this build
	now      func() time.Time
}

// FeedRefResolver resolves a feed_ref backend to its already-emitted payload.
// Implementation lives in sdk layer (feeds); service/datasource keeps the
// interface here so the core has no import cycle.
type FeedRefResolver interface {
	ResolveFeed(ctx context.Context, feedID string) (interface{}, error)
}

// Options are the construction options for New.
type Options struct {
	Store    Store
	Executor ToolExecutor
	Identity IdentityFunc // if nil, uses defaultIdentity (reads generic ctx keys)
	FeedRef  FeedRefResolver
	Now      func() time.Time // for tests; defaults to time.Now
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
	return &Service{
		store:    opts.Store,
		executor: opts.Executor,
		identity: id,
		cache:    newMemoryCache(),
		feedRef:  opts.FeedRef,
		now:      nowFn,
	}
}

// FetchOptions are per-call overrides carried on the wire.
type FetchOptions struct {
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
func (s *Service) Fetch(ctx context.Context, id string, inputs map[string]interface{}, opts FetchOptions) (*dsproto.FetchResult, error) {
	if s == nil {
		return nil, fmt.Errorf("datasource service: nil receiver")
	}
	ds, ok := s.store.Get(id)
	if !ok {
		return nil, fmt.Errorf("datasource %q not found", id)
	}
	if ds.Backend == nil {
		return nil, fmt.Errorf("datasource %q has no backend", id)
	}

	policy := dsproto.CachePolicyOrDefault(ds.Cache)
	scopeID := s.scopeID(ctx, policy.Scope)
	mergedArgs := mergeArgs(inputs, ds.Backend.Pinned)
	cacheKey := buildCacheKey(scopeID, ds.ID, policy.Key, mergedArgs)

	if !opts.BypassCache {
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
	rows, dataInfo := project(raw, &ds.DataSource)
	result := &dsproto.FetchResult{Rows: rows, DataInfo: dataInfo}

	s.cache.put(cacheKey, cacheEntry{
		result:    cloneResult(result),
		fetchedAt: s.now(),
	}, policy.MaxEntries)

	result.Cache = &dsproto.CacheMeta{
		Hit:        false,
		FetchedAt:  s.now(),
		TTLSeconds: int(policy.TTL.Seconds()),
	}
	return result, nil
}

// InvalidateCache drops all entries for a datasource in the caller's scope.
// When inputsHash is non-empty, only the entry matching that hash is dropped.
func (s *Service) InvalidateCache(ctx context.Context, id, inputsHash string) error {
	ds, ok := s.store.Get(id)
	if !ok {
		return fmt.Errorf("datasource %q not found", id)
	}
	policy := dsproto.CachePolicyOrDefault(ds.Cache)
	scopeID := s.scopeID(ctx, policy.Scope)
	prefix := scopeID + "|" + ds.ID + "|"
	if inputsHash == "" {
		s.cache.dropPrefix(prefix)
		return nil
	}
	s.cache.drop(prefix + inputsHash)
	return nil
}

func (s *Service) runBackend(ctx context.Context, ds *dsproto.DataSource, args map[string]interface{}) (interface{}, error) {
	switch ds.Backend.Kind {
	case dsproto.BackendInline:
		if ds.Backend.Rows == nil {
			return []map[string]interface{}{}, nil
		}
		rows := make([]map[string]interface{}, 0, len(ds.Backend.Rows))
		rows = append(rows, ds.Backend.Rows...)
		return rows, nil

	case dsproto.BackendMCPTool:
		if s.executor == nil {
			return nil, fmt.Errorf("datasource %q: mcp_tool backend but no executor configured", ds.ID)
		}
		if ds.Backend.Service == "" || ds.Backend.Method == "" {
			return nil, fmt.Errorf("datasource %q: mcp_tool backend missing service/method", ds.ID)
		}
		name := ds.Backend.Service + ":" + ds.Backend.Method
		raw, err := s.executor.Execute(ctx, name, args)
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
