# Elicitation system

When a tool needs extra input from the user, it raises an **elicitation**:
a JSON Schema the UI renders as a form. The elicitation service routes
requests, refines schemas (including installing lookup widgets), and returns
the user's answer to the waiting tool.

## Packages

| Path | Role |
|---|---|
| [service/elicitation/](../service/elicitation/) | Service entry point, awaiter, stdio/auto fallbacks |
| [service/elicitation/refiner/](../service/elicitation/refiner/) | Schema post-processing (`x-ui-widget`, `x-ui-order`, format inference, overlay hook) |
| [service/elicitation/router/](../service/elicitation/router/) | In-process dispatch by elicitation id |
| [service/elicitation/mcp/](../service/elicitation/mcp/) | Bridge for elicitations raised over MCP |
| [service/elicitation/action/](../service/elicitation/action/) | "Accept/decline/cancel" terminal actions |
| [protocol/mcp/expose/elicitation](../protocol/mcp/expose/) | Expose to MCP hosts |

## Lifecycle

1. A tool or reactor step calls `elicit.Elicit(ctx, schema, message)`.
2. Service assigns an elicitation id, refines the schema, stores it, and publishes an `elicitation_requested` event ([doc/streaming-events.md](streaming-events.md)).
3. UI renders the form. Current implementation: [ElicitationForm.jsx](../../agently/ui/src/components/chat/ElicitationForm.jsx).
4. User submits / declines / cancels → client calls `POST /v1/elicitations/{convID}/{elicID}/resolve`.
5. Handler pushes the result onto the awaiter's channel; the original `Elicit` call returns.
6. `elicitation_resolved` event is published.

## Refinement

Before the schema leaves the server it passes through `refiner.Refine`:

- Default `type` (string).
- Format inference for date/date-time from title/description.
- Array-of-string → `x-ui-widget: tags`.
- `x-ui-order` auto-assignment (name fields first, required next, then alpha).
- **Overlay hook** — optional plug-in that attaches forge `Item.Lookup` metadata (installed by the datasource stack; see [doc/lookups.md](lookups.md)).

## Awaiter types

| Awaiter | When |
|---|---|
| In-process (UI) | Normal server-mode: the HTTP resolve endpoint feeds the channel. |
| stdio | CLI mode without a UI — prompts on stdin. |
| Auto | Test mode — pre-programmed answers. |

Choice is made by the runtime wiring (app/executor).

## Timeout + cancel

Elicitations honour `ctx.Done()`: if the calling turn is cancelled, the awaiter closes with `ctx.Err()` and the pending elicitation is marked aborted.

## Extensibility

- **New widget hint**: add an `x-ui-*` convention + extend the refiner.
- **New terminal action**: add under `service/elicitation/action/`.
- **Pre-fill via profile**: the intake sidecar or a prompt profile can pre-populate `x-ui-default` hints before the form reaches the user.

## Related docs

- [doc/lookups.md](lookups.md) — overlay hook + picker widget.
- [doc/streaming-events.md](streaming-events.md)
