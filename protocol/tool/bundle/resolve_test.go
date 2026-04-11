package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
	asynccfg "github.com/viant/agently-core/protocol/async"
)

func TestResolveDefinitions(t *testing.T) {
	def := func(name string) *llm.ToolDefinition { return &llm.ToolDefinition{Name: name} }

	testCases := []struct {
		name     string
		bundle   *Bundle
		matches  map[string][]*llm.ToolDefinition
		expected []llm.ToolDefinition
	}{
		{
			name: "include_only",
			bundle: &Bundle{
				ID: "resources",
				Match: []llm.Tool{
					{Name: "resources/*"},
				},
			},
			matches: map[string][]*llm.ToolDefinition{
				"resources/*": {def("resources:read"), def("resources:list")},
			},
			expected: []llm.ToolDefinition{
				{Name: "resources:list"},
				{Name: "resources:read"},
			},
		},
		{
			name: "exclude_subtracts_from_rule",
			bundle: &Bundle{
				ID: "resources",
				Match: []llm.Tool{
					{Name: "resources/*", Exclude: []string{"resources:read"}},
				},
			},
			matches: map[string][]*llm.ToolDefinition{
				"resources/*":    {def("resources:read"), def("resources:list")},
				"resources:read": {def("resources:read")},
			},
			expected: []llm.ToolDefinition{
				{Name: "resources:list"},
			},
		},
		{
			name: "dedupe_across_rules",
			bundle: &Bundle{
				ID: "mixed",
				Match: []llm.Tool{
					{Name: "resources/*"},
					{Name: "resources:read"},
				},
			},
			matches: map[string][]*llm.ToolDefinition{
				"resources/*":    {def("resources:read"), def("resources:list")},
				"resources:read": {def("resources:read")},
			},
			expected: []llm.ToolDefinition{
				{Name: "resources:list"},
				{Name: "resources:read"},
			},
		},
		{
			name: "supports_raw_and_canonical_variants_for_include",
			bundle: &Bundle{
				ID: "steward",
				Match: []llm.Tool{
					{Name: "steward:AdHierarchy"},
					{Name: "steward-SaveRecommendation"},
				},
			},
			matches: map[string][]*llm.ToolDefinition{
				"steward:AdHierarchy":        {def("steward-AdHierarchy")},
				"steward-AdHierarchy":        {def("steward-AdHierarchy")},
				"steward-SaveRecommendation": {def("steward-SaveRecommendation")},
			},
			expected: []llm.ToolDefinition{
				{Name: "steward-AdHierarchy"},
				{Name: "steward-SaveRecommendation"},
			},
		},
		{
			name: "supports_canonical_bundle_names_against_raw_registry_names",
			bundle: &Bundle{
				ID: "steward",
				Match: []llm.Tool{
					{Name: "steward-AdHierarchy"},
					{Name: "steward-SaveRecommendation"},
				},
			},
			matches: map[string][]*llm.ToolDefinition{
				"steward:AdHierarchy":        {def("steward:AdHierarchy")},
				"steward:SaveRecommendation": {def("steward:SaveRecommendation")},
			},
			expected: []llm.ToolDefinition{
				{Name: "steward:AdHierarchy"},
				{Name: "steward:SaveRecommendation"},
			},
		},
		{
			name: "supports_raw_and_canonical_variants_for_exclude",
			bundle: &Bundle{
				ID: "steward",
				Match: []llm.Tool{
					{Name: "steward:*", Exclude: []string{"steward-SaveRecommendation"}},
				},
			},
			matches: map[string][]*llm.ToolDefinition{
				"steward:*":                  {def("steward-AdHierarchy"), def("steward-SaveRecommendation")},
				"steward-SaveRecommendation": {def("steward-SaveRecommendation")},
			},
			expected: []llm.ToolDefinition{
				{Name: "steward-AdHierarchy"},
			},
		},
		{
			name: "captures_prompt_approval_config",
			bundle: &Bundle{
				ID: "system_os",
				Match: []llm.Tool{
					{
						Name: "system/os:*",
						Approval: &llm.ApprovalConfig{
							Mode: llm.ApprovalModePrompt,
						},
					},
				},
			},
			matches: map[string][]*llm.ToolDefinition{
				"system/os:*": {def("system/os:getEnv")},
			},
			expected: []llm.ToolDefinition{
				{Name: "system/os:getEnv"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			matchFn := func(pattern string) []*llm.ToolDefinition {
				return tc.matches[pattern]
			}
			actual := ResolveDefinitions(tc.bundle, matchFn)
			assert.EqualValues(t, tc.expected, actual)
		})
	}
}

func TestResolveDefinitionsWithOptions_PromptApproval(t *testing.T) {
	bundle := &Bundle{
		ID: "system_os",
		Match: []llm.Tool{
			{
				Name: "system/os:*",
				Approval: &llm.ApprovalConfig{
					Mode: llm.ApprovalModePrompt,
				},
			},
		},
	}
	matchFn := func(pattern string) []*llm.ToolDefinition {
		if pattern == "system/os:*" {
			return []*llm.ToolDefinition{{Name: "system/os:getEnv"}}
		}
		return nil
	}

	actual := ResolveDefinitionsWithOptions(bundle, matchFn)
	cfg, ok := actual.ApprovalByID[normalizedApprovalKey("system/os:getEnv")]
	if assert.True(t, ok) {
		assert.NotNil(t, cfg)
		assert.True(t, cfg.IsPrompt())
	}
}

func TestResolveDefinitionsWithOptions_AsyncConfig(t *testing.T) {
	bundle := &Bundle{
		ID: "agents_async",
		Match: []llm.Tool{
			{
				Name: "llm/agents:start",
				Async: &asynccfg.Config{
					WaitForResponse: true,
					Run: asynccfg.RunConfig{
						Tool:            "llm/agents:start",
						OperationIDPath: "conversationId",
					},
					Status: asynccfg.StatusConfig{
						Tool:           "llm/agents:status",
						OperationIDArg: "conversationId",
						Selector:       asynccfg.Selector{StatusPath: "status"},
					},
				},
			},
		},
	}
	matchFn := func(pattern string) []*llm.ToolDefinition {
		if pattern == "llm/agents:start" || pattern == "llm/agents/start" {
			return []*llm.ToolDefinition{{Name: "llm/agents:start"}}
		}
		return nil
	}

	actual := ResolveDefinitionsWithOptions(bundle, matchFn)
	rule, ok := actual.AsyncByID[normalizedApprovalKey("llm/agents:start")]
	if assert.True(t, ok) {
		assert.NotNil(t, rule)
		assert.NotNil(t, rule.Async)
		assert.Equal(t, "conversationId", rule.Async.Run.OperationIDPath)
	}
}
