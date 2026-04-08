{{- $op := .Context.operation -}}
{{- if $op.terminal -}}
Async operation {{$op.id}} for {{$op.toolName}} changed to {{$op.status}}.
Call the matching status tool to fetch the latest result before answering.
{{- else -}}
Async operation {{$op.id}} for {{$op.toolName}} is still in progress ({{$op.status}}).
Call the matching status tool to fetch the latest result before answering.
{{- end -}}
