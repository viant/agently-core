package bundle

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/mcpname"
)

func TestResolveLiteralMCPURIPatterns(t *testing.T) {
	for _, name := range []string{
		"mcp://catalog/source:Read",
		" \tmcp://catalog/nested/source:Read\n",
		"mcp://catalog/nested/source:tool.name",
		"mcp://catalog/nested/source:nested/tool",
		"mcp://catalog/nested/source:tool-with-hyphens",
		"mcp://catalog/nested/source/Read",
	} {
		t.Run(strings.TrimSpace(name), func(t *testing.T) {
			raw := strings.TrimSpace(name)
			canonical := mcpname.Canonical(raw)
			for _, supported := range []string{raw, canonical} {
				t.Run(supported, func(t *testing.T) {
					var calls []string
					actual := ResolveDefinitions(&Bundle{Match: []llm.Tool{{Name: name}}}, func(pattern string) []*llm.ToolDefinition {
						calls = append(calls, pattern)
						if pattern == supported {
							return []*llm.ToolDefinition{{Name: raw}}
						}
						return nil
					})
					require.Equal(t, []string{raw, canonical}, calls, "literal URI must be matched first without rebuilding a service from its alias")
					require.Equal(t, []llm.ToolDefinition{{Name: raw}}, actual)
				})
			}
		})
	}
}

func TestResolveLiteralMCPURIExcludesAndMetadata(t *testing.T) {
	const name = "mcp://catalog/source:Read"
	const blocked = "mcp://catalog/source:Remove"
	approval := &llm.ApprovalConfig{Mode: llm.ApprovalModePrompt}
	async := &asynccfg.Config{Run: asynccfg.RunConfig{Tool: name, OperationIDPath: "id"}}
	b := &Bundle{Match: []llm.Tool{
		{Name: name, Exclude: []string{" " + blocked + " "}, AlwaysInclude: true, Tier: llm.ToolTierOnDemand, Approval: approval, Async: async},
		{Name: name, Exclude: []string{blocked}, Tier: llm.ToolTierCritical},
	}}
	definition := &llm.ToolDefinition{Name: name, Parameters: map[string]interface{}{"type": "object"}}
	before, err := json.Marshal([]interface{}{b, definition})
	require.NoError(t, err)
	var calls []string
	result := ResolveDefinitionsWithOptions(b, func(pattern string) []*llm.ToolDefinition {
		calls = append(calls, pattern)
		switch pattern {
		case name, mcpname.Canonical(name):
			// Deliberately overlapping results exercise exclusion and de-duplication.
			return []*llm.ToolDefinition{definition, {Name: blocked}}
		case blocked, mcpname.Canonical(blocked):
			return []*llm.ToolDefinition{{Name: blocked}}
		}
		return nil
	})
	require.Equal(t, []string{blocked, mcpname.Canonical(blocked), name, mcpname.Canonical(name), blocked, mcpname.Canonical(blocked), name, mcpname.Canonical(name)}, calls)
	require.Len(t, result.Definitions, 1)
	require.Equal(t, name, result.Definitions[0].Name)
	require.True(t, result.Definitions[0].AlwaysInclude)
	require.Equal(t, llm.ToolTierCritical, result.Definitions[0].Tier)
	require.Equal(t, approval, result.ApprovalByID[normalizedApprovalKey(name)])
	require.Equal(t, async, result.AsyncByID[normalizedApprovalKey(name)].Async)
	after, err := json.Marshal([]interface{}{b, definition})
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after), "bundle rules and source definitions must remain unchanged")
}

func TestPatternVariantsLegacyCompatibility(t *testing.T) {
	for _, name := range []string{"catalog:Read", "catalog.Read", "catalog/Read", "catalog-Read"} {
		require.Equal(t, []string{"catalog-Read", "catalog.Read", "catalog/Read", "catalog:Read"}, patternVariants(name), name)
	}
	require.Equal(t, []string{"catalog/path.Read", "catalog/path/Read", "catalog/path:Read", "catalog_path-Read"}, patternVariants("catalog/path:Read"))
	for _, wildcard := range []string{"*", "?", "[ab]", "[!a]", "[", "]"} {
		name := "mcp://catalog/source:" + wildcard
		require.Equal(t, []string{
			"mcp///catalog/source." + wildcard,
			"mcp///catalog/source/" + wildcard,
			"mcp///catalog/source:" + wildcard,
			name,
			"mcp___catalog_source-" + wildcard,
		}, patternVariants(name), "URI glob expansion remains compatible: %s", name)
	}
	require.Nil(t, patternVariants(" \n"))
}

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
				ID: "analyst",
				Match: []llm.Tool{
					{Name: "analyst:ResourceTree"},
					{Name: "analyst-SaveDecision"},
				},
			},
			matches: map[string][]*llm.ToolDefinition{
				"analyst:ResourceTree": {def("analyst-ResourceTree")},
				"analyst-ResourceTree": {def("analyst-ResourceTree")},
				"analyst-SaveDecision": {def("analyst-SaveDecision")},
			},
			expected: []llm.ToolDefinition{
				{Name: "analyst-ResourceTree"},
				{Name: "analyst-SaveDecision"},
			},
		},
		{
			name: "supports_canonical_bundle_names_against_raw_registry_names",
			bundle: &Bundle{
				ID: "analyst",
				Match: []llm.Tool{
					{Name: "analyst-ResourceTree"},
					{Name: "analyst-SaveDecision"},
				},
			},
			matches: map[string][]*llm.ToolDefinition{
				"analyst:ResourceTree": {def("analyst:ResourceTree")},
				"analyst:SaveDecision": {def("analyst:SaveDecision")},
			},
			expected: []llm.ToolDefinition{
				{Name: "analyst:ResourceTree"},
				{Name: "analyst:SaveDecision"},
			},
		},
		{
			name: "supports_raw_and_canonical_variants_for_exclude",
			bundle: &Bundle{
				ID: "analyst",
				Match: []llm.Tool{
					{Name: "analyst:*", Exclude: []string{"analyst-SaveDecision"}},
				},
			},
			matches: map[string][]*llm.ToolDefinition{
				"analyst:*":            {def("analyst-ResourceTree"), def("analyst-SaveDecision")},
				"analyst-SaveDecision": {def("analyst-SaveDecision")},
			},
			expected: []llm.ToolDefinition{
				{Name: "analyst-ResourceTree"},
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
					DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
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

func TestURIIncludesAndExcludesUseOnlyDeclaredPatterns(t *testing.T) {
	const server = "mcp://inventory/nested/connection"
	for _, method := range []string{"ReadItem", "literal.tool", "nested/tool:operation", "*"} {
		t.Run(method, func(t *testing.T) {
			var patterns []string
			actual := ResolveDefinitions(&Bundle{Match: []llm.Tool{{Name: server + ":" + method, Exclude: []string{server + ":omitted.tool"}}}}, func(pattern string) []*llm.ToolDefinition {
				patterns = append(patterns, pattern)
				if pattern == server+":omitted.tool" {
					return []*llm.ToolDefinition{{Name: server + "/omitted.tool"}}
				}
				return []*llm.ToolDefinition{{Name: server + "/ReadItem"}, {Name: server + "/omitted.tool"}}
			})
			assert.Equal(t, []string{server + ":omitted.tool", server + ":" + method}, patterns)
			assert.Equal(t, []llm.ToolDefinition{{Name: server + "/ReadItem"}}, actual)
		})
	}
}
