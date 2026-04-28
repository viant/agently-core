# Multi-model LLM providers (generic `llm.GenerateRequest` / `llm.GenerateResponse`)

Agently targets a **single generic request/response shape** (`genai/llm`)
that every provider adapts to. A caller — the reactor, the intake sidecar,
the augmenter, any service that needs a completion — writes code against the
generic types. Providers translate in and out at the edge.

## Core types

| Type | File | Role |
|---|---|---|
| `Model` (interface) | [genai/llm/model.go](../genai/llm/model.go) | One `Generate(ctx, *GenerateRequest) (*GenerateResponse, error)` method + `Implements(feature) bool` |
| `GenerateRequest` | [genai/llm/types.go](../genai/llm/types.go) | Messages, instructions, `Options`, `PromptCacheKey`, `PreviousResponseID` |
| `GenerateResponse` | [genai/llm/types.go](../genai/llm/types.go) | Choices (each with `Message`), `Usage`, `Model`, `ResponseID` |
| `Message` | [genai/llm/types.go](../genai/llm/types.go) | Role + content + tool calls + `ContentItem[]` for multimodal |
| `ContentItem` | [genai/llm/types.go](../genai/llm/types.go) | Universal text / image / video / audio / pdf asset wrapper |
| `Tool` / `ToolCall` | [genai/llm/tool.go](../genai/llm/tool.go) | Provider-neutral tool + tool-call representation |
| `Options` | [genai/llm/options.go](../genai/llm/options.go) | Temperature, max-tokens, stop, JSON mode, tool-choice, streaming, etc. |
| `BackoffAdvisor` (optional) | [genai/llm/model.go](../genai/llm/model.go) | Provider-hint retry policy |
| `Finder` / `Preference` | [genai/llm/finder.go](../genai/llm/finder.go), [genai/llm/preference.go](../genai/llm/preference.go) | Pick a model from workspace config by id / capability / cost |
| `Stream` | [genai/llm/stream.go](../genai/llm/stream.go) | Chunk type for streaming paths |

## Providers

Each provider is a self-contained package under `genai/llm/provider/`. All
translate their native wire types to and from the generic ones above.

| Provider | Path |
|---|---|
| OpenAI | [genai/llm/provider/openai/](../genai/llm/provider/openai/) |
| Anthropic via AWS Bedrock | [genai/llm/provider/bedrock/claude/](../genai/llm/provider/bedrock/claude/) |
| Anthropic via Vertex AI | [genai/llm/provider/vertexai/claude/](../genai/llm/provider/vertexai/claude/) |
| Google Gemini via Vertex AI | [genai/llm/provider/vertexai/gemini/](../genai/llm/provider/vertexai/gemini/) |
| Ollama (local) | [genai/llm/provider/ollama/](../genai/llm/provider/ollama/) |
| Grok (xAI) | [genai/llm/provider/grok/](../genai/llm/provider/grok/) |
| InceptionLabs | [genai/llm/provider/inceptionlabs/](../genai/llm/provider/inceptionlabs/) |
| Factory / loader | [genai/llm/provider/factory.go](../genai/llm/provider/factory.go), [genai/llm/provider/loader.go](../genai/llm/provider/loader.go) |

Each provider defines **its own** `Request` / `Response` native types in its
`types.go` (e.g. [openai/types.go](../genai/llm/provider/openai/types.go),
[bedrock/claude/types.go](../genai/llm/provider/bedrock/claude/types.go)).
Those are internal. Nothing outside the provider package imports them.

## Translation contract

```
generic                                 native
                                        
llm.GenerateRequest                     openai.Request
llm.GenerateResponse                    openai.Response
  └─ []Choice.Message                     └─ Choices[].Message
  └─ Usage (prompt/completion/total)      └─ Usage
  └─ Tool calls (provider-neutral)        └─ Tool calls (OpenAI function-call shape)
```

Providers implement two private helpers — `buildRequest` and `parseResponse` —
used only inside the provider package. The public surface is just `Model`.

### Feature probes

Not every provider supports every capability. Callers ask:

```go
if model.Implements(llm.FeatureJSONMode) { req.Options.JSONMode = true }
```

This avoids provider-specific branches in the reactor. Current feature names live at the top of [options.go](../genai/llm/options.go).

## Model selection

Workspace `models/*.yaml` declares one entry per model:

```yaml
id: openai_gpt-5_4
provider: openai
model: gpt-5-mini
temperature: 0
maxTokens: 8192
implements: [json_mode, streaming, tool_calling, vision]
```

- Agents reference `model: openai_gpt-5_4`.
- [genai/llm/finder.go](../genai/llm/finder.go) resolves the id into a live `Model` via the provider factory.
- [genai/llm/preference.go](../genai/llm/preference.go) lets the reactor pick among several models for a given turn (e.g. fast model for intake, stronger model for synthesis).

## Streaming

Streaming is exposed through the same interface — `Model.Generate` returns
tokens via the `Choice.Delta` mechanism when `Options.Stream == true`. The
reactor wires each chunk onto the streaming bus (see
[streaming-events.md](streaming-events.md)). Providers without native
streaming produce a single chunk at the end.

## Multimodal

`Message.Items` / `ContentItem` is the universal multimodal representation
(text, image, video, pdf, audio, binary) with sources `url` / `base64` /
`raw`. Providers that can't consume a given content type either drop it
(with a logged warning) or error depending on `Options.StrictContent`.

## Retries + backoff

- The default retry loop in [service/core/generate_execute.go](../service/core/generate_execute.go) retries on transient errors with exponential backoff.
- Providers implementing `BackoffAdvisor` ([model.go](../genai/llm/model.go)) get to direct the retry — useful for 429s with a `Retry-After` header or rate-limit buckets.

## Extensibility

- **Add a new provider**:
  1. Create `genai/llm/provider/<name>/` with `types.go`, `client.go`, `model.go`.
  2. Implement `llm.Model` — translate `GenerateRequest` in and `GenerateResponse` out.
  3. Register a factory hook in [genai/llm/provider/factory.go](../genai/llm/provider/factory.go).
  4. Declare a workspace `models/<id>.yaml` referencing your provider.
- **Add a new feature probe**: add a constant to `llm.FeatureXxx`, implement `Model.Implements` in each provider that supports it.
- **Add a ContentType**: append a constant in [types.go](../genai/llm/types.go) and teach providers to translate (or explicitly drop).

## Related docs

- [prompt-binding.md](prompt-binding.md) — how `GenerateRequest` is assembled per turn.
- [context-management.md](context-management.md) — how the message slice is budgeted.
- [embedius-embeddings.md](embedius-embeddings.md) — sibling abstraction for embedders.
- [streaming-events.md](streaming-events.md) — where streaming chunks go after the provider emits them.
