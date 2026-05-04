# Intake

This doc describes how agently-core decides what to do with a user turn when
`agentId=auto` (or auto-equivalent) is requested. It is the user-facing
counterpart to the design doc [intake-impt.md](../intake-impt.md).

There are exactly two intake layers:

| Layer | Decides | LLM call? |
|---|---|---|
| **Workspace intake** | **Who** owns the turn (which agent) — or whether to answer / clarify directly | one call when needed |
| **Agent intake** (per-agent sidecar) | **How** the chosen agent should run the turn (title, intent, profile, template, skills) | one optional call when an agent declares it |

These run **sequentially** — workspace intake first, agent intake second
(only when the chosen agent declares it AND the workspace result didn't
already cover that agent's scope).

The auto-selection LLM call is the **single decider**. There are no heuristic
shortcuts for capability detection — the LLM produces a structured
`{action, ...}` output that selects an agent, answers a workspace-capability
question, or asks for clarification. See §3.

---

## 1. Where it lives

| Concern | File |
|---|---|
| Workspace-intake config (per workspace) | `app/executor/config/default.go` `AgentAutoSelectionDefaults` |
| Agent-intake config (per agent) | `protocol/agent/intake.go` `Intake` |
| Workspace-intake LLM call | `service/agent/agent_classifier.go` `classifyAgentIDWithLLM` |
| Workspace-intake routing entry point | `service/agent/agent_resolution.go` `resolveTurnRouting` |
| Agent-intake sidecar | `service/agent/intake_query.go` `maybeRunIntakeSidecar` |
| Agent-intake service | `service/intake/service.go` |
| Result type (shared) | `service/intake/context.go` `TurnContext` |
| Workspace-intake result envelope | `service/agent/agent_classifier.go` `ClassifierResult` |

---

## 2. Workspace-intake configuration

In your workspace defaults file:

```yaml
default:
  agentAutoSelection:
    # Override the built-in router prompt. Optional. The default prompt is at
    # service/agent/prompts/router.md and instructs the LLM to output one of
    # {action: route|answer|clarify}. Custom prompts must preserve the same
    # output schema.
    prompt: |
      You are the workspace intake selector for <workspace name>. ...

    # Model used for the routing decision. Falls back to default.model when
    # empty. Should be a fast, cheap model — this is a classification task.
    model: claude-haiku

    # Output JSON key for the agent id. Default: "agentId". Use "agent_id"
    # if your custom prompt produces snake-case.
    outputKey: agentId

    # Per-call timeout. Default: 20 seconds.
    timeoutSec: 15
```

**That's it.** No pattern lists, no marker tables, no separate
capability-detection knobs — the LLM decides everything from the user
message and the authorized agent directory passed in-prompt at call time.

---

## 3. The unified output schema

The router LLM emits exactly **one** of these JSON shapes per call:

### 3.1 `route` — pick an authorized agent

```json
{"action":"route","agentId":"steward"}
```

The runtime resolves `steward` and runs `agent.Query()` normally. Same as
today's auto-selection behavior, just produced by a configurable prompt
instead of hardcoded markers.

### 3.2 `answer` — answer a capability question directly

```json
{"action":"answer","text":"## Summary\nThis workspace can …"}
```

The runtime publishes the `text` as the turn's assistant message and ends
the turn. **No second LLM call** runs — the classifier already produced the
authoritative answer. The message is written via the standard
`conversation.PatchMessage` path, which:

- persists the message to the DB through `dao.Operate`, and
- emits a `streaming.EventTypeAssistant` SSE event for live UIs.

The persisted message carries `status = "intake.answer"` so consumers that
care can distinguish capability answers from regular agent output.

### 3.3 `clarify` — ask the user to disambiguate

```json
{"action":"clarify","question":"Which order should I forecast?"}
```

Same publication path as `answer`, with `status = "intake.clarify"`.

### 3.4 Legacy schema

The classifier still recognizes the legacy `{"agentId":"X"}` shape (no
`action` key) and treats it as `action=route`. Existing custom prompts that
emit only an agent id continue to work.

---

## 4. Round-trip cost

For an `agentId=auto` turn:

| Outcome | Intake LLM calls | Agent LLM calls |
|---|---|---|
| `action: route` → agent runs | **1** (classifier) | the agent's normal turn calls |
| `action: answer` (capability question) | **1** (classifier produces the answer) | **0** |
| `action: clarify` (ambiguous) | **1** (classifier produces the question) | **0** |
| Explicit `agentId="foo"` (no auto) | **0** | the agent's normal turn calls |
| Caller supplies `RunInput.WorkspaceIntake` | **0** | the agent's normal turn calls (or 0 when the override carries an answer/clarify) |
| Cross-turn reuse of prior `route` decision | **0** | the agent's normal turn calls |

The workspace-intake call is exactly one LLM round-trip, and capability
questions are answered by that same single call — no second LLM-driven turn
is needed.

---

## 5. Caller-provided overrides

Programmatic clients, UIs, and tests can bypass the workspace-intake LLM
call entirely by passing a `*TurnContext` on `RunInput.WorkspaceIntake`.
The runtime validates it and uses it as the turn's intake result with
`Source = "caller-provided"`. See `service/intake/StoreCallerProvided` and
the skip rule in `service/agent/intake_query.go:maybeRunIntakeSidecar`.

Examples:

- A UI form where the user picked the agent and provided a title — pass a
  `TurnContext{SelectedAgentID: "steward", Title: "Forecast", Mode: "route"}`.
- A test that needs deterministic routing — pass a fixed override.
- A cached prior turn — pass last turn's `TurnContext` to keep stickiness
  without trusting topic-shift heuristics.

---

## 6. Agent-intake configuration

When an agent needs additional refinement beyond what workspace intake
produces, declare an `intake:` block on the agent. The agent intake sidecar
runs after agent resolution and overrides specific fields on the
`TurnContext`. It **never** changes the selected agent — that contract is
enforced in code by `intake.SanitizeAgentRefinement()`.

```yaml
# agent.yaml
id: steward
name: Steward Agent
intake:
  enabled: true
  scope:
    - title
    - context
    - intent
    - clarification
    - profile
    - template
  modelPreferences:
    hints: [claude-haiku]
    intelligencePriority: 0.2
    speedPriority: 0.9
  confidenceThreshold: 0.85
  maxTokens: 400
  timeoutSec: 15
  triggerOnTopicShift: true
  topicShiftThreshold: 0.65
```

The `modelPreferences` field accepts both YAML shapes:

```yaml
modelPreferences:
  hints: [claude-haiku, gpt-5-mini]                    # string-list form
  # or:
  hints: [{name: claude-haiku}, {name: gpt-5-mini}]    # MCP-style objects
```

Both normalize to `Hints []string` internally.

---

## 7. The TurnContext shape

Both layers (workspace + agent) and caller-provided overrides produce and
consume the **same** `TurnContext` type at `service/intake/context.go`.
There is no parallel "agent hints" struct.

| Field | Workspace intake | Agent intake | Caller override |
|---|---|---|---|
| `SelectedAgentID` | sets | **never sets** | sets |
| `Mode` (`route` / `clarify`) | sets | **never sets** | sets |
| `Source` (`workspace` / `agent` / `reused` / `caller-provided` / `fallback`) | sets to `workspace` | sets to `agent` | sets to `caller-provided` |
| `Title`, `Intent`, `Context` | optional | optional | optional |
| `SuggestedProfileId`, `TemplateId`, `ActivateSkills`, `AppendToolBundles` | optional | optional | optional |
| `ClarificationNeeded`, `ClarificationQuestion` | optional | optional | optional |
| `Confidence` | sets | optional | sets |

The "agent never writes who/mode" rule is enforced at runtime — if agent
intake's output sets `SelectedAgentID` or `Mode`, the runtime drops those
fields and emits a diagnostic via `intake.SanitizeAgentRefinement()`.

---

## 8. Persistence and SSE

All intake-driven assistant messages — capability answers, clarifications,
and regular agent responses — go through the same write path:

1. Construct an `apiconv.MutableMessage` with role=`assistant`, type=`text`,
   the conversation+turn IDs, and the content.
2. Call `s.conversation.PatchMessage(ctx, msg)`.
3. The conversation service persists the message via `dao.Operate` and
   publishes a `streaming.EventTypeAssistant` SSE event automatically (see
   `internal/service/conversation/service.go:780–824`).

Live UIs receive the same event regardless of whether the message came from
the workspace-intake classifier (`status=intake.answer` /
`status=intake.clarify`) or a normal agent generate (no special status).
Past turns are transcript-owned through the standard message store. There
is no parallel persistence path and no UI bypass.

---

## 9. Telemetry

The workspace-intake classifier logs one structured `agent.selector ...`
line per call, including:

- conversation id,
- model name,
- elapsed time,
- the chosen action (`route` / `answer` / `clarify`) when applicable,
- selected agent id (for `route`), or text/question length (for `answer` /
  `clarify`).

The preset short-circuit logs `agent.Query preset short-circuit ...` when
it bypasses the agent's LLM call.

---

## 10. What's NOT in this layer

- **Marketplace / installation / signing** — out of scope.
- **Per-conversation routing schemas** — workspace intake currently uses
  the workspace-wide schema; per-conversation overrides are deferred.
- **Embedding-based topic-shift detection** — agent-intake reuse uses the
  existing Jaccard helper at
  `service/agent/intake_query.go:jaccardWordSimilarity`. Embeddings are
  deferred until a real need surfaces.
- **Prompt caching for the workspace-intake call** — the prompt is large
  enough to benefit from prompt caching, but caching infrastructure is
  separate work; not yet wired.

---

## 11. Migration notes

If you previously relied on the hardcoded capability-detection markers
(`isCapabilityDiscoveryQuery` returning true for "what can you do",
"capabilities", etc.), those markers have been **removed**. The
workspace-intake LLM router replaces them with a configurable prompt.
Default behavior is unchanged for typical capability phrasings — the router
will emit `action: answer` for them — but you now control the detection by
editing the prompt rather than touching code.

If you previously relied on `agent_selector` being explicitly invoked for
capability turns (e.g. via custom routing), that still works for explicit
invocations (`agentId=agent_selector`). Auto-selection no longer routes
through `agent_selector` for capability questions because the classifier
produces the answer directly.
