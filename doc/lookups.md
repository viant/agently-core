# Datasources and named lookups

A named lookup connects a form field or chat input to options from a datasource.
The registry returns the bindings available in the requested rendering context.

## Discover lookup bindings

Use `GET /v1/api/lookups/registry?context=<target-kind>:<target-id>`.
Entries describe the lookup name, datasource, optional dialog or window,
and the input and output bindings for the picker.

For forms, see [schema overlays](overlays.md) and the
[elicitation system](elicitation-system.md).

## Fetch datasource rows

Use `POST /v1/api/datasources/{id}/fetch` with the declared datasource inputs.
The response contains rows and may contain datasource, timing, or cache metadata.
For report-owned datasources, use the owner URI to identify the report definition.

The Go request and response types are in [sdk/api/datasource.go](../sdk/api/datasource.go).
See the [SDK surface](sdk.md) for the corresponding client operations.
