package primitive

import (
	"encoding/json"

	shared "github.com/viant/forge/backend/provider/contract"
)

// Existing Agently imports share Forge's contract and generic catalog types.
type (
	NamespaceListRequest     = shared.NamespaceListRequest
	NamespaceGetRequest      = shared.NamespaceGetRequest
	NamespaceMetadata        = shared.NamespaceMetadata
	Namespace                = shared.Namespace
	KindSupport              = shared.KindSupport
	NamespaceCapabilities    = shared.NamespaceCapabilities
	NamespaceListResult      = shared.NamespaceListResult
	ListRequest              = shared.ListRequest
	GetRequest               = shared.GetRequest
	Locator                  = shared.Locator
	ResourceState            = shared.ResourceState
	Dependency               = shared.Dependency
	MenuProjection           = shared.MenuProjection
	GetResult                = shared.GetResult
	ExecutionProof           = shared.ExecutionProof
	DataSourceReference      = shared.DataSourceReference
	ListResult               = shared.ListResult
	CatalogQuery             = shared.CatalogQuery
	CatalogPage[T any]       = shared.CatalogPage[T]
	CatalogItem[T any]       = shared.CatalogItem[T]
	Catalog[T any]           = shared.Catalog[T]
	CreateResourceInput      = shared.CreateResourceInput
	UpdateDraftInput         = shared.UpdateDraftInput
	StampResourceInput       = shared.StampResourceInput
	CloneResourceInput       = shared.CloneResourceInput
	ArchiveResourceInput     = shared.ArchiveResourceInput
	ResourceResult           = shared.ResourceResult
	ResourceDescriptor       = shared.ResourceDescriptor
	ResourceDependencyPin    = shared.ResourceDependencyPin
	ResourceAuthoring        = shared.ResourceAuthoring
	ResourceAuthoringFactory = shared.ResourceAuthoringFactory
	MCPAppClosure            = shared.MCPAppClosure
	MCPAppOpenRequest        = shared.MCPAppOpenRequest
	MCPAppOpenResult         = shared.MCPAppOpenResult
	ToolfeedDefinition       = shared.ToolfeedDefinition
	ToolfeedPresentation     = shared.ToolfeedPresentation
	ToolfeedMatch            = shared.ToolfeedMatch
	ToolfeedActivation       = shared.ToolfeedActivation
)

const (
	AuthoringExtension           = shared.AuthoringExtension
	NamespaceMeta                = shared.NamespaceMeta
	RevisionMeta                 = shared.RevisionMeta
	AuthoringResourcesMeta       = shared.AuthoringResourcesMeta
	ResourcePinsMeta             = shared.ResourcePinsMeta
	LegacyAuthoringExtension     = shared.LegacyAuthoringExtension
	LegacyNamespaceMeta          = shared.LegacyNamespaceMeta
	LegacyRevisionMeta           = shared.LegacyRevisionMeta
	LegacyAuthoringResourcesMeta = shared.LegacyAuthoringResourcesMeta
	LegacyResourcePinsMeta       = shared.LegacyResourcePinsMeta
	MaxCatalogPageSize           = shared.MaxCatalogPageSize
	ToolfeedKind                 = shared.ToolfeedKind
	ToolfeedFormatVersion        = shared.ToolfeedFormatVersion
	ToolfeedSchemaURI            = shared.ToolfeedSchemaURI
)

var ErrCatalogCursor = shared.ErrCatalogCursor

func Plural(kind string) string { return shared.Plural(kind) }
func Kind(plural string) string { return shared.Kind(plural) }
func Methods(kind string, operations []string) map[string]string {
	return shared.Methods(kind, operations)
}
func DecodeList(raw []byte, kind string) (ListResult, error) { return shared.DecodeList(raw, kind) }
func NewCatalog[T any](snapshot string, items []CatalogItem[T]) *Catalog[T] {
	return shared.NewCatalog(snapshot, items)
}
func DecodeToolfeedDefinition(version int64, raw json.RawMessage) (*ToolfeedDefinition, error) {
	return shared.DecodeToolfeedDefinition(version, raw)
}
