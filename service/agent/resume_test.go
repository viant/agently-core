package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/service/reactor"
)

type resumeConvClient struct {
	apiconv.Client
	modelCalls []*apiconv.MutableModelCall
	toolCalls  []*apiconv.MutableToolCall
	messages   []*apiconv.MutableMessage
}

func (c *resumeConvClient) PatchModelCall(_ context.Context, m *apiconv.MutableModelCall) error {
	c.modelCalls = append(c.modelCalls, m)
	return nil
}

func (c *resumeConvClient) PatchToolCall(_ context.Context, t *apiconv.MutableToolCall) error {
	c.toolCalls = append(c.toolCalls, t)
	return nil
}

func (c *resumeConvClient) PatchMessage(_ context.Context, m *apiconv.MutableMessage) error {
	c.messages = append(c.messages, m)
	return nil
}

func intptr(v int) *int { return &v }

func assistantWithModelCall(id, status string, iteration int, completed bool, payload string) *agconv.MessageView {
	content := "assistant says"
	mc := &agconv.ModelCallView{MessageId: id, Status: status, Iteration: intptr(iteration)}
	if completed {
		now := time.Now()
		mc.CompletedAt = &now
	}
	if payload != "" {
		mc.ModelCallResponsePayload = &agconv.ModelCallStreamPayloadView{Id: "p-" + id, InlineBody: &payload}
	}
	return &agconv.MessageView{Id: id, Role: "assistant", Type: "text", Content: &content, CreatedAt: time.Now(), ModelCall: mc}
}

func toolOpMessage(id, opID, status string, iteration int, content string) *agconv.MessageView {
	var body *string
	if content != "" {
		body = &content
	}
	return &agconv.MessageView{Id: id, Role: "tool", Type: "tool_op", Content: body, CreatedAt: time.Now(),
		MessageToolCall: &agconv.MessageToolCallView{MessageId: id, OpId: opID, Status: status, Iteration: intptr(iteration)}}
}

func TestRecoveryStart_Boundaries(t *testing.T) {
	const twoTools = `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"op-a","name":"t/a","arguments":{"x":1}},{"id":"op-b","name":"t/b","arguments":{}}]}}]}`
	tests := []struct {
		name           string
		messages       []*agconv.MessageView
		wantPhase      planLoopPhase
		wantIter       int
		wantSteps      int
		wantCompleted  []string
		wantModelPatch bool
		wantErr        error
	}{
		{name: "no model call enters model phase at run iteration", wantPhase: planLoopPhaseModel, wantIter: 3},
		{name: "unfinished model attempt is discarded as a whole",
			messages:  []*agconv.MessageView{assistantWithModelCall("m1", "streaming", 2, false, "")},
			wantPhase: planLoopPhaseModel, wantIter: 2, wantModelPatch: true},
		{name: "unfinished model attempt with running tool row blocks",
			messages: []*agconv.MessageView{assistantWithModelCall("m1", "streaming", 2, false, ""), toolOpMessage("t1", "op-x", "running", 2, "")},
			wantErr:  errRecoveryAmbiguousToolOutcome},
		{name: "completed model attempt resumes only never-started tools",
			messages:  []*agconv.MessageView{assistantWithModelCall("m1", "completed", 2, true, twoTools), toolOpMessage("t1", "op-a", "completed", 2, "done-a")},
			wantPhase: planLoopPhaseTools, wantIter: 2, wantSteps: 2, wantCompleted: []string{"op-a"}},
		{name: "completed model attempt with running tool without result blocks",
			messages: []*agconv.MessageView{assistantWithModelCall("m1", "completed", 2, true, twoTools), toolOpMessage("t1", "op-a", "running", 2, "")},
			wantErr:  errRecoveryAmbiguousToolOutcome},
		{name: "completed model attempt without tools finishes without a model call",
			messages:  []*agconv.MessageView{assistantWithModelCall("m1", "completed", 4, true, `{"choices":[{"message":{"role":"assistant","content":"final"}}]}`)},
			wantPhase: planLoopPhaseTools, wantIter: 4, wantSteps: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &resumeConvClient{}
			svc := &Service{conversation: client, orchestrator: &reactor.Service{}}
			turn := &apiconv.Turn{Id: "run-1", Status: "running", Message: test.messages}
			start, err := svc.recoveryStart(context.Background(), turn, "run-1", 3)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("err=%v want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("recoveryStart error: %v", err)
			}
			if start.Phase != test.wantPhase || start.firstIteration() != test.wantIter {
				t.Fatalf("phase=%d iter=%d, want %d/%d", start.Phase, start.firstIteration(), test.wantPhase, test.wantIter)
			}
			if planStepCount(start.Plan) != test.wantSteps {
				t.Fatalf("steps=%d want %d", planStepCount(start.Plan), test.wantSteps)
			}
			if len(start.Completed) != len(test.wantCompleted) {
				t.Fatalf("completed=%v want %v", start.Completed, test.wantCompleted)
			}
			for _, op := range test.wantCompleted {
				if call, ok := start.Completed[op]; !ok || call.Result != "done-a" {
					t.Fatalf("completed %q missing or wrong result: %+v", op, call)
				}
			}
			if test.wantModelPatch != (len(client.modelCalls) == 1) {
				t.Fatalf("model call patches=%d, wantPatch=%v", len(client.modelCalls), test.wantModelPatch)
			}
			if test.wantModelPatch && client.modelCalls[0].Status != "canceled" {
				t.Fatalf("interrupted model call status=%q, want canceled", client.modelCalls[0].Status)
			}
			if test.wantModelPatch && (len(client.messages) != 1 || client.messages[0].Interim == nil || *client.messages[0].Interim != 1) {
				t.Fatalf("interrupted model message must be hidden as interim: %+v", client.messages)
			}
			if test.wantPhase == planLoopPhaseTools && start.MessageID != "m1" {
				t.Fatalf("tools phase must anchor to durable assistant message, got %q", start.MessageID)
			}
		})
	}
}

