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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	authctx "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/runtime/usage"
	agentsvc "github.com/viant/agently-core/service/agent"
	goalsys "github.com/viant/agently-core/service/goal"
	"github.com/viant/agently-core/service/scheduler"
	"github.com/viant/agently-core/workspace"
)

// Native runtime commits supply accounting and schedule values; all observed
// snapshots and lifecycle mutations cross the public AG-UI HTTP handler.
func TestAGUIGoalHTTPCommittedAccountingSchedulerAndPrincipal(t *testing.T) {
	enableAtomicGoals(t)
	require.NoError(t, os.WriteFile(filepath.Join(workspace.Root(), "config.yaml"), []byte("features:\n  goals:\n    enabled: true\n  wakeups:\n    enabled: true\n    minWakeDelaySeconds: 1\n    maxWakeDelaySeconds: 3600\n"), 0600))
	native, repo, db := atomicGoalFixture(t)
	_, err := db.Exec(`UPDATE conversation SET created_by_user_id='owner',visibility='private' WHERE id='goal-thread'`)
	require.NoError(t, err)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	conv, err := convservice.New(ctx, native)
	require.NoError(t, err)
	backend := &backendClient{goalRepo: repo, goalInvoker: native, conv: conv, data: data.NewService(native), streaming: streaming.NewMemoryBus(64)}
	schedStore, err := scheduler.NewDatlyStore(ctx, native, backend.data)
	require.NoError(t, err)
	sched := scheduler.New(schedStore, nil)
	backend.SetScheduler(sched)
	handler := handleAGUIRun(backend, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r.WithContext(authctx.WithUserInfo(r.Context(), &authctx.UserInfo{Subject: r.Header.Get("X-Test-Principal")})))
	}))
	defer server.Close()
	post := func(principal, op string, payload any) *http.Response {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"threadId": "goal-thread", "runId": uuid.NewString(), "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "requestId": uuid.NewString(), "operation": op, "payload": payload}}})
		require.NoError(t, err)
		req, err := http.NewRequest("POST", server.URL, bytes.NewReader(raw))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Principal", principal)
		response, err := server.Client().Do(req)
		require.NoError(t, err)
		return response
	}
	command := func(op string, payload any) string {
		t.Helper()
		res := post("owner", op, payload)
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, 200, res.StatusCode, string(raw))
		require.NotContains(t, string(raw), `"type":"RUN_ERROR"`)
		require.Contains(t, string(raw), `"type":"RUN_FINISHED"`)
		return string(raw)
	}
	command("goal.create", map[string]any{"objective": "HTTP native accounting", "tokenBudget": 100})
	// A completed independent foreground resource run precedes observation.
	command("goal.get", map[string]any{})
	goal, err := repo.Current(ctx, "goal-thread")
	require.NoError(t, err)
	scheduled, err := sched.ScheduleGoalWakeup(ctx, agentsvc.GoalWakeupRequest{ConversationID: "goal-thread", GoalID: goal.ID, UserID: "owner", AgentID: "coder", WakeAt: time.Now().UTC().Add(time.Hour), Preview: "native scheduled checkpoint", Payload: "Continue goal"})
	require.NoError(t, err)
	require.True(t, scheduled)
	res := post("owner", "goal.subscribe", map[string]any{"durationSeconds": 10})
	require.Equal(t, 200, res.StatusCode)
	defer res.Body.Close()
	snapshots := make(chan *AGUIGoalSnapshot, 32)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(res.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var event struct {
				Type    string
				Content AGUIGoalCommandResult
			}
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) == nil && event.Type == "ACTIVITY_SNAPSHOT" {
				snapshots <- event.Content.Goal
			}
		}
		done <- scanner.Err()
	}()
	wait := func(predicate func(*AGUIGoalSnapshot) bool) *AGUIGoalSnapshot {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case goal := <-snapshots:
				if goal != nil && predicate(goal) {
					return goal
				}
			case <-timer.C:
				t.Fatal("missing committed goal HTTP snapshot")
				return nil
			}
		}
	}
	first := wait(func(g *AGUIGoalSnapshot) bool { return g.Status == "active" })
	require.Equal(t, int64(0), first.TokensUsed)
	require.NotNil(t, first.ControllerSchedule)
	require.Equal(t, "native scheduled checkpoint", first.ControllerSchedule.Preview)
	require.Equal(t, "wakeup", first.ControllerSchedule.Mode)
	require.NotEmpty(t, first.ControllerSchedule.WakeAt)
	command("goal.pause", map[string]any{"reason": "HTTP pause"})
	paused := wait(func(g *AGUIGoalSnapshot) bool { return g.Status == "paused" })
	require.Nil(t, paused.ControllerSchedule)
	require.Nil(t, sched.CurrentGoalWakeup(ctx, "goal-thread", first.ID))
	command("goal.resume", map[string]any{})
	wait(func(g *AGUIGoalSnapshot) bool { return g.Status == "active" && g.ControllerSchedule == nil })
	totals := &usage.Aggregator{}
	totals.Add("fixture", 80, 70, 0, 0)
	_, committed, err := goalsys.NewRuntime(repo).AfterTurn(ctx, &goalsys.AfterTurnInput{ConversationID: "goal-thread", TurnStatus: "succeeded", RequestTime: time.Now().Add(-3 * time.Second), Usage: totals})
	require.NoError(t, err)
	require.Equal(t, goalsys.StatusBudgetLimited, committed.Status)
	command("goal.update", map[string]any{"objective": "HTTP committed usage projection"})
	accounted := wait(func(g *AGUIGoalSnapshot) bool { return g.Status == "budget_limited" && g.TokensUsed == 150 })
	require.GreaterOrEqual(t, accounted.TimeUsedSeconds, int64(3))
	require.Equal(t, committed.StatusReason, accounted.StatusReason)
	for _, op := range []string{"goal.get", "goal.create", "goal.update", "goal.clear", "goal.pause", "goal.resume", "goal.subscribe"} {
		payload := map[string]any{}
		if op == "goal.create" || op == "goal.update" {
			payload["objective"] = "foreign change"
		}
		foreign := post("intruder", op, payload)
		raw, err := io.ReadAll(foreign.Body)
		foreign.Body.Close()
		require.NoError(t, err)
		require.Equal(t, 403, foreign.StatusCode, string(raw))
	}
	unchanged, err := repo.Current(ctx, "goal-thread")
	require.NoError(t, err)
	require.Equal(t, goalsys.StatusBudgetLimited, unchanged.Status)
	require.Equal(t, int64(150), unchanged.TokensUsed)
	res.Body.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("HTTP reader did not detach")
	}
}
