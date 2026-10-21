# Cleanup / heartbeat: local MySQL regression

This test verifies that **cleanup no longer blocks its own heartbeat** after
the stage 1 lease check was moved outside the deletion transaction. It runs
the real worker against synthetic data and does not contact production.

## Run

From the Core checkout:

```sh
bash script/mysql/repro_cleanup_heartbeat.sh
# Repeat all three scenarios five times, bypassing the Go test cache:
bash script/mysql/repro_cleanup_heartbeat.sh 5
```

Requires local Docker, `openssl`, `lsof`, and Go 1.25.8+. The launcher also detects
a cached Go 1.25 toolchain under the default GOPATH; otherwise set `GO_BIN` to
the Go executable. It does not run `go mod tidy` or update dependencies.

The launcher creates a new `mysql:8.0.39` container, bound only to
`127.0.0.1:3308`, with a new `cleanup_heartbeat_repro_local` database and imports
the canonical `schema_versioned.ddl` migrations. Only the quoted database name
in `CREATE`/`USE`/`ALTER DATABASE` is rewritten in memory to target the dedicated schema; the flat
`schema.ddl` is not sufficient for the current cleanup readers. The launcher
refuses an occupied port and non-local Docker endpoint.
It never connects to the existing development MySQL on 3307 or to production.
Generated credentials are not printed. On completion, failure, or Ctrl-C, it
removes only its own labeled container and disposable fixture. No persistent
database volume is used. SIGKILL cannot run the exit trap; any leftover container
has the unique `agently-cleanup-heartbeat-` name printed at startup.

## Scenarios and evidence

The real maintenance worker uses the real native Datly data service and a small
old scheduled run, conversation, turn, and two messages. An unrelated control
conversation and the schedule definition must survive every scenario.

| Scenario | Settings | Required result |
| --- | --- | --- |
| Fast delete | No trigger delay; first heartbeat after 5 s | Successful deletion before first heartbeat |
| Forced overlap | 10 s message-delete delay; heartbeat 1.1 s | Successful renewal while DELETE is still executing; successful commit |
| Slow control | Same 10 s delay; first heartbeat after 30 s | Successful deletion |

A test-only `BEFORE DELETE` trigger sleeps for exactly one fixture message ID.
No sleep or timing override is added to the application. The lease TTL is 60 s,
the policy timeout is 30 s, and `innodb_lock_wait_timeout=3` is applied through
the DSN to every runtime connection. The runtime pool allows eight connections;
an independent observer pool reads diagnostics, so pool starvation is not used
to manufacture a failure.

In the overlap scenario, a forwarding observer delays the first real renewal
until MySQL reports the cleanup transaction sleeping inside the message DELETE
trigger. This barrier no longer depends on the removed lease lock. The test
requires successful renewal before that same transaction leaves `DO SLEEP(10)`,
zero locks on `maintenance_lease` held by the cleanup transaction, and no
same-worker lease lock wait. It then requires a successful cleanup commit.
Missing permissions/evidence, early completion, renewal errors or rollback fail
the test. Connection attributes identify only this test's runtime.

Look for `PROVEN heartbeat renewed during slow DELETE`, `deleted=1`, `failed=0`
and `error=<nil>`. **PASS now means the regression is fixed**, unlike the original
reproduction whose PASS required error 1205. Both controls must also pass.
Before checking effects or removing fixtures,
the test waits for the server-side transaction to finish, including a possible
remaining `SLEEP` after the client connection was canceled. A secondary lease
release timeout can occur during that interval and is included in the pass log.

The overlap interval exceeds the database clock's one-second precision: a
sub-second renewal can write identical timestamps and report zero changed rows.
This avoids confusing a no-op UPDATE with contention; production intervals are
unchanged. The fast control intentionally uses 5 s. Deterministic unit
tests separately exercise pass completion during an in-flight Renew: the
worker's own shutdown cancellation is ignored, but parent cancellation,
lease loss and SQL errors (including joined cancellation/SQL errors) survive.

The integration test is skipped without `AGENTLY_TEST_CLEANUP_HEARTBEAT_DSN`.
When explicitly provided, it accepts only the exact dedicated TCP address and
database above, requires empty cleanup tables and serializes invocations using
a named advisory lock. Do not point it at a database containing useful data.
The DSN safety unit test runs without MySQL:

```sh
go test -mod=readonly ./service/maintenance -run '^TestCleanupHeartbeatDSNSafety$'
```

## Stage 2: concurrent cleanup and non-locking reads

```sh
CLEANUP_REPRO_STAGE2=1 bash script/mysql/repro_cleanup_heartbeat.sh
```

Also runs the three heartbeat cases, then the existing MySQL maintenance and
manual-delete regressions. Stage 2 adds real native transactions proving:

- normal second acquisition skips; forced lease takeover admits two operations
  on the same tree, with an observed InnoDB write wait;
- only one operation reports the deleted tree; both finish successfully;
- captured runtime SQL uses READ COMMITTED, without FOR UPDATE, FOR SHARE or
  SERIALIZABLE, even when those flags are inherited by the caller;
- a partially matched payload batch tolerates a disappeared row, preserves a
  newly added reference and counts only actual deletes;
- a guarded anchor disappearing during deletion rolls back, rebuilds its plan
  and finishes as a no-op without reporting false progress;
- a real forced InnoDB deadlock rolls back cleanup, checks the lease again,
  replans and commits its second attempt.

The overlap test temporarily enables the **disposable server's** general query
log and restores its previous settings. Stage 2 connections allow 15 seconds
of normal write waiting so they can overlap the 10-second trigger; the original
heartbeat cases retain their 3-second timeout. The harness-only advisory lock
serializes separate test launches; application cleanup does not acquire it.
