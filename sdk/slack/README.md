# Completed response presentation

`PresentResponse` presents a completed public assistant response as text. It is a
pure function: no transport, credentials, model calls, storage or Slack API.
Supply the exact completed turn's canonical final, optional public standalone
messages selected by `sdk.SelectStandaloneAssistantMessages(turn)`, and a
caller-owned conversation URL. The link is first in every mode. Standalone
messages follow in canonical order, then the final answer/report. Execution
narration, internal messages and tool results are excluded by native ownership,
without matching headings such as "Key findings" or comparing message contents.
The source conversation and browser report remain unchanged.

Native selection retains repeated message IDs. Only this Slack presenter removes
repetitions: the first standalone occurrence with a nonempty ID is presented.
Messages without IDs remain separate, and the final is always independent.
Message contents are not compared for deduplication. Neither selection nor
presentation mutates the source messages.

```go
result, err := slack.PresentResponse(slack.ResponseInput{
    Content:            finalContent,
    StandaloneMessages: sdk.SelectStandaloneAssistantMessages(turn),
    ConversationURL:    "https://example.org/app/conversation/example",
    Mode:               slack.ResponseSummary,
})
```

| Mode | Output |
| --- | --- |
| `raw` (also the zero value) | Link first, followed by unchanged message bodies, including Forge fences |
| `cli_compatible` | Link first, native prose projection, `[Interactive content]`, frozen CLI-style committed summaries, `[report]` headings |
| `summary` | Link first, the same summary extraction with readable headings without technical markers |
| `normalized_summary` | Link first, prose and first nonempty authored `markdownBlock.markdown` in each final native committed report |
| `report_text` | Link first, prose and authored text fields from final `report-document-v1` reports |

The CLI-compatible baseline uses matching raw scope/ID `start` and `commit`,
the first nonempty exact `markdownBlock`, and commit order. It ignores
append/patch/replace. This small, frozen extractor is independent of the CLI;
changing either presentation does not change the other. It omits CLI
infrastructure output and earlier stream narration. Each standalone message and
the final are processed independently; report transactions never cross messages.

Native modes reuse `sdk.NormalizeRenderedContent`, including transaction
assembly, accepted patches/removals, replace and implicit commit. Summary modes
can produce identical output when the selected summary has not changed.
`normalized_summary` reflects accepted updates; `summary` retains its frozen
start/commit extraction. Summary modes limit each summary to 8192 Unicode runes
with an explicit truncation notice.
`report_text` has no additional global truncation limit. It reads document
title/subtitle/description and text from markdown, info panels, callouts,
sections, composites and tab groups in the native flat block order. References
are not followed again. All authored sections are included; browser visibility
is not emulated. KPI values, tables, charts, datasets and expressions are not
evaluated or converted, and other report grammars are not lowered to prose.

Non-raw modes use the native complete-text projector to hide recognized rich
payloads, including malformed/open fences; they never fall back to raw JSON.
Intentional omissions produce `Additional interactive content is available in
Agently.` Unsupported or incomplete content produces `Some content could not be
presented here. View the full response in Agently.` Notices are aggregated once
across messages, with incomplete presentation taking precedence, while retaining
available prose and summaries. Ordinary code/JSON fences and
literal placeholder text follow the existing native projection. A missing URL
omits the link; an empty answer with a URL still starts with the link. An unknown
mode returns an error; expected presentation
limitations return fixed diagnostic codes without payloads.

The caller owns Markdown delivery, chunking, retries, URL construction and access
control. Present once before persisting an outbox; retry those exact stored parts.
