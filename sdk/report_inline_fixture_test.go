package sdk

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type inlineReportFixture struct {
	Cases []struct {
		Name    string `json:"name"`
		Content string `json:"content"`
		Reports []struct {
			Scope           string `json:"scope"`
			ID              string `json:"id"`
			Grammar         string `json:"grammar"`
			Status          string `json:"status"`
			Sequence        int    `json:"sequence"`
			BlockCount      int    `json:"blockCount"`
			DataSourceCount int    `json:"dataSourceCount"`
		} `json:"reports"`
		Diagnostics []string `json:"diagnostics"`
	} `json:"cases"`
}

func TestNormalizeRenderedContent_SharedInlineReportFixtures(t *testing.T) {
	payload, err := os.ReadFile("testdata/report_inline_cases.json")
	require.NoError(t, err)
	var fixture inlineReportFixture
	require.NoError(t, json.Unmarshal(payload, &fixture))
	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			got := NormalizeRenderedContent(testCase.Content)
			require.NotNil(t, got)
			require.Len(t, got.Reports, len(testCase.Reports))
			for index, expected := range testCase.Reports {
				report := got.Reports[index]
				require.Equal(t, expected.Scope, report.Scope)
				require.Equal(t, expected.ID, report.ID)
				require.Equal(t, expected.Grammar, report.Grammar)
				require.Equal(t, expected.Status, report.Status)
				require.Equal(t, expected.Sequence, report.Sequence)
				require.Len(t, report.DataSources, expected.DataSourceCount)
				var source struct {
					Blocks []json.RawMessage `json:"blocks"`
				}
				require.NoError(t, json.Unmarshal(report.Source, &source))
				require.Len(t, source.Blocks, expected.BlockCount)
			}
			codes := warningCodes(got.Diagnostics)
			for _, code := range testCase.Diagnostics {
				require.Contains(t, codes, code)
			}
		})
	}
}
