# AG-UI storage in existing tables

AG-UI persistence uses `conversation`, `run`, and `call_payload` for protocol projections, run admission and ordered journal events. Protocol and native execution state have distinct ownership within the application's existing persistence graph.

Schema changes are additive for existing application records. Existing conversations default to `protocol_only=0`; existing runs default to `run_kind='execution'`. Protocol runs have `run_kind='agui'` and their own `protocol_status`, revision, admission keys and lease fields. Their native status and execution checkpoint fields are separate. Execution readers, writers and recovery must filter by execution kind.

Public opaque IDs are stored as binary values; exact-byte SHA-256 identity keys provide uniqueness independent of database text collation. A protocol thread may bind an existing authorized native conversation or use a separate internal conversation ID. Native turns are reserved through `protocol_turn_id`, without requiring an already-created turn foreign key.

Journal events use `call_payload.kind='agui.event'`, inline JSON, and `(run_id, sequence)` uniqueness. `run_id` references the protocol run with cascading deletion. Existing payload classes and native transcript records remain unchanged.

DQL under `dql/` is authoritative for application readers and writers. Outer view projections prefer wildcards. Explicit projections express logical aliases, binary conversions, keys, concurrency metadata, or a restricted mutation view. Generated Go and SQL are produced through `endly -t=transcribe` from `e2e/datly`; they are not edited to repair authoring errors. Schema provisioning DDL remains in the existing SQLite/MySQL bootstrap and upgrade files.
