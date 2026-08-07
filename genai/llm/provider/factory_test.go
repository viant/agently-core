package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm/provider/bedrock/converse"
)

func TestResolveDefaultOAuthClientURL_PrefersExplicit(t *testing.T) {
	got := resolveDefaultOAuthClientURL(ProviderAnthropic, "/tmp/custom.json")
	require.Equal(t, "/tmp/custom.json", got)
}

func TestFactoryCreatesGenericBedrockModel(t *testing.T) {
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	model, err := (&Factory{}).CreateModel(context.Background(), &Options{
		Provider: ProviderBedrock, Model: "qwen.qwen3-coder-next", Region: "us-east-1",
	})
	require.NoError(t, err)
	client, ok := model.(*converse.Client)
	require.True(t, ok)
	require.Equal(t, "qwen.qwen3-coder-next", client.Model)
	require.True(t, client.Implements("can-use-tools"))
	require.True(t, client.Implements("can-stream"))
}

func TestResolveDefaultOAuthClientURL_OpenAIDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".secret", "openai-oauth.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"client_id":"x"}`), 0o600))

	got := resolveDefaultOAuthClientURL(ProviderOpenAI, "")
	require.Equal(t, path, got)
}

func TestResolveDefaultOAuthClientURL_AnthropicDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".secret", "anthropic-oauth.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"client_id":"x"}`), 0o600))

	got := resolveDefaultOAuthClientURL(ProviderAnthropic, "")
	require.Equal(t, path, got)
}
