// Package datasource retains compatibility with shared Forge datasource contracts.
package datasource

import shared "github.com/viant/forge/backend/datasource/contract"

type (
	Operation                  = shared.Operation
	OperateResult              = shared.OperateResult
	DataSource                 = shared.DataSource
	BackendKind                = shared.BackendKind
	Backend                    = shared.Backend
	MCPRequestBinding          = shared.MCPRequestBinding
	MCPCall                    = shared.MCPCall
	SortField                  = shared.SortField
	ValueSet                   = shared.ValueSet
	ListArgument               = shared.ListArgument
	MCPFanout                  = shared.MCPFanout
	CacheScope                 = shared.CacheScope
	RefreshPolicy              = shared.RefreshPolicy
	CachePolicy                = shared.CachePolicy
	FetchResult                = shared.FetchResult
	CacheMeta                  = shared.CacheMeta
	InlineFilter               = shared.InlineFilter
	AuthorizationBinding       = shared.AuthorizationBinding
	UpstreamAuthorizationError = shared.UpstreamAuthorizationError
)

const (
	BackendMCPTool              = shared.BackendMCPTool
	BackendMCPTools             = shared.BackendMCPTools
	BackendMCPFanout            = shared.BackendMCPFanout
	BackendMCPResource          = shared.BackendMCPResource
	BackendFeedRef              = shared.BackendFeedRef
	BackendInline               = shared.BackendInline
	BackendAuthorization        = shared.BackendAuthorization
	BackendDatly                = shared.BackendDatly
	ScopeUser                   = shared.ScopeUser
	ScopeConversation           = shared.ScopeConversation
	ScopeGlobal                 = shared.ScopeGlobal
	RefreshStaleWhileRevalidate = shared.RefreshStaleWhileRevalidate
	RefreshOnMiss               = shared.RefreshOnMiss
	RefreshNone                 = shared.RefreshNone
	DefaultTTL                  = shared.DefaultTTL
	DefaultMaxEntries           = shared.DefaultMaxEntries
)

var (
	CachePolicyOrDefault = shared.CachePolicyOrDefault
	ErrDatasourceDenied  = shared.ErrDatasourceDenied
)
