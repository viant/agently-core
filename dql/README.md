# Datly contracts

This is the authoritative DQL tree for Core. Each operation declares its generated package, route, input type, parameter sources/defaults/codecs/predicates, and explicit output/view shape. SQL relation aliases remain SQL names; outer column aliases shape Go fields without changing physical column mappings.

- Stock contracts and required authored hook sidecars: `internal/datly/<domain>/read|write`.
- Authored transactional orchestration: `internal/store/<domain>`.
- Shared codecs, predicates, clocks and invariants: `internal/datly/codec`, `predicate`, `dbtime`, `invariant`.
- Public service and DTO import paths remain in `service/` and `pkg/agently/`.

Use `#import` aliases for shared types and predicate/codec references. The actual module is `github.com/viant/agently-core`; dot-only legacy `com.viant.*` names are not package identities.

From the Core root, run `python3 scripts/datly/build_transcriber.py`, then `python3 scripts/datly/transcribe.py`. Regeneration and source-boundary checks are `scripts/datly/verify_regeneration.py` and `scripts/datly/check_contracts.py`. Endly runs `e2e/datly/run.yaml`; full Core tests use `e2e/datly/core.yaml`.

Generated support is recreated by stock operation transcription. Keep custom methods in authored `hooks.go`/`lifecycle.go` sidecars where Go receiver rules require colocation. Reverse-engineering evidence lives under `migration/`; neither generated Go nor historical source copies replace these DQL contracts.
