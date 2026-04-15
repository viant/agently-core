package loader

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	"github.com/viant/agently-core/protocol/agent"
	meta "github.com/viant/agently-core/workspace/service/meta"
	yml "github.com/viant/agently-core/workspace/service/meta/yml"
	"gopkg.in/yaml.v3"
)

// TestService_Load tests the agent loading functionality
func TestService_Load(t *testing.T) {
	// Set up memory file system
	ctx := context.Background()

	// Test cases
	testCases := []struct {
		name         string
		url          string
		expectedJSON string
		expectedErr  bool
	}{
		{
			name: "Valid agent",
			url:  "tester.yaml",
			expectedJSON: `{
  "id":"agent-123",
  "name":"Database tester Agent",
  "icon":"https://example.com/icon.png",
  "source":{"url":"testdata/tester.yaml"},
  "model":"o1",
  "temperature":0.7,
  "description":"An example agent for demonstration purposes.",
  "knowledge":[{"filter":{"Exclusions":null,"Inclusions":["*.md"],"MaxFileSize":0},"url":"knowledge/"}],
  "resources":[{"uri":"knowledge/","role":"user","allowSemanticMatch":true}],
  "tool":{}
}`,
		},
		{
			name: "Agent with chains",
			url:  "with_chains.yaml",
			expectedJSON: `{
			  "id":"agent-chain-demo",
			  "name":"Chain Demo",
			  "source":{"url":"testdata/with_chains.yaml"},
			  "model":"gpt-4o",
            "followUps":[
                {"on":"succeeded","target":{"agentId":"summarizer"},"mode":"sync","conversation":"link","query":{"text":"Summarize the assistant reply: {{ .Output.Content }}"},"publish":{"role":"assistant"}},
			    {"on":"failed","target":{"agentId":"notifier"},"mode":"sync","conversation":"reuse","when":{"expr":"{{ ne .Output.Content \"\" }}"},"onError":"message"}
			  ]
			}`,
		},
		{
			name: "Agent internal flag",
			url:  "internal.yaml",
			expectedJSON: `{
  "id":"internal-demo",
  "name":"Internal Demo",
  "source":{"url":"testdata/internal.yaml"},
  "internal":true,
  "model":"gpt-4o",
  "tool":{}
}`,
		},
		{
			name:        "Invalid URL",
			url:         "nonexistent.yaml",
			expectedErr: true,
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			service := New(WithMetaService(meta.New(afs.New(), "testdata")))
			anAgent, err := service.Load(ctx, tc.url)

			if tc.expectedErr {
				assert.NotNil(t, err)
				return
			}
			expected := &agent.Agent{}
			err = json.Unmarshal([]byte(tc.expectedJSON), expected)
			assert.NoError(t, err)
			if !assert.EqualValues(t, expected, anAgent) {
				actualJSON, err := json.Marshal(anAgent)
				fmt.Println(string(actualJSON), err)
			}
		})
	}
}

func TestService_Load_UIFlags(t *testing.T) {
	ctx := context.Background()
	service := New(WithMetaService(meta.New(afs.New(), "testdata")))

	got, err := service.Load(ctx, "flags.yaml")
	assert.NoError(t, err)

	// All three flags are provided as false in YAML and must be parsed as such
	if assert.NotNil(t, got.ShowExecutionDetails, "ShowExecutionDetails must be set") {
		assert.False(t, *got.ShowExecutionDetails, "ShowExecutionDetails should be false")
	}
	if assert.NotNil(t, got.ShowToolFeed, "ShowToolFeed must be set") {
		assert.False(t, *got.ShowToolFeed, "ShowToolFeed should be false")
	}
	if assert.NotNil(t, got.AutoSummarize, "AutoSummarize must be set") {
		assert.False(t, *got.AutoSummarize, "AutoSummarize should be false")
	}
}

