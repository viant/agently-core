{{- $op := .Context.operation -}}
{{- if $op.terminal -}}
Async operation {{$op.id}} for {{$op.toolName}} changed to {{$op.status}}.
{{- if and (eq $op.state "completed") $op.response }}
The latest tool result is already available. Do not call the async tool again.
Answer the user from the latest result.
{{- else if eq $op.state "completed" }}
The async operation completed. Use the latest completed result already in the
conversation history and answer the user. Do not call the async tool again just
to confirm completion.
{{- else if or (eq $op.state "failed") (eq $op.state "canceled") }}
Do not retry the async tool automatically. Explain the terminal status to the
user and include the latest error or status details. Only retry if the user
explicitly asks for it.
{{- else }}
The operation is terminal. Use the latest terminal result already in the
conversation history and answer the user. Do not call the async tool again.
{{- end }}
{{- else -}}
Async operation {{$op.id}} for {{$op.toolName}} is still in progress ({{$op.status}}).
{{- if $op.statusToolName }}
Call `{{$op.statusToolName}}` next to fetch the latest status before answering.
{{- if $op.statusToolArgsJSON }}
Use these exact arguments:
{{$op.statusToolArgsJSON}}
{{- end }}
Do not call `{{$op.toolName}}` again while `{{$op.statusToolName}}` is available.
{{- else }}
If the integration uses in-band polling on the same tool, call `{{$op.toolName}}`
again with the same request before answering.
{{- if $op.requestArgsJSON }}
Reuse these exact request arguments:
{{$op.requestArgsJSON}}
{{- end }}
{{- end }}
Do not re-run unrelated discovery or resource-reading tools before that retry
unless the user request changed or the exact request arguments are unavailable.
{{- end -}}
