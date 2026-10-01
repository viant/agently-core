# Generated deletion cannot preserve legacy missing-ID no-op semantics

Status: implemented locally after explicit user authorization; native and legacy comparisons pass.
The opt-in is onDeleteNotFound=ignore, authored as delete_not_found(view,'ignore').
A GitHub issue POST returned HTTP 403; no remote issue was created.
Examined upstream Datly v1: `8220787014680a2c71dcd1ab8552feb1b3cd6766`.
Local tested Datly: `0e4454a484d4667a93320caccbc3bd614a3d8b57`.

## Observable behavior

The legacy Agently session DELETE accepts requested IDs that do not exist.
It deletes matching sessions, acknowledges all requested IDs, and succeeds.
Generated PATCH deletion using `delete_marker` requires every ID to match
Previous. Missing IDs produce `delete requires a matched complete identity`
before queueing, so a mixed request rejects the entire operation.

| Submitted identities | Legacy remaining rows | Generated remaining rows | Generated outcome |
| --- | --- | --- | --- |
| existing `s1` | `s2` | `s2` | success |
| missing `absent` | `s1,s2` | `s1,s2` | error |
| `s1,absent` | `s2` | `s1,s2` | error |

`datlyv1/tests/session_delete_evidence_test.go` executes both real runtimes
against independent SQLite fixtures, checks legacy acknowledgment, and reads
persisted state through each runtime's session reader. Expected divergence is
explicitly asserted; a passing investigation is not a passing migration gate.
Session deletion remains unlinked and is not included in the verified count.

## Native capability needed

An explicit opt-in idempotent deletion policy for generated writers must allow
a complete, supplied identity without a matching authorized Previous row to
become a no-op. Keep strict matching as the default. Do not interpret omission,
incomplete identities or arbitrary absent collections as deletion.

Requirements:

- Transcribe the policy from DQL into immutable view/role metadata.
- Preserve identity completeness, authorization scope and parent/link checks.
- A no-op must never insert, perform unscoped DML or delete out-of-scope rows.
- Preserve atomic mixed-batch execution and caller-owned transaction behavior.
- Preserve concurrency-token and active mutation-predicate conflicts; do not
  globally swallow guard failures or validation errors.
- Define nested/parent deletion behavior and return/output semantics explicitly.
- Verify pure-missing, mixed, compound identities, scope exclusions, malformed
  identity, duplicate IDs, rollback, caller transactions and regeneration.

Do not patch this with application SQL, a manual DML loop, edits to generated
contracts, or an external read/filter/write sequence that changes transactional
semantics. The new public/caller adapter may map the generated result to legacy
ID acknowledgments once native mutation semantics are established.

## Reproduction

```sh
cd agently-core-v1/datlyv1
go test ./tests -run TestSessionDeletionCompatibilityEvidence -count=1 -v
```

The test reports current matching/differing behavior. Runtime owner:
`runtime/handler/writer/handler.go` rejects a marked delete when Previous is
missing or identity is incomplete. Upstream v1 retains the same check and has
no corresponding missing-delete policy in view metadata.

## Implemented resolution

Strict matching remains default. The opt-in applies to leaf roles and cannot
relax incomplete identities or guarded views. Missing rows skip hooks and DML.
Six real-runtime legacy comparisons cover empty/repeated/known/missing/mixed
IDs and rollback. Native generation tests also cover scope exclusion and retained
mutation guards. The draft component is now verified and privately linked.
