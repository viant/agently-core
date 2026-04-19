package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/feedextract"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/elicitation"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/agently-core/workspace"
	wscodec "github.com/viant/agently-core/workspace/codec"
)

type feedTestRegistry struct {
	exec map[string]string
}

func (r *feedTestRegistry) Definitions() []llm.ToolDefinition                { return nil }
func (r *feedTestRegistry) MatchDefinition(string) []*llm.ToolDefinition     { return nil }
func (r *feedTestRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) { return nil, false }
func (r *feedTestRegistry) MustHaveTools([]string) ([]llm.Tool, error)       { return nil, nil }
func (r *feedTestRegistry) SetDebugLogger(io.Writer)                         {}
func (r *feedTestRegistry) Initialize(context.Context)                       {}
func (r *feedTestRegistry) Execute(_ context.Context, name string, _ map[string]interface{}) (string, error) {
	if out, ok := r.exec[name]; ok {
		return out, nil
	}
	return "", errors.New("not found")
}

func TestFeedNotifier_BuiltinFeedsLiveBehavior(t *testing.T) {
	tests := []struct {
		name              string
		specName          string
		toolName          string
		result            string
		expectedItemCount int
		expectedJSON      string
	}{
		{
			name:              "terminal",
			specName:          "terminal",
			toolName:          "system_exec-execute",
			result:            `{"commands":[{"input":"pwd","output":"/tmp"},{"input":"ls","output":"a\nb"}],"stdout":"/tmp"}`,
			expectedItemCount: 2,
			expectedJSON:      `{"input":{},"output":{"commands":[{"input":"pwd","output":"/tmp"},{"input":"ls","output":"a\nb"}],"stdout":"/tmp"}}`,
		},
		{
			name:              "plan",
			specName:          "plan",
			toolName:          "orchestration:updatePlan",
			result:            `{"explanation":"Ship it","plan":[{"status":"completed","step":"Write tests"},{"status":"pending","step":"Review PR"}]}`,
			expectedItemCount: 2,
			expectedJSON:      `{"input":{},"output":{"explanation":"Ship it","plan":[{"status":"completed","step":"Write tests","content":"**Status:** completed\n\n**Step:** Write tests"},{"status":"pending","step":"Review PR","content":"**Status:** pending\n\n**Step:** Review PR"}]}}`,
		},
		{
			name:              "changes",
			specName:          "changes",
			toolName:          "system_patch-apply",
			result:            `{"changes":[{"url":"/tmp/apply.go","kind":"modify"}],"status":"apply"}`,
			expectedItemCount: 1,
			expectedJSON:      `{"input":{},"output":{"changes":[{"url":"/tmp/apply.go","kind":"modify"}],"status":"apply"}}`,
		},
		{
			name:              "explorer",
			specName:          "explorer",
			toolName:          "resources-grepFiles",
			result:            `{"files":[{"Path":"bitset.go","Matches":3},{"Path":"state.go","Matches":2}]}`,
			expectedItemCount: 2,
			expectedJSON:      `{"input":null,"output":{"files":[{"Path":"bitset.go","Matches":3},{"Path":"state.go","Matches":2}]},"entries":[{"Path":"bitset.go","Matches":3},{"Path":"state.go","Matches":2}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := loadBuiltinFeedSpec(t, tc.specName)
			bus := streaming.NewMemoryBus(4)
			sub, err := bus.Subscribe(context.Background(), nil)
			require.NoError(t, err)
			defer sub.Close()

			notifier := newFeedNotifier(&FeedRegistry{specs: []*FeedSpec{spec}}, bus)
			ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
				ConversationID: "conv-live",
				TurnID:         "turn-live",
			})
			notifier.NotifyToolCompleted(ctx, tc.toolName, tc.result)

			ev := readEvent(t, sub)
			require.NotNil(t, ev)
			assert.Equal(t, streaming.EventTypeToolFeedActive, ev.Type)
			assert.Equal(t, spec.ID, ev.FeedID)
			assert.Equal(t, tc.expectedItemCount, ev.FeedItemCount)
			assert.JSONEq(t, tc.expectedJSON, mustMarshalJSON(t, ev.FeedData))

			notifier.EmitInactiveForMissing(ctx, "conv-live", nil)
			ev = readEvent(t, sub)
			require.NotNil(t, ev)
			assert.Equal(t, streaming.EventTypeToolFeedInactive, ev.Type)
			assert.Equal(t, spec.ID, ev.FeedID)
		})
	}
}

func TestFeedNotifier_SkipsChangesFeedWhenNoChangesExtracted(t *testing.T) {
	spec := loadBuiltinFeedSpec(t, "changes")
	bus := streaming.NewMemoryBus(4)
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	notifier := newFeedNotifier(&FeedRegistry{specs: []*FeedSpec{spec}}, bus)
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		ConversationID: "conv-live",
		TurnID:         "turn-live",
	})

	notifier.NotifyToolCompleted(ctx, "system_patch-apply", `{"stats":{},"status":"ok"}`)
	assertNoEvent(t, sub)
}

func TestResolveActiveFeedsFromState_BuiltinYAMLBehavior(t *testing.T) {
	tests := []struct {
		name              string
		specName          string
		toolSteps         []*ToolStepState
		payloads          map[string]string
		exec              map[string]string
		expectedFeedID    string
		expectedItemCount int
		expectedJSON      string
	}{
		{
			name:     "terminal",
			specName: "terminal",
			toolSteps: []*ToolStepState{
				{ToolName: "system_exec-execute", ResponsePayloadID: "p1"},
				{ToolName: "system_exec-execute", ResponsePayloadID: "p2"},
			},
			payloads: map[string]string{
				"p1": `{"commands":[{"input":"pwd","output":"/tmp"}],"stdout":"/tmp"}`,
				"p2": `{"commands":[{"input":"ls","output":"a\nb"}],"status":"ok"}`,
			},
			expectedFeedID:    "terminal",
			expectedItemCount: 2,
			expectedJSON:      `{"input":{},"output":{"commands":[{"input":"pwd","output":"/tmp"},{"input":"ls","output":"a\nb"}],"stdout":"/tmp","status":"ok"}}`,
		},
		{
			name:     "plan",
			specName: "plan",
			toolSteps: []*ToolStepState{
				{ToolName: "orchestration:updatePlan", ResponsePayloadID: "p1"},
			},
			payloads: map[string]string{
				"p1": `{"explanation":"Ship it","plan":[{"status":"completed","step":"Write tests"},{"status":"pending","step":"Review PR"}]}`,
			},
			expectedFeedID:    "plan",
			expectedItemCount: 2,
			expectedJSON:      `{"input":{},"output":{"explanation":"Ship it","plan":[{"status":"completed","step":"Write tests","content":"**Status:** completed\n\n**Step:** Write tests"},{"status":"pending","step":"Review PR","content":"**Status:** pending\n\n**Step:** Review PR"}]}}`,
		},
		{
			name:     "changes",
			specName: "changes",
			toolSteps: []*ToolStepState{
				{ToolName: "system_patch-apply", ResponsePayloadID: "apply"},
			},
			payloads: map[string]string{
				"apply": `{"changes":[{"url":"/tmp/apply.go","kind":"modify"}],"status":"apply"}`,
			},
			exec: map[string]string{
				"system/patch:snapshot": `{"changes":[{"url":"/tmp/a.go","kind":"create"},{"url":"/tmp/b.go","kind":"modify"}],"status":"ok"}`,
			},
			expectedFeedID:    "changes",
			expectedItemCount: 2,
			expectedJSON:      `{"input":{},"output":{"changes":[{"url":"/tmp/a.go","kind":"create"},{"url":"/tmp/b.go","kind":"modify"}],"status":"ok"}}`,
		},
		{
			name:     "explorer",
			specName: "explorer",
			toolSteps: []*ToolStepState{
				{ToolName: "resources-grepFiles", RequestPayloadID: "r1", ResponsePayloadID: "p1"},
				{ToolName: "resources-grepFiles", RequestPayloadID: "r2", ResponsePayloadID: "p2"},
			},
			payloads: map[string]string{
				"r1": `{"path":"repo","pattern":"old"}`,
				"r2": `{"path":"repo","pattern":"SetBit"}`,
				"p1": `{"files":[{"Path":"bitset.go","Matches":3}],"path":"repo"}`,
				"p2": `{"files":[{"Path":"state.go","Matches":2}],"stats":{"matches":5},"modeApplied":"grep"}`,
			},
			expectedFeedID:    "explorer",
			expectedItemCount: 2,
			expectedJSON:      `{"input":{"path":"repo","pattern":"SetBit"},"output":{"files":[{"Path":"bitset.go","Matches":3},{"Path":"state.go","Matches":2}],"path":"repo","stats":{"matches":5},"modeApplied":"grep"},"entries":[{"Path":"bitset.go","Matches":3},{"Path":"state.go","Matches":2}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := loadBuiltinFeedSpec(t, tc.specName)
			client := &backendClient{
				conv: newPayloadOnlyConversationClient(tc.payloads),
				registry: &feedTestRegistry{
					exec: tc.exec,
				},
				feeds: &FeedRegistry{specs: []*FeedSpec{spec}},
			}
			state := &ConversationState{
				ConversationID: "conv-resolve",
				Turns: []*TurnState{{
					TurnID: "turn-1",
					Execution: &ExecutionState{
						Pages: []*ExecutionPageState{{
							PageID:    "page-1",
							ToolSteps: tc.toolSteps,
						}},
					},
				}},
			}

			feeds := client.resolveActiveFeedsFromState(context.Background(), state)
			require.Len(t, feeds, 1)
			assert.Equal(t, tc.expectedFeedID, feeds[0].FeedID)
			assert.Equal(t, tc.expectedItemCount, feeds[0].ItemCount)
			assert.JSONEq(t, tc.expectedJSON, string(feeds[0].Data))
		})
	}
}

func TestResolveActiveFeedsFromState_UsesActivationToolWhenPayloadMissing(t *testing.T) {
	spec := loadBuiltinFeedSpec(t, "changes")
	client := &backendClient{
		conv: newPayloadOnlyConversationClient(map[string]string{
			"apply": `{"changes":[{"url":"/tmp/apply.go","kind":"modify"}],"status":"apply"}`,
		}),
		registry: &feedTestRegistry{
			exec: map[string]string{
				"system/patch:snapshot": `{"changes":[{"url":"/tmp/a.go","kind":"create"},{"url":"/tmp/b.go","kind":"modify"}],"status":"ok"}`,
			},
		},
		feeds: &FeedRegistry{specs: []*FeedSpec{spec}},
	}
	state := &ConversationState{
		ConversationID: "conv-resolve",
		Turns: []*TurnState{{
			TurnID: "turn-1",
			Execution: &ExecutionState{
				Pages: []*ExecutionPageState{{
					PageID: "page-1",
					ToolSteps: []*ToolStepState{
						{ToolName: "system_patch-apply", ResponsePayloadID: "apply"},
					},
				}},
			},
		}},
	}

	feeds := client.resolveActiveFeedsFromState(context.Background(), state)
	require.Len(t, feeds, 1)
	assert.Equal(t, "changes", feeds[0].FeedID)
	assert.Equal(t, 2, feeds[0].ItemCount)
	assert.JSONEq(t, `{"input":{},"output":{"changes":[{"url":"/tmp/a.go","kind":"create"},{"url":"/tmp/b.go","kind":"modify"}],"status":"ok"}}`, string(feeds[0].Data))
}

func TestBuiltinFeedYAMLDecodeAndGenericBridgeParity(t *testing.T) {
	tests := []struct {
		name              string
		specName          string
		requestPayloads   []string
		responsePayloads  []string
		expectedRootName  string
		expectedItemCount int
		expectedJSON      string
	}{
		{
			name:              "terminal",
			specName:          "terminal",
			responsePayloads:  []string{`{"commands":[{"input":"pwd","output":"/tmp"}],"stdout":"/tmp"}`, `{"commands":[{"input":"ls","output":"a\nb"}],"status":"ok"}`},
			expectedRootName:  "commands",
			expectedItemCount: 2,
			expectedJSON:      `{"input":{},"output":{"commands":[{"input":"pwd","output":"/tmp"},{"input":"ls","output":"a\nb"}],"stdout":"/tmp","status":"ok"}}`,
		},
		{
			name:              "plan",
			specName:          "plan",
			responsePayloads:  []string{`{"explanation":"Ship it","plan":[{"status":"completed","step":"Write tests"},{"status":"pending","step":"Review PR"}]}`},
			expectedRootName:  "planDetail",
			expectedItemCount: 2,
			expectedJSON:      `{"input":{},"output":{"explanation":"Ship it","plan":[{"status":"completed","step":"Write tests","content":"**Status:** completed\n\n**Step:** Write tests"},{"status":"pending","step":"Review PR","content":"**Status:** pending\n\n**Step:** Review PR"}]}}`,
		},
		{
			name:              "changes",
			specName:          "changes",
			responsePayloads:  []string{`{"changes":[{"url":"/tmp/a.go","kind":"create"},{"url":"/tmp/b.go","kind":"modify"}],"status":"ok"}`},
			expectedRootName:  "changes",
			expectedItemCount: 2,
			expectedJSON:      `{"input":{},"output":{"changes":[{"url":"/tmp/a.go","kind":"create"},{"url":"/tmp/b.go","kind":"modify"}],"status":"ok"}}`,
		},
		{
			name:              "explorer",
			specName:          "explorer",
			requestPayloads:   []string{`{"path":"repo","pattern":"SetBit"}`},
			responsePayloads:  []string{`{"files":[{"Path":"bitset.go","Matches":3}],"path":"repo"}`, `{"files":[{"Path":"state.go","Matches":2}],"stats":{"matches":5},"modeApplied":"grep"}`},
			expectedRootName:  "entries",
			expectedItemCount: 2,
			expectedJSON:      `{"input":{"path":"repo","pattern":"SetBit"},"output":{"files":[{"Path":"bitset.go","Matches":3},{"Path":"state.go","Matches":2}],"path":"repo","stats":{"matches":5},"modeApplied":"grep"},"entries":[{"Path":"bitset.go","Matches":3},{"Path":"state.go","Matches":2}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := loadBuiltinFeedSpec(t, tc.specName)
			feedSpec := toGenericFeedSpec(spec)
			require.NotNil(t, feedSpec)
			result, err := feedextract.Extract(&feedextract.Input{
				Spec:             feedSpec,
				RequestPayloads:  tc.requestPayloads,
				ResponsePayloads: tc.responsePayloads,
			})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tc.expectedRootName, result.RootName)
			assert.Equal(t, tc.expectedItemCount, result.ItemCount)
			assert.JSONEq(t, tc.expectedJSON, mustMarshalJSON(t, result.RootData))
		})
	}
}

func TestEmbeddedClient_GetTranscript_WithIncludeFeeds_BuiltinBehavior(t *testing.T) {
	iteration := 1
	payloads := map[string]string{
		"r1": `{"path":"repo","pattern":"SetBit"}`,
		"p1": `{"files":[{"Path":"bitset.go","Matches":3}],"path":"repo"}`,
		"p2": `{"files":[{"Path":"state.go","Matches":2}],"stats":{"matches":5},"modeApplied":"grep"}`,
	}
	conv := &conversation.Conversation{
		Id: "conv-transcript",
		Transcript: []*agconv.TranscriptView{
			{
				Id:             "turn-1",
				ConversationId: "conv-transcript",
				Status:         "completed",
				CreatedAt:      time.Now(),
				Message: []*agconv.MessageView{
					{
						Id:        "m1",
						Role:      "assistant",
						Interim:   1,
						Iteration: &iteration,
						ModelCall: &agconv.ModelCallView{MessageId: "m1", Status: "completed"},
						ToolMessage: []*agconv.ToolMessageView{
							{
								Id:              "tm1",
								ParentMessageId: stringPtr("m1"),
								Sequence:        intPtr(1),
								Iteration:       &iteration,
								ToolCall: &agconv.ToolCallView{
									MessageId:         "tm1",
									ToolName:          "resources-grepFiles",
									Status:            "completed",
									RequestPayloadId:  stringPtr("r1"),
									ResponsePayloadId: stringPtr("p1"),
								},
							},
							{
								Id:              "tm2",
								ParentMessageId: stringPtr("m1"),
								Sequence:        intPtr(2),
								Iteration:       &iteration,
								ToolCall: &agconv.ToolCallView{
									MessageId:         "tm2",
									ToolName:          "resources-grepFiles",
									Status:            "completed",
									ResponsePayloadId: stringPtr("p2"),
								},
							},
						},
					},
				},
			},
		},
	}
	client := &backendClient{
		conv:  newConversationWithPayloadsClient(conv, payloads),
		feeds: &FeedRegistry{specs: []*FeedSpec{loadBuiltinFeedSpec(t, "explorer")}},
	}

	resp, err := client.GetTranscript(context.Background(), &GetTranscriptInput{
		ConversationID:    "conv-transcript",
		IncludeToolCalls:  true,
		IncludeModelCalls: true,
	}, WithIncludeFeeds())
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Feeds, 1)
	assert.Equal(t, "explorer", resp.Feeds[0].FeedID)
	assert.Equal(t, 2, resp.Feeds[0].ItemCount)
	assert.JSONEq(t, `{"input":{"path":"repo","pattern":"SetBit"},"output":{"files":[{"Path":"bitset.go","Matches":3},{"Path":"state.go","Matches":2}],"path":"repo","stats":{"matches":5},"modeApplied":"grep"},"entries":[{"Path":"bitset.go","Matches":3},{"Path":"state.go","Matches":2}]}`, string(resp.Feeds[0].Data))
}

func TestHandleGetFeedData_ReturnsResolvedBuiltinFeed(t *testing.T) {
	iteration := 1
	payloads := map[string]string{
		"p1": `{"commands":[{"input":"pwd","output":"/tmp"}],"stdout":"/tmp"}`,
		"p2": `{"commands":[{"input":"ls","output":"a\nb"}],"status":"ok"}`,
	}
	conv := &conversation.Conversation{
		Id: "conv-feed-handler",
		Transcript: []*agconv.TranscriptView{
			{
				Id:             "turn-1",
				ConversationId: "conv-feed-handler",
				Status:         "completed",
				CreatedAt:      time.Now(),
				Message: []*agconv.MessageView{
					{
						Id:        "m1",
						Role:      "assistant",
						Interim:   1,
						Iteration: &iteration,
						ModelCall: &agconv.ModelCallView{MessageId: "m1", Status: "completed"},
						ToolMessage: []*agconv.ToolMessageView{
							{
								Id:              "tm1",
								ParentMessageId: stringPtr("m1"),
								Sequence:        intPtr(1),
								Iteration:       &iteration,
								ToolCall: &agconv.ToolCallView{
									MessageId:         "tm1",
									ToolName:          "system_exec-execute",
									Status:            "completed",
									ResponsePayloadId: stringPtr("p1"),
								},
							},
							{
								Id:              "tm2",
								ParentMessageId: stringPtr("m1"),
								Sequence:        intPtr(2),
								Iteration:       &iteration,
								ToolCall: &agconv.ToolCallView{
									MessageId:         "tm2",
									ToolName:          "system_exec-execute",
									Status:            "completed",
									ResponsePayloadId: stringPtr("p2"),
								},
							},
						},
					},
				},
			},
		},
	}
	spec := loadBuiltinFeedSpec(t, "terminal")
	client := &backendClient{
		conv:  newConversationWithPayloadsClient(conv, payloads),
		feeds: &FeedRegistry{specs: []*FeedSpec{spec}},
	}

	req := httptest.NewRequest("GET", "/v1/feeds/terminal/data?conversationId=conv-feed-handler", nil)
	req.SetPathValue("id", "terminal")
	rec := httptest.NewRecorder()
	handleGetFeedData(client).ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code, rec.Body.String())

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &decoded))
	assert.Equal(t, "terminal", decoded["feedId"])
	assert.Equal(t, "Terminal", decoded["title"])
	assert.NotNil(t, decoded["data"])
	assert.NotNil(t, decoded["dataSources"])
	assert.NotNil(t, decoded["ui"])
	dataJSON, err := json.Marshal(decoded["data"])
	require.NoError(t, err)
	assert.JSONEq(t, `{"input":{},"output":{"commands":[{"input":"pwd","output":"/tmp"},{"input":"ls","output":"a\nb"}],"stdout":"/tmp","status":"ok"}}`, string(dataJSON))
}

func TestFeedNotifier_DoesNotEmitInactiveWhenFeedStillMatched(t *testing.T) {
	spec := loadBuiltinFeedSpec(t, "terminal")
	bus := streaming.NewMemoryBus(4)
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	notifier := newFeedNotifier(&FeedRegistry{specs: []*FeedSpec{spec}}, bus)
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		ConversationID: "conv-still-active",
		TurnID:         "turn-1",
	})
	notifier.NotifyToolCompleted(ctx, "system_exec-execute", `{"commands":[{"input":"pwd","output":"/tmp"}]}`)
	ev := readEvent(t, sub)
	require.NotNil(t, ev)
	assert.Equal(t, streaming.EventTypeToolFeedActive, ev.Type)

	notifier.EmitInactiveForMissing(ctx, "conv-still-active", []string{"system_exec-execute"})
	assertNoEvent(t, sub)
}

func TestFeedNotifier_IgnoresUnmatchedTool(t *testing.T) {
	spec := loadBuiltinFeedSpec(t, "terminal")
	bus := streaming.NewMemoryBus(4)
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	notifier := newFeedNotifier(&FeedRegistry{specs: []*FeedSpec{spec}}, bus)
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		ConversationID: "conv-unmatched",
		TurnID:         "turn-1",
	})
	notifier.NotifyToolCompleted(ctx, "resources-grepFiles", `{"files":[{"Path":"a.go","Matches":1}]}`)
	assertNoEvent(t, sub)
}

func TestFeedNotifier_GuardBranches(t *testing.T) {
	t.Run("nil registry or bus", func(t *testing.T) {
		n := newFeedNotifier(nil, nil)
		n.NotifyToolCompleted(context.Background(), "system_exec-execute", `{}`)
		n.EmitInactiveForMissing(context.Background(), "conv", nil)
	})

	t.Run("matched tool without conversation id emits nothing", func(t *testing.T) {
		spec := loadBuiltinFeedSpec(t, "terminal")
		bus := streaming.NewMemoryBus(2)
		sub, err := bus.Subscribe(context.Background(), nil)
		require.NoError(t, err)
		defer sub.Close()
		n := newFeedNotifier(&FeedRegistry{specs: []*FeedSpec{spec}}, bus)
		n.NotifyToolCompleted(context.Background(), "system_exec-execute", `{}`)
		assertNoEvent(t, sub)
	})

	t.Run("inactive with nil bus/registry no-op", func(t *testing.T) {
		n := &feedNotifier{activeFeeds: map[string]map[string]bool{"conv": {"plan": true}}}
		n.EmitInactiveForMissing(context.Background(), "conv", nil)
		assert.Equal(t, true, n.activeFeeds["conv"]["plan"])
	})

	t.Run("matched tool with empty result does not emit active feed", func(t *testing.T) {
		spec := loadBuiltinFeedSpec(t, "terminal")
		bus := streaming.NewMemoryBus(2)
		sub, err := bus.Subscribe(context.Background(), nil)
		require.NoError(t, err)
		defer sub.Close()
		n := newFeedNotifier(&FeedRegistry{specs: []*FeedSpec{spec}}, bus)
		ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-empty", TurnID: "turn-1"})
		n.NotifyToolCompleted(ctx, "system_exec-execute", "")
		assertNoEvent(t, sub)
	})

	t.Run("uses turn conversation when context conversation missing", func(t *testing.T) {
		spec := loadBuiltinFeedSpec(t, "terminal")
		bus := streaming.NewMemoryBus(2)
		sub, err := bus.Subscribe(context.Background(), nil)
		require.NoError(t, err)
		defer sub.Close()
		n := newFeedNotifier(&FeedRegistry{specs: []*FeedSpec{spec}}, bus)
		ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-turn-only", TurnID: "turn-1"})
		n.NotifyToolCompleted(ctx, "system_exec-execute", `{"commands":[{"input":"pwd","output":"/tmp"}]}`)
		ev := readEvent(t, sub)
		require.NotNil(t, ev)
		assert.Equal(t, "conv-turn-only", ev.ConversationID)
	})
}

func TestFeedRegistry_LoadReloadAndListHandlers(t *testing.T) {
	originalRoot := workspace.Root()
	tmp := t.TempDir()
	workspace.SetRoot(tmp)
	defer workspace.SetRoot(originalRoot)

	feedsDir := filepath.Join(tmp, "feeds")
	require.NoError(t, os.MkdirAll(feedsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(feedsDir, "terminal.yaml"), []byte(`
id: terminal
match:
  service: system/exec
  method: execute
ui:
  title: Terminal
`), 0o644))

	reg := NewFeedRegistry()
	require.True(t, reg.MatchAny("system_exec-execute"))
	require.Len(t, reg.Specs(), 1)
	assert.Equal(t, "Terminal", reg.Specs()[0].Title)

	require.NoError(t, os.WriteFile(filepath.Join(feedsDir, "plan.yaml"), []byte(`
id: plan
match:
  service: orchestration
  method: updatePlan
ui:
  title: Plan
`), 0o644))
	reg.Reload()
	require.Len(t, reg.Specs(), 2)

	client := &backendClient{feeds: reg}
	req := httptest.NewRequest("GET", "/v1/feeds", nil)
	rec := httptest.NewRecorder()
	handleListFeeds(client).ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &decoded))
	feeds, ok := decoded["feeds"].([]interface{})
	require.True(t, ok)
	assert.Len(t, feeds, 2)
}

func TestEmbeddedFeedHelpers_DeprecatedAndPayloadAccess(t *testing.T) {
	iteration := 1
	toolName := "system_exec-execute"
	content := `{"commands":[{"input":"pwd","output":"/tmp"}],"stdout":"/tmp"}`
	turn := &conversation.Turn{
		Id:             "turn-1",
		ConversationId: "conv-1",
		Status:         "completed",
		Message: []*agconv.MessageView{
			{
				Id:        "m1",
				Role:      "assistant",
				Interim:   1,
				ToolName:  &toolName,
				Content:   &content,
				Iteration: &iteration,
				ToolMessage: []*agconv.ToolMessageView{
					{
						Id:              "tm1",
						ParentMessageId: stringPtr("m1"),
						ToolCall: &agconv.ToolCallView{
							MessageId: "tm1",
							ToolName:  toolName,
							Status:    "completed",
						},
					},
				},
			},
		},
	}
	spec := &FeedSpec{
		ID:    "terminal",
		Title: "Terminal",
		Match: FeedMatch{Service: "system/exec", Method: "execute"},
	}
	client := &backendClient{
		conv:  newConversationWithPayloadsClient(&conversation.Conversation{Id: "conv-1", Transcript: []*agconv.TranscriptView{(*agconv.TranscriptView)(turn)}}, nil),
		feeds: &FeedRegistry{specs: []*FeedSpec{spec}},
	}

	data, err := client.ResolveFeedData(context.Background(), spec, "conv-1")
	require.NoError(t, err)
	assert.JSONEq(t, `{"output":{"commands":[{"input":"pwd","output":"/tmp"}],"stdout":"/tmp"}}`, mustMarshalJSON(t, data))

	feeds := client.resolveActiveFeeds(context.Background(), conversation.Transcript{turn})
	require.Len(t, feeds, 1)
	assert.Equal(t, "terminal", feeds[0].FeedID)
	assert.Equal(t, 1, feeds[0].ItemCount)

	assert.Equal(t, content, client.findLastToolCallPayload(context.Background(), conversation.Transcript{turn}, "system_exec-execute"))
}

func TestEmbeddedFeedHelpers_FetchToolCallPayloadAndGetPayloads(t *testing.T) {
	payloads := map[string]string{
		"p1": `{"commands":[{"input":"pwd","output":"/tmp"}]}`,
	}
	msg := &conversation.Message{
		Id: "m1",
		ToolMessage: []*agconv.ToolMessageView{
			{
				Id: "tm1",
				ToolCall: &agconv.ToolCallView{
					MessageId:         "tm1",
					ResponsePayloadId: stringPtr("p1"),
				},
			},
		},
	}
	client := &backendClient{conv: newMessagesAndPayloadsClient(map[string]*conversation.Message{"m1": msg}, payloads)}

	assert.Equal(t, payloads["p1"], client.fetchToolCallResponsePayload(context.Background(), "m1"))
	got, err := client.GetPayload(context.Background(), "p1")
	require.NoError(t, err)
	require.NotNil(t, got)
	all, err := client.GetPayloads(context.Background(), []string{"p1", "missing", "p1", ""})
	require.NoError(t, err)
	assert.Len(t, all, 1)
	assert.Equal(t, "p1", all["p1"].Id)
	assert.Equal(t, payloads["p1"], client.fetchPayloadContent(context.Background(), "p1"))
}

func TestRecordOOBAuthElicitation(t *testing.T) {
	client := &backendClient{}
	err := client.RecordOOBAuthElicitation(context.Background(), "https://example.com/auth")
	require.Error(t, err)

	fakeConv := newMessagesAndPayloadsClient(nil, nil)
	elicSvc := elicitation.New(fakeConv, nil, elicrouter.New(), nil)
	client = &backendClient{elicSvc: elicSvc}
	err = client.RecordOOBAuthElicitation(context.Background(), "https://example.com/auth")
	require.Error(t, err)
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-auth", TurnID: "turn-auth"})
	require.NoError(t, client.RecordOOBAuthElicitation(ctx, "https://example.com/auth"))

	ctx = memory.WithConversationID(context.Background(), "conv-auth-only")
	require.NoError(t, client.RecordOOBAuthElicitation(ctx, "https://example.com/auth"))
}

func TestAdditionalFeedHelperBranches(t *testing.T) {
	t.Run("ResolveFeedData scope all returns nil", func(t *testing.T) {
		spec := &FeedSpec{
			ID:         "terminal",
			Title:      "Terminal",
			Match:      FeedMatch{Service: "system/exec", Method: "execute"},
			Activation: FeedActivation{Scope: "all"},
		}
		client := &backendClient{
			conv:  newConversationWithPayloadsClient(&conversation.Conversation{Id: "conv-1"}, nil),
			feeds: &FeedRegistry{specs: []*FeedSpec{spec}},
		}
		got, err := client.ResolveFeedData(context.Background(), spec, "conv-1")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("ResolveFeedData skips nil turns and empty content before later match", func(t *testing.T) {
		spec := &FeedSpec{
			ID:    "terminal",
			Title: "Terminal",
			Match: FeedMatch{Service: "system/exec", Method: "execute"},
		}
		toolName := "system_exec-execute"
		valid := `{"commands":[{"input":"pwd","output":"/tmp"}]}`
		turn1 := &agconv.TranscriptView{
			Id:             "turn-1",
			ConversationId: "conv-1",
			Status:         "completed",
			Message: []*agconv.MessageView{
				nil,
				{Id: "m1", Role: "assistant", ToolName: &toolName},
			},
		}
		turn2 := &agconv.TranscriptView{
			Id:             "turn-2",
			ConversationId: "conv-1",
			Status:         "completed",
			Message: []*agconv.MessageView{
				{Id: "m2", Role: "assistant", ToolName: &toolName, Content: &valid},
			},
		}
		client := &backendClient{
			conv: newConversationWithPayloadsClient(&conversation.Conversation{Id: "conv-1", Transcript: []*agconv.TranscriptView{turn1, turn2}}, nil),
		}
		got, err := client.ResolveFeedData(context.Background(), spec, "conv-1")
		require.NoError(t, err)
		assert.JSONEq(t, `{"output":{"commands":[{"input":"pwd","output":"/tmp"}]}}`, mustMarshalJSON(t, got))
	})

	t.Run("findLastToolCallPayload falls back to payload", func(t *testing.T) {
		toolName := "system_exec-execute"
		msgID := "m1"
		payloads := map[string]string{"p1": `{"commands":[{"input":"pwd","output":"/tmp"}]}`}
		msg := &conversation.Message{
			Id:       msgID,
			ToolName: &toolName,
		}
		msg.ToolMessage = []*agconv.ToolMessageView{
			{ToolCall: &agconv.ToolCallView{MessageId: "tm1", ResponsePayloadId: stringPtr("p1")}},
		}
		client := &backendClient{conv: newMessagesAndPayloadsClient(map[string]*conversation.Message{msgID: msg}, payloads)}
		turn := &conversation.Turn{Message: []*agconv.MessageView{{Id: msgID, ToolName: &toolName}}}
		assert.Equal(t, payloads["p1"], client.findLastToolCallPayload(context.Background(), conversation.Transcript{turn}, toolName))
	})

	t.Run("findLastToolCallPayload scans backward across turns", func(t *testing.T) {
		toolName := "system_exec-execute"
		content := `{"commands":[{"input":"pwd","output":"/tmp"}]}`
		turn1 := &conversation.Turn{Message: []*agconv.MessageView{{Role: "assistant", ToolName: &toolName}}}
		turn2 := &conversation.Turn{Message: []*agconv.MessageView{{Role: "assistant", ToolName: &toolName, Content: &content}}}
		got := (&backendClient{}).findLastToolCallPayload(context.Background(), conversation.Transcript{turn1, turn2}, toolName)
		assert.Equal(t, content, got)
	})

	t.Run("findLastToolCallPayload falls back to older turn", func(t *testing.T) {
		toolName := "system_exec-execute"
		content := `{"commands":[{"input":"pwd","output":"/tmp"}]}`
		turn1 := &conversation.Turn{Message: []*agconv.MessageView{{Role: "assistant", ToolName: &toolName, Content: &content}}}
		turn2 := &conversation.Turn{Message: []*agconv.MessageView{{Role: "assistant", ToolName: &toolName}}}
		got := (&backendClient{}).findLastToolCallPayload(context.Background(), conversation.Transcript{turn1, turn2}, toolName)
		assert.Equal(t, content, got)
	})

	t.Run("GetPayload validation errors", func(t *testing.T) {
		client := &backendClient{}
		_, err := client.GetPayload(context.Background(), "")
		require.Error(t, err)
		client.conv = newPayloadOnlyConversationClient(nil)
		_, err = client.GetPayload(context.Background(), "")
		require.Error(t, err)
		_, err = client.GetPayloads(context.Background(), []string{"p1"})
		require.NoError(t, err)
		client = &backendClient{}
		_, err = client.GetPayloads(context.Background(), []string{"p1"})
		require.Error(t, err)
	})

	t.Run("fetch helpers tolerate empty inputs", func(t *testing.T) {
		client := &backendClient{conv: newPayloadOnlyConversationClient(nil)}
		assert.Equal(t, "", client.fetchPayloadContent(context.Background(), ""))
		assert.Equal(t, "", client.fetchToolCallResponsePayload(context.Background(), ""))
		client = &backendClient{conv: errConversationClient{err: errors.New("boom")}}
		assert.Equal(t, "", client.fetchPayloadContent(context.Background(), "p1"))
	})

	t.Run("fetchToolCallResponsePayload missing branches", func(t *testing.T) {
		client := &backendClient{conv: newMessagesAndPayloadsClient(nil, nil)}
		assert.Equal(t, "", client.fetchToolCallResponsePayload(context.Background(), "missing"))
		msg := &conversation.Message{Id: "m1", ToolMessage: []*agconv.ToolMessageView{{ToolCall: &agconv.ToolCallView{MessageId: "tm1"}}}}
		client = &backendClient{conv: newMessagesAndPayloadsClient(map[string]*conversation.Message{"m1": msg}, nil)}
		assert.Equal(t, "", client.fetchToolCallResponsePayload(context.Background(), "m1"))
	})

	t.Run("ResolveFeedData invalid and missing branches", func(t *testing.T) {
		spec := &FeedSpec{
			ID:    "terminal",
			Title: "Terminal",
			Match: FeedMatch{Service: "system/exec", Method: "execute"},
		}
		client := &backendClient{}
		got, err := client.ResolveFeedData(context.Background(), spec, "conv-1")
		require.NoError(t, err)
		assert.Nil(t, got)

		toolName := "system_exec-execute"
		content := `not-json`
		turn := &conversation.Turn{
			Id:             "turn-1",
			ConversationId: "conv-1",
			Status:         "completed",
			Message: []*agconv.MessageView{
				{Id: "m1", Role: "assistant", Interim: 1, ToolName: &toolName, Content: &content},
			},
		}
		client = &backendClient{
			conv: newConversationWithPayloadsClient(&conversation.Conversation{Id: "conv-1", Transcript: []*agconv.TranscriptView{(*agconv.TranscriptView)(turn)}}, nil),
		}
		got, err = client.ResolveFeedData(context.Background(), spec, "conv-1")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("ResolveFeedData get conversation error", func(t *testing.T) {
		spec := &FeedSpec{ID: "terminal", Match: FeedMatch{Service: "system/exec", Method: "execute"}}
		client := &backendClient{conv: errConversationClient{err: errors.New("boom")}}
		_, err := client.ResolveFeedData(context.Background(), spec, "conv-1")
		require.Error(t, err)
	})

	t.Run("ResolveFeedData nil conversation result", func(t *testing.T) {
		spec := &FeedSpec{ID: "terminal", Match: FeedMatch{Service: "system/exec", Method: "execute"}}
		client := &backendClient{conv: nilConversationClient{}}
		got, err := client.ResolveFeedData(context.Background(), spec, "conv-1")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("resolveActiveFeeds empty and no-match", func(t *testing.T) {
		client := &backendClient{feeds: &FeedRegistry{specs: []*FeedSpec{{ID: "terminal", Match: FeedMatch{Service: "system/exec", Method: "execute"}}}}}
		assert.Nil(t, client.resolveActiveFeeds(context.Background(), nil))
		turn := &conversation.Turn{Message: []*agconv.MessageView{{Role: "assistant"}}}
		assert.Nil(t, client.resolveActiveFeeds(context.Background(), conversation.Transcript{turn}))
	})

	t.Run("resolveActiveFeeds skips malformed json content", func(t *testing.T) {
		toolName := "system_exec-execute"
		bad := `not-json`
		client := &backendClient{feeds: &FeedRegistry{specs: []*FeedSpec{{ID: "terminal", Title: "Terminal", Match: FeedMatch{Service: "system/exec", Method: "execute"}}}}}
		turn := &conversation.Turn{
			Message: []*agconv.MessageView{
				{
					Role:        "assistant",
					ToolName:    &toolName,
					Content:     &bad,
					ToolMessage: []*agconv.ToolMessageView{{ToolCall: &agconv.ToolCallView{ToolName: toolName}}},
				},
			},
		}
		feeds := client.resolveActiveFeeds(context.Background(), conversation.Transcript{turn})
		require.Len(t, feeds, 1)
		assert.Equal(t, 1, feeds[0].ItemCount)
		assert.Nil(t, feeds[0].Data)
	})

	t.Run("GetPayloads duplicate and nil payload branches", func(t *testing.T) {
		client := &backendClient{conv: nilPayloadConversationClient{}}
		got, err := client.GetPayloads(context.Background(), []string{"p1", "p1", ""})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("GetPayloads keeps successful payloads around errors", func(t *testing.T) {
		client := &backendClient{conv: mixedPayloadConversationClient{
			payloads: map[string]*conversation.Payload{
				"ok": {Id: "ok"},
			},
			fail: map[string]error{
				"bad": errors.New("boom"),
			},
		}}
		got, err := client.GetPayloads(context.Background(), []string{"ok", "bad"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "ok", got["ok"].Id)
	})

	t.Run("findLastToolCallPayload returns empty when nothing matches", func(t *testing.T) {
		toolName := "system_exec-execute"
		other := "resources-grepFiles"
		turn := &conversation.Turn{Message: []*agconv.MessageView{{Role: "assistant", ToolName: &other}}}
		assert.Equal(t, "", (&backendClient{}).findLastToolCallPayload(context.Background(), conversation.Transcript{turn}, toolName))
	})
}

func TestAdditionalFeedUtilityBranches(t *testing.T) {
	t.Run("registry load skips invalid yaml and derives title from id", func(t *testing.T) {
		originalRoot := workspace.Root()
		tmp := t.TempDir()
		workspace.SetRoot(tmp)
		defer workspace.SetRoot(originalRoot)
		feedsDir := filepath.Join(tmp, "feeds")
		require.NoError(t, os.MkdirAll(feedsDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(feedsDir, "bad.yaml"), []byte(":\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(feedsDir, "changes.yaml"), []byte(`
match:
  service: system/patch
  method: apply
`), 0o644))
		reg := NewFeedRegistry()
		require.Len(t, reg.Specs(), 1)
		assert.Equal(t, "Changes", reg.Specs()[0].Title)
	})

	t.Run("registry load skips directories and non-yaml", func(t *testing.T) {
		originalRoot := workspace.Root()
		tmp := t.TempDir()
		workspace.SetRoot(tmp)
		defer workspace.SetRoot(originalRoot)
		feedsDir := filepath.Join(tmp, "feeds")
		require.NoError(t, os.MkdirAll(filepath.Join(feedsDir, "subdir"), 0o755))
		require.NoError(t, os.MkdirAll(feedsDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(feedsDir, "note.txt"), []byte("ignore"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(feedsDir, "plan.yaml"), []byte(`
id: plan
match:
  service: orchestration
  method: updatePlan
ui:
  title: Plan
`), 0o644))
		reg := NewFeedRegistry()
		require.Len(t, reg.Specs(), 1)
		assert.Equal(t, "plan", reg.Specs()[0].ID)
	})

	t.Run("registry assigns file-based id when missing", func(t *testing.T) {
		originalRoot := workspace.Root()
		tmp := t.TempDir()
		workspace.SetRoot(tmp)
		defer workspace.SetRoot(originalRoot)
		feedsDir := filepath.Join(tmp, "feeds")
		require.NoError(t, os.MkdirAll(feedsDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(feedsDir, "custom.yaml"), []byte(`
match:
  service: test
  method: list
`), 0o644))
		reg := NewFeedRegistry()
		require.Len(t, reg.Specs(), 1)
		assert.Equal(t, "custom", reg.Specs()[0].ID)
	})

	t.Run("rule and parser edge branches", func(t *testing.T) {
		assert.False(t, matchesRule(FeedMatch{}, "a", "b"))
		assert.False(t, matchesRule(FeedMatch{Service: "x", Method: "y"}, "a", "b"))
		assert.True(t, matchesRule(FeedMatch{Service: "*", Method: "*"}, "a", "b"))
		svc, method := parseToolName("plain")
		assert.Equal(t, "plain", svc)
		assert.Equal(t, "*", method)
		svc, method = parseToolName("service/method")
		assert.Equal(t, "service", svc)
		assert.Equal(t, "method", method)
		svc, method = parseToolName("service:method")
		assert.Equal(t, "service", svc)
		assert.Equal(t, "method", method)
	})

	t.Run("feed payload match and count edges", func(t *testing.T) {
		svc, method := feedPayloadMatch(nil)
		assert.Equal(t, "", svc)
		assert.Equal(t, "", method)
		assert.Equal(t, 0, estimateItemCount(""))
		assert.Equal(t, 2, estimateItemCount(`[{"a":1},{"b":2}]`))
		assert.Equal(t, 1, estimateItemCount(`oops`))
		svc, method = feedPayloadMatch(&FeedSpec{Match: FeedMatch{Service: "resources", Method: "list"}, Activation: FeedActivation{Kind: "tool_call"}})
		assert.Equal(t, "resources", svc)
		assert.Equal(t, "list", method)
	})

	t.Run("emitFeed helpers no-op and fallback identity", func(t *testing.T) {
		spec := &FeedSpec{ID: "plan", Title: "Plan"}
		emitFeedActive(context.Background(), nil, "", "", spec, 1, map[string]interface{}{"x": 1})
		emitFeedInactive(context.Background(), nil, "", "plan")

		bus := streaming.NewMemoryBus(2)
		sub, err := bus.Subscribe(context.Background(), nil)
		require.NoError(t, err)
		defer sub.Close()
		ctx := memory.WithConversationID(context.Background(), "conv-emit")
		emitFeedActive(ctx, bus, "conv-emit", "", spec, 1, map[string]interface{}{"x": 1})
		ev := readEvent(t, sub)
		assert.Equal(t, "conv-emit", ev.ConversationID)
	})

	t.Run("emitFeedInactive picks turn id from context", func(t *testing.T) {
		bus := streaming.NewMemoryBus(2)
		sub, err := bus.Subscribe(context.Background(), nil)
		require.NoError(t, err)
		defer sub.Close()
		ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-inactive", TurnID: "turn-2"})
		emitFeedInactive(ctx, bus, "conv-inactive", "plan")
		ev := readEvent(t, sub)
		assert.Equal(t, "turn-2", ev.TurnID)
	})
}

func TestAdditionalGenericBridgeAndHandlerBranches(t *testing.T) {
	t.Run("generic conversion nil and empty branches", func(t *testing.T) {
		assert.Nil(t, toGenericFeedSpec(nil))
		assert.Nil(t, toGenericFeedSpec(&FeedSpec{ID: "x"}))
	})

	t.Run("generic conversion tolerates unmarshalable datasource", func(t *testing.T) {
		spec := &FeedSpec{ID: "x", DataSource: map[string]interface{}{"bad": make(chan int)}}
		out := toGenericFeedSpec(spec)
		assert.Nil(t, out)
	})

	t.Run("generic conversion skips invalid json datasource", func(t *testing.T) {
		spec := &FeedSpec{ID: "x", DataSource: map[string]interface{}{"bad": invalidJSONValue{}}}
		out := toGenericFeedSpec(spec)
		assert.Nil(t, out)
	})

	t.Run("generic conversion keeps valid datasource when mixed with invalid", func(t *testing.T) {
		spec := &FeedSpec{
			ID: "x",
			DataSource: map[string]interface{}{
				"bad":   invalidJSONValue{},
				"good":  map[string]interface{}{"source": "output"},
				"":      map[string]interface{}{"source": "input"},
				"empty": nil,
			},
		}
		out := toGenericFeedSpec(spec)
		require.NotNil(t, out)
		require.Len(t, out.DataSources, 1)
		assert.Equal(t, "output", out.DataSources["good"].Source)
	})

	t.Run("generic production bridge no-op for unknown feed", func(t *testing.T) {
		spec := &FeedSpec{
			ID: "custom",
			DataSource: map[string]interface{}{
				"root": map[string]interface{}{"source": "output"},
			},
		}
		out := toGenericFeedSpec(spec)
		require.NotNil(t, out)
		require.Len(t, out.DataSources, 1)
		assert.Empty(t, out.CountSource)
	})

	t.Run("normalizeJSON marshal failure", func(t *testing.T) {
		assert.Equal(t, "", normalizeJSON(map[string]interface{}{"bad": make(chan int)}))
	})

	t.Run("normalizeJSON pretty prints valid json", func(t *testing.T) {
		got := normalizeJSON(map[string]interface{}{"a": 1})
		assert.Contains(t, got, "\n")
		assert.Contains(t, got, `"a": 1`)
	})

}

func TestAdditionalLinkedFeedMergeBranches(t *testing.T) {
	iteration := 1
	feeds := []*ActiveFeedState{{
		FeedID:    "plan",
		ItemCount: 1,
		Data:      marshalToRawJSON(map[string]interface{}{"output": map[string]interface{}{"plan": []interface{}{map[string]interface{}{"step": "old"}}}}),
	}}
	client := &backendClient{
		conv: newConversationWithPayloadsClient(&conversation.Conversation{
			Id: "child-conv",
			Transcript: []*agconv.TranscriptView{
				{
					Id:             "turn-1",
					ConversationId: "child-conv",
					Status:         "completed",
					Message: []*agconv.MessageView{
						{
							Id:        "m1",
							Role:      "assistant",
							Interim:   1,
							Iteration: &iteration,
							ModelCall: &agconv.ModelCallView{
								MessageId: "m1",
								Status:    "completed",
							},
							ToolMessage: []*agconv.ToolMessageView{
								{
									Id:        "tm1",
									Iteration: &iteration,
									ToolCall: &agconv.ToolCallView{
										MessageId:         "tm1",
										ToolName:          "orchestration:updatePlan",
										Status:            "completed",
										ResponsePayloadId: stringPtr("p1"),
									},
								},
							},
						},
					},
				},
			},
		}, map[string]string{"p1": `{"explanation":"Ship it","plan":[{"status":"completed","step":"Write tests"},{"status":"pending","step":"Review PR"}]}`}),
		feeds: &FeedRegistry{specs: []*FeedSpec{loadBuiltinFeedSpec(t, "plan")}},
	}
	state := &ConversationState{
		ConversationID: "parent",
		Turns: []*TurnState{{
			TurnID: "turn-1",
			LinkedConversations: []*LinkedConversationState{
				{ConversationID: "child-conv"},
			},
		}},
	}
	merged := client.mergeLinkedConversationFeeds(context.Background(), feeds, state, map[string]struct{}{})
	require.Len(t, merged, 1)
	assert.Equal(t, 2, merged[0].ItemCount)

	none := client.resolveLinkedChildStates(context.Background(), &ConversationState{}, map[string]struct{}{})
	assert.Empty(t, none)

	visited := map[string]struct{}{"child-conv": {}}
	none = client.resolveLinkedChildStates(context.Background(), state, visited)
	assert.Empty(t, none)

	merged = client.mergeLinkedConversationFeeds(context.Background(), []*ActiveFeedState{{
		FeedID:    "plan",
		ItemCount: 5,
		Data:      marshalToRawJSON(map[string]interface{}{"output": map[string]interface{}{"plan": []interface{}{map[string]interface{}{"step": "existing"}}}}),
	}}, state, map[string]struct{}{})
	require.Len(t, merged, 1)
	assert.Equal(t, 5, merged[0].ItemCount)

	assert.Nil(t, client.mergeLinkedConversationFeeds(context.Background(), nil, nil, map[string]struct{}{}))
	assert.Empty(t, client.mergeLinkedConversationFeeds(context.Background(), nil, &ConversationState{}, map[string]struct{}{}))

	badClient := &backendClient{
		conv:  errConversationClient{err: errors.New("boom")},
		feeds: client.feeds,
	}
	assert.Empty(t, badClient.resolveLinkedChildStates(context.Background(), state, map[string]struct{}{}))

	noFeedClient := &backendClient{
		conv:  newConversationWithPayloadsClient(&conversation.Conversation{Id: "child-conv"}, nil),
		feeds: client.feeds,
	}
	merged = noFeedClient.mergeLinkedConversationFeeds(context.Background(), nil, state, map[string]struct{}{})
	assert.Empty(t, merged)
}

func TestResolveActiveFeedsWithVisited_ScopeAllAndSkipBranches(t *testing.T) {
	spec := loadBuiltinFeedSpec(t, "terminal")
	spec.Activation.Kind = "history"
	spec.Activation.Scope = "all"
	client := &backendClient{
		conv: newPayloadOnlyConversationClient(map[string]string{
			"p1": `{"commands":[{"input":"pwd","output":"/tmp"}]}`,
			"p2": `{"commands":[{"input":"ls","output":"a\nb"}]}`,
		}),
		feeds: &FeedRegistry{specs: []*FeedSpec{spec}},
	}
	state := &ConversationState{
		ConversationID: "conv-all",
		Turns: []*TurnState{
			{
				TurnID: "turn-1",
				Execution: &ExecutionState{Pages: []*ExecutionPageState{{
					ToolSteps: []*ToolStepState{
						{ToolName: "system_exec-execute", ResponsePayloadID: "p1"},
					},
				}}},
			},
			{
				TurnID: "turn-2",
				Execution: &ExecutionState{Pages: []*ExecutionPageState{{
					ToolSteps: []*ToolStepState{
						{ToolName: "system_exec-execute", ResponsePayloadID: "p2"},
						{ToolName: "resources-grepFiles"},
					},
				}}},
			},
		},
	}
	feeds := client.resolveActiveFeedsWithVisited(context.Background(), state, map[string]struct{}{})
	require.Len(t, feeds, 1)
	assert.Equal(t, 2, feeds[0].ItemCount)
	assert.JSONEq(t, `{"input":{},"output":{"commands":[{"input":"pwd","output":"/tmp"},{"input":"ls","output":"a\nb"}]}}`, string(feeds[0].Data))

	client = &backendClient{feeds: &FeedRegistry{specs: []*FeedSpec{spec}}}
	assert.Nil(t, client.resolveActiveFeedsWithVisited(context.Background(), nil, map[string]struct{}{}))
	assert.Nil(t, client.resolveActiveFeedsWithVisited(context.Background(), &ConversationState{}, map[string]struct{}{}))

	client = &backendClient{feeds: &FeedRegistry{specs: []*FeedSpec{nil, {}}}}
	stateSkip := &ConversationState{
		ConversationID: "conv-skip",
		Turns: []*TurnState{{
			TurnID: "turn-1",
			Execution: &ExecutionState{Pages: []*ExecutionPageState{{
				ToolSteps: []*ToolStepState{{ToolName: "system_exec-execute", ResponsePayloadID: "p1"}},
			}}},
		}},
	}
	assert.Nil(t, client.resolveActiveFeedsWithVisited(context.Background(), stateSkip, map[string]struct{}{}))

	spec.Activation.Scope = ""
	client = &backendClient{
		conv: newPayloadOnlyConversationClient(map[string]string{
			"p1": `{"commands":[{"input":"pwd","output":"/tmp"}]}`,
			"p2": `{"commands":[{"input":"ls","output":"a\nb"}]}`,
		}),
		feeds: &FeedRegistry{specs: []*FeedSpec{spec}},
	}
	feeds = client.resolveActiveFeedsWithVisited(context.Background(), state, map[string]struct{}{})
	require.Len(t, feeds, 1)
	assert.Equal(t, 1, feeds[0].ItemCount)
	assert.JSONEq(t, `{"input":{},"output":{"commands":[{"input":"ls","output":"a\nb"}]}}`, string(feeds[0].Data))
}

func TestFeedRegistryGetterAndEmptyHandlers(t *testing.T) {
	client := &backendClient{}
	assert.Nil(t, client.FeedRegistry())

	req := httptest.NewRequest("GET", "/v1/feeds", nil)
	rec := httptest.NewRecorder()
	handleListFeeds(client).ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"feeds":[]}`, rec.Body.String())
}

func TestHandleGetFeedData_ErrorBranches(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/feeds//data", nil)
	req.SetPathValue("id", "")
	rec := httptest.NewRecorder()
	handleGetFeedData(&backendClient{}).ServeHTTP(rec, req)
	require.Equal(t, 400, rec.Code)

	req = httptest.NewRequest("GET", "/v1/feeds/terminal/data", nil)
	req.SetPathValue("id", "terminal")
	rec = httptest.NewRecorder()
	handleGetFeedData(&backendClient{}).ServeHTTP(rec, req)
	require.Equal(t, 404, rec.Code)

	req = httptest.NewRequest("GET", "/v1/feeds/missing/data", nil)
	req.SetPathValue("id", "missing")
	rec = httptest.NewRecorder()
	handleGetFeedData(&backendClient{feeds: &FeedRegistry{specs: []*FeedSpec{loadBuiltinFeedSpec(t, "terminal")}}}).ServeHTTP(rec, req)
	require.Equal(t, 404, rec.Code)

	req = httptest.NewRequest("GET", "/v1/feeds/terminal/data?conversationId=conv-none", nil)
	req.SetPathValue("id", "terminal")
	rec = httptest.NewRecorder()
	handleGetFeedData(&backendClient{
		conv:  nilConversationClient{},
		feeds: &FeedRegistry{specs: []*FeedSpec{loadBuiltinFeedSpec(t, "terminal")}},
	}).ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":null`)
}

func TestLinkedChildFeedHelpers(t *testing.T) {
	iteration := 1
	childConv := &conversation.Conversation{
		Id: "child-conv",
		Transcript: []*agconv.TranscriptView{
			{
				Id:             "child-turn",
				ConversationId: "child-conv",
				Status:         "completed",
				CreatedAt:      time.Now(),
				Message: []*agconv.MessageView{
					{
						Id:        "m1",
						Role:      "assistant",
						Interim:   1,
						Iteration: &iteration,
						ModelCall: &agconv.ModelCallView{MessageId: "m1", Status: "completed"},
						ToolMessage: []*agconv.ToolMessageView{
							{
								Id:              "tm1",
								ParentMessageId: stringPtr("m1"),
								Sequence:        intPtr(1),
								Iteration:       &iteration,
								ToolCall: &agconv.ToolCallView{
									MessageId:         "tm1",
									ToolName:          "orchestration:updatePlan",
									Status:            "completed",
									ResponsePayloadId: stringPtr("p1"),
								},
							},
						},
					},
				},
			},
		},
	}
	client := &backendClient{
		conv:  newConversationWithPayloadsClient(childConv, map[string]string{"p1": `{"explanation":"Ship it","plan":[{"status":"completed","step":"Write tests"},{"status":"pending","step":"Review PR"}]}`}),
		feeds: &FeedRegistry{specs: []*FeedSpec{loadBuiltinFeedSpec(t, "plan")}},
	}
	parentState := &ConversationState{
		ConversationID: "parent-conv",
		Turns: []*TurnState{{
			TurnID: "parent-turn",
			LinkedConversations: []*LinkedConversationState{
				{ConversationID: "child-conv"},
			},
		}},
	}

	childStates := client.resolveLinkedChildStates(context.Background(), parentState, map[string]struct{}{})
	require.Len(t, childStates, 1)
	require.NotNil(t, childStates["child-conv"])

	merged := client.mergeLinkedConversationFeeds(context.Background(), nil, parentState, map[string]struct{}{})
	require.Len(t, merged, 1)
	assert.Equal(t, "plan", merged[0].FeedID)
	assert.Equal(t, 2, merged[0].ItemCount)
}

func TestGenericBridgeHelpers(t *testing.T) {
	spec := &FeedSpec{
		ID: "plan",
		DataSource: map[string]interface{}{
			"planInfo": map[string]interface{}{
				"source": "output",
			},
			"planDetail": map[string]interface{}{
				"dataSourceRef": "planInfo",
				"selectors": map[string]interface{}{
					"data": "plan",
				},
			},
		},
	}
	out := toGenericFeedSpec(spec)
	require.NotNil(t, out)
	require.Len(t, out.DataSources, 2)
	assert.Equal(t, "output", out.DataSources["planInfo"].Source)
	assert.Equal(t, "planInfo", out.DataSources["planDetail"].DataSourceRef)

	assert.JSONEq(t, `{"a":1}`, normalizeJSON(map[string]interface{}{"a": 1}))
}

func loadBuiltinFeedSpec(t *testing.T, name string) *FeedSpec {
	t.Helper()
	path := filepath.Join("..", "internal", "tool", "registry", ".agently", "feeds", name+".yaml")
	var spec FeedSpec
	require.NoError(t, wscodec.DecodeFile(path, &spec))
	if spec.ID == "" {
		spec.ID = name
	}
	if spec.Title == "" {
		if ui, ok := spec.UI.(map[string]interface{}); ok {
			if title, ok := ui["title"].(string); ok && title != "" {
				spec.Title = title
			}
		}
		if spec.Title == "" {
			spec.Title = name
		}
	}
	return &spec
}

func readEvent(t *testing.T, sub streaming.Subscription) *streaming.Event {
	t.Helper()
	select {
	case ev := <-sub.C():
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streaming event")
		return nil
	}
}

func assertNoEvent(t *testing.T, sub streaming.Subscription) {
	t.Helper()
	select {
	case ev := <-sub.C():
		t.Fatalf("unexpected event: %#v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func mustMarshalJSON(t *testing.T, value interface{}) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

type payloadOnlyConversationClient struct {
	payloads map[string]*conversation.Payload
}

func newPayloadOnlyConversationClient(raw map[string]string) *payloadOnlyConversationClient {
	payloads := make(map[string]*conversation.Payload, len(raw))
	for id, body := range raw {
		b := []byte(body)
		payloads[id] = &conversation.Payload{
			Id:         id,
			Kind:       "tool_response",
			MimeType:   "application/json",
			Storage:    "inline",
			InlineBody: &b,
		}
	}
	return &payloadOnlyConversationClient{payloads: payloads}
}

func (p *payloadOnlyConversationClient) GetConversation(context.Context, string, ...conversation.Option) (*conversation.Conversation, error) {
	return nil, errors.New("unexpected GetConversation")
}
func (p *payloadOnlyConversationClient) GetConversations(context.Context, *conversation.Input) ([]*conversation.Conversation, error) {
	return nil, errors.New("unexpected GetConversations")
}
func (p *payloadOnlyConversationClient) PatchConversations(context.Context, *conversation.MutableConversation) error {
	return errors.New("unexpected PatchConversations")
}
func (p *payloadOnlyConversationClient) GetPayload(_ context.Context, id string) (*conversation.Payload, error) {
	payload, ok := p.payloads[id]
	if !ok {
		return nil, errors.New("payload not found")
	}
	return payload, nil
}
func (p *payloadOnlyConversationClient) PatchPayload(context.Context, *conversation.MutablePayload) error {
	return errors.New("unexpected PatchPayload")
}
func (p *payloadOnlyConversationClient) PatchMessage(context.Context, *conversation.MutableMessage) error {
	return errors.New("unexpected PatchMessage")
}
func (p *payloadOnlyConversationClient) GetMessage(context.Context, string, ...conversation.Option) (*conversation.Message, error) {
	return nil, errors.New("unexpected GetMessage")
}
func (p *payloadOnlyConversationClient) GetMessageByElicitation(context.Context, string, string) (*conversation.Message, error) {
	return nil, errors.New("unexpected GetMessageByElicitation")
}
func (p *payloadOnlyConversationClient) PatchModelCall(context.Context, *conversation.MutableModelCall) error {
	return errors.New("unexpected PatchModelCall")
}
func (p *payloadOnlyConversationClient) PatchToolCall(context.Context, *conversation.MutableToolCall) error {
	return errors.New("unexpected PatchToolCall")
}
func (p *payloadOnlyConversationClient) PatchTurn(context.Context, *conversation.MutableTurn) error {
	return errors.New("unexpected PatchTurn")
}
func (p *payloadOnlyConversationClient) DeleteConversation(context.Context, string) error {
	return errors.New("unexpected DeleteConversation")
}
func (p *payloadOnlyConversationClient) DeleteMessage(context.Context, string, string) error {
	return errors.New("unexpected DeleteMessage")
}

type conversationWithPayloadsClient struct {
	*payloadOnlyConversationClient
	conversation *conversation.Conversation
}

func newConversationWithPayloadsClient(conv *conversation.Conversation, raw map[string]string) *conversationWithPayloadsClient {
	return &conversationWithPayloadsClient{
		payloadOnlyConversationClient: newPayloadOnlyConversationClient(raw),
		conversation:                  conv,
	}
}

func (c *conversationWithPayloadsClient) GetConversation(_ context.Context, id string, _ ...conversation.Option) (*conversation.Conversation, error) {
	if c.conversation == nil || c.conversation.Id != id {
		return nil, errors.New("conversation not found")
	}
	return c.conversation, nil
}

type messagesAndPayloadsClient struct {
	*payloadOnlyConversationClient
	messages map[string]*conversation.Message
}

func newMessagesAndPayloadsClient(messages map[string]*conversation.Message, raw map[string]string) *messagesAndPayloadsClient {
	return &messagesAndPayloadsClient{
		payloadOnlyConversationClient: newPayloadOnlyConversationClient(raw),
		messages:                      messages,
	}
}

func (c *messagesAndPayloadsClient) GetConversation(context.Context, string, ...conversation.Option) (*conversation.Conversation, error) {
	return nil, errors.New("unexpected GetConversation")
}

func (c *messagesAndPayloadsClient) GetMessage(_ context.Context, id string, _ ...conversation.Option) (*conversation.Message, error) {
	msg, ok := c.messages[id]
	if !ok {
		return nil, errors.New("message not found")
	}
	return msg, nil
}

func (c *messagesAndPayloadsClient) PatchPayload(_ context.Context, payload *conversation.MutablePayload) error {
	if c.payloads == nil {
		c.payloads = map[string]*conversation.Payload{}
	}
	body := payload.InlineBody
	c.payloads[payload.Id] = &conversation.Payload{
		Id:         payload.Id,
		Kind:       payload.Kind,
		MimeType:   payload.MimeType,
		Storage:    payload.Storage,
		InlineBody: body,
	}
	return nil
}

func (c *messagesAndPayloadsClient) PatchConversations(context.Context, *conversation.MutableConversation) error {
	return nil
}

func (c *messagesAndPayloadsClient) PatchMessage(_ context.Context, message *conversation.MutableMessage) error {
	if c.messages == nil {
		c.messages = map[string]*conversation.Message{}
	}
	id := message.Id
	if id == "" {
		id = "msg"
	}
	c.messages[id] = &conversation.Message{Id: id, Content: message.Content}
	return nil
}

type errConversationClient struct{ err error }

func (e errConversationClient) GetConversation(context.Context, string, ...conversation.Option) (*conversation.Conversation, error) {
	return nil, e.err
}
func (e errConversationClient) GetConversations(context.Context, *conversation.Input) ([]*conversation.Conversation, error) {
	return nil, e.err
}
func (e errConversationClient) PatchConversations(context.Context, *conversation.MutableConversation) error {
	return e.err
}
func (e errConversationClient) GetPayload(context.Context, string) (*conversation.Payload, error) {
	return nil, e.err
}
func (e errConversationClient) PatchPayload(context.Context, *conversation.MutablePayload) error {
	return e.err
}
func (e errConversationClient) PatchMessage(context.Context, *conversation.MutableMessage) error {
	return e.err
}
func (e errConversationClient) GetMessage(context.Context, string, ...conversation.Option) (*conversation.Message, error) {
	return nil, e.err
}
func (e errConversationClient) GetMessageByElicitation(context.Context, string, string) (*conversation.Message, error) {
	return nil, e.err
}
func (e errConversationClient) PatchModelCall(context.Context, *conversation.MutableModelCall) error {
	return e.err
}
func (e errConversationClient) PatchToolCall(context.Context, *conversation.MutableToolCall) error {
	return e.err
}
func (e errConversationClient) PatchTurn(context.Context, *conversation.MutableTurn) error {
	return e.err
}
func (e errConversationClient) DeleteConversation(context.Context, string) error { return e.err }
func (e errConversationClient) DeleteMessage(context.Context, string, string) error {
	return e.err
}

type nilPayloadConversationClient struct{}

func (n nilPayloadConversationClient) GetConversation(context.Context, string, ...conversation.Option) (*conversation.Conversation, error) {
	return nil, errors.New("unexpected GetConversation")
}
func (n nilPayloadConversationClient) GetConversations(context.Context, *conversation.Input) ([]*conversation.Conversation, error) {
	return nil, errors.New("unexpected GetConversations")
}
func (n nilPayloadConversationClient) PatchConversations(context.Context, *conversation.MutableConversation) error {
	return nil
}
func (n nilPayloadConversationClient) GetPayload(context.Context, string) (*conversation.Payload, error) {
	return nil, nil
}
func (n nilPayloadConversationClient) PatchPayload(context.Context, *conversation.MutablePayload) error {
	return nil
}
func (n nilPayloadConversationClient) PatchMessage(context.Context, *conversation.MutableMessage) error {
	return nil
}
func (n nilPayloadConversationClient) GetMessage(context.Context, string, ...conversation.Option) (*conversation.Message, error) {
	return nil, nil
}
func (n nilPayloadConversationClient) GetMessageByElicitation(context.Context, string, string) (*conversation.Message, error) {
	return nil, nil
}
func (n nilPayloadConversationClient) PatchModelCall(context.Context, *conversation.MutableModelCall) error {
	return nil
}
func (n nilPayloadConversationClient) PatchToolCall(context.Context, *conversation.MutableToolCall) error {
	return nil
}
func (n nilPayloadConversationClient) PatchTurn(context.Context, *conversation.MutableTurn) error {
	return nil
}
func (n nilPayloadConversationClient) DeleteConversation(context.Context, string) error { return nil }
func (n nilPayloadConversationClient) DeleteMessage(context.Context, string, string) error {
	return nil
}

type nilConversationClient struct{}

func (n nilConversationClient) GetConversation(context.Context, string, ...conversation.Option) (*conversation.Conversation, error) {
	return nil, nil
}
func (n nilConversationClient) GetConversations(context.Context, *conversation.Input) ([]*conversation.Conversation, error) {
	return nil, nil
}
func (n nilConversationClient) PatchConversations(context.Context, *conversation.MutableConversation) error {
	return nil
}
func (n nilConversationClient) GetPayload(context.Context, string) (*conversation.Payload, error) {
	return nil, nil
}
func (n nilConversationClient) PatchPayload(context.Context, *conversation.MutablePayload) error {
	return nil
}
func (n nilConversationClient) PatchMessage(context.Context, *conversation.MutableMessage) error {
	return nil
}
func (n nilConversationClient) GetMessage(context.Context, string, ...conversation.Option) (*conversation.Message, error) {
	return nil, nil
}
func (n nilConversationClient) GetMessageByElicitation(context.Context, string, string) (*conversation.Message, error) {
	return nil, nil
}
func (n nilConversationClient) PatchModelCall(context.Context, *conversation.MutableModelCall) error {
	return nil
}
func (n nilConversationClient) PatchToolCall(context.Context, *conversation.MutableToolCall) error {
	return nil
}
func (n nilConversationClient) PatchTurn(context.Context, *conversation.MutableTurn) error {
	return nil
}
func (n nilConversationClient) DeleteConversation(context.Context, string) error    { return nil }
func (n nilConversationClient) DeleteMessage(context.Context, string, string) error { return nil }

type mixedPayloadConversationClient struct {
	payloads map[string]*conversation.Payload
	fail     map[string]error
}

func (m mixedPayloadConversationClient) GetConversation(context.Context, string, ...conversation.Option) (*conversation.Conversation, error) {
	return nil, errors.New("unexpected GetConversation")
}
func (m mixedPayloadConversationClient) GetConversations(context.Context, *conversation.Input) ([]*conversation.Conversation, error) {
	return nil, errors.New("unexpected GetConversations")
}
func (m mixedPayloadConversationClient) PatchConversations(context.Context, *conversation.MutableConversation) error {
	return nil
}
func (m mixedPayloadConversationClient) GetPayload(_ context.Context, id string) (*conversation.Payload, error) {
	if err, ok := m.fail[id]; ok {
		return nil, err
	}
	return m.payloads[id], nil
}
func (m mixedPayloadConversationClient) PatchPayload(context.Context, *conversation.MutablePayload) error {
	return nil
}
func (m mixedPayloadConversationClient) PatchMessage(context.Context, *conversation.MutableMessage) error {
	return nil
}
func (m mixedPayloadConversationClient) GetMessage(context.Context, string, ...conversation.Option) (*conversation.Message, error) {
	return nil, nil
}
func (m mixedPayloadConversationClient) GetMessageByElicitation(context.Context, string, string) (*conversation.Message, error) {
	return nil, nil
}
func (m mixedPayloadConversationClient) PatchModelCall(context.Context, *conversation.MutableModelCall) error {
	return nil
}
func (m mixedPayloadConversationClient) PatchToolCall(context.Context, *conversation.MutableToolCall) error {
	return nil
}
func (m mixedPayloadConversationClient) PatchTurn(context.Context, *conversation.MutableTurn) error {
	return nil
}
func (m mixedPayloadConversationClient) DeleteConversation(context.Context, string) error { return nil }
func (m mixedPayloadConversationClient) DeleteMessage(context.Context, string, string) error {
	return nil
}

type invalidJSONValue struct{}

func (invalidJSONValue) MarshalJSON() ([]byte, error) { return []byte("{"), nil }

func stringPtr(value string) *string { return &value }
