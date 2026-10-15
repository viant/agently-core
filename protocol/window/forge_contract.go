// Package window retains compatibility with the shared Forge window contract.
package window

import shared "github.com/viant/forge/backend/window/contract"

type (
	ComponentDispatcher         = shared.ComponentDispatcher
	DeclaredComponentDispatcher = shared.DeclaredComponentDispatcher
	CatalogInput                = shared.CatalogInput
	Group                       = shared.Group
	WindowSummary               = shared.WindowSummary
	Catalog                     = shared.Catalog
	DefinitionInput             = shared.DefinitionInput
	Backend                     = shared.Backend
	DataSource                  = shared.DataSource
	Definition                  = shared.Definition
	FetchInput                  = shared.FetchInput
	FetchOutput                 = shared.FetchOutput
	PresentationInput           = shared.PresentationInput
	ComponentBinding            = shared.ComponentBinding
)

const (
	Version        = shared.Version
	CatalogTool    = shared.CatalogTool
	DefinitionTool = shared.DefinitionTool
	FetchTool      = shared.FetchTool
)

var (
	ErrResourceBinding             = shared.ErrResourceBinding
	NewDeclaredComponentDispatcher = shared.NewDeclaredComponentDispatcher
	ValidateResourceBindings       = shared.ValidateResourceBindings
	ValidateComponentDispatch      = shared.ValidateComponentDispatch
	ValidateFetchResource          = shared.ValidateFetchResource
	MergeHostBindings              = shared.MergeHostBindings
)
