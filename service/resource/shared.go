// Package resource preserves Core imports while delegating to the shared runtime.
package resource

import (
	context "context"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	shared "github.com/viant/forge/backend/resourcehost"
)

type Authoring = shared.Authoring

var NewAuthoring = shared.NewAuthoring

type CatalogQuery = shared.CatalogQuery
type NamespacePage = shared.NamespacePage

func ListPage[T any](ctx context.Context, g *Gateway, kind string, query CatalogQuery, hydrateDefinitions ...bool) (*primitive.CatalogPage[Located[T]], error) {
	return shared.ListPage[T](ctx, g, kind, query, hydrateDefinitions...)
}
func ListPageFrom[T any](ctx context.Context, g *Gateway, roots []Connection, kind string, query CatalogQuery, hydrate ...bool) (*primitive.CatalogPage[Located[T]], error) {
	return shared.ListPageFrom[T](ctx, g, roots, kind, query, hydrate...)
}
func GetFrom[T any](ctx context.Context, g *Gateway, root Connection, ref identity.ResourceRef, decode DefinitionDecoder[T]) (*Result[T], error) {
	return shared.GetFrom[T](ctx, g, root, ref, decode)
}

var DatasourceDirectory = shared.DatasourceDirectory
var NativeDatasourceLoader = shared.NativeDatasourceLoader
var ValidateDatasourceDefinition = shared.ValidateDatasourceDefinition
var DecodeDatasourceDefinition = shared.DecodeDatasourceDefinition

type DefinitionScope = shared.DefinitionScope

var WithDefinitionScope = shared.WithDefinitionScope

func ResolveDefinition[T any](ctx context.Context, g *Gateway, kind, key string, alias func(T) string, decoder ...DefinitionDecoder[T]) (*Result[T], error) {
	return shared.ResolveDefinition[T](ctx, g, kind, key, alias, decoder...)
}
func ResolveDefinitionIn[T any](ctx context.Context, g *Gateway, kind, key string, scope DefinitionScope, alias func(T) string, decoder ...DefinitionDecoder[T]) (*Result[T], error) {
	return shared.ResolveDefinitionIn[T](ctx, g, kind, key, scope, alias, decoder...)
}

var ReadSelectionCount = shared.ReadSelectionCount

const InternalProviderIdentity = shared.InternalProviderIdentity

type ExtensionLoader = shared.ExtensionLoader
type ExtensionDirectory = shared.ExtensionDirectory
type ExtensionReader = shared.ExtensionReader

var ExtensionBindings = shared.ExtensionBindings
var NewInternalProvider = shared.NewInternalProvider
var SnapshotExtensionDefinitions = shared.SnapshotExtensionDefinitions
var NativeWindowLoader = shared.NativeWindowLoader
var ConfinedWindowBindings = shared.ConfinedWindowBindings
var ErrCollision = shared.ErrCollision
var ErrUnavailable = shared.ErrUnavailable

type ActorResolver = shared.ActorResolver
type ActorVerifier = shared.ActorVerifier
type Connection = shared.Connection
type LocatedResource = shared.LocatedResource
type Gateway = shared.Gateway

var NewGateway = shared.NewGateway

type DiscoveredNamespace = shared.DiscoveredNamespace

var ResolveLocator = shared.ResolveLocator

type Result[T any] = shared.Result[T]
type Located[T any] = shared.Located[T]

func List[T any](ctx context.Context, g *Gateway, kind, namespace string) ([]Located[T], error) {
	return shared.List[T](ctx, g, kind, namespace)
}

type DefinitionDecoder[T any] = shared.DefinitionDecoder[T]

func Get[T any](ctx context.Context, g *Gateway, connection Connection, ref identity.ResourceRef, pin *identity.ResolvedResource, decoder ...DefinitionDecoder[T]) (*Result[T], error) {
	return shared.Get[T](ctx, g, connection, ref, pin, decoder...)
}

type DefinitionCodec[T any] = shared.DefinitionCodec[T]

func GetWithCodec[T any](ctx context.Context, g *Gateway, connection Connection, ref identity.ResourceRef, pin *identity.ResolvedResource, codec DefinitionCodec[T]) (*Result[T], error) {
	return shared.GetWithCodec[T](ctx, g, connection, ref, pin, codec)
}

var NewExtensionReader = shared.NewExtensionReader
var ValidExtensionPath = shared.ValidExtensionPath
var DefinitionScopeFromContext = shared.DefinitionScopeFromContext

