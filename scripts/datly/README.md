# Datly authoring and checks

Endly owns orchestration. From `e2e/datly`:

```sh
endly -r=build
endly -r=transcribe
endly -r=regeneration
endly -r=run
endly -r=core
```

`build` compiles the pinned native Datly CLI, the linked Core host, and the base
reader authoring task. CLI-only dependency resolution uses an ignored private
modfile in `bin/`; it does not change the application module manifest. Endly
stages the selected stock CLI sources in ignored `bin/_datly_cli` and adds only
imports for Core predicate/codec types. The authoring CLI implementation remains
stock; no framework sources or duplicate CLI parser are committed.

`transcribe` invokes native `datly transcribe get|patch` commands for the ordinary
contracts. `regeneration` snapshots `internal/datly`, transcribes again, and uses
`diff -ru` to check every file, including authored hooks. Review the explicit
contract commands when adding a DQL source. The relocation map remains the
migration inventory; it is not a second compiler.

`run` builds, transcribes all 65 contracts, checks byte-stable regeneration,
and runs Go contract/store/native tests. Use `testPackages` and `testRun` parameters for a focused Endly check.
`core` runs the full Core Go suite. Native validation reports skipped checks;
it does not establish database or HTTP behavior. Go and Endly fixtures supply
those checks. There is no separate Python SQL lexer or reserved-word gate.

`endly -r=validate` runs the full native CLI validator as a separate diagnostic.
The pinned validator currently rejects writer DQL using `delete_not_found`
because its planning path lacks the PATCH operation context. Validating emitted
writer Go also reports a missing delete marker. These are known native CLI
validation gaps, not successful checks. Operation-specific transcription and
the native Go behavior tests remain the acceptance gates; no custom validator
is substituted.

The three base readers reuse canonical generated input types and named SQL
resources. The current native CLI has no option for that package authority.
`e2e/datly/authoring` is the narrow Go task using the existing public Datly
`PackageCompilation` and Bindly resource APIs. It handles only conversation,
message, and schedule base readers; it is not an alternate transcription or
validation framework. Its discovery connection opens the disposable schema
read-only.

Run generation and regeneration before starting runtime fixtures. Replacing
source artifacts while a source-backed server is running invalidates its index.
