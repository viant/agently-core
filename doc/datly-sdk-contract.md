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
