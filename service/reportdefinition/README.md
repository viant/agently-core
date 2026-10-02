# Agently report-definition compiler

This package compiles `reporting.viant.ai/v1alpha1` `ReportDefinition` YAML into
Forge `report-document-v1` source and canonical `ReportDocument`, `ReportSpec`,
`ReportFill`, and `ReportPrint` JSON. It accepts the public authoring portion of
`agently/preview/report/examples/demo/report.yaml`. A legacy `x-mock-preview`
section, if present, is ignored.

```go
definition, err := reportdefinition.Parse(yamlBytes)
if err != nil { return err }
compiled, err := reportdefinition.Compile(ctx, definition, parameters, fetcher)
if err != nil { return err }
// compiled.ReportDocument, ReportSpec, ReportFill, and ReportPrint are Forge JSON.
```

Implement `DatasetFetcher.FetchDataset` in the calling application. Each
`FetchRequest` contains the declared endpoint and data source, dataset ID, and
validated query (including parameter-binding predicates). The fetcher returns
object rows keyed by projected output field names and reports `HasMore` for
offset pagination. The compiler drains paged datasets, validates returned rows,
and fails on incomplete results. It never connects to an endpoint or interprets
the endpoint as a tool name.

For a complete report with a per-dataset fetch bound, use `CompileBounded`:

```go
compiled, err := reportdefinition.CompileBounded(ctx, definition, parameters, fetcher, 1000)
if err != nil { return err }
// All datasets are complete; ReportSpec, ReportFill, and ReportPrint are available.
```

`CompileBounded` accepts a cap from 1 to 10,000, sends an explicit offset page
on every fetch, and never requests more than the remaining cap. The fetcher
must set `HasMoreKnown = true` on every response, including a final response
with `HasMore = false`. If more rows exist at the cap, completeness is unknown,
or a fetcher exceeds the requested page size, it returns an error and no Forge
artifacts. It preserves authored page sizes and starting offsets. `Compile`
retains its existing fetch contract for callers that do not need this bound.

For a bounded, renderable preview, call `CompilePreview` with a positive row cap
per dataset (AI Studio uses `200`):

```go
preview, err := reportdefinition.CompilePreview(ctx, definition, parameters, fetcher, 200)
if err != nil { return err }
// Render preview.ReportDocument, preview.ReportSpec, and preview.ReportFill.
// Inspect preview.Complete and preview.Datasets for completeness per dataset.
```

Preview always sends an explicit page to the fetcher. Its limit is the smaller
of the authored page size, the remaining row cap, and Forge's page bound; the
authored offset is preserved. It stops at the cap and never requests more than
the cap from any dataset. A fetcher must set `FetchResult.HasMoreKnown = true`
when it has an explicit `HasMore` value, including `false` at the end. A missing
signal is marked `completenessUnknown`; `HasMore = true` at the cap is marked
`rowCap`. Preview reports this in `preview.Datasets` and in each ReportFill
dataset's `provenance.truncated` flag. `PreviewResult` has no `ReportPrint` and
is not an export result. Use full `Compile` for complete ReportPrint export.

An application using Agently's MCP datasource service can declare exact
`backend.mcpRequest` paths in its workspace datasource: `queryPath` for the
authored query, optional `dataSourcePath`, `hasMorePath` in projected `dataInfo`
or `completeResult: true`, and optional `authContextPath` for a server-verified
authorization context. These mappings are server-owned; authored endpoint URLs
must not select an outbound destination or credential.

The compiler supports the preview grammar's `kpiGroup`, `table`, and `chart`
blocks. Dataset execution, authentication, and result-contract decoding belong
to the injected fetcher. Export preferences in the YAML are accepted but do not
select an export format; the caller chooses that when exporting Forge artifacts.
Forge's current fence limits constrain a report to 32 datasets, 100 lowered
blocks, and 10,000 rows per dataset. An authored page limit is at most 1,000.
