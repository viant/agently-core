# Native runtime startup profile

This probe measures `native.New` from entry until the initial runtime generation
is ready. It excludes process launch, Go compilation, database schema/data
provisioning, shutdown, and first-request component materialization. It is not an
end-to-end application startup measurement.

Each iteration opens a fresh, empty in-memory SQLite database. A SQLite
authorizer records operations after connection initialization, so unexpected
runtime schema discovery or queries are visible in the JSON output. Filesystem
caches remain warm between iterations; the first sample is reported separately
in the ordered `startup_ms` array.

Run from the Core checkout with CGO enabled (the probe uses SQLite's authorizer):

```sh
go build -o /tmp/agently-startup-profile ./e2e/startup/cmd/profile
/tmp/agently-startup-profile -root "$PWD" -n 5
/tmp/agently-startup-profile -root "$PWD" -n 8 -cpuprofile /tmp/agently-startup.cpu.pprof
go tool pprof -top -cum /tmp/agently-startup-profile /tmp/agently-startup.cpu.pprof
```

Use unprofiled runs for elapsed-time comparisons. The CPU profile covers the
whole initialization/shutdown loop; `startup_ms` excludes shutdown. Compare the
same machine, connector, sample count, source tree, and workload. Startup alone
does not prove first-query performance or reader/writer correctness.
