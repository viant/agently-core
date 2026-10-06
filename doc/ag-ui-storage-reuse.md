# AG-UI storage in existing tables

AG-UI persistence uses `conversation`, `run`, and `call_payload` for protocol projections, run admission and ordered journal events. Protocol and native execution state have distinct ownership within the application's existing persistence graph.

Schema changes are additive for existing application records. Existing conversations default to `protocol_only=0`; existing runs default to `run_kind='execution'`. Protocol runs have `run_kind='agui'` and their own `protocol_status`, revision, admission keys and lease fields. Their native status and execution checkpoint fields are separate. Execution readers, writers and recovery must filter by execution kind.

Public opaque IDs are stored as binary values; exact-byte SHA-256 identity keys provide uniqueness independent of database text collation. A protocol thread may bind an existing authorized native conversation or use a separate internal conversation ID. Native turns are reserved through `protocol_turn_id`, without requiring an already-created turn foreign key.

Journal events use `call_payload.kind='agui.event'`, inline JSON, and `(run_id, sequence)` uniqueness. `run_id` references the protocol run with cascading deletion. Existing payload classes and native transcript records remain unchanged.

DQL under `dql/` is authoritative for application readers and writers. Outer view projections prefer wildcards. Explicit projections express logical aliases, binary conversions, keys, concurrency metadata, or a restricted mutation view. Generated Go and SQL are produced through `endly -t=transcribe` from `e2e/datly`; they are not edited to repair authoring errors. Schema provisioning DDL remains in the existing SQLite/MySQL bootstrap and upgrade files.

## Validation on 2026-10-05

- SQLite: public `Ensure` upgrades the pre-protocol schema twice, preserving conversation, turn, message, run checkpoint, payload and report records. Foreign-key validation passes and the four POC tables are removed.
- MySQL 8.0.46: both current core and Steward versioned schemas upgrade from version 40 to 41 and execute twice successfully in disposable databases. Original conversation, turn, run checkpoint and tool payload records are preserved. The repeatable test enables session foreign-key checks explicitly and verifies byte-exact opaque IDs, journal sequence uniqueness and cascading event deletion.
- MySQL protocol event insertion succeeds; a raw ID with a trailing space remains byte-exact. Deleting its protocol run removes its event, within a rolled-back verification transaction.
- MySQL `EXPLAIN` uses `ux_payload_run_sequence` for replay, `idx_run_protocol_scope` for scoped status lookup, `idx_run_protocol_turn` for global authorized turn lookup with a representative population, and `idx_run_protocol_recovery` for ordered global recovery. Recovery needs no filesort. Existing native indexes are retained.

These schema checks do not replace the generated Datly store, SDK identity, deletion, and continuation integration tests. Those are required before restarting the local application against the new schema.

The full Endly transcribe workflow subsequently passed, including all leaf reader/writers and the three base authoring components. The current store and manage suites pass. Real MySQL Datly acceptance also passes concurrent lease claims, renewal, revision fencing, interrupt continuation admission, exact trailing-space thread identity and journal replay against a disposable upgraded schema. SQL was used only to provision the test schema; application operations used native Datly components.

Repeat the additive MySQL schema check with `python3 tools/schema/test_mysql_protocol_reuse.py CONTAINER` on a local test container exposing `mysql` and `MYSQL_ROOT_PASSWORD`. It creates unique disposable databases for each schema variant and removes them afterward; credentials stay inside the container process environment.

Authored-source policy: transcription overwrites generated Go/SQL, including any manual edits to those files. The redundant `regeneration` workflow has been removed; use `transcribe` directly. Handwritten lifecycle hooks and custom component code remain authored sources and are not generated-file edits.
