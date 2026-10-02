# Datly contracts

This is the authoritative DQL tree for Core. Each operation declares its generated package, route, input type, parameter sources/defaults/codecs/predicates, and explicit output/view shape. SQL relation aliases remain SQL names; outer column aliases shape Go fields without changing physical column mappings.

- Stock contracts and required authored hook sidecars: `internal/datly/<domain>/read|write`.
- Authored transactional orchestration: `internal/store/<domain>`.
- Shared codecs, predicates, clocks and invariants: `internal/datly/codec`, `predicate`, `dbtime`, `invariant`.
- Public services live in `service/`; business DTOs live in entity packages under `model/`.

Use `#import` aliases for shared types and predicate/codec references. The actual module is `github.com/viant/agently-core`; dot-only legacy `com.viant.*` names are not package identities.

Use the native Datly CLI through Endly: from `e2e/datly`, run `endly -r=build`, `endly -r=transcribe`, `endly -r=regeneration`, and `endly -r=run`. Full Core tests use `endly -r=core`. See [authoring instructions](../scripts/datly/README.md) for the three base-reader resource bindings.

Generated support is recreated by stock operation transcription. Keep custom methods in authored `hooks.go`/`lifecycle.go` sidecars where Go receiver rules require colocation. Reverse-engineering evidence lives under `migration/`; neither generated Go nor historical source copies replace these DQL contracts.
