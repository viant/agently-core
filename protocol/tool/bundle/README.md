# Tool exposure rules

Workspace bundle `match` rules and agent `tool.items` accept `tier` and
`alwaysInclude`:

```yaml
match:
  - name: "catalog:lookup"
    tier: critical
  - name: "catalog:details"
    tier: onDemand
  - name: "catalog:help"
    tier: onDemand
    alwaysInclude: true
```

- `critical` retains the admitted schema on every model iteration, including
  empty intake choices and clarification turns.
- `base` is initially available and may be narrowed by intake. An omitted tier
  preserves this legacy behavior.
- `onDemand` remains discoverable, but its schema is omitted until an exact
  intake selection, exact caller/profile choice, discovery-list result, or tool activation requests it. Selecting its owning bundle
  alone does not promote it.
- `alwaysInclude: true` retains an admitted schema regardless of intake
  narrowing, including an `onDemand` schema.

Discovery covers the full connected model-visible catalog, including tools absent
from every bundle. Bundle membership is a starting/workflow set, not an admission
requirement. Only explicit exclusions narrow discovery, and another trusted
positive rule can admit the excluded operation.

`llm/tools:list` makes every tool on the returned page available on the next
model iteration without a separate activation call. Unreturned pages remain
unselected. Successful same-turn discovery selections survive resume and are
revalidated against the current catalog. List filters narrow search results;
they do not turn workflow bundles into execution allowlists.

Retention rules apply across available trusted workspace/profile bundles and
explicit agent rules. Model visibility, rule exclusions, explicit caller scope,
profile restrictions and execution authorization still apply. Explicit runtime
bundles retain their existing union with caller tool names. A selected template
controls document injection, not tool removal.

Duplicate admitted rules combine retention flags. `critical` takes precedence;
otherwise an explicit `onDemand` declaration takes precedence over `base`.
Invalid tier names fail bundle/agent validation. Schema producers cannot set
this metadata through MCP tool definitions; it is applied after catalog lookup
and is excluded from serialized model schemas. Existing catalog caches and
cloning preserve source definitions without retaining rule metadata on them.
