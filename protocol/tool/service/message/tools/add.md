Add an assistant message to the current turn.

Rules:
- conversation id, turn id, and parent message linkage come from runtime context
- currently only `role="assistant"` is supported
- use this when the model intentionally wants to persist a mid-turn assistant note
- the emitted message remains in conversation history for later model calls in the same turn
- after calling this tool, do not repeat the same note verbatim in a later assistant answer
- do not use this to replace normal streaming or final assistant output
- a message containing `forge-report` fences is checked by the native report parser before saving; malformed JSON or an unclosed fence returns an error without saving the message
- on a report parsing error, correct the JSON encoding and retry with the complete report and its data; claim the report is saved only after this tool succeeds
