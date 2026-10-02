# MCP 2026 Upgrade Implementation Tasks

This directory decomposes
[the approved MCP 2026 extension proposal](../mcp-2026-extension-upgrade.md)
into independently reviewable implementation tasks.

The directory is named `mcp-2006` as requested. The wire protocol target is
MCP `2026-07-28`; `2006` is only the local planning-folder name.

## Execution rules

Each numbered file is an implementation contract. A task is complete only when:

1. every listed deliverable is implemented in the named repositories;
2. every required test and acceptance scenario passes;
3. compatibility, security, observability, and rollback requirements are met;
4. completion evidence is linked in the task's evidence section;
5. dependent task documents are updated if an implementation decision changes
   their assumptions.

Do not mark a task complete merely because its code compiles. Protocol fixtures,
negative tests, interoperability evidence, and migration behavior are part of
the deliverable.

## Task index

| ID | Task | Primary repositories | Depends on |
|---|---|---|---|
| 001 | [Pin specifications and establish conformance fixtures](001-specification-pins-and-fixtures.md) | `mcp-protocol`, `mcp-ext` | — |
| 002 | [Resolve schema packaging and generate July types](002-schema-packaging-and-generation.md) | `mcp-protocol` | 001 |
| 003 | [Implement stateless core discovery and HTTP routing](003-stateless-core-and-discovery.md) | `mcp` | 001, 002 |
| 004 | [Implement multi-round-trip requests](004-multi-round-trip-requests.md) | `mcp`, `mcp-protocol` | 002, 003 |
| 005 | [Implement subscriptions, caching, and tracing](005-subscriptions-caching-and-tracing.md) | `mcp`, `mcp-protocol` | 001, 002, 003, 006 |
| 006 | [Harden authorization and HTTP transport security](006-authorization-and-transport-security.md) | `mcp` | 001, 003 |
| 007 | [Create the `viant/mcp-ext` foundation](007-mcp-ext-foundation.md) | new `mcp-ext` | 001, 002 |
| 008 | [Implement the Tasks extension](008-tasks-extension.md) | `mcp-ext`, `mcp`, `agently-core` | 001, 003, 004, 006, 007 |
| 009 | [Implement MCP Apps server contracts and migrate `mcp-ui`](009-apps-server-and-mcp-ui-compatibility.md) | `mcp-ext`, `mcp-ui`, `agently-core` | 001, 002, 003, 007 |
| 010 | [Implement the Agently MCP Apps host](010-agently-apps-host.md) | `agently`, `agently-core` | 003, 006, 009 |
| 011 | [Upgrade canonical transcript and historical restoration](011-transcript-and-history-migration.md) | `agently-core`, `agently` | 009, 010 |
| 012 | [Upgrade Forge dependencies and runtime APIs](012-forge-runtime-and-virtual-window-api.md) | `forge` | 003, 007 |
| 013 | [Integrate MCP Apps as Forge virtual windows](013-agently-forge-app-integration.md) | `mcp-ext`, `agently`, `agently-core`, `forge` | 007, 010, 011, 012 |
| 014 | [Implement durable revisioned view state](014-durable-view-state.md) | `agently-core`, `agently`, `forge` | 011, 013 |
| 015 | [Reconcile reporting state and portable report tools](015-reporting-reconciliation.md) | `forge`, `agently-core`, `agently` | 009, 013, 014 |
| 016 | [Run interoperability rollout and legacy removal](016-interoperability-rollout-and-deprecation.md) | all | 003–015 |

## Critical path

```text
001 -> 002 -> 003 -----------------------> 009 -> 010 -> 011
       |      |                              |       |       |
       +----> 007 -> 009                     |       +-----> 013 -> 014 -> 015 -> 016
               |                             |               ^
               +----> 012 <-----------------+---------------+
003 -> 006 -> 005 ------------------------------------------> 016
  |      |
  +----> 004 -> 008 <---- 007, 006 ------------------------> 016
```

Tasks 004, 006, 007, and later 005, 008, and 012 can overlap after their stated
dependencies. Task 012 must publish the Forge virtual-window and scoped bridge
contracts before task 013 can implement the Agently adapter.

## Shared release gates

- Do not change the default MCP protocol until tasks 001–006 pass.
- Do not advertise official Apps until tasks 007, 009, and 010 pass official
  lifecycle and security fixtures.
- Do not enable Forge enrichment until tasks 012 and 013 prove parent-only
  bridge ownership.
- Do not claim durable history restoration until task 014 passes new-tab and
  authorized-device scenarios.
- Do not remove `mcpui:*` compatibility until task 016's usage and rollback
  gates pass.

## Status convention

Task files start with `Status: planned`. Change status only with evidence:

- `planned`: approved scope, implementation not started;
- `in progress`: code or fixtures are actively being changed;
- `blocked`: an explicit external decision or dependency prevents progress;
- `complete`: all acceptance evidence is recorded and independently reviewed.

## Cross-task decisions

The following decisions affect multiple tasks and must be recorded in the
parent proposal before dependent implementation begins:

- root `mcp-protocol/schema` freeze versus Go-major migration;
- exact pinned July schema commit and its provenance;
- final Apps and Tasks extension versions;
- Forge profile reverse-DNS identifier;
- durable view-state store and retention policy;
- supported legacy versions and removal dates.
- pre-discovery version advertisement/selection and no-downgrade behavior;
- MRTR replay policy and shared key/replay-store infrastructure;
- subscription fan-out/replay backend and numeric load limits;
- upstream AppBridge adoption versus documented independent implementation;
- first-party parent-adapter authentication/handle binding;
- server-issued versus host-local view IDs and historical resource policy;
- Forge profile identifier/governance and supported opaque-App actions;
- report-serving module ownership and portable tool namespace;
- independent-host qualification/waiver authority and rollout/removal approvers.

## Fable 5 review

Each numbered implementation contract received an independent Claude Fable 5
review on 2026-08-03. Every initial review returned `REVISE`. All blocking
findings were incorporated, and every task then received a second-pass
`APPROVE` with no blockers. The per-task completion-evidence section records
the verdict; non-blocking notes remain kickoff considerations rather than
untracked implementation requirements.
