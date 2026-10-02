# SQLX Criteria option checkpoint

SQLX owns `Criteria{Expression, Placeholders}`. It is passed directly to update
and delete `Exec` as an option. Existing predicate evaluation is outside SQLX;
Datly bridges its existing predicate result. xdatly has no SQLX dependency.

Repository checkout: `/Users/awitas/go/src/github.com/viant/sqlx-conditions`
Branch: `feature/mutation-conditions`
Commit: `716a37c8ca40eeb1ff66dd0bb1256f9590f4387a`
Base: published SQLX `main`, `cbe6dd70f75258382c97bcbe6a11f394dbf7b37e`.
Only the Criteria change is on this branch; unrelated local commits were excluded.

Verified: new criteria tests; complete updater/deleter/option suites; compound
and range predicates; zero-match row counts; explicit zero; NULL expression;
placeholder mismatch; sparse SET fields; caller rollback; existing IfMatch
composition; delete batch overflow and reusable-service isolation.

The full root-package suite also fails `TestParameterScanCompatibility` with
SHA 516aa2d83942ac27ebe72b487ddca5ff2ef5973ec23979bd0ba5451d653eca9e.
The same failure was reproduced on an unchanged baseline. This change does not
modify parameter scanning.

Push status: published by the user and verified on `origin/main` at the commit
above. Datly and the migration now require the remotely resolved version
`v0.26.1-0.20260928224516-716a37c8ca40`; no local SQLX replacement is used.

The original `/Users/awitas/go/src/github.com/viant/sqlx` contains the same commit.
`sqlx-criteria.patch` contains the committed change for review/recovery.
