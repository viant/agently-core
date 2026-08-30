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
		{
			name:            "catalog slash preserves method underscores",
			toolName:        "catalog/get_record_by_id",
			expectedService: "catalog",
			expectedMethod:  "get_record_by_id",
		},
		{
			name:            "catalog colon preserves method underscores",
			toolName:        "catalog:edit_record_by_id",
			expectedService: "catalog",
			expectedMethod:  "edit_record_by_id",
		},
		{
			name:            "catalog canonical preserves method underscores",
			toolName:        "catalog-create_record_by_id",
			expectedService: "catalog",
			expectedMethod:  "create_record_by_id",
		},
		{
			name:            "nested display service",
			toolName:        "nested:catalog/get_record_by_id",
			expectedService: "nested/catalog",
			expectedMethod:  "get_record_by_id",
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

func TestFeedRegistryMatchesAllWildcardServiceMethods(t *testing.T) {
	registry := &FeedRegistry{specs: []*FeedSpec{{
		ID:    "catalog-feed",
		Match: FeedMatch{Service: "catalog", Method: "*"},
	}}}
	for _, toolName := range []string{
		"catalog/create_record",
		"catalog:get_record",
		"catalog:edit_record",
		"catalog-create_record",
	} {
		if matched := registry.Match(toolName); len(matched) != 1 || matched[0].ID != "catalog-feed" {
			t.Fatalf("Match(%q) = %#v, want catalog-feed", toolName, matched)
		}
	}
}
