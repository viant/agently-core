/*
Package agui exposes durable AG-UI admission, continuation, projection, and replay.

The production ComponentStore invokes the private Manage Datly 1.0 capability.
Manage owns a serializable native transaction and composes transcribed run,
thread, event, lease, pending-run, and turn readers/writers. Generated writer
concurrency tokens supply atomic IfMatch guards; no direct SQL fallback exists.

Admission binds exact external identity to its authenticated principal and
retains the accepted input verbatim. A replay compares both the input hash and
canonical JSON, so an asserted matching hash cannot mask altered input. Fresh
external runs cannot admit the same nonempty internal turn twice. Continuation
CAS consumes one interrupted predecessor only in the same transaction that
admits the new run. ParentRunID describes spawning and never replaces PriorRunID.

Each journal append CAS advances Run.Revision and assigns contiguous per-run
sequence values while atomically changing lifecycle, pending data, or the
thread's separate state/message projection. Projection updates additionally
require ExpectedThreadRevision. Initial thread projection is null state and an
empty producer-owned message array; the caller supplies authoritative history.
Every accepted new admission advances an existing thread revision. Replay and
pending discovery use native read snapshots; replay supports pages up to 1000.

Claim and Renew govern journal observer ownership; they never start or repeat
agent execution. A separate lease revision prevents heartbeat updates from
changing the journal CAS revision. Claimed runs require Change.LeaseOwner on
all appends. Expired claims can be taken by a new observer, while its predecessor
cannot write further events. Runtime recovery remains the execution authority.

The wrapper retries only driver-coded transaction contention or admission
unique-key loss after one managed database unit confirms full rollback. It
never replays caller-pending, unknown, partial, or mixed transactions. Physical
schema provisioning adds protocol columns and indexes to conversation, run and
call_payload. Protocol-only conversation projections have internal UUIDs; exact
wire IDs remain separate and byte-preserved. Native execution runs and leases
remain independent. Journal payloads belong to protocol runs and cascade with
them; no separate AG-UI physical tables or transcript messages are required.
*/
package agui