func TestService_Load_ToolExposure(t *testing.T) {
	ctx := context.Background()
	service := New(WithMetaService(meta.New(afs.New(), "testdata")))

	// Minimal, focused assertions: exposure must be set consistently
	t.Run("tool.callExposure alias is parsed", func(t *testing.T) {
		got, err := service.Load(ctx, "tool_callExposure.yaml")
		assert.NoError(t, err)
		if assert.NotNil(t, got) && assert.NotNil(t, got.Tool) {
			assert.EqualValues(t, agent.ToolCallExposure("conversation"), got.ToolCallExposure)
			assert.EqualValues(t, agent.ToolCallExposure("conversation"), got.Tool.CallExposure)
		}
	})

	t.Run("new tool block with toolCallExposure", func(t *testing.T) {
		got, err := service.Load(ctx, "tool_new.yaml")
		assert.NoError(t, err)
		if assert.NotNil(t, got) && assert.NotNil(t, got.Tool) {
			assert.EqualValues(t, agent.ToolCallExposure("conversation"), got.ToolCallExposure)
			assert.EqualValues(t, agent.ToolCallExposure("conversation"), got.Tool.CallExposure)
		}
	})

	t.Run("tool.callexposure (lowercase) is parsed", func(t *testing.T) {
		got, err := service.Load(ctx, "tool_callexposure.yaml")
		assert.NoError(t, err)
		if assert.NotNil(t, got) && assert.NotNil(t, got.Tool) {
			assert.EqualValues(t, agent.ToolCallExposure("conversation"), got.ToolCallExposure)
			assert.EqualValues(t, agent.ToolCallExposure("conversation"), got.Tool.CallExposure)
		}
	})

	t.Run("top-level toolCallExposure mirrors into tool block", func(t *testing.T) {
		got, err := service.Load(ctx, "tool_top.yaml")
		assert.NoError(t, err)
		if assert.NotNil(t, got) && assert.NotNil(t, got.Tool) {
			assert.EqualValues(t, agent.ToolCallExposure("conversation"), got.ToolCallExposure)
			assert.EqualValues(t, agent.ToolCallExposure("conversation"), got.Tool.CallExposure)
		}
	})

	t.Run("tool.allowOverflowHelpers is parsed", func(t *testing.T) {
		got, err := service.Load(ctx, "tool_overflow_helpers.yaml")
		assert.NoError(t, err)
		if assert.NotNil(t, got) && assert.NotNil(t, got.Tool) {
			if assert.NotNil(t, got.Tool.AllowOverflowHelpers) {
				assert.False(t, *got.Tool.AllowOverflowHelpers)
			}
			assert.False(t, got.Tool.OverflowHelpersAllowed())
		}
	})
}

func TestService_Load_ToolBundles(t *testing.T) {
	ctx := context.Background()
	service := New(WithMetaService(meta.New(afs.New(), "testdata")))

	testCases := []struct {
		name            string
		url             string
		expectedBundles []string
		expectedExpo    agent.ToolCallExposure
	}{
		{
			name:            "tool.bundles are parsed from mapping tool block",
			url:             "tool_bundles.yaml",
			expectedBundles: []string{"system/exec", "system/os"},
			expectedExpo:    agent.ToolCallExposure("conversation"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := service.Load(ctx, testCase.url)
			require.NoError(t, err)
			require.NotNil(t, got)

			actualBundles := []string(nil)
			if got.Tool.Bundles != nil {
				actualBundles = append([]string(nil), got.Tool.Bundles...)
			}
			assert.EqualValues(t, testCase.expectedBundles, actualBundles)
			assert.EqualValues(t, testCase.expectedExpo, got.Tool.CallExposure)
			assert.EqualValues(t, testCase.expectedExpo, got.ToolCallExposure)
		})
	}
}

func TestService_Load_Capabilities(t *testing.T) {
	ctx := context.Background()
	service := New(WithMetaService(meta.New(afs.New(), "testdata")))

	t.Run("top-level capabilities are parsed", func(t *testing.T) {
		got, err := service.Load(ctx, "capabilities_model_artifact_generation.yaml")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.NotNil(t, got.Capabilities)
		assert.True(t, got.Capabilities.ModelArtifactGeneration)
	})

	t.Run("unsupported capability keys fail fast", func(t *testing.T) {
		got, err := service.Load(ctx, "capabilities_invalid.yaml")
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "unsupported capabilities key")
	})
}

func TestService_Load_InstructionPrompt(t *testing.T) {
	ctx := context.Background()
	service := New(WithMetaService(meta.New(afs.New(), "testdata")))

	got, err := service.Load(ctx, "instruction_prompt.yaml")
	require.NoError(t, err)
	require.NotNil(t, got)

	if assert.NotNil(t, got.InstructionPrompt) {
		assert.Equal(t, "Preferred instruction prompt", got.InstructionPrompt.Text)
	}
	if assert.NotNil(t, got.Instruction) {
		assert.Equal(t, "Legacy instruction alias", got.Instruction.Text)
	}
	if assert.NotNil(t, got.EffectiveInstructionPrompt()) {
		assert.Equal(t, "Preferred instruction prompt", got.EffectiveInstructionPrompt().Text)
	}
}

func TestService_Load_InstructionAliasFallback(t *testing.T) {
	ctx := context.Background()
	service := New(WithMetaService(meta.New(afs.New(), "testdata")))

	got, err := service.Load(ctx, "instruction_alias_only.yaml")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.Instruction)
	require.NotNil(t, got.InstructionPrompt)
	assert.Equal(t, "Use only alias", got.Instruction.Text)
	assert.Equal(t, "Use only alias", got.InstructionPrompt.Text)
	assert.Equal(t, "Use only alias", got.EffectiveInstructionPrompt().Text)
}

func TestService_Load_ToolApprovalQueueOnItemIgnored(t *testing.T) {
	// approvalQueue on agent items is no longer supported; use bundle rules instead.
	// The tool still loads; the approvalQueue key is silently ignored.
	ctx := context.Background()
	service := New(WithMetaService(meta.New(afs.New(), "testdata")))

	got, err := service.Load(ctx, "tool_approval_queue.yaml")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Tool.Items, 1)
	assert.Equal(t, "mcpform", got.Tool.Items[0].Name)
	assert.Nil(t, got.Tool.Items[0].Approval)
}

