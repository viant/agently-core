package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	iauth "github.com/viant/agently-core/internal/auth"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/mcpname"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type completionConversation struct {
	*convmem.Client
	successPatches, failedPatches int
	failSuccessOnce               bool
}

func (c *completionConversation) PatchTurn(ctx context.Context, in *apiconv.MutableTurn) error {
	if in.Status == "succeeded" {
		c.successPatches++
		if c.failSuccessOnce {
			c.failSuccessOnce = false
			return errors.New("native completion unavailable")
		}
	}
	if in.Status == "failed" {
		c.failedPatches++
	}
	return c.Client.PatchTurn(ctx, in)
}

type completionStore struct {
	aguistore.Store
	failCapture, failFinish, shortLease bool
}

func (s *completionStore) Claim(ctx context.Context, principal, thread, run string, revision int64, owner string, lease time.Duration) (*aguistore.Run, error) {
	if s.shortLease {
		lease = time.Second
	}
	return s.Store.Claim(ctx, principal, thread, run, revision, owner, lease)
}
func (s *completionStore) Append(ctx context.Context, principal, thread, run string, revision int64, events []json.RawMessage, change *aguistore.Change) (*aguistore.Run, error) {
	if s.failCapture && len(events) == 0 && change != nil {
		var p aguiMCPProxyPending
		_ = json.Unmarshal(change.Pending, &p)
		if len(p.HostResult) > 0 {
			s.failCapture = false
			return nil, errors.New("host receipt commit failed")
		}
	}
	if s.failFinish {
		for _, raw := range events {
			var e struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(raw, &e)
			if e.Type == "RUN_FINISHED" {
				s.failFinish = false
				return nil, errors.New("finished journal commit failed")
			}
		}
	}
	return s.Store.Append(ctx, principal, thread, run, revision, events, change)
}
func TestMCPAppsCompletionReceiptPreservesExecutionCaptureAndRecovery(t *testing.T) {
	for _, mode := range []string{"success", "failed capture", "failed first completion", "recovery after captured effect"} {
		t.Run(mode, func(t *testing.T) {
			fixture, _ := newDurableAGUIServer(t)
			ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
			native := &completionConversation{Client: convmem.New(), failSuccessOnce: mode == "failed first completion"}
			require.NoError(t, native.EnsureConversation("native-host", func(c *apiconv.MutableConversation) { c.SetStatus("running"); c.SetCreatedByUserID("owner") }))
			registry := &childMCPAppsNativeRegistry{originalConversation: "native-host", integrationToolRegistry: integrationToolRegistry{defs: []llm.ToolDefinition{{Name: mcpname.NewName("same-server", "tool").String()}}}}
			service := agentsvc.New(nil, nil, nil, registry, nil, native)
			backend, err := NewEmbedded(service, native)
			require.NoError(t, err)
			input := &agui.RunAgentInput{ThreadID: "proxy", RunID: "run"}
			record, _, err := fixture.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "proxy", RunID: "run", TurnID: "guest-turn", Input: rawAGUI(map[string]any{"threadId": "proxy", "runId": "run", "messages": []any{}})})
			require.NoError(t, err)
			binding := AGUIMCPAppsBindings{App: AGUIMCPAppBinding{AppInstanceID: "issued-instance", ThreadID: "native-host", ServerID: "same-server", PublicServerID: "alias", ServerHash: "hash", ResourceURI: "ui://demo/app"}, Authorize: func(context.Context, AGUIMCPAppBinding) error { return nil }, AuthorizeTool: func(context.Context, AGUIMCPAppBinding, string) error { return nil }, ToolCaller: backend.ExecuteMCPUIToolCall}
			ctx = WithAGUIMCPAppsBindings(ctx, binding)
			proxy := &MCPAppsProxyRequest{ServerID: "alias", ServerHash: "hash", Method: "tools/call", Params: map[string]json.RawMessage{"name": json.RawMessage(`"tool"`)}}
			store := &completionStore{Store: fixture.store, failCapture: mode == "failed capture", failFinish: mode == "recovery after captured effect", shortLease: mode == "recovery after captured effect"}
			err = runAGUIMCPProxyWorker(ctx, backend, store, record, input, proxy, nil)
			if mode == "recovery after captured effect" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 1, registry.calls, "canonical effect executes once")
			latest, err := fixture.store.GetRun(ctx, "owner", "proxy", "run")
			require.NoError(t, err)
			switch mode {
			case "success":
				require.Equal(t, 1, native.successPatches, "successful native completion is not repeated")
				require.Equal(t, aguistore.StatusFinished, latest.Status)
			case "failed capture":
				require.Equal(t, 0, native.successPatches)
				require.Equal(t, 1, native.failedPatches)
				require.Equal(t, aguistore.StatusError, latest.Status)
			case "failed first completion":
				require.Equal(t, 2, native.successPatches, "missing success proof retains full finalization")
				require.Equal(t, aguistore.StatusFinished, latest.Status)
			case "recovery after captured effect":
				require.Equal(t, 1, native.successPatches)
				require.Equal(t, aguistore.StatusRunning, latest.Status)
				require.Contains(t, string(latest.Pending), "nativeOperationId")
				time.Sleep(1100 * time.Millisecond)
				require.NoError(t, runAGUIMCPProxyWorker(ctx, backend, store, latest, input, proxy, nil))
				require.Equal(t, 1, registry.calls, "recovery consumes the durable receipt, never repeats the effect")
				require.Equal(t, 2, native.successPatches, "a new worker has no request-scoped completion proof")
				latest, err = fixture.store.GetRun(ctx, "owner", "proxy", "run")
				require.NoError(t, err)
				require.Equal(t, aguistore.StatusFinished, latest.Status)
			}
			events, err := fixture.store.Replay(ctx, "owner", "proxy", "run", 0, 10)
			require.NoError(t, err)
			if mode == "failed capture" {
				require.Contains(t, string(events[len(events)-1].Event), "RUN_ERROR")
			} else {
				require.Contains(t, string(events[len(events)-1].Event), "RUN_FINISHED")
			}
			require.Error(t, runAGUIMCPProxyWorker(ctx, backend, store, latest, input, proxy, nil))
			require.Equal(t, 1, registry.calls)
		})
	}
}
