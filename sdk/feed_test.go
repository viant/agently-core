package sdk

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseToolName(t *testing.T) {
	testCases := []struct {
		name            string
		toolName        string
		expectedService string
		expectedMethod  string
	}{
		{
			name:            "resources hyphen",
			toolName:        "resources-grepFiles",
			expectedService: "resources",
			expectedMethod:  "grepfiles",
		},
		{
			name:            "system patch underscore hyphen",
			toolName:        "system_patch-apply",
			expectedService: "system/patch",
			expectedMethod:  "apply",
		},
		{
			name:            "system exec underscore hyphen",
			toolName:        "system_exec-execute",
			expectedService: "system/exec",
			expectedMethod:  "execute",
		},
		{
			name:            "colon separator",
			toolName:        "orchestration:updatePlan",
			expectedService: "orchestration",
			expectedMethod:  "updateplan",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service, method := parseToolName(testCase.toolName)
			assert.Equal(t, testCase.expectedService, service)
			assert.Equal(t, testCase.expectedMethod, method)
		})
	}
}

func TestFeedPayloadMatch(t *testing.T) {
	spec := &FeedSpec{
		Match: FeedMatch{Service: "system/patch", Method: "apply"},
		Activation: FeedActivation{
			Kind:    "tool_call",
			Service: "system/patch",
			Method:  "snapshot",
		},
	}

	service, method := feedPayloadMatch(spec)
	assert.Equal(t, "system/patch", service)
	assert.Equal(t, "snapshot", method)

	service, method = feedPayloadMatch(&FeedSpec{
		Match: FeedMatch{Service: "resources", Method: "list"},
	})
	assert.Equal(t, "resources", service)
	assert.Equal(t, "list", method)
}

func TestBuildFeedData(t *testing.T) {
	t.Run("explorer aggregates files and preserves latest input", func(t *testing.T) {
		got := buildFeedData("explorer",
			[]string{`{"path":"repo","pattern":"SetBit"}`},
			[]string{
				`{"files":[{"Path":"bitset.go","Matches":3}]}`,
				`{"files":[{"Path":"state.go","Matches":2}]}`,
			},
		)
		assert.Equal(t, map[string]interface{}{"path": "repo", "pattern": "SetBit"}, got["input"])
		output, ok := got["output"].(map[string]interface{})
		assert.True(t, ok)
		files, ok := output["files"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, files, 2)
		entries, ok := got["entries"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, entries, 2)
	})

	t.Run("terminal preserves commands output", func(t *testing.T) {
		got := buildFeedData("terminal", nil, []string{
			`{"commands":[{"input":"pwd","output":"/tmp"}],"stdout":"/tmp"}`,
		})
		output, ok := got["output"].(map[string]interface{})
		assert.True(t, ok)
		commands, ok := output["commands"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, commands, 1)
		assert.Equal(t, "/tmp", output["stdout"])
	})

	t.Run("changes prefers snapshot payload structure", func(t *testing.T) {
		got := buildFeedData("changes", nil, []string{
			`{"changes":[{"url":"/tmp/a.go","kind":"create"}],"status":"ok"}`,
		})
		output, ok := got["output"].(map[string]interface{})
		assert.True(t, ok)
		changes, ok := output["changes"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, changes, 1)
	})
}