type HostedAppBinding = shared.HostedAppBinding

const HostedAppSandbox = shared.HostedAppSandbox

type HostedAppReader = shared.HostedAppReader

var NewHostedAppReader = shared.NewHostedAppReader
var ValidateHostedAppBindings = shared.ValidateHostedAppBindings

func Invoke[T any](ctx context.Context, g *Gateway, connection Connection, uri identity.ResourceURI, operation string, input any) (T, error) {
	return shared.Invoke[T](ctx, g, connection, uri, operation, input)
}

type LocalResourceBinding = shared.LocalResourceBinding
type LocalResourceValidator = shared.LocalResourceValidator
type LocalResourceAuthorizer = shared.LocalResourceAuthorizer
type LocalRevisionPolicyFactory = shared.LocalRevisionPolicyFactory
type LocalWindowListVisibility = shared.LocalWindowListVisibility
type LocalWindowIndex = shared.LocalWindowIndex
type LocalConfig = shared.LocalConfig
type LocalProvider = shared.LocalProvider

var NewLocalProvider = shared.NewLocalProvider
var NewLocalMCPHandler = shared.NewLocalMCPHandler
var NewMCPHandler = shared.NewMCPHandler
var WorkspaceWindowBindings = shared.WorkspaceWindowBindings
var ValidateWindowBundle = shared.ValidateWindowBundle
var ValidateReportEnvelope = shared.ValidateReportEnvelope
var NewLocalMCPClient = shared.NewLocalMCPClient
var LookupDirectory = shared.LookupDirectory
var NativeLookupLoader = shared.NativeLookupLoader
var DecodeLookupDefinition = shared.DecodeLookupDefinition
var ValidateLookupDefinition = shared.ValidateLookupDefinition

type LookupReader = shared.LookupReader

var NewLookupReader = shared.NewLookupReader
var MCPDirectory = shared.MCPDirectory
var NativeMCPLoader = shared.NativeMCPLoader
var DecodeMCPDefinition = shared.DecodeMCPDefinition
var ValidateMCPDefinition = shared.ValidateMCPDefinition
var MCPDefinitionYAML = shared.MCPDefinitionYAML
var MCPResourceDocuments = shared.MCPResourceDocuments

type MCPDefinitionSource = shared.MCPDefinitionSource
type MCPReferenceReader = shared.MCPReferenceReader

var NewMCPReferenceReader = shared.NewMCPReferenceReader

type MenuActionVisibility = shared.MenuActionVisibility
type MenuVisibilityProjection = shared.MenuVisibilityProjection

var ValidateMenu = shared.ValidateMenu
var NativeComposedWindowLoader = shared.NativeComposedWindowLoader
var NativeDocumentURI = shared.NativeDocumentURI

type NativeSkillSource = shared.NativeSkillSource
type NativeSnapshotDefinition = shared.NativeSnapshotDefinition
type StaticWorkspaceWindowEnricher = shared.StaticWorkspaceWindowEnricher
type NativeSnapshotOptions = shared.NativeSnapshotOptions
type NativeAssetSnapshot = shared.NativeAssetSnapshot

var SnapshotWindowDefinitions = shared.SnapshotWindowDefinitions
var ConfinedReportingOptions = shared.ConfinedReportingOptions
var NativeReportLoader = shared.NativeReportLoader
var PublicReadActors = shared.PublicReadActors
var RegisterDatasourceSelectionAdmission = shared.RegisterDatasourceSelectionAdmission

type ReadFactsScope = shared.ReadFactsScope

var ReadContext = shared.ReadContext

type ReportGatewayCatalog = shared.ReportGatewayCatalog

var ToolfeedDirectory = shared.ToolfeedDirectory
var NativeToolfeedLoader = shared.NativeToolfeedLoader
var ValidateToolfeedDefinition = shared.ValidateToolfeedDefinition
var SnapshotToolfeedDefinitions = shared.SnapshotToolfeedDefinitions

type WindowContentCheck = shared.WindowContentCheck
type WindowCatalog = shared.WindowCatalog
type CompositeWindowCatalog = shared.CompositeWindowCatalog

var ErrUnsupportedWindowTransport = shared.ErrUnsupportedWindowTransport

type WindowViewReader = shared.WindowViewReader

var NewWindowViewReader = shared.NewWindowViewReader
