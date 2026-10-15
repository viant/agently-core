// Package provider preserves Core imports while delegating to the shared runtime.
package provider

import (
	shared "github.com/viant/forge/backend/resourcehost/producer"
)

type UpdateResourceRequest = shared.UpdateResourceRequest
type StampResourceRequest = shared.StampResourceRequest
type ArchiveResourceRequest = shared.ArchiveResourceRequest

var ErrCatalogNotFound = shared.ErrCatalogNotFound
var ErrCatalogCollision = shared.ErrCatalogCollision

type LocalCatalog = shared.LocalCatalog

var NewLocalMCPClient = shared.NewLocalMCPClient

type EmptyCatalog = shared.EmptyCatalog
type MenuVisibilityProjection = shared.MenuVisibilityProjection
type MenuActionVisibility = shared.MenuActionVisibility

var ValidateMenu = shared.ValidateMenu

type NativeResource = shared.NativeResource
type ActorResolver = shared.ActorResolver
type ActorVerifier = shared.ActorVerifier

var ErrUnavailable = shared.ErrUnavailable

type LocalResourceBinding = shared.LocalResourceBinding
type LocalResourceValidator = shared.LocalResourceValidator
type LocalResourceAuthorizer = shared.LocalResourceAuthorizer
type LocalRevisionPolicyFactory = shared.LocalRevisionPolicyFactory
type LocalWindowListVisibility = shared.LocalWindowListVisibility
type LocalWindowIndex = shared.LocalWindowIndex
type DefinitionValidationCache = shared.DefinitionValidationCache
type LocalConfig = shared.LocalConfig
type LocalProvider = shared.LocalProvider

var NewLocalProvider = shared.NewLocalProvider
var NewLocalMCPHandler = shared.NewLocalMCPHandler
var NewMCPServer = shared.NewMCPServer
var NewMCPHandler = shared.NewMCPHandler

type ResourceChangeSource = shared.ResourceChangeSource
type ResourceChanges = shared.ResourceChanges
