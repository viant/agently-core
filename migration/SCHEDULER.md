# Scheduler migration

The canonical schedule reader now represents public get/list and trusted
internal scheduler listing. Optional identity predicates retain legacy lookup
behavior. Required host providers `scheduleaccess/internal` and
`visibility/subject` own internal access and the verified subject, respectively.
Anonymous subjects are supplied explicitly as an empty pointer value. Public
scope excludes internal schedules and allows non-private or owned schedules;
internal scope intentionally matches the legacy run-due listing. Scope checks
remain outside optional predicates and cannot be supplied by query parameters.

Eight real legacy/new comparisons check full fields and identities for anonymous,
owner, other owner, internal scheduler, cross-owner get, own private get, hidden
internal get and missing identity. Both required providers are checked for
failure when absent. The baseline is isolated in migration/legacyprobe and uses
the original components. The new reader is privately linked. No authored Go
persistence or SQL execution is introduced.

The schedule writer is now transcribed and privately linked. Authored input Init
uses generated typed read indexes to allocate missing UUIDs before identity
freezes. Its entity lifecycle owns owner/visibility defaults, timestamp defaults,
missing legacy-owner backfill and clearing next_run_at when schedule definition
changes. The generator owns validation, sparse mutation and transactions. A
required verified visibility subject supplies the effective user explicitly.

27 real legacy/new cases compare stored fields and successful response bodies,
including omission, NULL, zero, no-op collections, UUID allocation, timestamps,
all schedule-change rules and database constraints. Both versions reject new
rows with nil internal; valid inserts explicitly supply false, preserving the
actual legacy contract rather than assuming the database default applies. Two
additional checks preserve caller commit/rollback ownership.

Seven legacy/new deletion cases now reuse the same writer: empty/repeated/blank/
missing/mixed identities, run cascades and a late rejection restoring parents
and runs. The business input hook skips empty delete identities, matching legacy
acknowledgments; other deletion mechanics use the native marker and leaf
onDeleteNotFound=ignore policy. Two more caller transaction checks prove cascade
commit/rollback. There is no separate generated schedule delete component.

Run leases, run listing,
run fact cube and actual service caller cutover remain pending. Passing reader
fixtures is not proof that scheduled execution runs through Datly 1.0. OAuth
fixtures pass separately; live IdP validation still requires clarification of the
missing requested basic-secret path. No production jobs are executed.

## Lease baseline before native guard integration

The isolated original claim/release components have 11 captured fixture cases in
`schedule-lease-legacy-baseline.json`. They cover unleased/disabled schedules,
expired and live competitors, same-owner renewal, identical own leases, missing
schedules, matching/mismatched release and release while disabled. These observations now have full-field native comparisons through the canonical
schedule writer. Actual scheduler service cutover is still pending.

The SQLite nominal expiry-boundary case is explicitly qualified: stored SQL
timestamp text and modernc's Go-time argument representation can compare
unequal even for the same instant. The observed legacy claim succeeds in that
fixture. Native tests must separate timestamp representation behavior from
strict semantic expiry and exercise values written by both driver paths.

## Native schedule leases

The canonical writer accepts optional leaseMode/leaseOwner/leaseNow predicate
inputs alongside a single schedule identity/body. The business input hook
validates the lease request, freezes a default clock, and excludes unknown
schedule identities so PATCH cannot create a missing lease target. Entity Init
limits lease operations to lease_owner/lease_until, preserving other columns
and the legacy timestamp behavior. The compiled business LeasePredicate uses
the existing predicate.Handler contract; native mutation_predicate applies its
criteria at UPDATE execution. No product SQL execution was authored.

The output LeaseResult reports the operation result. Root recovery handles a
known guard loss as false and uses the generated canonical reader for the
legacy already-owned/unexpired fallback. The engine retains actual rollback
evidence and owns transaction completion. Eleven real legacy comparisons pass,
including identical own leases, empty/missing state and the recorded SQLite
representation boundary. Four independent-connection fixtures change owner,
disable or delete a schedule after Current lookup and prove the guard preserves
the competing state. MySQL-specific zero-affected-row execution has not been
run against a live MySQL instance.

Normal write/deletion cases still pass when the optional lease predicate is
omitted. Datly a3ce362d corrects the inactive optional-group deletion policy;
active criteria and concurrency tokens remain strict. The stock transcribing
CLI is built with a temporary Go overlay adding only the compiled business
model import. It still runs datly transcribe and does not copy or edit framework
command sources. Full Endly validation passed for this slice: 60/60 contracts, regeneration,
boundaries, nested tests (280.627 seconds), and runtime build.
