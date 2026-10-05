package sdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/runtime/streaming"
	goalsys "github.com/viant/agently-core/service/goal"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/datly/standalone"
)

func goalTakeoverBackend(t *testing.T, root string) (*backendClient, *standalone.Server) {
	t.Helper()
	ctx := context.Background()
	runtime, err := native.New(ctx, native.Options{WorkspaceRoot: root})
	require.NoError(t, err)
	conv, err := convservice.New(ctx, runtime)
	require.NoError(t, err)
	return &backendClient{goalRepo: goalsys.NewStore(runtime), goalInvoker: runtime, conv: conv, data: data.NewService(runtime), streaming: streaming.NewMemoryBus(64)}, runtime
}
func goalTakeoverServer(backend *backendClient) *httptest.Server {
	handler := handleAGUIRun(backend, nil)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r.WithContext(authctx.WithUserInfo(r.Context(), &authctx.UserInfo{Subject: r.Header.Get("X-Test-Principal")})))
	}))
}

// The child is intentionally killed after public admission. An actual process
// crash leaves the owned lease intact; neither lease nor journal is fabricated.
func TestAGUIGoalHTTPTwoRuntimeCrashTakeover(t *testing.T) {
	if root := os.Getenv("AGUI_GOAL_TAKEOVER_CHILD_ROOT"); root != "" {
		workspace.SetRoot(root)
		backend, _ := goalTakeoverBackend(t, root)
		server := goalTakeoverServer(backend)
		require.NoError(t, os.WriteFile(filepath.Join(root, "child-url"), []byte(server.URL), 0600))
		select {}
	}
	enableAtomicGoals(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("features:\n  goals:\n    enabled: true\n"), 0600))
	initial, runtime := goalTakeoverBackend(t, root)
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	row := conversation.NewConversation()
	row.SetId("takeover-thread")
	row.SetCreatedByUserID("owner")
	row.SetVisibility("private")
	require.NoError(t, initial.conv.PatchConversations(owner, row))
	_, err := initial.CreateGoal(owner, &CreateGoalInput{ConversationID: "takeover-thread", Objective: "before crash"})
	require.NoError(t, err)
	require.NoError(t, runtime.Shutdown(context.Background()))
	executable, err := os.Executable()
	require.NoError(t, err)
	child := exec.Command(executable, "-test.run=^TestAGUIGoalHTTPTwoRuntimeCrashTakeover$", "-test.timeout=120s")
	child.Env = append(os.Environ(), "AGUI_GOAL_TAKEOVER_CHILD_ROOT="+root)
	childLog, err := os.Create(filepath.Join(root, "child.log"))
	require.NoError(t, err)
	defer childLog.Close()
	child.Stdout = childLog
	child.Stderr = childLog
	require.NoError(t, child.Start())
	killed := false
	defer func() {
		if !killed {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	var firstURL string
	require.Eventually(t, func() bool {
		value, err := os.ReadFile(filepath.Join(root, "child-url"))
		firstURL = string(value)
		return err == nil
	}, 10*time.Second, 20*time.Millisecond)
	runID := uuid.NewString()
	accepted, _ := json.Marshal(map[string]any{"threadId": "takeover-thread", "runId": runID, "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "requestId": uuid.NewString(), "operation": "goal.subscribe", "payload": map[string]any{"durationSeconds": 300}}}})
	post := func(url, principal string, raw []byte) *http.Response {
		t.Helper()
		request, err := http.NewRequest("POST", url, bytes.NewReader(raw))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Test-Principal", principal)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		return response
	}
	first := post(firstURL, "owner", accepted)
	require.Equal(t, 200, first.StatusCode)
	scanner := bufio.NewScanner(first.Body)
	prefix := []string{}
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data:") {
			prefix = append(prefix, strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "data:")))
			if len(prefix) == 2 {
				break
			}
		}
	}
	require.Len(t, prefix, 2)
	require.Contains(t, prefix[0], `"type":"RUN_STARTED"`)
	require.Contains(t, prefix[1], "before crash")
	require.NoError(t, child.Process.Kill())
	_ = child.Wait()
	killed = true
	first.Body.Close()
	second, nextRuntime := goalTakeoverBackend(t, root)
	defer nextRuntime.Shutdown(context.Background())
	server := goalTakeoverServer(second)
	defer server.Close()
	store := aguistore.New(nextRuntime)
	before, err := store.GetRun(owner, "owner", "takeover-thread", runID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusRunning, before.Status)
	require.NotNil(t, before.LeaseUntil)
	t.Logf("Real crashed runtime lease expires at %s", before.LeaseUntil.UTC().Format(time.RFC3339Nano))
	reattached := post(server.URL, "owner", accepted)
	require.Equal(t, 200, reattached.StatusCode)
	defer reattached.Body.Close()
	observed := make(chan string, 32)
	readerDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(reattached.Body)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "data:") {
				observed <- strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "data:"))
			}
		}
		readerDone <- scanner.Err()
	}()
	all := []string{}
	receive := func(timeout time.Duration) string {
		t.Helper()
		select {
		case event := <-observed:
			all = append(all, event)
			return event
		case <-time.After(timeout):
			t.Fatal("missing HTTP takeover event")
			return ""
		}
	}
	require.Equal(t, prefix[0], receive(time.Second))
	require.Equal(t, prefix[1], receive(time.Second))
	require.Eventually(t, func() bool {
		latest, err := store.GetRun(owner, "owner", "takeover-thread", runID)
		return err == nil && latest.LeaseOwner != before.LeaseOwner && latest.Status == aguistore.StatusRunning
	}, 90*time.Second, 100*time.Millisecond)
	// A successful native command in runtime two publishes its own committed
	// invalidation; the recovered observer uses runtime two's real bus.
	command := func(principal, operation string, payload any) (int, string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"threadId": "takeover-thread", "runId": uuid.NewString(), "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "requestId": uuid.NewString(), "operation": operation, "payload": payload}}})
		response := post(server.URL, principal, raw)
		defer response.Body.Close()
		wire, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		return response.StatusCode, string(wire)
	}
	status, wire := command("owner", "goal.update", map[string]any{"objective": "after real takeover"})
	require.Equal(t, 200, status, wire)
	require.NotContains(t, wire, `"type":"RUN_ERROR"`)
	found := false
	for i := 0; i < 4; i++ {
		if strings.Contains(receive(5*time.Second), "after real takeover") {
			found = true
			break
		}
	}
	require.True(t, found)
	status, _ = command("intruder", "run.cancel", map[string]any{"runId": runID})
	require.Equal(t, 403, status)
	status, wire = command("owner", "run.cancel", map[string]any{"runId": runID})
	require.Equal(t, 200, status, wire)
	terminal := false
	for i := 0; i < 4; i++ {
		event := receive(5 * time.Second)
		if strings.Contains(event, `"type":"RUN_FINISHED"`) {
			require.Contains(t, event, `"cancelled"`)
			terminal = true
			break
		}
	}
	require.True(t, terminal)
	select {
	case err := <-readerDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("cancelled stream did not terminate")
	}
	latest, err := store.GetRun(owner, "owner", "takeover-thread", runID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusCancelled, latest.Status)
	replay := post(server.URL, "owner", accepted)
	require.Equal(t, 200, replay.StatusCode)
	wireBytes, err := io.ReadAll(replay.Body)
	replay.Body.Close()
	require.NoError(t, err)
	replayEvents := []string{}
	for _, line := range strings.Split(string(wireBytes), "\n") {
		if strings.HasPrefix(line, "data:") {
			replayEvents = append(replayEvents, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	require.Equal(t, all, replayEvents, "identical accepted input replays every committed event exactly")
	started := 0
	for _, event := range replayEvents {
		if strings.Contains(event, `"type":"RUN_STARTED"`) {
			started++
		}
	}
	require.Equal(t, 1, started)
	foreign := post(server.URL, "intruder", accepted)
	require.Equal(t, 403, foreign.StatusCode)
	foreign.Body.Close()
}
