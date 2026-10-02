# 007 — Create the `viant/mcp-ext` Foundation

Status: planned  
Parent workstream: WS-3  
Primary repository: new `github.com/viant/mcp-ext`  
Depends on: [001](001-specification-pins-and-fixtures.md), [002](002-schema-packaging-and-generation.md)

## Outcome

Create a standalone extension module implementing SEP-2133 identifiers,
version negotiation, registries, and shared compatibility contracts without
coupling core MCP transports to Apps, Tasks, Agently, or Forge.

## Repository boundary

Initial layout:

```text
github.com/viant/mcp-ext
  extension/     identifiers, versions, capabilities, selection, registry
  apps/          task 009
  tasks/         task 008
  compat/        versioned adapter contracts
```

Foundational packages depend only on `mcp-protocol`. An optional
`mcpadapter` package, if later justified and owned by a downstream task, may
depend on `mcp`. `mcp` must never import `mcp-ext`. The `forge/` profile is
owned by task 013 after its identifier/governance decision is recorded.

## Entry gate

Before repository creation, approve and record the public/internal package
boundaries and naming in the parent proposal. Apps, Tasks, Forge, and adapter
packages are downstream scopes; this task creates only the generic foundation.

## Deliverables

- module/repository manifest, license, CI, security policy, and release process;
- reverse-DNS extension identifier validation;
- extension version type and compatibility comparison;
- client/server extension capability maps;
- deterministic mutual version/feature selection;
- unknown extension preservation and policy hooks;
- registry for extension-owned methods, resources, metadata codecs, and
  validators without global mutable state;
- typed errors for unsupported, incompatible, malformed, and policy-denied
  extensions;
- compatibility adapter interface keyed by core and extension version;
- limits for peer-supplied capability map entries, encoded size, and nesting;
- provenance manifests from task 001;
- package-level dependency tests preventing imports of Agently or Forge runtime.

## Design constraints

- Extension negotiation is request-scoped on the July path.
- Extension wire versions are independent from the Go module release version.
- Unknown extensions are not decoded into product-specific structs.
- Registry instances are immutable after construction or concurrency-safe.
- An extension cannot override a core method without an explicit core hook.
- Private Viant metadata uses an approved reverse-DNS namespace.

## Implementation steps

1. Create the repository and initial Go module.
2. Implement identifier/version/capability models from pinned SEP-2133.
3. Implement deterministic selection with golden fixtures.
4. Implement registry builder and immutable runtime view.
5. Add generic hooks needed by `mcp` composition without introducing a cycle.
6. Add provenance, adapter-resolution, input-bound, dependency-boundary, race,
   and fuzz tests.
7. Tag an initial pre-production release consumed by tasks 008 and 009.

## Tests

- valid/invalid reverse-DNS identifiers;
- exact, compatible, incompatible, missing, and unknown versions;
- deterministic selection regardless of map iteration order;
- concurrent registry reads and prohibited late mutation;
- method-name collision and core override rejection;
- unknown-extension round-trip behavior;
- import-boundary CI proving foundational packages do not pull `mcp`, Agently,
  or Forge runtime;
- fuzz tests for untrusted capability maps.
- compatibility adapter exact/missing/mismatched version-pair resolution;
- oversized/deep capability maps return typed errors.

## Acceptance criteria

- Minimal client/server code can negotiate two synthetic independent
  extensions without Agently or downstream Apps/Tasks packages.
- Synthetic extensions can be enabled independently.
- Foundation packages import no transport/runtime implementation.
- Version conflicts return typed deterministic errors.
- A tagged release is available for downstream tasks.

## Rollout and rollback

The foundation is additive. Downstream modules adopt it behind feature flags.
Rollback removes the downstream dependency; it does not change core schemas or
legacy behavior.

## Completion evidence

- Repository/tag: _pending_
- Dependency-boundary test: _pending_
- Negotiation fixture test: _pending_
- API review: _pending_
- Provenance/race/fuzz/boundary report: _pending_
- Package-boundary decision: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
