package slack

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/viant/agently-core/sdk"
)

// All examples below are independently constructed synthetic authoring inputs.
const sampleStart = `{"version":1,"id":"brief","sequence":1,"mode":"start","grammar":"report-document-v1","title":"Example report","blocks":[{"id":"summary","kind":"markdownBlock","markdown":"**Authored conclusion.**"}]}`
const sampleCommit = `{"version":1,"id":"brief","sequence":2,"mode":"commit"}`
const sampleURL = "https://example.org/app/conversation/example"

func fence(body string) string { return "```forge-report\n" + body + "\n```\n" }
func present(t *testing.T, content string, mode ResponseMode) ResponseOutput {
	t.Helper()
	got, err := PresentResponse(ResponseInput{Content: content, ConversationURL: sampleURL, Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPresentationModesGolden(t *testing.T) {
	content := "Public conclusion.\n\n" + fence(sampleStart) + fence(sampleCommit)
	header := "[View full response in Agently](" + sampleURL + ")\n\n"
	for _, tc := range []struct {
		mode ResponseMode
		want string
	}{
		{"", header + content}, {ResponseRaw, header + content},
		{ResponseCLICompatible, header + "Public conclusion.\n\n[Interactive content]\n\n[report] Example report\n**Authored conclusion.**"},
		{ResponseSummary, header + "Public conclusion.\n\n### Example report\n**Authored conclusion.**"},
		{ResponseNormalizedSummary, header + "Public conclusion.\n\n### Example report\n**Authored conclusion.**"},
		{ResponseReportText, header + "Public conclusion.\n\n### Example report\n**Authored conclusion.**"},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			got := present(t, content, tc.mode)
			if got.Text != tc.want || len(got.Diagnostics) != 0 {
				t.Fatalf("got %+v, want %q", got, tc.want)
			}
		})
	}
	if _, err := PresentResponse(ResponseInput{Mode: "unknown"}); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestStandaloneDeduplicationBelongsToSlackPresentation(t *testing.T) {
	for _, mode := range []ResponseMode{"", ResponseRaw, ResponseCLICompatible, ResponseSummary, ResponseNormalizedSummary, ResponseReportText} {
		t.Run(string(mode), func(t *testing.T) {
			input := ResponseInput{ConversationURL: sampleURL, Mode: mode, Content: "Final text.", StandaloneMessages: []*sdk.TurnMessageState{
				nil,
				{MessageID: "note", Content: "First version."},
				{MessageID: "note", Content: "Later version."},
				{MessageID: "other", Content: "First version."},
				{Content: "Without identity."},
				{Content: "Without identity."},
				{MessageID: "empty"},
				{MessageID: "empty", Content: "Later nonempty version."},
				{MessageID: "final-text", Content: "Final text."},
			}}
			before, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			got, err := PresentResponse(input)
			if err != nil {
				t.Fatal(err)
			}
			want := "[View full response in Agently](" + sampleURL + ")\n\nFirst version.\n\nFirst version.\n\nWithout identity.\n\nWithout identity.\n\nFinal text.\n\nFinal text."
			if got.Text != want || len(got.Diagnostics) != 0 {
				t.Fatalf("deduplication changed: %+v, want %q", got, want)
			}
			after, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("presentation mutated canonical input")
			}
		})
	}
}

func TestOrdinaryTextAndLiteralPlaceholder(t *testing.T) {
	content := "Literal [Interactive content].\n\n```json\n{\"example\":true}\n```\n\n```go\nfmt.Println(\"hello\")\n```"
	for _, mode := range []ResponseMode{ResponseCLICompatible, ResponseSummary, ResponseNormalizedSummary, ResponseReportText} {
		got := present(t, content, mode)
		if got.Text != "[View full response in Agently]("+sampleURL+")\n\n"+content || len(got.Diagnostics) != 0 {
			t.Fatalf("ordinary text changed: %+v", got)
		}
		if hello := present(t, "Hello!", mode); !strings.HasPrefix(hello.Text, "[View full response in Agently]("+sampleURL+")\n\nHello!") {
			t.Fatal(hello)
		}
	}
	got := present(t, "Literal [Interactive content].\n"+fence(sampleStart)+fence(sampleCommit), ResponseSummary)
	if strings.Count(got.Text, "[Interactive content]") != 1 {
		t.Fatal("literal marker removed")
	}
}

func TestCompatibleExtractionFrozenRules(t *testing.T) {
	// Raw (untrimmed) scope/ID identity, exact kinds and modes, commit order,
	// repeated start, foreign commit, ignored append/patch/replace and replay.
	input := fence(`{"scope":"a","id":"same","mode":"start","title":"A","blocks":[{"kind":"markdownBlock","markdown":"first"},{"kind":"markdownBlock","markdown":"second"}]}`) +
		fence(`{"scope":"b","id":"same","mode":"start","title":"B","blocks":[{"kind":"markdownBlock","markdown":"other scope"}]}`) +
		fence(`{"scope":"a","id":"same","mode":"append","blocks":[{"kind":"markdownBlock","markdown":"ignored"}]}`) +
		fence(`{"scope":"a","id":"same","mode":"patch","blocks":[{"kind":"markdownBlock","markdown":"ignored"}]}`) +
		fence(`{"scope":"a","id":"same","mode":"replace","blocks":[{"kind":"markdownBlock","markdown":"ignored"}]}`) +
		fence(`{"scope":" a","id":"same","mode":"commit"}`) +
		fence(`{"scope":"b","id":"same","mode":"commit"}`) +
		fence(`{"scope":"a","id":"same","mode":"commit"}`) +
		fence(`{"scope":"a","id":"same","mode":"commit"}`)
	got, limited := compatibleSummaries(input)
	if limited || len(got) != 2 || got[0] != (reportText{"B", "other scope"}) || got[1] != (reportText{"A", "first"}) {
		t.Fatalf("%+v limited=%v", got, limited)
	}
	input += fence(`{"scope":"a","id":"same","mode":"start","blocks":[{"kind":"markdownBlock","markdown":"stale"}]}`) +
		fence(`{"scope":"a","id":"same","mode":"start","blocks":[{"kind":"MarkdownBlock","markdown":"wrong kind"},{"kind":"markdownBlock","markdown":""}]}`) +
		fence(`{"scope":"a","id":"same","mode":"commit"}`)
	got, _ = compatibleSummaries(input)
	if len(got) != 2 {
		t.Fatal("repeated start failed to clear pending")
	}
}

func TestSummaryLimitUnicode(t *testing.T) {
	for _, size := range []int{8192, 8193} {
		text := strings.Repeat("ą🙂", size/2) + strings.Repeat("終", size%2)
		raw, err := json.Marshal(map[string]any{"version": 1, "id": "brief", "mode": "start", "sequence": 1, "grammar": "report-document-v1", "blocks": []any{map[string]any{"id": "s", "kind": "markdownBlock", "markdown": text}}})
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []ResponseMode{ResponseCLICompatible, ResponseSummary, ResponseNormalizedSummary} {
			got := present(t, fence(string(raw))+fence(sampleCommit), mode)
			if !utf8.ValidString(got.Text) || strings.Contains(got.Text, summaryTruncated) != (size > 8192) {
				t.Fatal("incorrect Unicode truncation")
			}
			if !strings.Contains(got.Text, string([]rune(text)[:8192])) {
				t.Fatal("summary lost content")
			}
		}
		got := present(t, fence(string(raw))+fence(sampleCommit), ResponseReportText)
		if !strings.Contains(got.Text, text) || strings.Contains(got.Text, summaryTruncated) {
			t.Fatal("report_text truncated")
		}
	}
}

func TestNormalizedTransactionsUseFinalNativeSource(t *testing.T) {
	for _, tc := range []struct{ name, content, want string }{
		{"patch_remove", fence(sampleStart) + fence(`{"version":1,"id":"brief","sequence":2,"mode":"append","blocks":[{"id":"next","kind":"markdownBlock","markdown":"Appended."}]}`) + fence(`{"version":1,"id":"brief","sequence":3,"mode":"patch","blocks":[{"id":"next","markdown":"Patched."}],"removeBlockIds":["summary"]}`), "Patched."},
		{"replace", fence(sampleStart) + fence(`{"version":1,"id":"brief","sequence":2,"mode":"replace","grammar":"report-document-v1","title":"Replacement","blocks":[{"id":"summary","kind":"markdownBlock","markdown":"Replaced."}]}`), "Replaced."},
		{"implicit_commit", fence(sampleStart), "**Authored conclusion.**"},
		{"idempotent_replay", fence(sampleStart) + fence(sampleStart) + fence(sampleCommit), "**Authored conclusion.**"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []ResponseMode{ResponseNormalizedSummary, ResponseReportText} {
				got := present(t, tc.content, mode)
				if !strings.Contains(got.Text, tc.want) || len(got.Diagnostics) != 0 {
					t.Fatalf("%s: %+v", mode, got)
				}
				if tc.name == "patch_remove" && strings.Contains(got.Text, "Authored conclusion") {
					t.Fatal("removed block shown")
				}
			}
		})
	}
	legacy := present(t, fence(sampleStart), ResponseSummary)
	if strings.Contains(legacy.Text, "Authored conclusion") || len(legacy.Diagnostics) == 0 {
		t.Fatal("legacy implicit commit changed")
	}
}

