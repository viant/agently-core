# Datasource request metadata

A datasource can retain logical UI metadata while explicitly excluding it from MCP tool arguments:

```yaml
backend:
  kind: mcp_tool
  service: example
  method: ReadRows
  requestMetadata:
    - semanticSelection
```

`requestMetadata` is optional. Each string names one literal top-level key in the final transport argument map. Only listed keys are removed. Names are case-sensitive; dots, whitespace and `*` are literal characters, not paths or patterns. Duplicate or absent keys have no additional effect. An empty name can only remove an actual empty key. This is not schema-based filtering: undeclared arguments remain present and the upstream tool may reject them.

The service deep-clones JSON-shaped argument maps and slices before execution, preserving scalar types and nested scopes. Executor mutation cannot change caller inputs, pinned configuration, or logical cache-key inputs. Cache identity is computed from the complete logical request before transport exclusion (subject to the datasource's existing explicit cache-key policy); admission and authored request snapshots remain unchanged. Registry execution-protection claims receive the exact physical argument map passed to `Execute`.

For `mcp_tools` and `mcp_fanout`, the declaration applies to each final mapped tool call, including the fanout seed. Inline and feed backends do not use MCP argument exclusion. Declare only metadata that the selected tool does not consume; do not list filters, dimensions, paging or other query controls to bypass validation.

## Report-owned datasource requests

The existing `POST /v1/api/datasources/{id}/fetch` endpoint accepts a logical
report owner for an attached datasource:

```json
{"owner":{"uri":"report://example/summary"},"inputs":{"scope":0}}
```

The backend selects the current report definition and dispatches only the named
attached datasource through the existing dataset/MCP executor. The browser does
not supply a datasource definition, tool name, URL, revision, or content hash.
Caller-supplied owner revisions and conflicting standalone/window selectors are
rejected. Lookup controls and other report-owned reads share this same path;
there is no report-choice MCP operation. Only the selected source's dependencies
are loaded, and the report itself is not compiled for a datasource read.
