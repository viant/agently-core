# Datly and public SDK contract

One Core module owns authoritative DQL in `dql/`, generated persistence in
`internal/datly/`, and typed business orchestration in `internal/store/`.
Datly 1.0 and unified xdatly v1 supply the runtime and hook contracts.
The Core and Agently module files use published Datly/SQLX without local
framework replacements. Public DTO package paths remain stable.

## Contract boundaries

| Boundary | Authority | Consumers |
|---|---|---|
| Persistence | Root DQL, stock transcription, generated readers/writers/cubes and authored predicates/codecs/hooks | Core stores and business compositions |
| Application | Core service interfaces, shared native runtime and trusted providers | HTTP handlers and embedded Go client |
| Public wire | Go SDK operations, HTTP handlers and JSON/SSE models | Go HTTP, TypeScript, Swift and Kotlin clients |

Generated components are private host capabilities. Request fields cannot
replace trusted owner/internal providers, component targets or transactions.
Business compositions use generated components and managed units. DQL defines
input parameters, predicates, defaults, codecs and explicit output types;
generated Go is recreated by stock transcription. Shared types use Go package
identities and import aliases.

Field visibility and route visibility are independent. `internal(view.column)`
is shorthand for the field tag `internal:"true"`; global `$internal(true)`
restricts the component route to internal invocation.

## Client sources and local Agently wiring

| Client | SDK source | Agently local resolution | Verified checks |
|---|---|---|---|
| Go embedded and HTTP | [sdk](../sdk/) | Core replacement points to `../agently-core-v1` | Full Core and Agently Go suites, actual application HTTP/embedded tests |
| TypeScript | [sdk/ts](../sdk/ts/) | UI dependency points to the sibling SDK | 413 SDK tests, typecheck/build, actual server contracts; UI suite/build |
| Kotlin/Android | [sdk/android](../sdk/android/) | Android sibling-source mode | 97 SDK tests, actual server contracts, app/Forge checks |
| Swift/iOS | [sdk/ios](../sdk/ios/) | Local package link and sibling-source tasks | 91 SDK tests, actual server contracts, 174 app tests |

Actual-server checks cover conversation/transcript JSON, authenticated SSE,
401/403/error behavior, queue edits, reporting lifecycle/adoption,
export retry/status/artifact access and audit persistence. See
[root caller evidence](../migration/root-layout-caller-validation.json),
[client checks](../migration/client-sdk-cutover-validation.json),
[published Core acceptance](../migration/published-core-final-validation.json),
and [Agently acceptance](../migration/agently-closing-validation.json).

Local JWT fixtures establish application behavior. They do not establish live
IdP authorization. Live token exchange and scheduler authorization with secrets are deferred by
the user. Public discovery and production JWKS verification pass.
Authorization redesign is deferred until after merge to main. The migration
preserves existing detail-read behavior and existing protected-operation
checks. That timing instruction does not authorize a merge.

## Authoring and acceptance

1. Declare DQL input parameters, predicates, defaults/codecs and output shape.
   Compare original behavior for sparse fields, NULL/zero presence, selectors,
   errors, owner scope and transaction ownership.
2. Run `(cd e2e/datly && endly -r=run)` for transcription, regeneration,
   boundary, contract/store/native tests and the linked command build.
   Finish generation before starting source-discovering runtime fixtures.
3. Exercise the affected operations through the application and public SDKs.
   For transactions, test contention and late rollback on SQLite and MySQL.
4. Run the full Core and Agently Go suites/builds and affected client checks.
   Audit direct product SQL, SDK0 imports and dependency resolution.

The full [published Endly gate](../migration/published-endly-final-validation.json)
passes all 62 contracts and regeneration of 823 artifacts. See the
[component catalog](../components.json),
[input audit](../migration/dql-input-contract-audit.json), and
[final acceptance audit](../migration/final-acceptance-audit.json).
Historical migration checkpoints retain their original evidence in `migration/`.
The user authorized Core and Agently commits on `v1`; no push was requested.
