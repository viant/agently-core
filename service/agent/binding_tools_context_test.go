package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/prompt"
)

func TestApplyToolContext_DataDriven(t *testing.T) {
	type testCase struct {
		name            string
		initial         map[string]interface{}
		defs            []*llm.ToolDefinition
		expectToolsKey  string
		expectHasWebdrv bool
	}

	testCases := []testCase{
		{
			name:    "uses tools key when empty",
			initial: map[string]interface{}{},
			defs: []*llm.ToolDefinition{
				{Name: "webdriver-browserRun"},
				{Name: "resources.readImage"},
			},
			expectToolsKey:  "tools",
			expectHasWebdrv: true,
		},
		{
			name:    "uses agentlyTools when tools conflicts",
			initial: map[string]interface{}{"tools": "keep"},
			defs: []*llm.ToolDefinition{
				{Name: "resources-readImage"},
			},
			expectToolsKey:  "agentlyTools",
			expectHasWebdrv: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := cloneContextMap(tc.initial)
			applyToolContext(ctx, tc.defs)

			if tc.expectToolsKey == "agentlyTools" {
				assert.EqualValues(t, "keep", ctx["tools"])
			}

			raw := ctx[tc.expectToolsKey]
			toolsCtx, ok := raw.(map[string]interface{})
			require.EqualValues(t, true, ok)

			present, ok := toolsCtx["present"].(map[string]interface{})
			require.EqualValues(t, true, ok)
			services, ok := toolsCtx["services"].(map[string]interface{})
			require.EqualValues(t, true, ok)

			if tc.expectHasWebdrv {
				assert.EqualValues(t, true, toolsCtx["hasWebdriver"])
				assert.EqualValues(t, true, services["webdriver"])
				assert.EqualValues(t, true, present["webdriver-browserRun"])
			} else {
				assert.EqualValues(t, false, toolsCtx["hasWebdriver"])
			}
			assert.EqualValues(t, true, toolsCtx["hasResources"])
		})
	}
}

func TestApplyDelegationContext_DataDriven(t *testing.T) {
	type testCase struct {
		name            string
		agentID         string
		maxDepth        int
		initialContext  map[string]interface{}
		expectCurrent   int
		expectDelegated bool
		expectRemaining int
		expectSelfID    string
	}

	testCases := []testCase{
		{
			name:            "top level agent has zero depth and full remaining budget",
			agentID:         "coder",
			maxDepth:        1,
			initialContext:  map[string]interface{}{},
			expectCurrent:   0,
			expectDelegated: false,
			expectRemaining: 1,
			expectSelfID:    "coder",
		},
		{
			name:     "delegated same agent exposes depth state",
			agentID:  "coder",
			maxDepth: 1,
			initialContext: map[string]interface{}{
				"DelegationDepths": map[string]interface{}{"coder": 1, "chatter": 0},
			},
			expectCurrent:   1,
			expectDelegated: true,
			expectRemaining: 0,
			expectSelfID:    "coder",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{}
			binding := &prompt.Binding{Context: cloneContextMap(tc.initialContext)}
			input := &QueryInput{
				Agent: &agentmdl.Agent{
					Identity: agentmdl.Identity{ID: tc.agentID},
					Delegation: &agentmdl.Delegation{
						Enabled:  true,
						MaxDepth: tc.maxDepth,
					},
				},
			}
			svc.applyDelegationContext(input, binding)

			assert.EqualValues(t, true, binding.Context["DelegationEnabled"])
			assert.EqualValues(t, tc.maxDepth, binding.Context["DelegationMaxDepth"])
			assert.EqualValues(t, tc.expectSelfID, binding.Context["DelegationSelfID"])
			assert.EqualValues(t, tc.expectCurrent, binding.Context["DelegationCurrentDepth"])
			assert.EqualValues(t, tc.expectDelegated, binding.Context["DelegationIsDelegated"])
			assert.EqualValues(t, tc.expectRemaining, binding.Context["DelegationRemainingDepth"])

			rawDepths, ok := binding.Context["DelegationDepths"].(map[string]interface{})
			require.EqualValues(t, true, ok)
			if tc.expectCurrent > 0 {
				assert.EqualValues(t, tc.expectCurrent, rawDepths[tc.agentID])
			}
		})
	}
}

func TestApplyWorkdirContext_DataDriven(t *testing.T) {
	type testCase struct {
		name                   string
		initialContext         map[string]interface{}
		defaultWorkdir         string
		expectWorkdir          string
		expectResolvedWorkdir  string
		expectAgentDefaultPath string
	}

	testCases := []testCase{
		{
			name:                   "propagates explicit workdir and agent default",
			initialContext:         map[string]interface{}{"workdir": "/tmp/repo"},
			defaultWorkdir:         "~/support",
			expectWorkdir:          "/tmp/repo",
			expectResolvedWorkdir:  "/tmp/repo",
			expectAgentDefaultPath: "~/support",
		},
		{
			name:                   "promotes resolvedWorkdir when workdir missing",
			initialContext:         map[string]interface{}{"resolvedWorkdir": "/tmp/other"},
			defaultWorkdir:         "~/support",
			expectWorkdir:          "",
			expectResolvedWorkdir:  "/tmp/other",
			expectAgentDefaultPath: "~/support",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			initialContext := cloneContextMap(tc.initialContext)
			if value, ok := initialContext["workdir"].(string); ok && value != "" {
				initialContext["workdir"] = tempDir
			}
			if value, ok := initialContext["resolvedWorkdir"].(string); ok && value != "" {
				initialContext["resolvedWorkdir"] = tempDir
			}
			svc := &Service{}
			binding := &prompt.Binding{Context: initialContext}
			input := &QueryInput{
				Agent: &agentmdl.Agent{
					DefaultWorkdir: tc.defaultWorkdir,
				},
			}
			svc.applyWorkdirContext(input, binding)

			assert.EqualValues(t, tc.expectAgentDefaultPath, binding.Context["AgentDefaultWorkdir"])
			assert.EqualValues(t, tempDir, binding.Context["ResolvedWorkdir"])
			if tc.expectWorkdir != "" {
				assert.EqualValues(t, tempDir, binding.Context["workdir"])
			}
		})
	}
}
