# Go consumer conformance to AG-UI 1.0

The tests compare Agently’s Go consumer with the upstream [AG-UI 1.0 schema](https://docs.ag-ui.com/spec/1.0) and official `@ag-ui/client` reducer. Exact test dependency versions and schema provenance are recorded with the fixture source and TypeScript package manifest.

`runtime/aguistate/reference_semantics_test.go` compares accepted/rejected
sequences, normalized events, messages, and state against checked-in fixtures
produced by the actual upstream `transformChunks`, `verifyEvents`, and
`defaultApplyEvents` functions. The fixtures include an accepted stream with
all 31 standard event variants and focused positive/negative ownership cases.

Regenerate the reference expectations from the repository root after installing
the pinned TypeScript dependencies:

```sh
node protocol/agui/internal/generate/reducer-fixtures.mjs
```

The generator refuses a different client/protocol version and clones source
inputs before invoking the upstream reducer so the oracle cannot mutate future
fixture inputs. Go tests consume the generated JSON without requiring Node.

The verified ownership rules include:

- First ownership survives closes and untagged reopens within a run.
- Omitted continuation attribution agrees with the existing owner. An explicit
  empty subagent ID is distinct from the parent owner.
- Reasoning spans and reasoning messages have separate open sets and a shared
  retained ownership bucket. A span does not namespace its inner messages.
- Tool starts inherit parent-message ownership when untagged; repeated starts
  reuse the existing call and preserve arguments. Results establish their own
  message attribution, independently of the caller.
- Snapshot and run-input history seed message, reasoning, activity, and tool
  ownership. Replacing snapshots can assign a new owner.
- Replacing activity snapshots replace attribution; nonreplacing snapshots
  retain ownership. Activity history omission follows the upstream client
  authority metadata convention; streamed reasoning remains when absent from
  snapshots that contain no reasoning.
- Attribution-only producers need no subagent lifecycle events. Closed
  subagents remain valid attribution and parent references; their invocation
  IDs cannot be reopened within the run.
- `RUN_FINISHED` requires all explicitly open streams, spans, steps, and
  subagents to close. `RUN_ERROR` can occur with open entities or as the first
  event. A subsequent run resets lifecycle tracking.

The server intentionally applies these additional validity requirements, which
are not equivalent to every permissive upstream reducer error path:

- Terminal run/thread identity must match the bound run.
- Failed state/activity JSON Patch operations reject atomically. The JavaScript
  presentation reducer instead warns and leaves the document unchanged.
- Activity patches must preserve object content, as required by the wire
  ActivityMessage schema. The JavaScript reducer's TypeScript cast alone does
  not enforce this after applying an arbitrary root replacement.
- Chunk timestamps are retained on synthesized start/content events. The pinned
  JavaScript transformer does not forward these optional timestamps.

These are consumer/storage rules, not evidence that native producers emit all
31 variants or that every application feature has been migrated to AG-UI.
Backend operation coverage and production shell migration have separate ledgers.
Keep these intentional validation differences when changing the reducer: bound
terminal identity checks, atomic rejection of failed JSON Patch operations,
ActivityMessage object validation, and retained timestamps on synthesized
chunk events are server consumer/storage rules; they must not be silently
rewritten to mimic the more permissive upstream presentation reducer.
