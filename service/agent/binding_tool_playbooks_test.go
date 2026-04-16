package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/afs"
	afsurl "github.com/viant/afs/url"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/workspace"
)

func TestAppendToolPlaybooks_DataDriven(t *testing.T) {
	type testCase struct {
		name        string
		defs        []*llm.ToolDefinition
		setup       func(root string) error
		initialDocs []*binding.Document
		expectedLen int
		expectedURI string
	}

	root := t.TempDir()
	_ = os.Setenv("AGENTLY_WORKSPACE", root)
	t.Cleanup(func() { _ = os.Unsetenv("AGENTLY_WORKSPACE") })

	playbooksDir := filepath.Join(root, workspace.KindTool)
	_ = os.MkdirAll(playbooksDir, 0755)
	_ = os.MkdirAll(filepath.Join(root, workspace.KindToolInstructions), 0755)

	ctx := context.Background()
	service := &Service{fs: afs.New()}

	cases := []testCase{
		{
			name: "injects webdriver instructions from tools/instructions",
			defs: []*llm.ToolDefinition{{Name: "webdriver-browserRun"}},
			setup: func(root string) error {
				return os.WriteFile(filepath.Join(root, workspace.KindToolInstructions, "webdriver.md"), []byte("webdriver instructions"), 0644)
			},
			expectedLen: 1,
			expectedURI: afsurl.ToFileURL(filepath.Join(root, workspace.KindToolInstructions, "webdriver.md")),
		},
		{
			name: "falls back to legacy tools/ when instructions file is missing",
			defs: []*llm.ToolDefinition{{Name: "webdriver-browserRun"}},
			setup: func(root string) error {
				return os.WriteFile(filepath.Join(root, workspace.KindTool, "webdriver.md"), []byte("webdriver instructions legacy"), 0644)
			},
			expectedLen: 1,
			expectedURI: afsurl.ToFileURL(filepath.Join(root, workspace.KindTool, "webdriver.md")),
		},
		{
			name:        "skips when no webdriver tools present",
			defs:        []*llm.ToolDefinition{{Name: "resources-readImage"}},
			setup:       func(string) error { return nil },
			expectedLen: 0,
		},
		{
			name: "dedupes when playbook already present",
			defs: []*llm.ToolDefinition{{Name: "webdriver-browserRun"}},
			setup: func(root string) error {
				return os.WriteFile(filepath.Join(root, workspace.KindToolInstructions, "webdriver.md"), []byte("webdriver instructions"), 0644)
			},
			initialDocs: []*binding.Document{{SourceURI: afsurl.ToFileURL(filepath.Join(root, workspace.KindToolInstructions, "webdriver.md"))}},
			expectedLen: 1,
		},
		{
			name:        "no error when playbook missing",
			defs:        []*llm.ToolDefinition{{Name: "webdriver-browserRun"}},
			setup:       func(string) error { return nil },
			expectedLen: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(filepath.Join(root, workspace.KindTool, "webdriver.md"))
			_ = os.Remove(filepath.Join(root, workspace.KindToolInstructions, "webdriver.md"))
			if tc.setup != nil {
				assert.EqualValues(t, nil, tc.setup(root))
			}
			docs := &binding.Documents{Items: tc.initialDocs}
			err := service.appendToolPlaybooks(ctx, tc.defs, docs)
			assert.EqualValues(t, nil, err)
			assert.EqualValues(t, tc.expectedLen, len(docs.Items))
			if tc.expectedURI != "" && len(docs.Items) > 0 && docs.Items[0] != nil {
				assert.EqualValues(t, tc.expectedURI, docs.Items[0].SourceURI)
			}
		})
	}
}
