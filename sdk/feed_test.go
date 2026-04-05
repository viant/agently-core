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
