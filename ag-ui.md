# AG-UI integration

Agently serves AG-UI runs at `POST /v1/ag-ui/run`. The handler is registered in
[`sdk/handler.go`](sdk/handler.go); request handling and event delivery are
implemented in [`sdk/handler_agui.go`](sdk/handler_agui.go).

Use the SDK transport documentation for supported client APIs:

- [TypeScript](sdk/ts/AG-UI.md)
- [Swift](sdk/ios/AG-UI.md)
- [Kotlin](sdk/android/AG-UI.md)

The pinned protocol schema is stored at
[`protocol/agui/schema-1.0.json`](protocol/agui/schema-1.0.json).
Authentication remains with the host's configured HTTP middleware. Optional
extensions depend on the connected server's advertised capabilities.
