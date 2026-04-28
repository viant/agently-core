# Embeddings & vector search (embedius)

Agently uses [viant/embedius](https://github.com/viant/embedius) for
embeddings + vector search. Feature reach: knowledge retrieval, skill
discovery, semantic search over prior conversations, and similarity-ranked
prompt profile selection.

## Packages

| Path | Role |
|---|---|
| [genai/embedders/](../genai/embedders/) | Embedder abstraction + providers (OpenAI, Bedrock, Vertex, local) |
| [service/augmenter/](../service/augmenter/) | Uses embeddings to augment prompts with relevant docs |
| Workspace `embedders/*.yaml` | Per-embedder config |
| Workspace `knowledge/` | Source documents; indexed at load time |

## Embedder model

An embedder turns text into a float vector of a provider-specific dimension. Agently wraps the provider behind a common `Embedder.Embed(ctx, texts) → [][]float32` interface.

Selection: `config.yaml` has a `defaultEmbedder` (workspace-wide); individual
consumers may override.

## Index + search

Embedius owns the index (flat, HNSW, or persistent depending on config). Ingestion:

1. On workspace load, the augmenter + skill/knowledge loaders enumerate documents.
2. Each doc is chunked (paragraph or fixed-window), embedded, and appended to the index.
3. Metadata (doc id, chunk id, tags, source path) travels with each vector.

Query:

```go
hits, err := augmenter.Retrieve(ctx, query, topK)
```

Hits carry `score`, `document`, `chunk`, and metadata; the consumer decides what to inject into the prompt.

## Where it's used

- **Knowledge injection** — at bind time, the binder may call retrieve() and inject top-K chunks.
- **Skill discovery** — when multiple SKILL.md candidates match, embedding similarity breaks ties ([doc/skills.md](skills.md)).
- **Prompt profile selection** — intake can use embeddings over `appliesTo` tags as one input to the confidence score ([doc/planning-and-intake.md](planning-and-intake.md)).

## Workspace config

```yaml
# embedders/openai_text.yaml
id: openai_text
provider: openai
model: text-embedding-3-small
dimension: 1536
```

## Extensibility

- **New embedder provider**: implement `Embedder` under `genai/embedders/<provider>/`.
- **Custom chunker**: satisfy `augmenter.Chunker`; default is paragraph-aware.
- **Re-index trigger**: hotswap (see [doc/workspace-system.md](workspace-system.md)) watches `knowledge/` and signals re-index on change.

## Related docs

- [doc/prompt-binding.md](prompt-binding.md)
- [doc/augmentation.md](augmentation.md)
