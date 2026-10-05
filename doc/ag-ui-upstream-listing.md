# AG-UI community integration submission

This is a submission plan, not a certification claim. Submit after the implementation is merged into main and its public dependency revisions can be installed without local checkout replacements.

AG-UI's official overview lists supported frameworks, SDKs and clients. Its contribution guide asks contributors to coordinate through an issue before significant upstream integration work:

- https://docs.ag-ui.com/introduction
- https://github.com/ag-ui-protocol/ag-ui/blob/main/CONTRIBUTING.md

## Proposed entries

- **Agently Core**: Go agent framework/backend with AG-UI POST streaming, durable run observation and conversation history, shared state, client-tool and human-input continuation, cancellation and application extensions.
- **Agently**: web, iOS, Android and CLI clients using AG-UI for conversation interaction. Authentication and application services remain the host/BFF's responsibility.

Request the appropriate community integration/client listing and maintainership expectations. Do not describe the projects as certified unless upstream explicitly grants that designation.

## Evidence to include

Publish installation instructions, pinned dependencies, a runnable generic demo, and a capability matrix separating standard AG-UI behavior from Agently extensions. Include protocol tests, interrupt/tool-result round trips, detach/replay/restart tests, authentication/ownership tests and external-backend interoperability evidence. Unsupported extensions must not produce fabricated chat content.

For a full integration PR, follow the upstream guide's TypeScript HTTP-agent package, integration examples, Dojo registration and CI/e2e requirements. Coordinate the scope with maintainers before preparing that upstream change. Keep domain-specific deployments and credentials out of the public demo.

## Remaining release gates

Main-branch merge, removal of local development dependency replacements, complete shell parity, and final live interoperability acceptance remain prerequisites. Internal passing suites or a partial starter matrix do not establish complete client/backend compliance.
