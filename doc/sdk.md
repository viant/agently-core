# SDK surface

Three SDK flavours share one contract (`Client` interface):

1. **Embedded** — in-process Go; direct service calls, fastest.
2. **HTTP** — Go client over HTTP; exercises the same endpoints browser clients use.
3. **Mobile** — Swift (iOS) and Kotlin (Android); mirror the Go `Client` method-for-method.

## Packages

| Path | Role |
|---|---|
| [sdk/client.go](../sdk/client.go) | The canonical `Client` interface (single source of truth) |
| [sdk/embedded.go](../sdk/embedded.go) | In-process `backendClient` built from an `executor.Runtime` |
| [sdk/http.go](../sdk/http.go) | `HTTPClient` with `doJSON` transport helpers |
| [sdk/handler*.go](../sdk/handler.go) | HTTP route → backend method glue (runs `Client` methods server-side) |
| [sdk/api/](../sdk/api/) | Wire types (request/response DTOs) |
| [sdk/canonical_*.go](../sdk/canonical.go) | Event → state reducer shared by every client flavour |
| [sdk/ts/src/](../sdk/ts/src/) | TypeScript client (hand-written, 1:1) |
| [sdk/ios/Sources/AgentlySDK/](../sdk/ios/Sources/AgentlySDK/) | Swift client |
| [sdk/android/src/main/java/com/viant/agentlysdk/](../sdk/android/src/main/java/com/viant/agentlysdk/) | Kotlin client |

## Construction

| Mode | Builder |
|---|---|
| Embedded | `NewBackendFromRuntime(rt)` — wires every service including the datasource stack ([doc/lookups.md](lookups.md)) |
| HTTP | `NewHTTP(baseURL, opts...)` |
| Local HTTP (tests) | `NewLocalHTTPFromRuntime(rt)` — spins up the HTTP server in-process so tests exercise the wire contract |

---

## Using the client

Every code example below does the same three things: open a conversation,
send a query, stream events. The API surface is intentionally identical
across platforms.

### Go — HTTP client

```go
import (
    "context"
    "log"

    agentlysdk "github.com/viant/agently-core/sdk"
    "github.com/viant/agently-core/sdk/api"
    agentsvc "github.com/viant/agently-core/service/agent"
)

ctx := context.Background()
client, err := agentlysdk.NewHTTP("http://localhost:8585")
if err != nil { log.Fatal(err) }

// 1. Create a conversation.
conv, err := client.CreateConversation(ctx, &agentlysdk.CreateConversationInput{
    AgentID: "orchestrator",
    Title:   "Data exploration",
})
if err != nil { log.Fatal(err) }

// 2. Send a query. Response includes the final assistant message; stream
//    the events separately if you want incremental output.
out, err := client.Query(ctx, &agentsvc.QueryInput{
    ConversationID: conv.ID,
    Query:          "Summarize Q4 performance",
})
if err != nil { log.Fatal(err) }
log.Printf("assistant: %s", out.Content)

// 3. Stream events (tool calls, token deltas, elicitations, etc.).
sub, err := client.StreamEvents(ctx, &agentlysdk.StreamEventsInput{
    ConversationID: conv.ID,
})
if err != nil { log.Fatal(err) }
defer sub.Close()
for ev := range sub.Events() {
    log.Printf("event type=%s turn=%s", ev.Type, ev.TurnID)
}

// 4. Feature: fetch rows from a datasource-backed picker.
rows, err := client.FetchDatasource(ctx, &api.FetchDatasourceInput{
    ID:     "advertiser",
    Inputs: map[string]interface{}{"q": "acme"},
})
```

### Go — Embedded client

Same `Client` interface, same methods, no HTTP:

```go
rt := executor.New(/* ... configure runtime ... */)
client, err := agentlysdk.NewBackendFromRuntime(rt)   // implements Client
```

Callers should NOT care which flavour they hold — `agentlysdk.Client` is
the only type a call site should reference.

### TypeScript — HTTP client

```ts
import { AgentlyClient } from '@viant/agently-sdk';

const client = new AgentlyClient({ baseURL: '/v1' });

const conv = await client.createConversation({ agentId: 'orchestrator' });
const res  = await client.query({ conversationId: conv.id, query: 'Summarize Q4' });
console.log('assistant:', res.content);

// Stream via an SSE subscription (returns AsyncIterable).
for await (const ev of client.streamEvents({ conversationId: conv.id })) {
    console.log('event', ev.type, ev.turnId);
}

// Picker-backed fetch.
const rows = await client.fetchDatasource({
    id: 'advertiser',
    inputs: { q: 'acme' },
});
```

Auth: when the workspace runs with BFF OAuth, the session cookie is
attached automatically (browsers) or via `tokenProvider` (Node). See
[auth-system.md](auth-system.md).

### Swift — iOS

```swift
import AgentlySDK

let client = AgentlyClient(
    endpoints: AgentlyClient.defaultEndpoints(baseURL: "https://agently.example.com/v1")
)

Task {
    let conv = try await client.createConversation(
        CreateConversationInput(agentID: "orchestrator")
    )
    let res = try await client.query(
        QueryInput(conversationID: conv.id, query: "Summarize Q4")
    )
    print("assistant:", res.content)

    // Stream events.
    for try await ev in client.streamEvents(
        StreamEventsInput(conversationID: conv.id)
    ) {
        print("event", ev.type, ev.turnID ?? "-")
    }

    // Picker-backed fetch.
    let rows = try await client.fetchDatasource(
        FetchDatasourceInput(id: "advertiser", inputs: ["q": .string("acme")])
    )
}
```

