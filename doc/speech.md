# Speech transcription

A small optional module that accepts an audio upload and returns transcribed
text. Used by clients that want push-to-talk input. Off by default — enabled
at backend construction time.

## Package

| Path | Role |
|---|---|
| [service/speech/handler.go](../service/speech/handler.go) | `Handler` — HTTP route + multipart parsing |
| [service/speech/transcriber.go](../service/speech/transcriber.go) | `Transcriber` interface + default OpenAI Whisper implementation |
| [sdk/handler.go](../sdk/handler.go) | `WithSpeechHandler(...)` option + route registration |

## Wire contract

`POST /v1/api/speech/transcribe` — `multipart/form-data`:

| Form field | Required | Description |
|---|---|---|
| `audio` | yes | The audio file (WAV, MP3, M4A, WebM, …). Max 25 MB by default. |
| `filename` | no | Original filename (helps some providers pick a format). |
| `language` | no | BCP-47 hint passed through to the provider when supported. |

Response:

```json
{ "text": "transcribed content here" }
```

On error, standard JSON error envelope.

## Pluggable transcriber

The handler takes any `Transcriber`:

```go
type Transcriber interface {
    Transcribe(ctx context.Context, filename string, audio io.Reader) (string, error)
}
```

The default provider is OpenAI Whisper via
[OpenAITranscriber](../service/speech/transcriber.go). To use a different
backend (Azure Speech, Google STT, local Whisper.cpp, …), implement the
interface and wire it at boot.

## Enabling

The speech handler is opt-in. At backend construction:

```go
import (
    "github.com/viant/agently-core/sdk"
    "github.com/viant/agently-core/service/speech"
)

transcriber := speech.NewOpenAITranscriber(os.Getenv("OPENAI_API_KEY"))
handler := speech.NewHandler(transcriber, speech.WithMaxUploadSize(50<<20))

server, err := sdk.NewHTTPServerFromRuntime(rt, sdk.WithSpeechHandler(handler))
```

Without `WithSpeechHandler`, the route simply isn't registered — client calls
to `/v1/api/speech/transcribe` return 404.

## Auth

The speech route inherits the same middleware chain as every `/v1/api/*`
endpoint. When OAuth is enabled in the workspace, the user must be
authenticated; the caller's `ctx` flows into the transcriber (useful for
providers that key usage per user).

## Extensibility

- **Custom backend**: implement `Transcriber`, pass it to `NewHandler`.
- **Streaming transcription**: the current interface is one-shot; a streaming variant would add a second method and a WebSocket/SSE route — not built today.
- **Per-language models**: the OpenAI transcriber accepts a model override via `WithModel("whisper-1")`; a custom impl can select per `language` hint.

## Related docs

- [auth-system.md](auth-system.md) — same middleware applies.
- [sdk.md](sdk.md) — the speech endpoint is not yet wrapped by the Go / TS / Swift / Kotlin SDKs; clients call the HTTP route directly.
