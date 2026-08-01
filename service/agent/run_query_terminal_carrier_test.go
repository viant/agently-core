package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	base "github.com/viant/agently-core/genai/llm/provider/base"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/binding"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/reactor"
)

func terminalCarrierBinding(turnID string, messages ...*binding.Message) *binding.Binding {
	return &binding.Binding{History: binding.History{
		CurrentTurnID: turnID,
		Current:       &binding.Turn{ID: turnID, Messages: messages},
	}}
}

func terminalCarrierRecord() *asynccfg.OperationRecord {
	return &asynccfg.OperationRecord{
		ID:                         "job-1",
		ParentConvID:               "conv-1",
		ParentTurnID:               "turn-1",
		ToolMessageID:              "tool-msg-1",
		ToolCallID:                 "call-1",
		ExecutionMode:              string(asynccfg.ExecutionModeWait),
		TerminalCarrierBeforeModel: true,
		State:                      asynccfg.StateCompleted,
		Status:                     "succeeded",
		KeyData:                    []byte(`{"status":"succeeded","artifactId":"artifact-1"}`),
	}
}

func TestReconcileTerminalCarriersBeforeModel_BlocksOnlyActiveOptInWait(t *testing.T) {
	tests := []struct {
		name    string
		record  *asynccfg.OperationRecord
		blocked bool
	}{
		{
			name: "active opted-in wait blocks",
			record: &asynccfg.OperationRecord{
				ParentConvID: "conv-1", ParentTurnID: "turn-1", ExecutionMode: string(asynccfg.ExecutionModeWait),
				TerminalCarrierBeforeModel: true, State: asynccfg.StateRunning,
			},
			blocked: true,
		},
		{
			name: "active non-opt-in wait is untouched",
			record: &asynccfg.OperationRecord{
				ParentConvID: "conv-1", ParentTurnID: "turn-1", ExecutionMode: string(asynccfg.ExecutionModeWait), State: asynccfg.StateRunning,
			},
		},
		{
			name: "opted-in detach is untouched",
			record: &asynccfg.OperationRecord{
				ParentConvID: "conv-1", ParentTurnID: "turn-1", ExecutionMode: string(asynccfg.ExecutionModeDetach),
				TerminalCarrierBeforeModel: true, State: asynccfg.StateRunning,
			},
		},
		{
			name: "other conversation is untouched",
			record: &asynccfg.OperationRecord{
				ParentConvID: "conv-other", ParentTurnID: "turn-1", ExecutionMode: string(asynccfg.ExecutionModeWait),
				TerminalCarrierBeforeModel: true, State: asynccfg.StateRunning,
			},
		},
		{
			name: "other turn is untouched",
			record: &asynccfg.OperationRecord{
				ParentConvID: "conv-1", ParentTurnID: "turn-other", ExecutionMode: string(asynccfg.ExecutionModeWait),
				TerminalCarrierBeforeModel: true, State: asynccfg.StateRunning,
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			blocked, err := reconcileTerminalCarriersBeforeModel(nil, []*asynccfg.OperationRecord{testCase.record}, "conv-1", "turn-1")
			require.NoError(t, err)
			require.Equal(t, testCase.blocked, blocked)
		})
	}
}

func TestReconcileTerminalCarriersBeforeModel_RequiresOneExactDeclaredCarrier(t *testing.T) {
	tests := []struct {
		name     string
		records  []*asynccfg.OperationRecord
		messages []*binding.Message
	}{
		{
			name: "missing declaration",
			records: func() []*asynccfg.OperationRecord {
				rec := terminalCarrierRecord()
				rec.ToolMessageID = ""
				return []*asynccfg.OperationRecord{rec}
			}(),
		},
		{
			name:    "no message matches both ids",
			records: []*asynccfg.OperationRecord{terminalCarrierRecord()},
			messages: []*binding.Message{
				{ID: "tool-msg-1", ToolOpID: "call-other", Kind: binding.MessageKindToolResult, Content: "queued"},
				{ID: "tool-msg-other", ToolOpID: "call-1", Kind: binding.MessageKindToolResult, Content: "queued"},
			},
		},
		{
			name:    "ambiguous exact carrier",
			records: []*asynccfg.OperationRecord{terminalCarrierRecord()},
			messages: []*binding.Message{
				{ID: "tool-msg-1", ToolOpID: "call-1", Kind: binding.MessageKindToolResult, Content: "queued-a"},
				{ID: "tool-msg-1", ToolOpID: "call-1", Kind: binding.MessageKindToolResult, Content: "queued-b"},
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			b := terminalCarrierBinding("turn-1", testCase.messages...)
			_, err := reconcileTerminalCarriersBeforeModel(b, testCase.records, "conv-1", "turn-1")
			require.Error(t, err)
			for _, msg := range testCase.messages {
				require.Contains(t, msg.Content, "queued")
			}
		})
	}
}

