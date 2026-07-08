# Forge Dashboard Isolation Issue

## Problem summary

In the Steward workspace, a standalone `message:add` assistant bubble can initially render Forge dashboard content correctly, but after the next assistant message arrives, the first bubble may lose its data or appear partially reset.

This is not primarily a transcript-loss issue.

The more likely root cause is dashboard identity overlap in the Agently-to-Forge integration layer:

- Forge dashboard rendering is provided by `viant/forge`
- Agently constructs the Forge dashboard context and runtime identity
- Agently currently scopes Forge dashboard identity too broadly

When two assistant messages render Forge dashboards with the same title and/or the same local datasource ids, the second message can reuse or overwrite the first message's dashboard runtime state.


## Evidence

### 1. Plain Forge chat is message-scoped

In Forge chat message rendering:

- file: `/Users/awitas/go/src/github.com/viant/forge/src/components/chat/MessageCard.jsx`

Relevant behavior:

- `buildDashboardBlockContext(..., messageID)` derives:
  - `dashboardKey = message-dashboard:${messageID || "unknown"}`
- `identity.windowId` is also message-scoped

This means two different assistant messages with the same dashboard title do **not** collide by default in Forge chat.


### 2. Agently `RichContent` is title-scoped

In Agently chat rich-content rendering:

- file: `/Users/awitas/go/src/github.com/viant/agently/ui/src/components/chat/RichContent.jsx`

Relevant behavior:

- `buildForgeDashboardContext(payload, dataStore, block)` derives:
  - `dashboardKey = forge-ui:${payload.title || 'message'}`
- `buildForgeExportContext(payload, dataStore)` also derives:
  - `dashboardKey = forge-ui:${payload.title || 'message'}`
- `identity.windowId` is derived from that same title-scoped key

This is unsafe because dashboard titles are not guaranteed to be unique across assistant messages.

Two different assistant bubbles with:

- the same `forge-ui.title`
- and the same local datasource ids

can end up sharing:

- Forge dashboard window identity
- Forge dashboard runtime state


### 3. Datasource ids are also reused without namespacing

Still in Agently `RichContent.jsx`:

- `buildForgeDataSources(store)` preserves each datasource as:
  - `id: entry.id`
  - `name: entry.id`

No per-message namespacing is applied.

If two messages both use a local datasource id such as:

- `baseline`
- `orders`
- `summary`

then the second message can conceptually overlap the first unless the enclosing dashboard/window identity is already safely isolated.

This makes the problem worse for multi-message chat flows.


## Why this matches the Steward symptom

Observed behavior:

1. First `message:add` bubble appears with data
2. Next assistant message arrives
3. First bubble remains visible
4. But its dashboard data appears gone or reset

That pattern is more consistent with:

- shared dashboard runtime identity
- shared datasource identity
- rerender/reprojection of the second dashboard overwriting the first

than with:

- transcript row deletion
- message ordering only
- missing bubble persistence

Those latter issues would usually remove or reorder the entire bubble, not preserve the bubble shell while mutating its internal dashboard state.


## Root cause

The root cause is likely:

- Agently scopes Forge dashboard identity by dashboard title
- instead of by assistant message identity

That violates the actual isolation boundary in chat:

- each assistant message bubble must own its own Forge runtime namespace

not:

- each dashboard title


## Correct isolation model

Forge UI rendered inside chat should be isolated by **message identity**, not by title and not by datasource id alone.

Recommended namespace:

- message namespace:
  - `msg:<messageId>`
- dashboard key:
  - `forge-ui:msg:<messageId>`
- datasource id rewrite:
  - local `baseline` -> `msg:<messageId>:baseline`

This supports all of:

- multiple dashboard blocks in one message
- dashboard blocks with no collection
- identical dashboard titles across different messages
- identical local datasource ids across different messages


## Important constraint

We must support:

- multiple dashboard blocks
- blocks without collections
- repeated local datasource ids

So the fix must **not** depend on collection presence.

The correct scope is:

- per assistant message bubble


## Proposed hardening

### A. Agently-side fix

Files:

- `/Users/awitas/go/src/github.com/viant/agently/ui/src/components/chat/BubbleMessage.jsx`
- `/Users/awitas/go/src/github.com/viant/agently/ui/src/components/chat/RichContent.jsx`

Changes:

1. Pass message identity into `RichContent`
   - at minimum:
     - `messageId`
   - optionally:
     - `turnId`

2. Change dashboard identity generation from title-scoped to message-scoped
   - current:
     - `forge-ui:${payload.title || 'message'}`
   - proposed:
     - `forge-ui:msg:${messageId || fallback}`

3. Namespace datasources per message
   - rewrite `forge-data.id`
   - rewrite each `block.dataSourceRef`
   - do this in Agently before constructing Forge context

Suggested rule:

- `scopedId = msg:${messageId}:${localId}`

Then:

- datasource store uses `scopedId`
- blocks reference `scopedId`
- Forge receives only isolated datasource ids


### B. Optional Forge hardening

Forge itself appears structurally fine for message-scoped usage, but additional guardrails in Forge could still help:

- if `dashboardKey` or `windowId` is absent or obviously non-unique, warn in development
- document that dashboard identity must be caller-scoped for chat integrations

This is secondary.

The first fix should be in Agently because that is where the collision is introduced.


## Non-goals

Do **not** fix this with:

- DOM cloning
- post-render state copying
- “if next message arrives, preserve previous data” heuristics
- transcript fallbacks
- delayed rerender hacks

Those would be bandaids.

The correct fix is identity isolation.


## Regression tests to add

### Agently UI tests

Add tests around `RichContent.jsx` integration to cover:

1. Two assistant messages
2. Same `forge-ui.title`
3. Same `forge-data.id`
4. Second message arrival must not mutate first message's rendered dashboard

Also cover:

- messages with multiple dashboard blocks
- messages with blocks that do not use collection data


### Browser smoke

Add a browser-driven smoke covering:

1. first assistant message renders a forge-ui dashboard
2. second assistant message renders another forge-ui dashboard with same title/id
3. verify the first dashboard still retains its original rendered content


## Recommended implementation order

1. Agently: pass `messageId` into `RichContent`
2. Agently: namespace dashboard key by `messageId`
3. Agently: namespace datasource ids and `dataSourceRef`s by `messageId`
4. Add unit tests for same-title / same-datasource collisions
5. Add browser smoke for two-message dashboard isolation
6. Optionally add Forge dev-mode warnings for non-isolated chat dashboards


## Bottom line

The dashboard renderer is in `viant/forge`, but the collision is most likely caused by Agently's integration contract:

- Forge chat path: message-scoped identity
- Agently rich-content path: title-scoped identity

That difference is likely why one message's dashboard data appears to disappear when the next assistant message arrives.

The right fix is:

- message-scoped Forge dashboard identity
- per-message datasource namespacing

not a transcript or rerender workaround.
