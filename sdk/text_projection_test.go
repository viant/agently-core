package sdk

import "testing"

// Expected values describe the native projection contract independently of the
// additive completed-text API and its shared implementation.
func TestNativeTextProjectionContract(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
		complete, rich      bool
	}{
		{"plain", "Ordinary text and literal [Interactive content].", "Ordinary text and literal [Interactive content].", true, false},
		{"unicode", "Zażółć 🙂 終", "Zażółć 🙂 終", false, false},
		{"code", "```json\n{\"example\":true}\n```", "```json\n{\"example\":true}\n```", true, false},
		{"open_code", "```go\nfmt.Println()", "```go\nfmt.Println()", false, false},
		{"unknown_language", "```forge-other\n{}\n```", "```forge-other\n{}\n```", true, false},
		{"ui", "Before.\n```forge-ui\n{}\n```\nAfter.", "Before.\n[Interactive content]\nAfter.", true, true},
		{"data_case", "```FORGE-DATA\n{}\n```", "[Interactive content]", true, true},
		{"report_open_body", "```forge-report\n{", "[Interactive content]", false, true},
		{"report_complete_open_body", "```forge-report\n{", "[Interactive content]", true, true},
		{"malformed_complete", "Before.```forge-data invalid", "Before.[Interactive content]", true, true},
		{"malformed_stream", "Before.```forge-data invalid", "Before.", false, false},
		{"partial_language", "Before.```forge-re", "Before.", false, false},
		{"partial_language_complete", "Before.```forge-re", "Before.```forge-re", true, false},
		{"partial_delimiter", "Before.``", "Before.", false, false},
		{"complete_delimiter", "Before.``", "Before.``", true, false},
		{"multiple_rich", "```forge-ui\n{}\n```\n```forge-data\n{}\n```", "[Interactive content]\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, rich := plainAGUIContent(tc.content, tc.complete)
			if got != tc.want || rich != tc.rich {
				t.Fatalf("native projection = (%q, %v), want (%q, %v)", got, rich, tc.want, tc.rich)
			}
			if tc.complete {
				got, rich = ProjectTextContent(tc.content, aguiInteractiveFallback)
				if got != tc.want || rich != tc.rich {
					t.Fatalf("completed projection = (%q, %v), want (%q, %v)", got, rich, tc.want, tc.rich)
				}
			}
		})
	}

}

func TestCompletedTextProjectionCustomPlaceholder(t *testing.T) {
	for _, placeholder := range []string{"", "[Open full response]"} {
		got, rich := ProjectTextContent("Literal [Interactive content].\n```forge-ui\n{}\n```", placeholder)
		if got != "Literal [Interactive content].\n"+placeholder || !rich {
			t.Fatalf("custom placeholder changed literal content: %q", got)
		}
	}
}