func TestRichFallbackNeverLeaksRawPayload(t *testing.T) {
	for _, input := range []string{
		"```forge-report\n{\"private_example\":\"RAW_PAYLOAD\"}\n```",
		"```forge-report\nRAW_PAYLOAD",
		"```forge-report broken RAW_PAYLOAD",
		"```forge-ui\n{\"example\":\"RAW_PAYLOAD\"}\n```",
		"```forge-data\n{\"id\":\"example\",\"data\":\"RAW_PAYLOAD\"}\n```",
		fence(strings.Replace(sampleStart, `"sequence":1`, `"sequence":2`, 1)),
		fence(sampleStart) + fence(`{"version":1,"id":"brief","sequence":3,"mode":"commit"}`),
	} {
		for _, mode := range []ResponseMode{ResponseCLICompatible, ResponseSummary, ResponseNormalizedSummary, ResponseReportText} {
			got := present(t, "Public prose.\n"+input, mode)
			if strings.Contains(got.Text, "RAW_PAYLOAD") || strings.Contains(got.Text, "```forge-") || !strings.Contains(got.Text, "Public prose.") || !strings.Contains(got.Text, sampleURL) || len(got.Diagnostics) == 0 {
				t.Fatalf("%s: %+v", mode, got)
			}
		}
	}
	for _, mode := range []ResponseMode{ResponseSummary, ResponseNormalizedSummary, ResponseReportText} {
		got := present(t, fence(sampleStart)+fence(sampleCommit)+"```forge-ui\nRAW_PAYLOAD", mode)
		if !strings.Contains(got.Text, "Authored conclusion") || strings.Contains(got.Text, "RAW_PAYLOAD") || strings.Count(got.Text, interactiveNotice) != 1 {
			t.Fatalf("mixed input: %+v", got)
		}
	}
}

