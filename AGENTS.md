# Public framework boundaries

## Capability reuse and explicit user approval

- Before adding any feature or mechanism, inspect the owning framework's existing APIs, configuration, documentation, implementation, tests, and callers at the exact version used by the application. Prove the relevant native behavior with an executable example before proposing an alternative.
- Trace the existing path across layers and identify its owner. Reuse that path; remove demonstrated duplication instead of adding parallel wrappers, adapters, fallback paths, caches, validation, permission checks, or configuration switches.
- Before implementing a new feature or framework extension, consult the user with the existing options, evidence of the remaining gap, the smallest proposed change and owning layer, and compatibility implications. Obtain explicit approval of that approach first. A request to fix a regression is not approval to invent a new mechanism. Agent or reviewer recommendations do not substitute for user approval.
- Keep validation, strict/lenient argument handling, authorization and contract enforcement in the framework or producer that already owns them. Do not silently replace native lenient behavior with stricter application, host, proxy or UI rejection.
- Passing tests does not justify a redundant feature. Preserve existing contracts and add regressions through the real native path. Do not introduce a new API, flag, gate, compatibility layer or workaround solely to make a test pass.
- Carry these requirements into delegated work. When the user has already approved the exact approach, continue within that authorization; ask again only if the proposed approach or scope changes.

- Commit documentation only for implemented, supported behavior and reproducible usage. Keep feature proposals, migration plans, implementation journals, dated acceptance diaries, and agent audit notes outside the public repository.

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
