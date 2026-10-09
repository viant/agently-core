package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestE2EDefaultsPreserveRuntimePromptReferencesAndFollowingAssets(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, seedLiteralDefaults(root, defaultsFS, "defaults"))
	require.NoError(t, fs.WalkDir(defaultsFS, "defaults", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		original, err := fs.ReadFile(defaultsFS, name)
		if err != nil {
			return err
		}
		written, err := os.ReadFile(filepath.Join(root, name[len("defaults/"):]))
		if err != nil {
			return err
		}
		require.Equal(t, original, written, "literal asset %s", name)
		return nil
	}))
	var config struct {
		Prompt struct {
			URI string `yaml:"uri"`
		} `yaml:"prompt"`
		SystemPrompt struct {
			URI string `yaml:"uri"`
		} `yaml:"systemPrompt"`
	}
	body, err := os.ReadFile(filepath.Join(root, "agents/simple/simple.yaml"))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(body, &config))
	for _, ref := range []string{config.Prompt.URI, config.SystemPrompt.URI} {
		require.NotEmpty(t, ref)
		_, err := os.Stat(filepath.Join(root, "agents/simple", ref))
		require.NoError(t, err)
	}
	for _, name := range []string{"models/openai_gpt4o_mini.yaml", "tools/bundles/system_exec.yaml", "tools/bundles/system_os.yaml", "embedders/openai_text.yaml"} {
		_, err := os.Stat(filepath.Join(root, name))
		require.NoError(t, err)
	}
	_, err = os.Stat(filepath.Join(root, "agents/simple/prompt/user"))
	require.ErrorIs(t, err, fs.ErrNotExist, "bootstrap must not strip the runtime prompt filename")
	user, err := os.ReadFile(filepath.Join(root, "agents/simple/prompt/user.tmpl"))
	require.NoError(t, err)
	parsed, err := template.New("runtime-user").Parse(string(user))
	require.NoError(t, err)
	var output bytes.Buffer
	input := struct {
		Context string
		Task    struct{ Prompt string }
	}{Context: "known context"}
	input.Task.Prompt = "known runtime query"
	require.NoError(t, parsed.Execute(&output, input))
	require.Contains(t, output.String(), "known context")
	require.Contains(t, output.String(), "known runtime query")
}

func TestE2EDefaultsDoNotOverwriteExistingRuntimeAssets(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "agents/simple/prompt/user.tmpl")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0755))
	require.NoError(t, os.WriteFile(file, []byte("custom runtime prompt"), 0600))
	require.NoError(t, seedLiteralDefaults(root, defaultsFS, "defaults"))
	body, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "custom runtime prompt", string(body))
	_, err = os.Stat(filepath.Join(root, "models/openai_gpt4o_mini.yaml"))
	require.NoError(t, err, "existing prompts must not stop later model seeding")
}