func TestReportTextAuthoredFieldsAndNoReferenceDuplication(t *testing.T) {
	input := fence(`{"version":1,"id":"brief","sequence":1,"mode":"start","grammar":"report-document-v1","title":"Document","subtitle":"Subtitle","description":"Description","blocks":[
{"id":"tabs","kind":"tabGroupBlock","title":"Tabs","sectionIds":["section"]},
{"id":"section","kind":"sectionBlock","title":"Section","subtitle":"Section subtitle","description":"Section description","blockIds":["group"]},
{"id":"group","kind":"compositeBlock","title":"Group","description":"Group description","childBlockIds":["text","info","note"]},
{"id":"text","kind":"markdownBlock","title":"Text","markdown":"Markdown once."},
{"id":"info","kind":"infoPanelBlock","title":"Info","eyebrow":"Eyebrow","description":"Info description","body":"Info body."},
{"id":"note","kind":"calloutBlock","title":"Note","description":"Note description","body":"Note body."},
{"id":"chart","kind":"chartBlock","title":"DATA_SECRET","options":{"name":"DATA_SECRET"}},
{"id":"kpi","kind":"kpiBlock","value":"DATA_SECRET"},
{"id":"table","kind":"tableBlock","rows":[{"x":"DATA_SECRET"}]}]}`)
	got := present(t, input, ResponseReportText)
	for _, text := range []string{"Document", "Subtitle", "Description", "Tabs", "Section subtitle", "Section description", "Group description", "Markdown once.", "Eyebrow", "Info description", "Info body.", "Note description", "Note body."} {
		if strings.Count(got.Text, text) != 1 {
			t.Fatalf("missing or duplicated %q: %s", text, got.Text)
		}
	}
	if strings.Contains(got.Text, "DATA_SECRET") || len(got.Diagnostics) == 0 {
		t.Fatalf("unsupported fields: %+v", got)
	}
	unknown := present(t, fence(`{"version":1,"id":"brief","sequence":1,"mode":"start","grammar":"dashboard-v1","blocks":[{"id":"report","kind":"dashboard.report","body":"DATA_SECRET"}]}`), ResponseReportText)
	if strings.Contains(unknown.Text, "DATA_SECRET") || len(unknown.Diagnostics) == 0 {
		t.Fatal(unknown)
	}
}

