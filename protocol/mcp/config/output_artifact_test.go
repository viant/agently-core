package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutputArtifactPolicyConfiguration(t *testing.T) {
	var config MCPClient
	require.NoError(t, json.Unmarshal([]byte(`{"outputArtifacts":{"SpreadsheetEditorDownload":{"name":"spreadsheet-editor.xlsx","maxBytes":1024}}}`), &config))
	policy := config.OutputArtifacts["SpreadsheetEditorDownload"]
	require.NoError(t, policy.Validate())
	require.EqualValues(t, 1024, policy.ByteLimit())
	require.EqualValues(t, MaxOutputArtifactBytes, (OutputArtifact{}).ByteLimit())
	for _, name := range []string{"../workbook.xlsx", "a/b", `a\b`, "..", "a\r\nb", string([]byte{'a', 0})} {
		require.Error(t, (OutputArtifact{Name: name}).Validate())
	}
	require.Error(t, (OutputArtifact{MaxBytes: -1}).Validate())
	require.Error(t, (OutputArtifact{MaxBytes: MaxOutputArtifactBytes + 1}).Validate())
}
