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

	// Cache policy. When nil, defaults apply (enabled=true, scope=user,
	// ttl=30m, refreshPolicy=stale-while-revalidate, maxEntries=5000).
	// Writers should declare cache: {enabled:false}; datasource semantics are
	// never inferred from a tool name or HTTP method.
	Cache *CachePolicy `json:"cache,omitempty" yaml:"cache,omitempty"`
}

// BackendKind enumerates the supported backend types. Adding a new kind is a
// single Source implementation + registration — nothing else in the pipeline
// changes.
type BackendKind string

const (
	BackendMCPTool     BackendKind = "mcp_tool"
	BackendMCPTools    BackendKind = "mcp_tools"
	BackendMCPFanout   BackendKind = "mcp_fanout"
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
	Service string      `json:"service,omitempty" yaml:"service,omitempty"`
	Method  string      `json:"method,omitempty" yaml:"method,omitempty"`
	Calls   []MCPCall   `json:"calls,omitempty" yaml:"calls,omitempty"`
	Fanout  *MCPFanout  `json:"fanout,omitempty" yaml:"fanout,omitempty"`
	Sort    []SortField `json:"sort,omitempty" yaml:"sort,omitempty"`

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

// MCPCall describes one call in a fixed MCP composite or a fanout stage.
type MCPCall struct {
	ID             string                            `json:"id,omitempty" yaml:"id,omitempty"`
	Service        string                            `json:"service,omitempty" yaml:"service,omitempty"`
	Method         string                            `json:"method,omitempty" yaml:"method,omitempty"`
	Args           map[string]string                 `json:"args,omitempty" yaml:"args,omitempty"`
	Pinned         map[string]interface{}            `json:"pinned,omitempty" yaml:"pinned,omitempty"`
	FieldMap       map[string]string                 `json:"fieldMap,omitempty" yaml:"fieldMap,omitempty"`
	Constants      map[string]interface{}            `json:"constants,omitempty" yaml:"constants,omitempty"`
	ValueMaps      map[string]map[string]interface{} `json:"valueMaps,omitempty" yaml:"valueMaps,omitempty"`
	IgnoreNotFound bool                              `json:"ignoreNotFound,omitempty" yaml:"ignoreNotFound,omitempty"`
}

type SortField struct {
	Field     string `json:"field,omitempty" yaml:"field,omitempty"`
	Direction string `json:"direction,omitempty" yaml:"direction,omitempty"`
}

type ValueSet struct {
	Selector  string                 `json:"selector,omitempty" yaml:"selector,omitempty"`
	Constants map[string]interface{} `json:"constants,omitempty" yaml:"constants,omitempty"`
}

type ListArgument struct {
	Target string            `json:"target,omitempty" yaml:"target,omitempty"`
	Fields map[string]string `json:"fields,omitempty" yaml:"fields,omitempty"`
}

// MCPFanout runs Seed once, expands ValueSets from ItemsSelector, and invokes
// Call once per seed item. Selector prefixes are inputs, item, selection, and result.
type MCPFanout struct {
	Seed                 MCPCall           `json:"seed" yaml:"seed"`
	ItemsSelector        string            `json:"itemsSelector,omitempty" yaml:"itemsSelector,omitempty"`
	Call                 MCPCall           `json:"call" yaml:"call"`
	ValueSets            []ValueSet        `json:"valueSets,omitempty" yaml:"valueSets,omitempty"`
	ListArgument         *ListArgument     `json:"listArgument,omitempty" yaml:"listArgument,omitempty"`
	ResultMap            string            `json:"resultMap,omitempty" yaml:"resultMap,omitempty"`
	ResultKey            string            `json:"resultKey,omitempty" yaml:"resultKey,omitempty"`
	IncludeUnresolved    bool              `json:"includeUnresolved,omitempty" yaml:"includeUnresolved,omitempty"`
	UnmappedAsUnresolved bool              `json:"unmappedAsUnresolved,omitempty" yaml:"unmappedAsUnresolved,omitempty"`
	FieldMap             map[string]string `json:"fieldMap,omitempty" yaml:"fieldMap,omitempty"`
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
// default via CachePolicyOrDefault. Enabled=false bypasses get/put and makes
// invalidation a no-op.
type CachePolicy struct {
	Enabled       *bool         `json:"enabled,omitempty" yaml:"enabled,omitempty"`
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
	enabled := true
	out := CachePolicy{
		Enabled:       &enabled,
		Scope:         ScopeUser,
		TTL:           DefaultTTL,
		MaxEntries:    DefaultMaxEntries,
		RefreshPolicy: RefreshStaleWhileRevalidate,
	}
	if p == nil {
		return out
	}
	if p.Enabled != nil {
		value := *p.Enabled
		out.Enabled = &value
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
	Metrics  map[string]interface{}   `json:"metrics,omitempty"`
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
