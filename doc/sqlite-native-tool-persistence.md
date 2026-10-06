# Native SQLite tool persistence

A parallel tool batch exposed two independent persistence failures:

- The provisioned file-backed SQLite connector used shared cache and deferred transactions. Concurrent Datly read/validate/write transactions could deadlock on table locks or fail when upgrading a read snapshot to a writer.
- Tool request-payload persistence ignored the error from linking the new payload to its tool-call row. The executor could therefore run a tool without a durable request reference.

Both patterns existed in the repository baseline; the evidence does not establish that AG-UI introduced them.

Provisioned file-backed databases now use private cache, WAL, the existing busy timeout, and `_txlock=immediate`. The driver reserves the writer slot when the Datly-managed transaction starts, before validation reads. Datly retains commit/rollback ownership. The connection pool remains two, and external tool execution remains parallel. No statement retry, transaction replay, new writer wrapper, schema change, or business-tool retry is introduced.

Explicit caller DSNs are unchanged. Shared-cache in-memory databases are unchanged because their connections must address the same in-memory database. The sqlite3 DSN conversion preserves `_txlock=immediate` while translating existing pragma parameters. The default modernc driver honors read-only transaction options without applying its writer begin mode. Native Datly GETs continue to use their existing reader path; the regression continuously reads the transcript during writes. The alternative sqlite3 driver's existing `ReadOnly` option limitation is unchanged; this change does not introduce a new explicit read transaction.

SQLite documents shared-cache table-lock failure and discourages shared-cache mode for ordinary applications: [SQLite shared cache](https://www.sqlite.org/sharedcache.html). The configured driver exposes transaction lock mode through `_txlock`: [modernc SQLite driver](https://pkg.go.dev/modernc.org/sqlite).

Request payload linking now propagates its original error. A request persistence failure stops before the tool body and remains an infrastructure error, so a partial record cannot authorize model continuation. Terminal cleanup remains best-effort and cannot erase the original cause.

Both in-memory conversation clients now accept sparse updates of an existing tool call by message ID, matching the native writer. New calls still require their operation identity. Surfacing the formerly ignored link error exposed this pre-existing test/runtime parity issue.

## Validation

The native reproduction uses a private database created through the application provisioner, the linked Datly runtime, the real conversation service and model-call recorder, two concurrent transcript readers, and fifteen concurrent tool executor calls per round. The tool bodies wait until all fifteen have entered before returning; this proves database coordination does not serialize the external tool phase. Three rounds verify forty-five distinct completed tool rows, each with both request and response payload references.

Before the change, the native writer reproduction failed immediately with the observed SQLite deadlock code 6. Private cache alone failed with busy/snapshot codes 5/517. Immediate transactions with shared cache failed with shared-cache lock code 262. The combined policy passed, then the full executor regression passed three repetitions on both supported SQLite drivers (270 tool calls, each invoked once).

```sh
go test ./sdk -run '^TestNativeParallelToolPersistenceWithTranscriptReaders$' -count=3 -timeout=120s
go test ./app/store/native ./app/store/data ./internal/service/conversation ./internal/service/sqlite ./service/core/modelcall ./service/shared/toolexec -count=1 -timeout=180s
```

The targeted request-link failure regression verifies the original error is retained and the business tool invocation count remains zero. Connector tests cover generated DSNs, sqlite3 translation, explicit caller configuration, and in-memory policy preservation.
