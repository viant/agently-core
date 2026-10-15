// Package catalog preserves Core imports while delegating to the shared runtime.
package catalog

import (
	shared "github.com/viant/forge/backend/reporting/catalog"
)

type AuthoredReportInventory = shared.AuthoredReportInventory
type Provider = shared.Provider
type ReportCatalogCandidate = shared.ReportCatalogCandidate
type ReportCapabilities = shared.ReportCapabilities
type ReportCatalogEntry = shared.ReportCatalogEntry
type ReportCatalogQuery = shared.ReportCatalogQuery
type ReportCatalogResult = shared.ReportCatalogResult
type ReportInventory = shared.ReportInventory
type ReportInventoryFunc = shared.ReportInventoryFunc
type ReportCatalogService = shared.ReportCatalogService
type ReportResourceSummary = shared.ReportResourceSummary
type ReportResourceOptions = shared.ReportResourceOptions
type ReportResourceSource = shared.ReportResourceSource

var NewReportResourceSource = shared.NewReportResourceSource
