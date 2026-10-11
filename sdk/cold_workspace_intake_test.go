package sdk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/openai"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/sdkbackend"
	agentfinder "github.com/viant/agently-core/protocol/agent/finder"
	agentloader "github.com/viant/agently-core/protocol/agent/loader"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/sdk"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/service/meta"
)

type coldModels struct{ model llm.Model }

func (f coldModels) Find(context.Context, string) (llm.Model, error) { return f.model, nil }

func TestNativeSDKColdWorkspaceIntakeUsesConfiguredCatalog(t *testing.T) {
	for _, n := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(n, "")
	}
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old) })
	require.NoError(t, os.MkdirAll(filepath.Join(root, "agents"), 0700))
	for _, id := range []string{"coder", "chatter"} {
		body := fmt.Sprintf("id: %s\nname: %s\ndescription: %s configured catalog agent\nmodelRef: fixture-model\nprofile:\n  publish: true\nprompt:\n  text: '{{.Task.Prompt}}'\nsystemPrompt:\n  text: 'Actual %s execution'\n", id, id, id, id)
		require.NoError(t, os.WriteFile(filepath.Join(root, "agents", id+".yaml"), []byte(body), 0600))
	}
	var mu sync.Mutex
	requests := []map[string]any{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&input))
		mu.Lock()
		requests = append(requests, input)
		number := len(requests)
		mu.Unlock()
		text := "CODER_COMPLETED"
		if number == 1 {
			text = `{"action":"route","agentId":"coder"}`
		}
		response := map[string]any{"id": fmt.Sprintf("response-%d", number), "object": "response", "status": "completed", "model": "gpt-4o-mini", "output": []any{map[string]any{"id": fmt.Sprintf("message-%d", number), "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text}}}}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1, "total_tokens": 11}}
		data, _ := json.Marshal(response)
		if input["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":%q,\"delta\":%q}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", fmt.Sprintf("message-%d", number), text, data)
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		}
	}))
	defer upstream.Close()
	finder := agentfinder.New(agentfinder.WithLoader(agentloader.New(agentloader.WithMetaService(meta.New(afs.New(), root)))))
	require.Empty(t, finder.All(), "regression must begin genuinely cold")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nativeStarted := time.Now()
	nr, err := native.New(ctx, native.Options{WorkspaceRoot: root})
	t.Logf("native linked bootstrap: %s", time.Since(nativeStarted))
	require.NoError(t, err)
	t.Cleanup(func() { cancel(); require.NoError(t, nr.Shutdown(context.Background())) })
	rt, err := executor.NewBuilder().WithNativeRuntime(nr).WithAgentFinder(finder).WithModelFinder(coldModels{openai.NewClient("fixture-only", "gpt-4o-mini", openai.WithBaseURL(upstream.URL))}).WithDefaults(&config.Defaults{Model: "fixture-model", Agent: "chatter"}).WithStreamingBus(streaming.NewMemoryBus(256)).Build(ctx)
	require.NoError(t, err)

	backend, err := sdkbackend.FromRuntime(rt)
	require.NoError(t, err)
	handler := sdk.NewHandler(backend)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "cold-owner"})))
	}))
	defer server.Close()
	client, err := sdk.NewHTTP(server.URL)
	require.NoError(t, err)
	requestCtx, requestCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer requestCancel()
	result, err := client.QueryAGUI(requestCtx, &agentsvc.QueryInput{AgentID: "auto", Query: "Analyze and fix the repository build"}, nil)
	require.NoError(t, err)
	require.Equal(t, "success", result.Outcome.Type)
	require.Equal(t, "CODER_COMPLETED", result.Content)
	require.Len(t, finder.All(), 2, "startup must preload all configured local assets before first request")
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, requests, 2, "cold intake must perform router then selected agent")
	first, _ := json.Marshal(requests[0])
	second, _ := json.Marshal(requests[1])
	require.Contains(t, string(first), "coder")
	require.Contains(t, string(first), "chatter")
	require.True(t, strings.Contains(string(second), "Actual coder execution"), "must execute selected configured agent, not default chatter")
}
