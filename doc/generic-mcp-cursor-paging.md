# Generic MCP cursor paging

Forge windows can opt into cursor paging through their existing datasource block.
No producer metadata or Datly conventions are required. Missing/empty `mode`
retains legacy numeric paging and serialization.

```yaml
selectors:
  data: reply.widgets
  dataInfo: nav
paging:
  enabled: true
  mode: cursor
  size: 25
  parameters:
    page: resumeToken
    size: batchSize
  dataInfoSelectors:
    hasMore: remaining
    nextCursor: nextToken
backend:
  kind: mcp_tool
  service: arbitrary
  method: readWidgets
```

`parameters.page` is the authored opaque token argument in cursor mode. The first
request omits it. The size argument is sent only when its name is authored.
Continuation selectors are relative to projected datasource `dataInfo` and must
produce a boolean `hasMore`; a next token is required only when more is true.
Go report execution preserves text JSON numeric/object cursors exactly through the verified 2025 HTTP client codec, including large integers. The current MCP library converts structuredContent numbers through float64; all float-decoded numeric cursor parts there fail clearly before rows/continuation dispatch. Structured string cursors and objects without decoded floating-point parts are supported. An apparent decoded integer can have rounded from a fractional source token, so its original numeric value is unverified and refused. Requests containing numbers changed by the configured July metadata or authenticated stdio codec are refused before dispatch. These are verified library codec limits, not MCP protocol requirements. Browser controls accept strings, booleans, and objects without numeric parts. All numeric cursor parts are unsupported by the current browser JSON codec and fail before dispatch: even a safe-looking decoded integer can have rounded from a fractional raw token. Numeric page/offset positions in existing paging modes are unchanged. No numeric page, offset, or limit argument is added.

The existing Table controls remember visited page tokens. Changes to parameters,
query/filter/sort, selected parent/window/target, or datasource definition reset
history. Missing/repeated continuation rejects the response before rows are
released. Stale requests cannot populate a new association. Resource-bound cache
identity includes principal, selected parent/provider/candidate/authority,
selected target, and exact datasource descriptor; it does not renew the original
lease or cached result TTL. The partition uses the trusted current user from authentication; resource-bound requests without a trusted user bypass caching. Legacy unbound cache behavior is unchanged. A user identifier alone cannot observe an external server business ACL revision. When the host/provider cannot supply current verified credential/fact/group/projection revisions and leases for an opaque scope, disable datasource caching (`cache.enabled: false`).

Explicit page/offset modes can also author HasMore. An explicit boolean false disables Next even on a full final page without counts; malformed booleans are rejected. Empty-mode count/full-page behavior stays unchanged.

Generic resource reports (`report.execute`) aggregate explicitly configured cursor,
page, or offset datasources from the initial page using the same fixed tool and
inputs. They require an authored boolean HasMore selector; cursor mode also
requires a NextCursor selector. Unknown completeness, non-initial numeric
starting position, missing/repeated tokens, no progress, and bounds return an
error without partial artifacts. Bounds are 1,000 pages, 10,000 rows, and 8 MiB
of encoded rows per dataset. Source/authority/lease checks surround every page
and final artifact release. Empty-mode reports retain their existing one-call
behavior.

The separate ReportDefinition YAML query compiler remains offset-based. It
rejects an authored cursor paging field instead of silently ignoring it. Automatic
cursor conversion for that contract is a tracked follow-up, not a supported
claim. Use the generic datasource/resource report path above for cursor tools.

Proofs include a strict real HTTP MCP tools/list + tools/call fixture through Core
SDK and report execution, plus a rendered existing PaginationBar browser fixture.
The browser fixture tests the same paging/session/extraction/connector helpers;
it does not claim an end-to-end host application or Agently application acceptance run.
