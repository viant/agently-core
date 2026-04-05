package sdk

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenericFeedExtractionExpectedResults(t *testing.T) {
	tests := []struct {
		name              string
		feedID            string
		requestPayloads   []string
		responsePayloads  []string
		expectedRootName  string
		expectedItemCount int
		expectedJSON      string
	}{
		{
			name:              "terminal",
			feedID:            "terminal",
			requestPayloads:   nil,
			responsePayloads:  []string{`{"commands":[{"input":"pwd","output":"/tmp"}],"stdout":"/tmp"}`, `{"commands":[{"input":"ls","output":"a\nb"}],"status":"ok"}`},
			expectedRootName:  "commands",
			expectedItemCount: 2,
			expectedJSON: `{
				"input": {},
				"output": {
					"commands": [
						{"input":"pwd","output":"/tmp"},
						{"input":"ls","output":"a\nb"}
					],
					"stdout": "/tmp",
					"status": "ok"
				}
			}`,
		},
		{
			name:              "plan",
			feedID:            "plan",
			requestPayloads:   nil,
			responsePayloads:  []string{`{"explanation":"stale","plan":[{"status":"pending","step":"old"}]}`, `{"explanation":"Ship it","plan":[{"status":"completed","step":"Write tests"},{"status":"pending","step":"Review PR"}]}`},
			expectedRootName:  "planDetail",
			expectedItemCount: 2,
			expectedJSON: `{
				"input": {},
				"output": {
					"explanation": "Ship it",
					"plan": [
						{
							"status": "completed",
							"step": "Write tests",
							"content": "**Status:** completed\n\n**Step:** Write tests"
						},
						{
							"status": "pending",
							"step": "Review PR",
							"content": "**Status:** pending\n\n**Step:** Review PR"
						}
					]
				}
			}`,
		},
		{
			name:              "changes",
			feedID:            "changes",
			requestPayloads:   nil,
			responsePayloads:  []string{`{"changes":[{"url":"/tmp/old.go","kind":"modify"}],"status":"stale"}`, `{"changes":[{"url":"/tmp/a.go","kind":"create"},{"url":"/tmp/b.go","kind":"modify"}],"status":"ok"}`},
			expectedRootName:  "changes",
			expectedItemCount: 2,
			expectedJSON: `{
				"input": {},
				"output": {
					"changes": [
						{"url":"/tmp/a.go","kind":"create"},
						{"url":"/tmp/b.go","kind":"modify"}
					],
					"status": "ok"
				}
			}`,
		},
		{
			name:            "explorer",
			feedID:          "explorer",
			requestPayloads: []string{`{"path":"repo","pattern":"SetBit"}`},
			responsePayloads: []string{
				`{"files":[{"Path":"bitset.go","Matches":3}],"path":"repo"}`,
				`{"files":[{"Path":"state.go","Matches":2}],"stats":{"matches":5},"modeApplied":"grep"}`,
			},
			expectedRootName:  "entries",
			expectedItemCount: 2,
			expectedJSON: `{
				"input": {
					"path": "repo",
					"pattern": "SetBit"
				},
				"output": {
					"files": [
						{"Path":"bitset.go","Matches":3},
						{"Path":"state.go","Matches":2}
					],
					"path": "repo",
					"stats": {"matches": 5},
					"modeApplied": "grep"
				},
				"entries": [
					{"Path":"bitset.go","Matches":3},
					{"Path":"state.go","Matches":2}
				]
			}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := loadBuiltinFeedSpec(t, tc.feedID)
			extracted, err := extractGenericFeed(spec, tc.requestPayloads, tc.responsePayloads)
			require.NoError(t, err)
			require.NotNil(t, extracted)
			assert.Equal(t, tc.expectedRootName, extracted.RootName)
			assert.Equal(t, tc.expectedItemCount, extracted.ItemCount)
			assertJSONMapEq(t, tc.expectedJSON, extracted.RootData)
		})
	}
}

func extractGenericFeed(spec *FeedSpec, requestPayloads, responsePayloads []string) (*genericFeedResult, error) {
	result, err := extractFeedData(spec, requestPayloads, responsePayloads)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return &genericFeedResult{
		RootName:  result.RootName,
		RootData:  result.RootData,
		ItemCount: result.ItemCount,
	}, nil
}

type genericFeedResult struct {
	RootName  string
	RootData  map[string]interface{}
	ItemCount int
}

func assertJSONMapEq(t *testing.T, expected string, actual map[string]interface{}) {
	t.Helper()
	assert.JSONEq(t, expected, mustMarshalJSON(t, actual))
}
