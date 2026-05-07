package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveDefaultOAuthClientURL_PrefersExplicit(t *testing.T) {
	got := resolveDefaultOAuthClientURL(ProviderAnthropic, "/tmp/custom.json")
	require.Equal(t, "/tmp/custom.json", got)
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
