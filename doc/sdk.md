# SDK surface

Go HTTP, TypeScript, Swift and Kotlin use AG-UI for outward conversation
interaction and retain the supporting authenticated application APIs. The Go
`Client` covers caller operations; host-side `Backend` additionally owns the
native execution event bus consumed by the AG-UI coordinator. That backend bus
is not an alternative HTTP conversation transport.

For the Datly 1.0 server migration and cross-platform compatibility gate, see
[datly-sdk-contract.md](datly-sdk-contract.md).

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
| Local HTTP | `NewLocalHTTPFromRuntime(ctx, rt)` — spins up the HTTP server in-process so tests exercise the wire contract |

---

## Using the client

Every code example below does the same three things: open a conversation,
send a query and observe its run. Each language exposes its own typed surface
over the same wire protocol.

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

// 2. Prepare one run using authenticated native metadata for thread mapping.
prepared, err := client.PrepareAGUIChat(ctx, &agentsvc.QueryInput{
    ConversationID: conv.Id,
    Query:          "Summarize Q4 performance",
})
if err != nil { log.Fatal(err) }
stream, err := client.RunAGUI(ctx, &prepared.Input, nil)
if err != nil { log.Fatal(err) }
defer stream.Close()
result, err := agentlysdk.CollectAGUI(stream, conv.Id, func(event agentlysdk.AGUIEvent) error {
    log.Printf("event type=%s", event.Type)
    return nil
})
if err != nil { log.Fatal(err) }
log.Printf("assistant: %s", result.Content)
// Interrupted runs require an explicit resume entry. Interrupted transport
// returns AGUIObservationError with IDs/cursor; attach rather than resubmit.

// 4. Feature: fetch rows from a datasource-backed picker.
rows, err := client.FetchDatasource(ctx, &api.FetchDatasourceInput{
    ID:     "customer",
    Inputs: map[string]interface{}{"q": "ace"},
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
    id: 'customer',
    inputs: { q: 'ace' },
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
        FetchDatasourceInput(id: "customer", inputs: ["q": .string("ace")])
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
        FetchDatasourceInput(id = "customer", inputs = mapOf("q" to JsonPrimitive("ace")))
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

## Uploaded assets as resources

Authenticated Go/HTTP uploads return a `Resource` / `resource` descriptor in
addition to the existing file ID and download information. Send its URI in
`QueryInput.ResourceURIs` (JSON/TypeScript `resourceURIs`) to make the file
available without automatically sending its bytes to the model:

```go
// client and conv are from the construction/query example above.
// The configured client/context must carry an authenticated effective user.
file, err := client.UploadFile(ctx, &agentlysdk.UploadFileInput{
    ConversationID: conv.Id,
    Name: "customers.csv",
    ContentType: "text/csv",
    Data: []byte("id,name\n1,Alice\n"),
})
if err != nil { log.Fatal(err) }
if file.Resource == nil { log.Fatal("upload did not publish a user-scoped resource") }

result, err := client.Query(ctx, &agentsvc.QueryInput{
    ConversationID: conv.Id,
    AgentID: "orchestrator",
    Query: "Inspect this customer file",
    ResourceURIs: []string{file.Resource.URI},
})
if err != nil { log.Fatal(err) }
_ = result
```

`POST /upload` also supports authenticated uploads before a conversation exists;
its returned `uri` is directly usable in `resourceURIs`. Anonymous callers retain
legacy staging/upload responses. Existing `attachments` keep their automatic
presentation semantics.

See [Uploaded assets and resource tools](resources.md) for inspection, selected
reads, provider-native presentation, export, limits, and tool handoff. The new
resource fields are exposed in the Go/HTTP, TypeScript, Swift, and Kotlin contracts.

### Mobile resource uploads

Swift and Kotlin `uploadFile` return the optional `resource` descriptor. Supply a
conversation ID to use `/v1/files`, or omit it to use `/upload` before a
conversation exists. Both paths use the client's configured authentication.
Upload validation rejects empty content and files larger than 64 MiB.

```swift
let uploaded = try await client.uploadFile(UploadFileInput(
    name: "customers.xlsx",
    contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    data: fileData
))
if let resource = uploaded.resource {
    let result = try await client.query(QueryInput(
        conversationID: conversationID,
        agentID: "orchestrator",
        query: "Inspect this workbook",
        resourceURIs: [resource.uri]
    ))
    // Consume result or the conversation event stream.
}
```

```kotlin
val uploaded = client.uploadFile(UploadFileInput(
    name = "customers.xlsx",
    contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    data = fileBytes
))
uploaded.resource?.let { resource ->
    val result = client.query(QueryInput(
        conversationId = conversationId,
        agentId = "orchestrator",
        query = "Inspect this workbook",
        resourceURIs = listOf(resource.uri)
    ))
}
```

The mobile app composers prefer resource references when returned by the server.
For older or anonymous upload responses without `resource`, they retain the
legacy `QueryAttachment` flow. Both SDKs decode current lowercase upload fields
and legacy uppercase `ID`/`URI` fields. File-picker access and loading the selected
bytes remain the app's responsibility.

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

Compile-time assertions at [sdk/client.go:14-18](../sdk/client.go) pin `*backendClient` and `*HTTPClient` to `Client`; only the host backend must also implement native `Backend.StreamEvents`.

## Wire contract

- HTTP routes under `/v1/api/*` and `/v1/*` — see [sdk/handler.go](../sdk/handler.go) `registerCoreRoutes`.
- JSON for most requests; multipart for file upload; SSE for streaming.
- All routes inherit the same middleware chain (auth, debug, CORS, request id). OAuth, when enabled in the workspace, applies uniformly.

## Streaming

`HTTPClient.RunAGUI` returns an SSE run stream whose events retain the complete
wire JSON and opaque cursor. `AttachAGUI` observes the same admitted run without
submitting another user message. `Query` collects a simple run response and
returns a typed interrupt error when an explicit decision is needed. Unsupported
native-only QueryInput controls are rejected before I/O.

`ObserveApplicationEvents` requires a visible conversation and observes only
scoped supporting application/native-background events, suppressing execution
already carried by AG-UI. Host-side `Backend.StreamEvents` stays on the private
in-process bus. The old query endpoint and unscoped stream route are not mounted.

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
