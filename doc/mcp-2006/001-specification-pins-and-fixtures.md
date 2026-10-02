# 001 — Pin Specifications and Establish Conformance Fixtures

Status: planned  
Parent workstream: WS-0  
Primary owner: `mcp-protocol` maintainers  
Reviewers: `mcp`, `mcp-ext`, security, Agently UI

## Outcome

Establish immutable, reproducible source artifacts and golden fixtures for the
MCP `2026-07-28` wire target, MCP Apps, and Tasks before production code is
changed.

## Why this task exists

As of the proposal's review, the July identifier was in use by official SDKs,
while upstream TypeScript documentation still referenced `schema/draft/`. The
implementation must not generate from a moving branch or treat release-blog
examples as schemas.

## Inputs

- upstream MCP schema repository and exact reviewed commit;
- SEP-2575, SEP-2567, SEP-2260, SEP-2322, SEP-2243, SEP-2549, SEP-414;
- SEP-2133, SEP-1865, SEP-2663;
- authorization SEP-2468, SEP-837, SEP-2352, SEP-2207, SEP-2350, SEP-2351;
- stable or explicitly pinned MCP Apps and Tasks specifications;
- current `mcp-protocol/schema/2025-11-25` fixtures.

## Deliverables

### Provenance manifest

Add a machine-readable provenance manifest to `mcp-protocol` recording:

- protocol identifier;
- upstream repository;
- exact commit SHA;
- schema paths and SHA-256 hashes;
- draft/stable provenance classification;
- retrieval date;
- generator version and invocation;
- reviewed SEP set;
- license/provenance note.

Create equivalent version manifests for Apps and Tasks in `mcp-ext` when that
repository exists. Until task 007 creates it, keep reviewed fixture inputs in a
staging directory or branch that can be transferred without rewriting history.

### Golden fixtures

Capture canonical JSON for:

- `server/discover` request/result;
- per-request protocol, client-info, capabilities, and extensions metadata;
- `tools/list`, `tools/call`, and `resources/read` cache fields;
- `Mcp-Method` and `Mcp-Name` header agreement/disagreement;
- an `InputRequiredResult`, `requestState`, and resumed request;
- `subscriptions/listen` lifecycle;
- trace-context propagation;
- extension advertisement, deterministic mutual version selection, and unknown
  extension preservation/rejection;
- Roots, Sampling, and Logging deprecation annotations and retained behavior;
- authorization issuer validation/mix-up, credential binding, application type,
  refresh, discovery suffix, and scope step-up;
- hostile Origin/Host, DNS rebinding, forwarded/duplicate headers, request
  bounds, and July handling of legacy `Mcp-Session-Id`;
- Apps tool metadata, UI resource, initialization, input/result, tool call,
  cancellation, and teardown;
- Tasks create/get/update/cancel and every terminal state;
- legacy `2025-11-25` comparison fixtures.

Fixtures must include valid and invalid examples. Invalid examples need the
expected JSON-RPC/HTTP error and security disposition.

### Mechanism matrix

Create a reviewed table mapping each planned mechanism to:

- exact spec/SEP section;
- exact method and field names;
- transport applicability;
- required/optional status;
- compatibility behavior;
- owning implementation task.

The matrix must enumerate at least `server/discover`, MRTR/`requestState`,
`subscriptions/listen`, `Mcp-Method`/`Mcp-Name`, `ttlMs`/`cacheScope`, trace
carriers, generic SEP-2133 negotiation, authorization/transport controls, and
the Roots/Sampling/Logging deprecation set.

## Implementation steps

1. Resolve whether a versioned upstream July schema exists.
2. Select and review the exact commit; record differences from the release
   overview.
3. Hash and vendor/reference the schema using the repository's existing
   generation conventions.
4. Pin Apps and Tasks independently; do not assume matching release numbers.
5. Produce valid and negative fixtures from the pinned source.
6. Review every named mechanism and authorization SEP.
7. Amend the parent proposal and downstream tasks if names or semantics differ.
8. Add CI that recomputes every vendored-input hash and fails unless a changed
   input is accompanied by a reviewed manifest update.

## Tests

- provenance hashes reproduce on a clean checkout;
- generator invocation produces byte-equivalent generated input;
- fixtures parse with named, version-pinned upstream SDKs or conformance tools
  recorded in the manifest;
- invalid fixtures are rejected by at least one upstream implementation or
  conformance scenario;
- no fixture references an unpinned `main` or `draft` URL as its authority.
- licensed upstream conformance scenarios are vendored/referenced, or each
  licensing exclusion has a recorded disposition.

## Acceptance criteria

- Every mechanism enumerated in WS-0 has an exact pinned source.
- All unresolved upstream discrepancies are documented, not silently guessed.
- Generated-code tasks can run without network access using the recorded input.
- Security reviewers approve authorization, request-state, and transport
  fixtures.
- The parent plan accurately reflects the pin.

## Rollback

This task changes no runtime behavior. Reverting the provenance/fixture commit
must not modify existing generated `2025-11-25` code.

## Completion evidence

- Provenance manifest commit: _pending_
- Fixture test command/result: _pending_
- Upstream cross-check: _pending_
- Security review: _pending_
- Staged Apps/Tasks manifest transfer to task 007: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
