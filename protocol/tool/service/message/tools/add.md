Add an assistant message to the current turn.

Rules:
- conversation id, turn id, and parent message linkage come from runtime context
- currently only `role="assistant"` is supported
- use this when the model intentionally wants to persist a mid-turn assistant note
- the emitted message remains in conversation history for later model calls in the same turn
- after calling this tool, do not repeat the same note verbatim in a later assistant answer
- do not use this to replace normal streaming or final assistant output
