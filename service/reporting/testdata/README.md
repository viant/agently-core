# Synthetic authored reporting contract

`synthetic_authored_report.json` is independently constructed by the standalone
`scripts/generate-synthetic-report-fixture.mjs` generator. It has six neutral
datasources and eight content-fingerprinted dependencies, using the public
`mcp_tool` backend contract. All source names, dimensions, measures, report blocks,
and sample values are invented. Generation does not read retired definitions,
captured responses, application checkouts, or network resources.

Regenerate with `node scripts/generate-synthetic-report-fixture.mjs` from the
repository root. Use `--check` to verify reproducibility without writing files.
The fixture exercises the typed compiler, datasource runner, resource identity,
dependency revalidation, and export paths through the regular public contracts.