func TestMultipleReportsAndMissingSummary(t *testing.T) {
	second := strings.ReplaceAll(strings.ReplaceAll(sampleStart, "brief", "other"), "Authored conclusion.", "Second conclusion.")
	input := fence(sampleStart) + fence(sampleCommit) + fence(second) + fence(strings.ReplaceAll(sampleCommit, "brief", "other"))
	for _, mode := range []ResponseMode{ResponseSummary, ResponseNormalizedSummary, ResponseReportText} {
		got := present(t, input, mode)
		if !strings.Contains(got.Text, "Second conclusion.") || strings.Count(got.Text, "View full response in Agently") != 1 {
			t.Fatal(got)
		}
	}
	got := present(t, fence(`{"version":1,"id":"brief","sequence":1,"mode":"start","grammar":"report-document-v1","blocks":[{"id":"table","kind":"tableBlock"}]}`)+fence(sampleCommit), ResponseSummary)
	if len(got.Diagnostics) == 0 || !strings.Contains(got.Text, interactiveNotice) {
		t.Fatal(got)
	}
	missing := fence(`{"version":1,"id":"missing","sequence":1,"mode":"start","grammar":"report-document-v1","blocks":[{"id":"table","kind":"tableBlock"}]}`) + fence(`{"version":1,"id":"missing","sequence":2,"mode":"commit"}`)
	for _, mode := range []ResponseMode{ResponseCLICompatible, ResponseSummary, ResponseNormalizedSummary, ResponseReportText} {
		mixed := present(t, input+missing, mode)
		if !strings.Contains(mixed.Text, "Authored conclusion.") || strings.Count(mixed.Text, interactiveNotice) != 1 {
			t.Fatalf("partial reports: %+v", mixed)
		}
	}
}

func TestNestedRichContentUsesNativeProjection(t *testing.T) {
	start := strings.Replace(sampleStart, `**Authored conclusion.**`, "Visible.\\n```forge-ui\\nRAW_PAYLOAD\\n```", 1)
	for _, mode := range []ResponseMode{ResponseCLICompatible, ResponseSummary, ResponseNormalizedSummary, ResponseReportText} {
		got := present(t, fence(start)+fence(sampleCommit), mode)
		if strings.Contains(got.Text, "RAW_PAYLOAD") || !strings.Contains(got.Text, "Visible.") || len(got.Diagnostics) == 0 {
			t.Fatal(got)
		}
	}
}

func FuzzPresentResponse(f *testing.F) {
	for _, seed := range []string{"hello", fence(sampleStart) + fence(sampleCommit), "```forge-report\n{", "```json\n{}\n```"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		for _, mode := range []ResponseMode{ResponseRaw, ResponseCLICompatible, ResponseSummary, ResponseNormalizedSummary, ResponseReportText} {
			first := present(t, input, mode)
			second := present(t, input, mode)
			if first.Text != second.Text || strings.Join(first.Diagnostics, ",") != strings.Join(second.Diagnostics, ",") {
				t.Fatal("nondeterministic output")
			}
			if mode == ResponseRaw && first.Text != "[View full response in Agently]("+sampleURL+")"+rawSuffix(input) {
				t.Fatal("raw changed")
			}
		}
	})
}

func BenchmarkPresentResponse(b *testing.B) {
	input := fence(sampleStart) + fence(sampleCommit)
	for _, mode := range []ResponseMode{ResponseRaw, ResponseSummary, ResponseNormalizedSummary, ResponseReportText} {
		b.Run(string(mode), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = PresentResponse(ResponseInput{Content: input, ConversationURL: sampleURL, Mode: mode})
			}
		})
	}
}

func rawSuffix(content string) string {
	if content == "" {
		return ""
	}
	return "\n\n" + content
}

func TestAllModesLinkFirstAndStandaloneMessages(t *testing.T) {
	for _, mode := range []ResponseMode{ResponseRaw, ResponseCLICompatible, ResponseSummary, ResponseNormalizedSummary, ResponseReportText} {
		for _, final := range []string{"Final prose.", "", fence(sampleStart) + fence(sampleCommit)} {
			got, err := PresentResponse(ResponseInput{Mode: mode, ConversationURL: sampleURL, Content: final,
				StandaloneMessages: []*sdk.TurnMessageState{nil, {MessageID: "note", Content: "### Public findings\nAuthored findings. ą🙂"}}})
			if err != nil {
				t.Fatal(err)
			}
			prefix := "[View full response in Agently](" + sampleURL + ")\n\n### Public findings\nAuthored findings. ą🙂"
			if !strings.HasPrefix(got.Text, prefix) || strings.Count(got.Text, sampleURL) != 1 || strings.Count(got.Text, "Authored findings.") != 1 {
				t.Fatalf("%s: %q", mode, got.Text)
			}
			if strings.Count(got.Text, "Final prose.") != strings.Count(final, "Final prose.") {
				t.Fatal("final lost or duplicated")
			}
		}
		got := present(t, "### Key findings\nEmbedded findings.", mode)
		if strings.Count(got.Text, "Embedded findings.") != 1 {
			t.Fatal("embedded findings duplicated")
		}
		empty := present(t, "", mode)
		if empty.Text != "[View full response in Agently]("+sampleURL+")" {
			t.Fatal(empty)
		}
		// URL is optional for SDK callers. Raw bodies retain whitespace and code.
		for _, body := range []string{"", "  ", "\n```json\n{}\n```\n"} {
			got, err := PresentResponse(ResponseInput{Mode: mode, Content: body})
			if err != nil || strings.Contains(got.Text, "https://") {
				t.Fatal(got, err)
			}
			if mode == ResponseRaw && got.Text != body {
				t.Fatal("raw body changed")
			}
		}
	}
}

