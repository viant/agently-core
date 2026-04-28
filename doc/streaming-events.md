# Streaming & event bus

A single in-process bus (`runtime/streaming.Bus`) fans out events from the
reactor, tool executor, and async operation manager to any subscriber — most
commonly the HTTP Server-Sent Events endpoint that keeps the UI live.

## Core types

- [runtime/streaming/event.go](../runtime/streaming/event.go) — canonical `Event` type + known `EventType` constants.
- [runtime/streaming/bus.go](../runtime/streaming/bus.go) — pub/sub bus; subscribers receive a fan-out copy.
- [sdk/stream_tracker.go](../sdk/stream_tracker.go) — higher-level stateful tracker that reduces events into canonical snapshots for clients that need aggregated state.
- [sdk/stream_event_meta.go](../sdk/stream_event_meta.go) — metadata envelopes carried on every event (turn id, message id, conversation id).

## Producers

| Source | Emits |
|---|---|
| Reactor loop | `turn_started`, `message_appended`, `plan_step`, `final_response`, `turn_completed` |
| Tool executor | `tool_started`, `tool_completed`, `tool_feed_active`, `tool_feed_inactive` |
| Async operation manager | `async_op_started`, `async_op_updated`, `async_op_completed` (see [doc/async.md](async.md)) |
| Elicitation | `elicitation_requested`, `elicitation_resolved` |
| Skill lifecycle | `skill_started`, `skill_completed` |

## Consumers

- **HTTP SSE** at `GET /v1/stream` — handlers live in [sdk/handler_common.go](../sdk/handler_common.go) / [sdk/http.go](../sdk/http.go).
- **In-process reducers** — `canonical_*.go` files in `sdk/` reduce events into state snapshots (see [doc/conversation-model.md](conversation-model.md)).
- **Feed notifier** — bridges `tool_feed_*` events to the UI feed registry ([sdk/feed_notifier.go](../sdk/feed_notifier.go)).
- **Debug** — [sdk/debug.go](../sdk/debug.go) logs every event when `AGENTLY_DEBUG_STREAM` is set.

## Subscribing from Go

```go
sub, err := bus.Subscribe(runtime.StreamFilter{ConversationID: id})
defer sub.Close()
for ev := range sub.Events() { /* ... */ }
```

The filter narrows by conversation / turn id; unfiltered subscriptions receive every event. The bus is back-pressured via bounded per-subscriber channels; slow subscribers are dropped (logged) rather than blocking producers.

## Delivery semantics

- **At-most-once per subscriber.** No retries, no persistence. Events that matter long-term are persisted separately (turns, messages, tool calls, async operation records).
- **Ordering is per-producer, not global.** Within a single reactor turn the order is deterministic; cross-turn ordering needs a correlation id.
- **Reconnect**: HTTP SSE clients resume via `Last-Event-ID` against the canonical transcript; events missed while disconnected are recovered from the persisted reducer snapshot, not replayed from the bus.

## Extensibility

- **New event type**: add a constant in [runtime/streaming/event.go](../runtime/streaming/event.go), emit via `bus.Publish(Event{Type: ...})`; SDK reducers pick it up only if explicitly registered.
- **New reducer**: mirror the pattern in `sdk/canonical_reducer.go` — reducers are small pure functions `(prev State, ev Event) → State`.
- **Test harness**: [runtime/streaming/bus_test.go](../runtime/streaming/bus_test.go) shows the pattern for asserting on emitted events.

## Related docs

- [doc/conversation-model.md](conversation-model.md) — how events become durable transcript state.
- [doc/async.md](async.md) — event flow for long-running operations.
