package resource

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/intent"
)

func TestProfileSummaryExcludesInstructionsAndPreservesDeclarations(t *testing.T) {
	original := intent.Profile{ID: "example", Name: "Example review", Description: "Review an example", PreferredTools: []string{"example:read*"}, Instructions: "Private instruction body", Messages: []intent.Message{{Role: "system", Text: "Additional instruction body"}}}
	raw, err := json.Marshal(original)
	require.NoError(t, err)
	summary, err := decodeProfileSummary(raw)
	require.NoError(t, err)
	require.Empty(t, summary.Messages)
	require.Empty(t, summary.Instructions)
	require.Equal(t, original.PreferredTools, summary.PreferredTools)
	copy, err := clonePublishedAI(summary)
	require.NoError(t, err)
	copy.PreferredTools[0] = "mutated"
	require.Equal(t, "example:read*", summary.PreferredTools[0])
}

// Compare only the per-read copy cost after the owning cache has decoded once.
// This is not an end-to-end catalog or model latency benchmark.
func BenchmarkProfileDiscoveryCopy(b *testing.B) {
	full := intent.Profile{ID: "example", Name: "Example review", Description: "Review an example", PreferredTools: []string{"example:read*"}, Messages: []intent.Message{{Role: "system", Text: strings.Repeat("Synthetic instruction. ", 8192)}}}
	raw, err := json.Marshal(full)
	if err != nil {
		b.Fatal(err)
	}
	summary, err := decodeProfileSummary(raw)
	if err != nil {
		b.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		value intent.Profile
	}{{"full-body", full}, {"summary", summary}} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := clonePublishedAI(test.value); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
