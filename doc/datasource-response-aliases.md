# Datasource response aliases

An authored datasource can give logical field names to exact producer row keys:

```yaml
selectors:
  data: Data
  metrics: Metrics
responseAliases:
  amount: Amount
  categoryId: CategoryId
backend:
  kind: mcp_tool
  service: example
  method: ReadRows
```

`responseAliases` is optional and maps each logical **target** key to a literal producer **source** key. Keys are case-sensitive, top-level own properties. Dots and brackets are literal characters, not paths; no case conversion, wildcard matching, scalar coercion or default values are inferred. Missing source keys add nothing. Present `null`, `false`, `0`, arrays and objects retain their values and types. Original producer keys remain present.

Configuration is captured and validated before cache lookup or backend execution. Empty keys, reserved prototype segments (`__proto__`, `prototype`, `constructor`), and chained/cyclic alias declarations are rejected. Multiple independent aliases may name the same source. If a row already has a target, it must be structurally equal to its source; conflicting values fail the fetch instead of overwriting data. Unsupported mutable or non-JSON row graphs fail before caching. Projected rows are safely cloned before aliases are added, leaving producer data unchanged.

Aliases run after envelope selection and before paging/cache insertion. Cache identity includes a versioned SHA-256 digest of the captured alias configuration alongside the unchanged logical request hash. Changing an alias therefore cannot reuse rows projected with another map; input-hash invalidation removes all associated alias variants. Backend arguments and execution audit inputs are unaffected.

This operation belongs to fresh datasource fetches. It does not edit saved report Fill rows, artifact hashes, journal payloads, or cold completed-report restoration. Keep envelope selectors and every alias explicit, based on the producer's declared JSON contract.