### Kotlin — Android

```kotlin
import com.viant.agentlysdk.AgentlyClient
import com.viant.agentlysdk.*

val client = AgentlyClient(
    endpoints = mapOf("appAPI" to EndpointConfig(baseURL = "https://agently.example.com/v1"))
)

val scope = CoroutineScope(Dispatchers.Main)
scope.launch {
    val conv = client.createConversation(CreateConversationInput(agentId = "orchestrator"))
    val res  = client.query(QueryInput(conversationId = conv.id, query = "Summarize Q4"))
    Log.i(TAG, "assistant: ${res.content}")

    // Stream events.
    client.streamEvents(conv.id).collect { ev ->
        Log.i(TAG, "event ${ev.type} turn=${ev.turnId}")
    }

    // Picker-backed fetch.
    val rows = client.fetchDatasource(
        FetchDatasourceInput(id = "advertiser", inputs = mapOf("q" to JsonPrimitive("acme")))
    )
}
```

---

## Typical client lifecycle

1. **Boot** — construct once, hold the reference for the app lifetime.
2. **Open or resume a conversation** — either create fresh (`createConversation`) or reconnect via `getConversation(id)` + `getTranscript(id)` on cold start. The transcript is the source of truth; no client-side history store needed.
3. **Subscribe to the event stream** — start before calling `query` so you don't miss early events. Events carry `turnID` + `messageID` for reducer merging.
4. **Send turns** — `query` blocks for the final response; use events for progress, `cancelTurn` / `cancelQueuedTurn` to abort.
5. **Elicitations + approvals** — when the stream emits `elicitation_requested` or `tool_approval_pending`, call `resolveElicitation` / `decideToolApproval` to unblock the turn.
6. **Picker data** — `fetchDatasource` + `listLookupRegistry` for form inputs that need live data (see [lookups.md](lookups.md)).

## Error handling

- HTTP client surfaces typed `HttpError`s carrying the server's JSON body.
- Embedded client returns domain errors from the underlying service (auth failures, not-found, validation).
- Streaming subscriptions close with an error on transport failure; callers should re-subscribe, not assume terminal.
- `cancelTurn` is idempotent — safe to call multiple times.

## Reconnect + resume

- HTTP SSE reconnects use `Last-Event-ID`; when the server has retained the event id, missed events are replayed from the reducer snapshot.
- After a long disconnect, prefer a full `getTranscript(id)` refresh rather than relying on replay — reducers are append-only and the snapshot is cheap.

## The `Client` interface

One Go interface captures every operation a caller can perform:

- Conversations + turns + messages (CRUD, transcript, streaming)
- Tool + skill definitions, activation, approval
- Templates + prompts + workspace resources
- Datasources + lookup registry ([doc/lookups.md](lookups.md))
- Schedules ([doc/scheduler.md](scheduler.md))
- Files (upload / download)
- Auth (providers, login, OAuth)

Compile-time assertions at [sdk/client.go:14-18](../sdk/client.go) pin `*backendClient` and `*HTTPClient` to the full interface — any missing method fails the build.

## Wire contract

- HTTP routes under `/v1/api/*` and `/v1/*` — see [sdk/handler.go](../sdk/handler.go) `registerCoreRoutes`.
- JSON for most requests; multipart for file upload; SSE for streaming.
- All routes inherit the same middleware chain (auth, debug, CORS, request id). OAuth, when enabled in the workspace, applies uniformly.

## Streaming

`Client.StreamEvents(ctx, input)` returns a Go-channel-like subscription the caller drains. The HTTP impl backs it with Server-Sent Events; the embedded impl backs it with the in-process bus. See [doc/streaming-events.md](streaming-events.md).

## Cross-platform symmetry

When a new wire method lands it must ship across all platforms in the same release:

- Go: `Client` interface + embedded + HTTP.
- TS: [sdk/ts/src/client.ts](../sdk/ts/src/client.ts).
- Swift: extend `AgentlyClient` in [sdk/ios/Sources/AgentlySDK/](../sdk/ios/Sources/AgentlySDK/).
- Kotlin: extend `AgentlyClient` in [sdk/android/src/main/.../Client.kt](../sdk/android/src/main/java/com/viant/agentlysdk/Client.kt).

Compile-time assertions enforce symmetry on Go; the other three languages rely on hand-mirroring + platform-specific tests.

## Extensibility

- **Add an endpoint**: (1) declare wire types in `sdk/api/`, (2) add to `Client` interface, (3) implement embedded + HTTP, (4) register the HTTP handler in `registerCoreRoutes`, (5) mirror in TS + Swift + Kotlin, (6) add tests per platform.
- **Add a streaming event**: extend `runtime/streaming.Event` + reducers (see [doc/streaming-events.md](streaming-events.md)).

## Related docs

- [doc/streaming-events.md](streaming-events.md)
- [doc/auth-system.md](auth-system.md) — session / token handling is transparent to the SDK caller.
- [doc/lookups.md](lookups.md) §13 — a worked example of extending the SDK end-to-end.