func TestRunLease_LostStopsDispatch(t *testing.T) {
	svc := &Service{}
	canceled := false
	lease := svc.newRunLease("", func() { canceled = true })
	if lease.Owner() == "" || !lease.Active() {
		t.Fatalf("new lease must be active with a token")
	}
	other := svc.newRunLease("", nil)
	if other.Owner() == lease.Owner() {
		t.Fatalf("lease tokens must be unique per admission")
	}
	lease.markLost()
	lease.markLost()
	if lease.Active() || !canceled {
		t.Fatalf("lost lease must be inactive and cancel local execution once")
	}
}

func TestRunLease_DeadlineCancelsWithoutHeartbeat(t *testing.T) {
	canceled := make(chan struct{}, 1)
	lease := &runLease{onLost: func() { canceled <- struct{}{} }}
	lease.arm("owner", time.Now().Add(20*time.Millisecond))
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("lease deadline did not cancel execution")
	}
	if lease.Active() {
		t.Fatal("expired lease remained active")
	}
}

func TestRunLeaseTimestamp_UTCSecondPrecision(t *testing.T) {
	local := time.Date(2026, 9, 17, 10, 30, 45, 987654321, time.FixedZone("PDT", -7*60*60))
	got := runLeaseTimestamp(local)
	want := time.Date(2026, 9, 17, 17, 30, 45, 0, time.UTC)
	if got.Location() != time.UTC || !got.Equal(want) || got.Nanosecond() != 0 {
		t.Fatalf("runLeaseTimestamp(%v)=%v, want %v in UTC at whole-second precision", local, got, want)
	}
}

func TestResolveResumeAgentID_PrefersAdmittedTurnAgent(t *testing.T) {
	turnAgent := "steward"
	conversationAgent := "default-agent"
	conv := &apiconv.Conversation{AgentId: &conversationAgent}
	if got := resolveResumeAgentID("intake_sidecar", &turnAgent, conv); got != "steward" {
		t.Fatalf("resolveResumeAgentID()=%q, want admitted turn agent", got)
	}
}

func TestResolveResumeAgentID_SkipsInternalHelperTurn(t *testing.T) {
	helper := "intake_sidecar"
	steward := "steward"
	conv := &apiconv.Conversation{AgentId: &steward}
	if got := resolveResumeAgentID("", &helper, conv); got != "steward" {
		t.Fatalf("resolveResumeAgentID()=%q, want conversation agent", got)
	}
}

func TestResumeTurnQuery_RestoresPersistedUserTask(t *testing.T) {
	older := "older request"
	latest := "  troubleshoot order 2696000  "
	turn := &apiconv.Turn{Message: []*agconv.MessageView{
		{Role: "user", Content: &older},
		{Role: "assistant", Content: &older},
		{Role: "user", RawContent: &latest},
	}}
	if got := resumeTurnQuery(turn); got != "troubleshoot order 2696000" {
		t.Fatalf("resumeTurnQuery()=%q", got)
	}
}
