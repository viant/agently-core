package model

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider"
	"github.com/viant/agently-core/genai/llm/provider/openai"
	"github.com/viant/agently-core/workspace/loader/fs"
	meta "github.com/viant/agently-core/workspace/service/meta"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadedModelMergesConsecutiveSystemMessages(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "compatible.yaml"), []byte("id: compatible\noptions:\n  provider: openai\n  model: example\n  contextContinuation: false\n  mergeConsecutiveSystemMessages: true\n"), 0600))
	loader := New(fs.WithMetaService[provider.Config](meta.New(afs.New(), root)))
	config, err := loader.Load(context.Background(), "compatible.yaml")
	require.NoError(t, err)
	require.True(t, config.Options.MergeConsecutiveSystemMessages)
	t.Setenv("OPENAI_API_KEY", "synthetic")
	model, err := (&provider.Factory{}).CreateModel(context.Background(), &config.Options)
	require.NoError(t, err)
	request, err := model.(*openai.Client).ToRequest(&llm.GenerateRequest{Messages: []llm.Message{llm.NewSystemMessage("First"), llm.NewSystemMessage("Second"), llm.NewUserMessage("Question")}})
	require.NoError(t, err)
	require.Len(t, request.Messages, 2)
	require.Equal(t, "system", request.Messages[0].Role)
	require.Equal(t, "user", request.Messages[1].Role)
}
