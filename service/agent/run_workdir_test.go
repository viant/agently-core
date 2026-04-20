package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/workspace"
)

func TestEnsureResolvedWorkdir_DataDriven(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))
	filePath := filepath.Join(repoDir, "README.md")
	require.NoError(t, os.WriteFile(filePath, []byte("ok"), 0o644))

	type testCase struct {
		name     string
		input    *QueryInput
		expected string
	}

	testCases := []testCase{
		{
			name: "uses existing context workdir first",
			input: &QueryInput{
				Context: map[string]interface{}{"workdir": repoDir},
			},
			expected: repoDir,
		},
		{
			name: "uses agent default workdir",
			input: &QueryInput{
				Agent: &agentmdl.Agent{DefaultWorkdir: repoDir},
			},
			expected: repoDir,
		},
		{
			name: "extracts directory path from query",
			input: &QueryInput{
				Query: "Analyze " + repoDir + " and summarize it.",
			},
			expected: repoDir,
		},
		{
			name: "extracts file path from query and normalizes to dir",
			input: &QueryInput{
				Query: "Inspect " + filePath + " for issues.",
			},
			expected: repoDir,
		},
		{
			name: "trims trailing punctuation from query path",
			input: &QueryInput{
				Query: "Analyze " + repoDir + ".",
			},
			expected: repoDir,
		},
		{
			name:     "falls back to workspace root",
			input:    &QueryInput{},
			expected: repoDir,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			workspace.SetRoot(repoDir)
			got := ensureResolvedWorkdir(tc.input)
			assert.EqualValues(t, tc.expected, got)
			assert.EqualValues(t, tc.expected, tc.input.Context["workdir"])
			assert.EqualValues(t, tc.expected, tc.input.Context["resolvedWorkdir"])
		})
	}
}
