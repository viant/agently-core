# Follow-up chains

A **follow-up chain** is a multi-turn sequence inside a single conversation
where each turn builds on the previous one: clarify → plan → execute →
refine. Agently doesn't require a magic primitive for this — it falls out of
the turn model + the intake sidecar — but a few concrete affordances make
chains robust.

## Affordances

| Affordance | Where | Role |
|---|---|---|
| `TurnContext` carry-over | [runtime/requestctx/](../runtime/requestctx/) | Intake's classification + per-turn metadata flow into the next turn via conversation context |
| Follow-up intent in intake | [service/intake/](../service/intake/) | Detects "this turn references the prior turn" and carries the profile forward |
| Child-agent turns linked to parent | [protocol/tool/service/llm/agents/](../protocol/tool/service/llm/agents/) | Each child turn records `parentTurnId`; canonical reducer shows the chain |
| Async op ↔ same conversation | [doc/async.md](async.md) | Background work re-enters the reactor as a new turn when results arrive |
| Steering | [sdk/handler.go](../sdk/handler.go) `/v1/conversations/{id}/turns/{turnId}/steer` | Edit a queued follow-up before it runs |
| Scheduled follow-ups | [service/scheduler/](../service/scheduler/) | Cron emits a turn against an existing conversation |

## Anatomy of a chain

```
turn T0   user:    "help me diagnose campaign 123"
          intake:  profile=performance_analysis, confidence=0.9
          reactor: plan → tool calls → assistant response
turn T1   user:    "now recommend fixes"
          intake:  same profile carries over (follow-up detected)
          reactor: consumes T0's findings via conversation history
                   (see context-management.md)
turn T2   runtime: async op from T0 completes → auto-inserted as turn
          reactor: resumes, adds the freshly-arrived data to the narrative
```

## Follow-up detection

The intake sidecar uses:

- Recent turns' profile ids — a short-lived "sticky profile" window.
- Semantic similarity against the last turn's question.
- Explicit user cues ("continue", "based on that", pronouns referencing prior entities).

When detection is high-confidence, the sidecar populates `RunInput.PromptProfileId` so the orchestrator doesn't re-run `prompt:list`. See [doc/prompts.md](prompts.md).

## Parent / child chains

Delegation via `llm/agents:start` creates a separate conversation for the child but links back:

- Parent turn record carries `childConversationId`.
- Child's final message is surfaced back to the parent as a tool call result.
- UI shows the child inline or expanded depending on user preference.

## Queueing & ordering

When a user sends a message while a turn is in-flight:

- The new message is queued on `turn_queue` ([doc/conversation-model.md](conversation-model.md)).
- On completion of the current turn, the next queued turn starts.
- The UI can edit / reorder / drop queued turns before they run.

## Extensibility

- **Custom follow-up detector**: replace the intake classifier (see [doc/planning-and-intake.md](planning-and-intake.md)).
- **Chain-scope memory**: attach data to the conversation row for the whole chain's lifetime (use `pkg/agently/conversation` write methods).
- **Automatic chains** (cron → follow-up): wire a scheduled task that issues a follow-up turn against an existing conversation id.

## Related docs

- [doc/planning-and-intake.md](planning-and-intake.md)
- [doc/async.md](async.md)
- [doc/conversation-model.md](conversation-model.md)
