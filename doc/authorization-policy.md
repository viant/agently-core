# Authorization policy

Agently Core can delegate visibility decisions to one workspace-configured MCP
tool. The mechanism is product-neutral: core sends operations and opaque
candidate IDs; the MCP service maps its own roles, tenant rules, capabilities,
and resource ACLs into an allow decision.

## Configuration

Enable only the surfaces that need external authorization:

```yaml
policy:
  authorization:
    mcpTool: myMcp:authorize
    reports: {}
    ui: {}
    starterPrompt: {}
    intent: {}
```

An empty subsection enables that operation. It may instead use
`{enabled: true}` or be disabled with `false` / `{enabled: false}`.
`starterTask` is accepted as a compatibility alias for `starterPrompt`.

If no subsection is enabled, current behavior is unchanged. Enabling any
subsection without `mcpTool` makes workspace runtime construction fail.

## MCP contract

The configured tool receives the authenticated request context and an explicit
operation:

```json
{
  "operation": "window.view",
  "conversationId": "conv-123",
  "candidates": [
    {
      "id": "reportBuilder",
      "kind": "window",
      "metadata": {"title": "Report Builder"}
    }
  ],
  "context": {}
}
```

Supported operations are:

| Configuration | Operation | Enforcement |
|---|---|---|
| `reports` | `report.view` | Saved-report list and direct get |
| `ui` | `window.view` | Navigation and direct Forge window metadata |
| `starterPrompt` | `starterPrompt.view` | Workspace starter-prompt metadata |
| `intent` | `intent.view` | Prompt-profile list/get and runtime injection |

The tool returns a short-lived decision:

```json
{
  "policyVersion": "v17",
  "expiresAt": "2026-09-12T18:05:00Z",
  "allow": true,
  "allowedIds": ["reportBuilder"]
}
```

`policyVersion` and a future `expiresAt` are required. `allow: false` denies
the entire request. With `allow: true`, omitted or empty `allowedIds` allows
all submitted candidates; otherwise core intersects the candidates with that
list. Matching is case-insensitive.

The response may be the direct JSON object or nested in the usual MCP
`data`, `result`, `output`, `structuredContent`, or text-content envelope.
Malformed, expired, missing, and failed decisions fail closed.

Starter-prompt candidate IDs use `<agentId>:<starterPromptId>` so the same
starter ID on two agents cannot accidentally share a grant. The original
values are also provided as candidate metadata.

## Enforcement behavior

Report authorization runs after existing effective-user ownership filtering.
It can reduce visible reports but cannot expose reports hidden by the report
store. A denied direct get returns not-found to avoid revealing existence.
List filtering happens before the requested result limit is applied.

Window authorization removes denied windows from navigation, including empty
navigation groups, and rechecks direct `/window/{key}` requests. A denied
direct request returns not-found. Policy-service failures return service
unavailable without loading the requested window.

Starter-prompt authorization filters both normal workspace metadata and public
agent metadata. Categories with no remaining prompts are removed.

Intent authorization is applied after the existing agent prompt-profile
allow-list. It filters `prompt:list`, makes denied profiles unavailable to
`prompt:get`, and rechecks `RunInput.promptProfileId` before profile messages
are rendered or injected.

## Legacy Forge authorization

The existing configuration remains supported unchanged:

```yaml
ui:
  authorization:
    tool: myMcp:resourceAuthorization
```

This legacy tool resolves resource capabilities used by Forge conditions such
as `visibleWhen`, `hiddenWhen`, `disabledWhen`, and `readOnlyWhen`. It controls
content inside an authored window. The new `policy.authorization.ui` controls
whether a user can discover or load the window itself.

The two mechanisms are independent. If both are configured, the whole-window
check and the legacy resource-capability check must both pass. They may point
to the same MCP tool only if that tool implements both request contracts.

## Security boundaries

- Identity travels through `context.Context`; client-supplied roles are never
  accepted as grants.
- External policy can only reduce workspace and ownership-filtered candidates.
- Hiding a report, window, starter prompt, or profile does not authorize its
  underlying tools, datasources, callbacks, or writes.
- Tool and datasource services must continue enforcing their own permissions.

## Implementation

The shared contract and MCP resolver live in
[`service/policy`](../service/policy/). Workspace parsing lives in
[`workspace/config`](../workspace/config/). Call-site enforcement is owned by
the reporting service, Forge HTTP handler, workspace metadata handler, prompt
service, and agent prompt-profile binding.
