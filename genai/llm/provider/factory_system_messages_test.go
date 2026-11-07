package provider

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm/provider/openai"
	"gopkg.in/yaml.v3"
	"testing"
)

func TestFactoryPassesSystemMessageCompatibilityOption(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic")
	var options Options
	require.NoError(t, yaml.Unmarshal([]byte("provider: openai\nmodel: example\ncontextContinuation: false\nmergeConsecutiveSystemMessages: true\n"), &options))
	model, err := (&Factory{}).CreateModel(context.Background(), &options)
	require.NoError(t, err)
	client, ok := model.(*openai.Client)
	require.True(t, ok)
	require.True(t, client.MergeConsecutiveSystemMessages)
}
