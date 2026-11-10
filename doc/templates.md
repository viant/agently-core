# Output templates & template bundles

Templates shape the agent's final output — reports, dashboards, compact
summaries — without pushing formatting logic into the prompt. An agent calls
`template:get` before producing output and the template body guides the
response structure.

## Packages

| Path | Role |
|---|---|
| [protocol/template/](../protocol/template/) | Template types |
| [protocol/templatebundle/](../protocol/templatebundle/) | Bundle types (grouping templates) |
| [protocol/tool/service/template/](../protocol/tool/service/template/) | `template:list`, `template:get` internal tools |
| Workspace `templates/*.yaml`, `templates/bundles/*.yaml` | Template + bundle definitions |

## Template shape

```yaml
id: analytics_dashboard
title: Analytics dashboard
description: Interactive dashboard with KPI cards and charts.
# Instructions that go into the model's turn when template:get is called.
document: |
  Produce the output as a forge-fenced block with …
appliesTo: [performance, pacing]
```

## Bundles

A bundle groups related templates. Agent YAML references a bundle:

```yaml
# agents/analytics.yaml
templateBundles: [performance]
```

When bound, only templates in those bundles are visible via `template:list` /
`template:get`. Bundles are the equivalent of tool bundles ([doc/tool-system.md](tool-system.md)) for output formatting.

## Runtime flow

1. Agent's system prompt contains instructions to call `template:list` when an output template is needed.
2. `template:list` returns visible template ids + descriptions.
3. Model picks one, calls `template:get` with `includeDocument: true`.
4. The template's `document` body is injected as a system block; the model then produces the output respecting it.

Agent-scoped filtering:

- [protocol/tool/service/template/service.go](../protocol/tool/service/template/service.go) — `allowedTemplates()` at lines ~167–218 narrows by bundle.
- `injectTemplateDocument()` at lines ~235–258 handles the inject.

## Internal + external unification

The `template:*` tools are in-process, but a workspace can also load
templates from an MCP server that implements `template:list` / `template:get`
with the same contract. The runtime doesn't care — bundles and visibility
work identically against both.

## Extensibility

- **New template**: drop a YAML under `templates/` and (optionally) add it to a bundle.
- **Dynamic template generation**: an agent can create a template at runtime via `template:save` (when enabled).
- **Template validation**: implement `template.Validator` to enforce bundle-level rules (required sections, schema hints).

## Related docs

- [doc/prompts.md](prompts.md) — intake profiles use template ids to pair instructions with output shapes.
- [doc/agent-orchestration.md](agent-orchestration.md)

## Markdown authoring

The native authored template snapshot accepts `.md` alongside YAML and JSON.
YAML frontmatter contains the ordinary template metadata (`id`, `name`, etc.).
The body is literal instructions. Explicit `template-schema`, `template-fences`,
and `template-examples` fenced blocks contain the corresponding YAML or JSON
value. Ordinary code blocks remain instructions, including named blocks inside
a longer literal fence. Defining a contract field twice is an error.

````markdown
---
id: example-output
name: Example output
format: json
---
```template-schema
{"type":"object","properties":{"status":{"type":"string"}}}
```
Return the requested status.
````

An explicit `schemaRef: forge:report-spec` or `schemaRef: forge:window` may replace
inline `schema` on the template or an output fence. Core resolves these references
from its pinned Forge canonical schema exports while compiling or decoding the
immutable definition. Existing snapshot and gateway invalidation own its lifetime;
there is no runtime remote schema lookup. YAML/JSON definitions can use the same
references. A schema and reference on the same field are mutually exclusive.

The top-level compiled schema uses Core's existing provider `OutputSchema` path.
Fenced schemas describe their individual blocks and do not turn a mixed Markdown
response into whole-response JSON. Provider constraint support varies; no new
text grammar language or universal provider guarantee is introduced. References
are opt-in and full canonical schemas can be large; metadata-only discovery does
not expand them into all agent prompts.

For schema guidance within Markdown instructions, `${ReportSchema}` and
`${WindowSchema}` expand to the respective complete, compact canonical JSON
Schema. Expansion occurs in the existing immutable definition preparation path
using Core's native template engine. Unknown placeholders remain literal.
Fenced and inline code examples are literal and never expanded. These prose
macros do not automatically enable whole-response JSON output constraints.
