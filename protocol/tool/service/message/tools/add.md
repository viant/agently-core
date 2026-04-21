Add an assistant message to the current turn.

Rules:
- conversation id, turn id, and parent message linkage come from runtime context
- currently only `role="assistant"` is supported
- use this when the model intentionally wants to persist a mid-turn assistant note
- do not use this to replace normal streaming or final assistant output
