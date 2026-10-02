# 002 — Resolve Schema Packaging and Generate July Types

Status: planned  
Parent workstream: WS-1  
Primary repository: `mcp-protocol`  
Depends on: [001](001-specification-pins-and-fixtures.md)

## Outcome

Provide reproducible, version-correct Go types for the July wire target without
silently breaking consumers of the unversioned root `schema` package.

## Required decision

Before generation, approve exactly one root-package strategy:

### Strategy A — freeze the current root

- root `schema` types remain `2025-11-25`;
- root `LatestProtocolVersion` remains `2025-11-25`;
- July consumers import `schema/2026-07-28` explicitly;
- runtime defaults are selected outside the frozen schema package.

### Strategy B — Go module-path major migration

- first establish the current API as the supported v1 line, then publish July
  types under a `/v2` module path;
- regenerate root types and version constant together for July;
- migrate every in-scope Viant importer explicitly;
- keep v1 maintained for the compatibility window and publish guidance for
  external importers, which cannot be migrated by this task.

Changing root types in the current major version while leaving importers
untouched is prohibited.

## Deliverables

- decision record for Strategy A or B;
- generated `schema/2026-07-28` package from task 001's input;
- generic core extension containers only—no Apps or Tasks-specific structs;
- July request metadata and `server/discover` types;
- full JSON Schema 2020-12 tool input/output representation;
- caching, trace, MRTR, subscriptions, and published error types;
- deprecation annotations for Roots, Sampling, and Logging without premature
  wire removal;
- retained versioned `2025-11-25` package and experimental Tasks types;
- compatibility aliases only where wire semantics are identical;
- a package/version agreement CI check.
- a parent-proposal update recording the chosen strategy and any downstream
  assumption changes before tasks 003–005 begin.

## Importer inventory

Inventory and disposition every root import in:

- `viant/mcp`;
- `viant/mcp-ui`;
- `viant/agently-core`;
- `viant/agently`;
- `viant/forge`;
- examples and external modules discoverable in the Viant workspace.

For each importer record: freeze, migrate, or use a runtime-neutral adapter.

## Implementation steps

1. Generate into a disposable review package and compare public names; this
   does not authorize committing generated output before strategy approval.
2. Classify generated changes as additive, removed, or semantically changed.
3. Resolve the root-package strategy and importer inventory.
4. Commit provenance beside generated files.
5. Add version constants inside versioned packages.
6. Implement generic request/result unions without extension-specific imports.
7. Add compile fixtures for both July and `2025-11-25` in the same module.
8. Update schema generation documentation.

## Tests

- clean regeneration produces no diff;
- every task 001 golden fixture round-trips without field loss;
- task 001 negative fixtures are rejected or handled with the pinned strictness;
- JSON Schema 2020-12 composition, references, conditionals, and unrestricted
  output schema cases round-trip;
- July types cannot accidentally marshal legacy Tasks fields;
- old versioned package tests remain unchanged and green;
- root package version/type agreement test passes;
- importer inventory has no unclassified root import.
- aliases have recorded semantic-equivalence review plus round-trip tests;
- generated output contains no Apps/Tasks imports or extension-owned structs;
- package/version CI recomputes provenance hashes and matches version constants.

## Acceptance criteria

- The selected strategy is approved before any root behavior changes.
- Every schema package advertises the version represented by its types.
- Apps and Tasks types are absent from July core output.
- Both July and supported legacy code can compile in one workspace.
- Generation is fully reproducible from task 001 artifacts.

## Rollout and rollback

Publish July types without making them the default. Under Strategy A, rollback
removes the new versioned package. Under Strategy B, revert in-scope importer
migrations and preserve/retract the `/v2` prerelease according to Go module
policy; never rewrite a published tag. A default version change belongs to task
016 after runtime conformance.

## Completion evidence

- Root strategy decision: _pending_
- Importer inventory: _pending_
- Generation command/result: _pending_
- Compatibility compile matrix: _pending_
- Parent proposal decision update: _pending_
- Package/version CI result: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
