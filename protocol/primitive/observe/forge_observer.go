package observe

import (
	"context"
	"net/http"

	shared "github.com/viant/forge/backend/provider/contract/observe"
)

type (
	Phase        = shared.Phase
	Outcome      = shared.Outcome
	Cache        = shared.Cache
	Labels       = shared.Labels
	Event        = shared.Event
	Observer     = shared.Observer
	ObserverFunc = shared.ObserverFunc
	Span         = shared.Span
	Collector    = shared.Collector
)

const (
	GatewayTool             = shared.GatewayTool
	GatewayRequestEncode    = shared.GatewayRequestEncode
	GatewayMCPCall          = shared.GatewayMCPCall
	GatewayResponseDecode   = shared.GatewayResponseDecode
	ProviderNamespaceList   = shared.ProviderNamespaceList
	ProviderNamespaceGet    = shared.ProviderNamespaceGet
	ProviderList            = shared.ProviderList
	ProviderGet             = shared.ProviderGet
	ProviderWrite           = shared.ProviderWrite
	SourceInventory         = shared.SourceInventory
	SourceLookup            = shared.SourceLookup
	SourceWrite             = shared.SourceWrite
	SourceMetadata          = shared.SourceMetadata
	BindingResolver         = shared.BindingResolver
	RevisionResolve         = shared.RevisionResolve
	RevisionRead            = shared.RevisionRead
	Validation              = shared.Validation
	Authorization           = shared.Authorization
	Identity                = shared.Identity
	MenuVisibility          = shared.MenuVisibility
	MCPDispatch             = shared.MCPDispatch
	Serialization           = shared.Serialization
	OK                      = shared.OK
	Error                   = shared.Error
	Denied                  = shared.Denied
	Canceled                = shared.Canceled
	CacheUnknown            = shared.CacheUnknown
	CacheHit                = shared.CacheHit
	CacheMiss               = shared.CacheMiss
	CacheBypass             = shared.CacheBypass
	HTTPRequest             = shared.HTTPRequest
	HTTPWindowResolve       = shared.HTTPWindowResolve
	HTTPWindowPermission    = shared.HTTPWindowPermission
	HTTPWindowBind          = shared.HTTPWindowBind
	HTTPResponseEncode      = shared.HTTPResponseEncode
	GatewayDefinitionDecode = shared.GatewayDefinitionDecode
)

func WithObserver(ctx context.Context, observer Observer) context.Context {
	return shared.WithObserver(ctx, observer)
}
func Enabled(ctx context.Context) bool { return shared.Enabled(ctx) }
func Start(ctx context.Context, phase Phase, labels Labels) Span {
	return shared.Start(ctx, phase, labels)
}
func OutcomeFor(err error) Outcome { return shared.OutcomeFor(err) }
func HTTP(next http.Handler, sink func(map[string]float64)) http.Handler {
	return shared.HTTP(next, sink)
}