func TestService_Load_StarterTasks(t *testing.T) {
	ctx := context.Background()
	service := New(WithMetaService(meta.New(afs.New(), "testdata")))

	got, err := service.Load(ctx, "starter_tasks.yaml")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.StarterTasks, 2)

	assert.Equal(t, "analyze-repo", got.StarterTasks[0].ID)
	assert.Equal(t, "Analyze this repo", got.StarterTasks[0].Title)
	assert.Equal(t, "Analyze this repository.", got.StarterTasks[0].Prompt)
	assert.Equal(t, "Architecture summary and next steps.", got.StarterTasks[0].Description)
	assert.Equal(t, "tree-structure", got.StarterTasks[0].Icon)

	assert.Equal(t, "write-tests", got.StarterTasks[1].ID)
	assert.Equal(t, "Add missing tests", got.StarterTasks[1].Title)
	assert.Equal(t, "Add focused tests for a weakly covered area.", got.StarterTasks[1].Prompt)
	assert.Equal(t, "Targeted coverage improvements.", got.StarterTasks[1].Description)
	assert.Equal(t, "flask", got.StarterTasks[1].Icon)
}

func TestParseResourceEntry_SystemFlag(t *testing.T) {
	makeNode := func(doc string) *yml.Node {
		var root yaml.Node
		require.NoError(t, yaml.Unmarshal([]byte(doc), &root))
		require.Greater(t, len(root.Content), 0)
		return (*yml.Node)(root.Content[0])
	}

	t.Run("system true sets role", func(t *testing.T) {
		node := makeNode(`uri: workspace://foo
system: true`)
		res, err := parseResourceEntry(node)
		assert.NoError(t, err)
		if assert.NotNil(t, res) {
			assert.Equal(t, "system", res.Role)
		}
	})

	t.Run("system false keeps user role", func(t *testing.T) {
		node := makeNode(`uri: workspace://bar
role: user
system: false`)
		res, err := parseResourceEntry(node)
		assert.NoError(t, err)
		if assert.NotNil(t, res) {
			assert.Equal(t, "user", res.Role)
		}
	})

	t.Run("conflicting role and system raises error", func(t *testing.T) {
		node := makeNode(`uri: workspace://baz
role: user
system: true`)
		_, err := parseResourceEntry(node)
		assert.Error(t, err)
	})
}

func TestParseResourceEntry_MCPShorthand(t *testing.T) {
	makeNode := func(doc string) *yml.Node {
		var root yaml.Node
		require.NoError(t, yaml.Unmarshal([]byte(doc), &root))
		require.Greater(t, len(root.Content), 0)
		return (*yml.Node)(root.Content[0])
	}

	t.Run("mcp shorthand defaults roots", func(t *testing.T) {
		node := makeNode(`mcp: github
system: true`)
		res, err := parseResourceEntry(node)
		assert.NoError(t, err)
		if assert.NotNil(t, res) {
			assert.Equal(t, "github", res.MCP)
			assert.Empty(t, res.Roots)
			assert.Equal(t, "system", res.Role)
			assert.Empty(t, res.URI)
		}
	})

	t.Run("mcp shorthand with roots list", func(t *testing.T) {
		node := makeNode(`mcp: github
roots:
  - mediator
  - mcp:github://github.vianttech.com/viant/mdp
role: user`)
		res, err := parseResourceEntry(node)
		assert.NoError(t, err)
		if assert.NotNil(t, res) {
			assert.Equal(t, "github", res.MCP)
			assert.Equal(t, []string{"mediator", "mcp:github://github.vianttech.com/viant/mdp"}, res.Roots)
			assert.Equal(t, "user", res.Role)
			assert.Empty(t, res.URI)
		}
	})

	t.Run("mcp shorthand conflicts with uri", func(t *testing.T) {
		node := makeNode(`mcp: github
uri: workspace://foo`)
		_, err := parseResourceEntry(node)
		assert.Error(t, err)
	})
}

func TestParseKnowledge_MinScoreAndMaxFiles(t *testing.T) {
	makeNode := func(doc string) *yml.Node {
		var root yaml.Node
		require.NoError(t, yaml.Unmarshal([]byte(doc), &root))
		require.Greater(t, len(root.Content), 0)
		return (*yml.Node)(root.Content[0])
	}

	node := makeNode(`url: knowledge/
inclusionMode: match
maxFiles: 7
minScore: 0.83
filter:
  inclusions: ["*.md"]
  maxFileSize: 4096`)

	kn, err := parseKnowledge(node)
	require.NoError(t, err)
	require.NotNil(t, kn)
	assert.Equal(t, "knowledge/", kn.URL)
	assert.Equal(t, "match", kn.InclusionMode)
	assert.Equal(t, 7, kn.MaxFiles)
	if assert.NotNil(t, kn.MinScore) {
		assert.InDelta(t, 0.83, *kn.MinScore, 0.0001)
	}
	if assert.NotNil(t, kn.Filter) {
		assert.Equal(t, []string{"*.md"}, kn.Filter.Inclusions)
		assert.Equal(t, 4096, kn.Filter.MaxFileSize)
	}
}
