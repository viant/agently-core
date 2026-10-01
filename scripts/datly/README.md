# Datly tooling

Run from the Core repository root:

```sh
python3 scripts/datly/build_transcriber.py
python3 scripts/datly/transcribe.py
python3 scripts/datly/check_contracts.py
python3 scripts/datly/verify_regeneration.py
```

The stock CLI build copies command sources into temporary ignored `bin/` storage and links the Core predicate and codec packages. Its private modfile keeps CLI dependency changes out of the application manifest. Set `DATLY_MODFILE` to select a verification modfile; set `DATLY_BIN` to select an already built CLI.

The relocation map supplies the exact contract inventory. Each DQL source must declare its mapped `internal/datly/` destination. Regeneration compares all files in those contract directories, including authored lifecycle hooks. Authored host, support, and store packages are outside that snapshot.

Endly runs native contract/store tests separately from the full Core suite:

```sh
(cd e2e/datly && endly -r=run)
(cd e2e/datly && endly -r=core)
```

The native workflow accepts `testPackages` and `testRun` overrides. The full Core workflow runs `go test -count=1 ./...`.

Run transcription and regeneration before starting runtime test fixtures. Regeneration replaces generated directories while discovery reads them.

The boundary gate rejects unquoted MySQL reserved table/view aliases, including nested views and CTEs. `mysql_reserved_words.json` records the MySQL 8.4 manual snapshot used offline; update its version, authority, and retrieval date when refreshing the list. Quote an alias when its name is part of the generated Go/JSON contract. SQL strings, comments, and DQL metadata arguments are excluded by the lexer. Run lexer and inventory regressions with `python3 -m unittest discover -s scripts/datly -p 'test_*.py'`.