func TestPrepareTerminalCarriersBeforeModel_SteeringBypassesActiveCarrier(t *testing.T) {
	manager := asynccfg.NewManager()
	manager.Register(context.Background(), asynccfg.RegisterInput{
		ID:                         "job-1",
		ParentConvID:               "conv-1",
		ParentTurnID:               "turn-1",
		ExecutionMode:              string(asynccfg.ExecutionModeWait),
		TerminalCarrierBeforeModel: true,
		Status:                     "running",
	})
	svc := &Service{asyncManager: manager}
	turn := memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}

	blocked, err := svc.prepareTerminalCarriersBeforeModel(context.Background(), nil, turn, true)
	require.NoError(t, err)
	require.False(t, blocked)

	blocked, err = svc.prepareTerminalCarriersBeforeModel(context.Background(), nil, turn, false)
	require.NoError(t, err)
	require.True(t, blocked)
}

type terminalCarrierCaptureFinder struct {
	calls   atomic.Int32
	request *llm.GenerateRequest
}

func (f *terminalCarrierCaptureFinder) Find(context.Context, string) (llm.Model, error) {
	return terminalCarrierCaptureModel{finder: f}, nil
}

type terminalCarrierCaptureModel struct {
	finder *terminalCarrierCaptureFinder
}

func (m terminalCarrierCaptureModel) Generate(_ context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.finder.calls.Add(1)
	m.finder.request = request
	return &llm.GenerateResponse{
		Choices: []llm.Choice{{
			Index:        0,
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "final answer"},
			FinishReason: "stop",
		}},
		Model: "mock-model",
	}, nil
}

func (terminalCarrierCaptureModel) Implements(feature string) bool {
	return feature == base.CanUseTools
}

func TestServiceRunPlanLoop_TerminalCarrierSnapshotControlsGeneratedRequest(t *testing.T) {
	tests := []struct {
		name       string
		optIn      bool
		recordConv string
		recordTurn string
		want       string
	}{
		{name: "exact opted-in carrier uses terminal artifact", optIn: true, recordConv: "conv-1", recordTurn: "turn-1", want: `{"status":"succeeded","artifactId":"artifact-1"}`},
		{name: "non-opt-in carrier remains queued", recordConv: "conv-1", recordTurn: "turn-1", want: `{"jobId":"job-1","status":"queued"}`},
		{name: "other conversation cannot patch carrier", optIn: true, recordConv: "conv-other", recordTurn: "turn-1", want: `{"jobId":"job-1","status":"queued"}`},
		{name: "other turn cannot patch carrier", optIn: true, recordConv: "conv-1", recordTurn: "turn-other", want: `{"jobId":"job-1","status":"queued"}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
			stale := `{"jobId":"job-1","status":"queued"}`
			conv := &apiconv.Conversation{
				Id: "conv-1",
				Transcript: []*agconv.TranscriptView{{
					Id:             "turn-1",
					ConversationId: "conv-1",
					Message: []*agconv.MessageView{
						{Id: "user-1", ConversationId: "conv-1", TurnId: cancelPtr("turn-1"), Role: "user", Type: "text", Mode: cancelPtr("task"), Content: cancelPtr("export it"), CreatedAt: now},
						{
							Id: "tool-msg-1", ConversationId: "conv-1", TurnId: cancelPtr("turn-1"), Role: "tool", Type: "tool_op", Content: &stale, CreatedAt: now.Add(time.Second),
							MessageToolCall: &agconv.MessageToolCallView{MessageId: "tool-msg-1", TurnId: cancelPtr("turn-1"), OpId: "call-1", ToolName: "export:start", Status: "completed"},
						},
					},
				}},
			}
			convClient := newLoopConvClient(conv)
			finder := &terminalCarrierCaptureFinder{}
			llmSvc := core.New(finder, nil, convClient)
			manager := asynccfg.NewManager()
			manager.Register(context.Background(), asynccfg.RegisterInput{
				ID:                         "job-1",
				ParentConvID:               testCase.recordConv,
				ParentTurnID:               testCase.recordTurn,
				ToolCallID:                 "call-1",
				ToolMessageID:              "tool-msg-1",
				ToolName:                   "export:start",
				ExecutionMode:              string(asynccfg.ExecutionModeWait),
				TerminalCarrierBeforeModel: testCase.optIn,
				Status:                     "succeeded",
				KeyData:                    []byte(`{"status":"succeeded","artifactId":"artifact-1"}`),
			})
			svc := &Service{
				llm:          llmSvc,
				conversation: convClient,
				orchestrator: reactor.New(llmSvc, nil, convClient, nil, nil),
				defaults:     &config.Defaults{},
				asyncManager: manager,
			}
			ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
			ctx = memory.WithRunMeta(ctx, memory.RunMeta{RunID: "turn-1", Iteration: 1})
			input := &QueryInput{
				ConversationID: "conv-1",
				UserId:         "user-1",
				Query:          "export it",
				Agent: &agentmdl.Agent{
					Identity:       agentmdl.Identity{ID: "simple"},
					ModelSelection: llm.ModelSelection{Model: "mock-model"},
					Prompt:         &binding.Prompt{Text: "You are helpful."},
				},
			}

			output := &QueryOutput{}
			require.NoError(t, svc.runPlanLoop(ctx, input, output))
			require.Equal(t, "final answer", output.Content)
			require.EqualValues(t, 1, finder.calls.Load())
			require.NotNil(t, finder.request)
			var generatedCarrier string
			for _, msg := range finder.request.Messages {
				if msg.ToolCallId == "call-1" {
					generatedCarrier = msg.Content
				}
			}
			require.NotEmpty(t, generatedCarrier)
			require.JSONEq(t, testCase.want, generatedCarrier)
		})
	}
}
