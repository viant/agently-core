# UI Ownership Model

## Goal

Define the ownership and scope boundaries for Agently UI so we do not mix:

- app-global navigation
- conversation-owned hosted workspace UI
- message/turn-embedded Forge rendering
- active-turn SSE state
- transcript-backed historical state

This document is the source of truth for the current boundary model.

## Scope Layers

There are three distinct UI scope layers.

### 1. App-global top-level surfaces

These are peer top-level application surfaces:

- `chat`
- `schedule`
- `runs`

Rules:

- top-level surfaces replace each other
- they are not children of each other
- switching from one top-level surface to another is a top-level replace transition

Examples:

- `chat -> schedule`
- `schedule -> runs`
- `runs -> chat`

### 2. Conversation-owned hosted workspace UI

These are children of the active chat surface for a specific conversation.

Examples:

- `order`
- `metricReportBuilder`
- future user-defined hosted workspace windows

Rules:

- hosted workspace children belong to a specific conversation
- hosted workspace children are children of `chat/new`
- hosted workspace children must never survive replacement of the parent chat surface
- reopening a conversation may restore its hosted workspace subtree
- hosted workspace children from one conversation must not leak into another conversation

### 3. Message/turn-embedded Forge content

These are not windows. They are embedded renderable artifacts inside transcript/message content.

Examples:

- ````forge-ui` fenced blocks
- ````forge-data` fenced blocks
- template-rendered inline dashboards

Rules:

- embedded Forge content is message/turn-scoped, not window-scoped
- it is restored from transcript/message state, not from window restoration
- it must not be treated as a hosted child window

## Tool Scope

UI-related tools are not all the same.

### Conversation-scoped UI tools

These operate on the active conversation UI context:

- `ui/view:*`
- `ui/window:*`
- `ui/control:*`
- `ui/datasource:*`

Rules:

- they resolve through active conversation id + active UI client
- they act on the conversation-owned hosted subtree by default
- they must not inspect or mutate unrelated conversations

### Global UI primitives with user-bounded resolution

Lookups are different.

Examples:

- lookup dialogs
- lookup-backed datasource fetches

Rules:

- lookups are a global UI primitive
- their data resolution is still user-bounded through MCP/auth context
- they are not equivalent to chat-hosted workspace ownership
- do not force lookup semantics into the same ownership model as hosted chat windows

## Streaming And Transcript Boundary

There is one hard rule:

- **active turn: SSE is the source of truth**
- **past turns: transcript is the source of truth**

Never mix them.

### Active turn

Rules:

- derive active turn lifecycle from SSE
- do not let transcript rewrite the currently live turn
- do not “catch up” the active turn from transcript while it is still SSE-owned

### Past turns

Rules:

- once the turn is terminal and no longer SSE-owned, transcript is authoritative
- historical render state comes from transcript truth

## Transition Rules

### Top-level replace

When switching away from chat:

- remove `chat/new`
- remove all hosted children whose parent is `chat/new`
- activate the replacement top-level surface

This is subtree replacement, not visibility toggling.

### Chat restoration

When switching back to chat:

- recreate/focus `chat/new`
- restore only that conversation's hosted workspace subtree
- do not restore app-global surfaces as if they were conversation-owned

### Hosted compare/open behavior

For compare flows:

- open multiple hosted children under the same parent/region
- render them as one hosted workspace pane with a compare tab strip
- switching compare tabs changes the active hosted child, not the top-level surface

## Verification Invariants

The following must always hold:

1. Active turn state comes from SSE only.
2. Historical state comes from transcript only.
3. Replacing a top-level surface removes the old top-level subtree.
4. Hosted conversation children never appear in the root tab strip.
5. Hosted children from conversation A never appear when conversation B is active.
6. Lookup/global primitives do not silently inherit chat-hosted ownership semantics.

## Current Intended Model

```text
AgentlyShell
  TopLevelSurface
    ChatSurface(conversationId)
      HostedWorkspaceChildren[]
    ScheduleSurface
    RunsSurface

TranscriptMessage
  EmbeddedForgeBlocks[]
```

This separation is intentional. Any code path that blurs these layers is a bug.
