# Concurrent AG-UI journal history

Status: approved by the user on 2026-10-02. The user selected atomic updates
that save only the messages a run actually changed. Implemented using the
existing Datly thread revision CAS and shared JSON column; no new tables.

## Confirmed defect

Two protocol observers on one conversation can currently lose accepted history.
The native Datly store and actual journal writer reproduced this sequence:

1. Run A publishes `a-answer = prefix`.
2. Run B seeds that history and publishes its own `b-answer = b`.
3. A publishes `a-answer = prefix suffix`.
4. B publishes `b-answer = b second`.
5. The saved thread incorrectly reverts `a-answer` to `prefix`.

There is no CAS conflict in this sequence. The writer merges its entire cached
message graph over the latest thread graph before applying each event, so
unchanged inherited messages become writes. Atomic transactions alone cannot
prevent this semantic loss.

## Proposed change

Keep a run's local reduction baseline distinct from the canonical shared thread
history. Compute the message changes attributable to each accepted event batch
against that local baseline. Apply only those changed message identities to the
latest server-owned thread graph within the existing Datly transaction and
thread revision CAS. An unrelated observer's updated messages are preserved.

The journal's public snapshots must contain the resulting canonical graph,
while the run-local baseline retains the producer's reduction state. Restoring
an observer reconstructs its local reduction/checkpoint from its own journal
and the native causal identities; it must not interpret unrelated inherited
messages as edits. Explicit full-history replacement must be separately declared
and revision-checked, rather than inferred from a convenience text snapshot.
Conflicting changes to the same message must reject or reconcile against the
actual native owner; last-writer-wins on copied stale history is insufficient.

This uses the already approved protocol thread/run/event records and Datly 1.0
components. It requires no new database or legacy SQL path. Saved history stays
server authoritative. The change is substantial because it changes shared
projection ownership and recovery behavior, so consultation is required.

## Evidence required

Preserve the exact reproduced A/B sequence; several interleaved messages and
argument/result updates; genuine same-message conflicts; canonical snapshot
replay; observer restart; atomic late failure rollback; and native queued runs
on one conversation. Run native SQLite and MySQL concurrent acceptance against
the same Datly components. Existing single-run and all31 reducer fixtures must
remain valid. Do not mark this requirement complete until these checks pass.

## Implemented evidence

The writer retains its producer-local accepted message baseline. Each batch
merges only changed message identities into the latest canonical thread graph.
Same-message conflicts reject; inherited messages are not writes. Public message
snapshots contain the canonical merged graph. The shared messages and journal
commit together through the existing Datly transaction/revision guard.

Mandatory native SQLite tests cover the reproduced interleaving, same-message
conflict, local projection/baseline rollback, canonical snapshots, recovery and
late journal-write failure. An actual MySQL 8.4 acceptance test uses two independent
Datly runtimes and verifies the same interleaving plus canonical snapshot replay.
Those checks passed. No per-message SQL fallback or separate history database was
introduced. Higher-level HTTP/queued interleaving remains a separate audit gate.
