# 011 — Upgrade Canonical Transcript and Historical Restoration

Status: planned  
Parent workstream: WS-6  
Primary repositories: `agently-core`, `agently`  
Depends on: [009](009-apps-server-and-mcp-ui-compatibility.md), [010](010-agently-apps-host.md)

## Outcome

Persist enough immutable invocation evidence to reconstruct the correct App
when a user reopens conversation history, including repeated calls that share
one resource URI. Keep legacy transcripts readable without inventing durable
identities that were never stored.

## Canonical snapshot

At tool-call time persist, subject to redaction and size policy:

- protocol and negotiated Apps versions;
- tool name plus the declaration/UI metadata used for that invocation;
- input and complete `CallToolResult`, or durable payload references;
- resource URI plus immutable resource content/version identity;
- conversation ID, tool-call ID, view UUID, timestamps, and outcome;
- provenance and compatibility-adapter version.

Resource URI is not an instance ID. Two calls to the same tool/resource create
two distinct views.

## Entry decisions

Before schema work, record the server-issued versus host-local `viewUUID`
policy and the historical resource policy (archive original content, resolve
current content, or run a declared migration) in the parent proposal.

## Deliverables

- versioned transcript schema and migration/read-time projection;
- capture at the authoritative MCP invocation boundary;
- associate the call with registered tool `resourceUri` before result arrival
  and remove shallow response-payload URI discovery from the preferred path;
- external payload and resource-content archival with integrity, authorization,
  retention, and orphan cleanup; canonical transcripts store references/hashes,
  never inline HTML;
- restoration projection that supplies both input and complete result to Apps;
- legacy projection that uses only available evidence and exposes degraded
  restoration explicitly;
- resource-version resolution with safe behavior when content is unavailable;
- compatibility tests across retained transcript versions.

## Implementation steps

1. Inventory current transcript records and every read/write path.
2. Define the canonical snapshot schema, redaction map, and payload thresholds.
3. Capture tool declaration metadata before server definitions can change.
4. Write external payload first, then atomically commit its integrity-bound
   reference with invocation outcome; sweep uncommitted orphan payloads.
5. Build current and legacy read projections into the Apps host lifecycle.
6. Add resource-version lookup and explicit unavailable/degraded states.
7. Backfill only fields derivable without guessing; leave synthetic IDs local
   to the rendered session and label them non-durable.

## Tests

- A/B/A history navigation restores the correct distinct instances;
- browser refresh recreates identical initial App input/result;
- repeated tool/resource calls across turns and conversations do not collide;
- success, error, cancellation, large referenced payload, and redacted fields;
- server metadata changes after the original invocation;
- legacy transcripts with missing input/result/resource metadata;
- deleted, expired, unauthorized, or integrity-failed payload references;
- projection into both official Apps and the temporary legacy adapter.
- registered metadata wins over shallow response URI lookups; fallback use is
  explicit and measured.

## Acceptance criteria

- Clicking history restores the invocation represented by that transcript item,
  not the latest resource or another call's state.
- Complete `CallToolResult` semantics survive persistence and restoration.
- Legacy records remain readable and degraded behavior is visible.
- No view identity is derived from resource URI alone.
- Authorization is rechecked when referenced content is loaded.
- Redacted/unavailable content restores as an explicitly degraded state rather
  than claiming a complete result.

## Rollout and rollback

Dual-read old and new transcript versions; write the new version behind a flag
until validation passes. Rollback stops new writes but retains the reader and
does not destroy newly captured evidence.

Observe transcript version, restore source, degraded/legacy projection, and
payload-reference failures. Promote new writes only after the A/B/A, refresh,
legacy-corpus, and payload-integrity gates pass.

## Completion evidence

- Transcript schema/migration: _pending_
- A/B/A restoration test: _pending_
- Legacy corpus result: _pending_
- Payload security test: _pending_
- Fable 5 review: initial `REVISE`; corrections applied; final `APPROVE` (2026-08-03)
