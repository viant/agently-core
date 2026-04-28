# Augmentation (RAG-style knowledge injection)

The augmenter enriches the model's prompt with relevant workspace knowledge:
documents, skill bodies, prior conversation summaries — whatever is
registered under `knowledge/` — retrieved by embedding similarity and
injected as system blocks during prompt binding.

## Packages

| Path | Role |
|---|---|
| [service/augmenter/](../service/augmenter/) | Retrieve + rank + trim to token budget |
| [genai/embedders/](../genai/embedders/) | Embedder used for similarity |
| Workspace `knowledge/` | Documents indexed at load time |

## Flow

```
user query
  ├─ embed(query)
  ├─ similarity search over knowledge index  (see embedius-embeddings.md)
  ├─ rank + dedup + per-document cap
  ├─ fit to token budget (reserve room for history + user turn)
  └─ emit as `Document` bindings for the prompt binder
```

## Budgeting

The augmenter honours the agent's `knowledgeTokenLimit`. If top-K hits overflow:

- Keep the top-scoring chunk per document.
- If still over budget, drop the lowest-scoring doc entirely.
- Never truncate a chunk mid-sentence — chunks are atomic.

## Knowledge roots

Knowledge can come from:

- Workspace `knowledge/` (markdown, json, yaml). Default.
- MCP `resources/*` — pulled live from any configured MCP server.
- Tool feed results (see [doc/feed-system.md](feed-system.md)) — short-lived, turn-scoped.

Internal and MCP-external sources flow through the same retrieval path; the
binder doesn't distinguish them.

## Extensibility

- **Custom retriever**: satisfy `augmenter.Retriever` and register on the runtime.
- **Per-agent filters**: agent YAML can declare `knowledgeTags: [foo, bar]` to narrow the candidate set.
- **Reranker**: plug in a cross-encoder by implementing `augmenter.Reranker` — the default returns similarity scores unchanged.

## Related docs

- [doc/embedius-embeddings.md](embedius-embeddings.md)
- [doc/prompt-binding.md](prompt-binding.md)
- [doc/feed-system.md](feed-system.md)
