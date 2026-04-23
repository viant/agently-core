// Package datasource defines the workspace DataSource — a forge types.DataSource
// extended with an MCP-backed Backend and a per-user cache policy.
//
// A DataSource declares: how to fetch rows (Backend), how to project them
// (forge Selectors/Paging/UniqueKey embedded), and how to cache them (Cache).
// The service/datasource package consumes these declarations; the framework
// knows nothing about what any particular datasource "means".
package datasource

import (
	"time"

	"github.com/viant/forge/backend/types"
)

// DataSource is a workspace resource (extension/forge/datasources/*.yaml).
// It embeds the forge DataSource model and adds two sections: Backend (how
// rows are fetched) and Cache (how results are memoised per-user).
type DataSource struct {
	// Embedded forge DataSource carries Selectors, Parameters, Paging,
	// FilterSet, UniqueKey, Cardinality, SelectionMode, etc.
	types.DataSource `json:",inline" yaml:",inline"`

	// ID uniquely identifies this datasource within a workspace. It is
	// also the URL path segment used by the HTTP endpoint
	// /v1/api/datasources/{id}/fetch.
	ID string `json:"id" yaml:"id"`

	// Title is a human-readable label shown in admin UIs. Optional.
	Title string `json:"title,omitempty" yaml:"title,omitempty"`

	// Backend describes the upstream source. Required.
	Backend *Backend `json:"backend" yaml:"backend"`

	// Cache policy. When nil, defaults apply (scope=user, ttl=30m,
	// refreshPolicy=stale-while-revalidate, maxEntries=5000).
	Cache *CachePolicy `json:"cache,omitempty" yaml:"cache,omitempty"`
}

// BackendKind enumerates the supported backend types. Adding a new kind is a
// single Source implementation + registration — nothing else in the pipeline
// changes.
type BackendKind string

const (
	BackendMCPTool     BackendKind = "mcp_tool"
	BackendMCPResource BackendKind = "mcp_resource"
	BackendFeedRef     BackendKind = "feed_ref"
	BackendInline      BackendKind = "inline"
)

// Backend declares the upstream source for a DataSource.
//
// Auth is intentionally NOT a field here. The caller's identity flows through
// context.Context to the existing MCP tool-call path
// (internal/tool/registry Registry.Execute → protocol/mcp/proxy CallTool,
// with token attached by protocol/mcp/manager WithAuthTokenContext).
// service/datasource.Fetch preserves ctx by construction.
type Backend struct {
	Kind BackendKind `json:"kind" yaml:"kind"`

	// mcp_tool
	Service string `json:"service,omitempty" yaml:"service,omitempty"`
	Method  string `json:"method,omitempty" yaml:"method,omitempty"`

	// mcp_resource
	URI string `json:"uri,omitempty" yaml:"uri,omitempty"`

	// feed_ref
	Feed string `json:"feed,omitempty" yaml:"feed,omitempty"`

	// inline
	Rows []map[string]interface{} `json:"rows,omitempty" yaml:"rows,omitempty"`

	// Pinned args — fixed inputs the workspace author sets. On merge with
	// caller-supplied inputs, Pinned wins on conflict.
	Pinned map[string]interface{} `json:"pinned,omitempty" yaml:"pinned,omitempty"`
}

// CacheScope keys the cache to a user, a conversation, or shares globally.
// This is a cache-key concern only. It is not an auth policy; auth flows via
// context.Context.
type CacheScope string

const (
	ScopeUser         CacheScope = "user"
	ScopeConversation CacheScope = "conversation"
	ScopeGlobal       CacheScope = "global"
)

// RefreshPolicy controls how stale entries are served.
type RefreshPolicy string

const (
	RefreshStaleWhileRevalidate RefreshPolicy = "stale-while-revalidate"
	RefreshOnMiss               RefreshPolicy = "refresh-on-miss"
	RefreshNone                 RefreshPolicy = "no-refresh"
)

// CachePolicy is per-datasource. Any omitted field picks up the documented
// default via CachePolicyOrDefault.
type CachePolicy struct {
	Scope         CacheScope    `json:"scope,omitempty" yaml:"scope,omitempty"`
	TTL           time.Duration `json:"ttl,omitempty" yaml:"ttl,omitempty"`
	MaxEntries    int           `json:"maxEntries,omitempty" yaml:"maxEntries,omitempty"`
	Key           []string      `json:"key,omitempty" yaml:"key,omitempty"`
	RefreshPolicy RefreshPolicy `json:"refreshPolicy,omitempty" yaml:"refreshPolicy,omitempty"`
}

// Defaults used when CachePolicy is nil or has zero-value fields.
const (
	DefaultTTL        = 30 * time.Minute
	DefaultMaxEntries = 5000
)

// CachePolicyOrDefault returns p filled in with defaults for zero-valued fields.
func CachePolicyOrDefault(p *CachePolicy) CachePolicy {
	out := CachePolicy{
		Scope:         ScopeUser,
		TTL:           DefaultTTL,
		MaxEntries:    DefaultMaxEntries,
		RefreshPolicy: RefreshStaleWhileRevalidate,
	}
	if p == nil {
		return out
	}
	if p.Scope != "" {
		out.Scope = p.Scope
	}
	if p.TTL > 0 {
		out.TTL = p.TTL
	}
	if p.MaxEntries > 0 {
		out.MaxEntries = p.MaxEntries
	}
	if p.RefreshPolicy != "" {
		out.RefreshPolicy = p.RefreshPolicy
	}
	if len(p.Key) > 0 {
		out.Key = append([]string{}, p.Key...)
	}
	return out
}

// FetchResult is returned by service/datasource.Fetch — the already-projected
// forge payload that Item.Lookup dialogs consume.
type FetchResult struct {
	Rows     []map[string]interface{} `json:"rows"`
	DataInfo map[string]interface{}   `json:"dataInfo,omitempty"`
	Cache    *CacheMeta               `json:"cache,omitempty"`
}

// CacheMeta carries provenance on a FetchResult so clients can render
// "from cache / just fetched / stale".
type CacheMeta struct {
	Hit        bool      `json:"hit"`
	Stale      bool      `json:"stale,omitempty"`
	FetchedAt  time.Time `json:"fetchedAt"`
	TTLSeconds int       `json:"ttlSeconds,omitempty"`
}
