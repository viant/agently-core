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

- [doc/prompts.md](prompts.md) — prompt profiles use template ids to pair instructions with output shapes.
- [doc/agent-orchestration.md](agent-orchestration.md)
