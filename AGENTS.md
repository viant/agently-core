# Public framework boundaries

- Keep runtime behavior generic and driven by declared contracts. Do not special-case customer applications, business resource names, report IDs, or production identifiers.
- Never commit internal business records, live MCP responses, private deployment configuration, screenshots, captures, performance traces, credentials, or investigation notes. Keep such evidence outside this public repository.
- Use small, independently constructed synthetic fixtures. Use reserved example domains and portable paths; do not copy fixtures from private repositories or production sessions.
- Audit sensitive history using local Git objects. Do not request removed revisions from third-party mirrors or archive services: those requests can replicate the content. Keep backup refs and audit evidence out of public pushes.
- Before publishing, inspect the complete outgoing diff and its commit history, not only the current worktree. Squash unpublished investigation commits so excluded data is absent from every outgoing commit.
- Follow `scripts/PUBLIC_BOUNDARIES.md`: run the static privacy regressions and audit both the index and every outgoing commit against the reviewed clean ancestor. Keep commit messages generic and review fixture provenance before updating attestations.
- When a Go function accepts context.Context, place it first.

# Design-system contributor entry point

Before changing UI or its theme contract, read [the canonical usage guide](protocol/ui/theme/README.md).
Reuse the documented shared components and semantic roles. Extend the owning
shared component with its usage example, states and existing checks when a
reusable capability is missing. Record remaining legacy scope in the change.

- Producer-specific schemas, tool names, business rules, and report gates must remain in data producers. Renaming domain fields does not make business-specific behavior a framework responsibility. Generic MCP payloads must remain opaque to the execution framework.