func TestIndependentMessagesNeverJoinReportTransactions(t *testing.T) {
	// Two starts with the same report identity are independent canonical messages.
	// A following commit must not commit the previous message's report.
	for _, mode := range []ResponseMode{ResponseCLICompatible, ResponseSummary} {
		got, err := PresentResponse(ResponseInput{Mode: mode, ConversationURL: sampleURL,
			StandaloneMessages: []*sdk.TurnMessageState{{Content: "Public note.\n" + fence(sampleStart)}}, Content: fence(sampleCommit)})
		if err != nil || strings.Contains(got.Text, "Authored conclusion.") || !strings.Contains(got.Text, "Public note.") || strings.Count(got.Text, interactiveNotice) != 1 {
			t.Fatal(got, err)
		}
	}
	for _, mode := range []ResponseMode{ResponseNormalizedSummary, ResponseReportText} {
		second := strings.ReplaceAll(sampleStart, "Authored conclusion.", "Independent conclusion.")
		got, err := PresentResponse(ResponseInput{Mode: mode, StandaloneMessages: []*sdk.TurnMessageState{{Content: fence(sampleStart)}}, Content: fence(second)})
		if err != nil || strings.Count(got.Text, "Authored conclusion.") != 1 || strings.Count(got.Text, "Independent conclusion.") != 1 {
			t.Fatal(got, err)
		}
	}
}

func TestSummaryModesAgreeWithoutUpdatesAndDifferAfterPatch(t *testing.T) {
	original := fence(sampleStart) + fence(sampleCommit)
	if present(t, original, ResponseSummary).Text != present(t, original, ResponseNormalizedSummary).Text {
		t.Fatal("unchanged reports should agree")
	}
	updated := fence(sampleStart) + fence(`{"version":1,"id":"brief","sequence":2,"mode":"patch","blocks":[{"id":"summary","markdown":"Updated conclusion."}]}`) + fence(`{"version":1,"id":"brief","sequence":3,"mode":"commit"}`)
	initial, normalized := present(t, updated, ResponseSummary), present(t, updated, ResponseNormalizedSummary)
	if !strings.Contains(initial.Text, "Authored conclusion.") || strings.Contains(initial.Text, "Updated conclusion.") || !strings.Contains(normalized.Text, "Updated conclusion.") || strings.Contains(normalized.Text, "Authored conclusion.") {
		t.Fatal(initial, normalized)
	}
}

func TestPresentationNoticesAreAggregatedAcrossMessages(t *testing.T) {
	extra := strings.Replace(sampleStart, `"blocks":[`, `"subtitle":"More detail","blocks":[`, 1)
	for _, mode := range []ResponseMode{ResponseCLICompatible, ResponseSummary, ResponseNormalizedSummary} {
		got, err := PresentResponse(ResponseInput{Mode: mode, Content: fence(extra) + fence(sampleCommit), StandaloneMessages: []*sdk.TurnMessageState{{Content: fence(extra) + fence(sampleCommit)}}})
		if err != nil || strings.Count(got.Text, additionalNotice) != 1 || strings.Contains(got.Text, interactiveNotice) {
			t.Fatal(got, err)
		}
		got, err = PresentResponse(ResponseInput{Mode: mode, Content: "```forge-report\nBROKEN", StandaloneMessages: []*sdk.TurnMessageState{{Content: fence(extra) + fence(sampleCommit)}}})
		if err != nil || strings.Count(got.Text, interactiveNotice) != 1 || strings.Contains(got.Text, additionalNotice) || strings.Contains(got.Text, "BROKEN") {
			t.Fatal(got, err)
		}
	}
	got := present(t, "```forge-ui\n{\"view\":\"example\"}\n```", ResponseReportText)
	if !strings.Contains(got.Text, additionalNotice) || strings.Contains(got.Text, interactiveNotice) {
		t.Fatal(got)
	}
}
