# Scheduler

Cron / interval / ad-hoc jobs run by the agently runtime. The scheduler lets
agents do useful work on a timer — nightly reports, cache warmups, periodic
syncs — with distributed lease coordination and per-user OAuth token replay.

## Packages

| Path | Role |
|---|---|
| [service/scheduler/](../service/scheduler/) | Service: schedule CRUD, trigger loop, lease management, token refresh |
| [pkg/agently/scheduler/](../pkg/agently/scheduler/) | Datly DAO — schedules, runs, failures |
| [schedule/](../schedule/) | Legacy schedule types (kept for compatibility) |
| [sdk/handler_datasources_bootstrap.go](../sdk/embedded_datasources_bootstrap.go) | Also wires scheduler at backend construction |

## Schedule shape

```yaml
id: nightly-cache-warm
kind: cron                 # cron | interval | adhoc
expression: "0 3 * * *"    # cron: crontab | interval: "5m" | adhoc: ISO timestamp
agentId: my-orchestrator
ownerUser: admin@example.com
query: "Warm caches for all active tenants"
templateId: ops-report     # optional
toolBundles: [ops-warm-tools]
timeoutMs: 1800000
enabled: true
```

## Distributed lease

Multiple agently instances can share the same schedule store:

1. Each instance polls `pending` schedules.
2. A candidate issues `LEASE` with a node id + lease TTL; DB-level atomicity ensures only one wins.
3. Winner runs the schedule; loser skips.
4. Lease auto-expires; dead nodes don't block subsequent runs.

## Token replay (owner identity)

Schedules execute under the **owner user's identity**:

- Owner's OAuth refresh token is held server-side (BFF session store).
- At trigger time, scheduler refreshes the access token, attaches it to `ctx`, and invokes the agent.
- MCP calls spawned by the agent naturally inherit the owner's token (see [doc/auth-system.md](auth-system.md) + [doc/mcp-integration.md](mcp-integration.md)).

This is how scheduled runs call the same MCP servers a user would — no service-account workaround.

## HTTP surface

- `GET /v1/api/agently/scheduler/` — list
- `POST /v1/api/agently/scheduler/` — upsert batch
- `PATCH /v1/api/agently/scheduler/{id}` — enable/disable, edit
- `DELETE /v1/api/agently/scheduler/{id}` — remove (owner-scoped)
- `POST /v1/api/agently/scheduler/{id}/run` — run now

## Multi-node deployment

When multiple agently-core processes share a schedule store the following
guarantees hold:

- **Exactly-once trigger per tick** — lease acquisition is a single atomic DB write; the winner runs, others skip.
- **Node-id liveness** — each instance registers a heartbeat; stale heartbeats (> `leaseTTL`) release their leases automatically, so a crashed node never stalls a schedule.
- **Token refresh coherence** — the owner's refresh token lives in the shared session/OAuth store, not on any individual node. Whichever node wins the lease refreshes and uses the fresh access token. On refresh, the updated access/refresh pair is written back to the store so later nodes skip the refresh round-trip.
- **Refresh-race tolerance** — two nodes that both refresh concurrently produce equivalent results: the IdP's refresh endpoint is idempotent for fresh tokens, and the store uses last-writer-wins with a refresh epoch counter. A momentarily-stale token retried by the losing node succeeds on the next cycle.
- **No ticking drift** — schedule triggers are anchored to the cron/interval expression interpreted in UTC, not to node-local clocks. Missed ticks during node outages are surfaced via `missedRuns` and optionally catch-up-run via config (`catchUp: false` by default — skip, don't burst).

## Failure handling

- Failures are recorded per-run with error text.
- Consecutive failures trigger exponential backoff before next attempt.
- `maxFailures` disables the schedule after N consecutive hits (operator re-enables).

## Extensibility

- **Custom trigger kind**: add a parser + evaluator to `service/scheduler/trigger/`.
- **Custom lease store**: satisfy `scheduler.Store` — the default uses Datly; a Redis impl is trivial.
- **Post-run hook**: subscribe to `ScheduleCompleted` events on the streaming bus.

## Related docs

- [doc/auth-system.md](auth-system.md) — token refresh path.
- [doc/agent-orchestration.md](agent-orchestration.md) — what actually runs.
