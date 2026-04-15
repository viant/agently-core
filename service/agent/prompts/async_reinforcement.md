You are managing asynchronous tool operations for the current turn.

Rules:
1. If there are no changed or active async operations, continue normal reasoning.
2. Async polling has three modes:
   - `sameToolReuse: true`: call the original tool again with `requestArgsJSON`.
   - `statusToolName` + `statusToolArgsJSON`: call that explicit status tool.
   - `runtimePolled: true`: do not call any status tool yourself; wait for runtime updates managed autonomously by the runtime.
3. Never call the original start tool again for a non-terminal operation.
4. Never poll a terminal operation again.
5. For async child-agent operations: once the child conversation
   is terminal and has a final result, stop polling that child entirely. Use
   the latest child result already present in conversation history and move on
   to synthesis.
6. When all operations are resolved, use the latest results already in
   conversation history to answer.
7. Prefer changed operations over unchanged active ones.
8. Do not retry failed or canceled operations automatically unless the user
   explicitly asks.

Turn async summary:
- pending: {{.Context.turnAsync.pending}}
- active: {{.Context.turnAsync.active}}
- completed: {{.Context.turnAsync.completed}}
- failed: {{.Context.turnAsync.failed}}
- canceled: {{.Context.turnAsync.canceled}}
- allResolved: {{.Context.turnAsync.allResolved}}
{{- if .Context.changedOperations}}

Changed async operations:
{{- range .Context.changedOperations}}
- id: `{{.id}}`
  tool: `{{.toolName}}`
  status: `{{.status}}`
  terminal: {{.terminal}}
  {{- if .runtimePolled}}
  runtime polled: true (do not call the status tool yourself)
  {{- end}}
  {{- if .sameToolReuse}}
  same-tool reuse args: `{{.requestArgsJSON}}`
  {{- end}}
  {{- if .statusToolName}}
  status tool: `{{.statusToolName}}`
  status tool args: `{{.statusToolArgsJSON}}`
  {{- end}}
  {{- if .message}}
  message: {{.message}}
  {{- end}}
  {{- if and .terminal .error}}
  error: {{.error}}
  {{- end}}
  {{- if and (not .terminal) .instruction}}
  instruction: {{.instruction}}
  {{- end}}
  {{- if and .terminal .terminalInstruction}}
  terminal instruction: {{.terminalInstruction}}
  {{- end}}
{{- end}}
{{- end}}

Behavior:
{{- if .Context.turnAsync.allResolved}}
All async operations reached terminal state. Use the latest results in conversation history to answer the user. Do not poll again unless explicitly asked.
{{- else if .Context.turnAsync.hasSameToolReuse}}
At least one operation requires same-tool polling. Call the same tool with the exact `requestArgsJSON` shown above.
{{- else if .Context.turnAsync.hasExplicitStatusTool}}
At least one operation exposes a separate status function. Call that `statusToolName` with the exact `statusToolArgsJSON` shown above.
{{- else}}
Active operations are being handled autonomously by the runtime. Do not call any status tool yourself. Wait for the next async state update.
{{- end}}
