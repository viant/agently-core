package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/viant/agently-core/internal/testutil/dbtest"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agconvlist "github.com/viant/agently-core/pkg/agently/conversation/list"
	agconvwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	agmessage "github.com/viant/agently-core/pkg/agently/message"
	agmessagelist "github.com/viant/agently-core/pkg/agently/message/list"
	agmessagewrite "github.com/viant/agently-core/pkg/agently/message/write"
	agmodelcallwrite "github.com/viant/agently-core/pkg/agently/modelcall/write"
	agpayload "github.com/viant/agently-core/pkg/agently/payload"
	agpayloadwrite "github.com/viant/agently-core/pkg/agently/payload/write"
	agrun "github.com/viant/agently-core/pkg/agently/run"
	agrunactive "github.com/viant/agently-core/pkg/agently/run/active"
	agrunstale "github.com/viant/agently-core/pkg/agently/run/stale"
	agrunsteps "github.com/viant/agently-core/pkg/agently/run/steps"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	agtoolcall "github.com/viant/agently-core/pkg/agently/toolcall/byOp"
	agtoolcallwrite "github.com/viant/agently-core/pkg/agently/toolcall/write"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturnbyid "github.com/viant/agently-core/pkg/agently/turn/byId"
	agturnlistall "github.com/viant/agently-core/pkg/agently/turn/list"
	agturnnext "github.com/viant/agently-core/pkg/agently/turn/nextQueued"
	agturncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
	agturnlist "github.com/viant/agently-core/pkg/agently/turn/queuedList"
	agturnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/datly/view"
	hstate "github.com/viant/xdatly/handler/state"
	_ "modernc.org/sqlite"
)

func TestDataService_ConversationPredicates(t *testing.T) {
	svc := newSeededService(t, seedForConversationPredicates)
	ctx := context.Background()

	cases := []struct {
		name    string
		id      string
		input   *agconv.ConversationInput
		wantNil bool
	}{
		{
			name: "id match",
			id:   "c-main",
		},
		{
			name:    "id mismatch",
			id:      "c-missing",
			wantNil: true,
		},
		{
			name: "agent id match",
			id:   "c-main",
			input: &agconv.ConversationInput{
				AgentId: "agent-1",
				Has:     &agconv.ConversationInputHas{AgentId: true},
			},
		},
		{
			name: "agent id mismatch",
			id:   "c-main",
			input: &agconv.ConversationInput{
				AgentId: "agent-x",
				Has:     &agconv.ConversationInputHas{AgentId: true},
			},
			wantNil: true,
		},
		{
			name: "parent id match",
			id:   "c-main",
			input: &agconv.ConversationInput{
				ParentId: "parent-conv-1",
				Has:      &agconv.ConversationInputHas{ParentId: true},
			},
		},
		{
			name: "parent id mismatch",
			id:   "c-main",
			input: &agconv.ConversationInput{
				ParentId: "parent-conv-x",
				Has:      &agconv.ConversationInputHas{ParentId: true},
			},
			wantNil: true,
		},
		{
			name: "parent turn id match",
			id:   "c-main",
			input: &agconv.ConversationInput{
				ParentTurnId: "parent-turn-1",
				Has:          &agconv.ConversationInputHas{ParentTurnId: true},
			},
		},
		{
			name: "parent turn id mismatch",
			id:   "c-main",
			input: &agconv.ConversationInput{
				ParentTurnId: "parent-turn-x",
				Has:          &agconv.ConversationInputHas{ParentTurnId: true},
			},
			wantNil: true,
		},
		{
			name: "schedule id match",
			id:   "c-main",
			input: &agconv.ConversationInput{
				ScheduleId: "sch-1",
				Has:        &agconv.ConversationInputHas{ScheduleId: true},
			},
		},
		{
			name: "schedule id mismatch",
			id:   "c-main",
			input: &agconv.ConversationInput{
				ScheduleId: "sch-x",
				Has:        &agconv.ConversationInputHas{ScheduleId: true},
			},
			wantNil: true,
		},
		{
			name: "schedule run id match",
			id:   "c-main",
			input: &agconv.ConversationInput{
				ScheduleRunId: "sch-run-1",
				Has:           &agconv.ConversationInputHas{ScheduleRunId: true},
			},
		},
		{
			name: "schedule run id mismatch",
			id:   "c-main",
			input: &agconv.ConversationInput{
				ScheduleRunId: "sch-run-x",
				Has:           &agconv.ConversationInputHas{ScheduleRunId: true},
			},
			wantNil: true,
		},
		{
			name: "has schedule id true on scheduled conversation",
			id:   "c-main",
			input: &agconv.ConversationInput{
				HasScheduleId: true,
				Has:           &agconv.ConversationInputHas{HasScheduleId: true},
			},
		},
		{
			name: "has schedule id true on unscheduled conversation",
			id:   "c-other",
			input: &agconv.ConversationInput{
				HasScheduleId: true,
				Has:           &agconv.ConversationInputHas{HasScheduleId: true},
			},
			wantNil: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.GetConversation(ctx, tc.id, tc.input)
			if err != nil {
				t.Fatalf("GetConversation() error: %v", err)
			}
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil conversation, got id=%s", got.Id)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected conversation, got nil")
			}
			if got.Id != tc.id {
				t.Fatalf("unexpected conversation id: got %s, want %s", got.Id, tc.id)
			}
		})
	}

	t.Run("since filter narrows transcript", func(t *testing.T) {
		in := &agconv.ConversationInput{
			Since: "t-anchor",
			Has: &agconv.ConversationInputHas{
				Since: true,
			},
		}
		got, err := svc.GetConversation(ctx, "c-main", in)
		if err != nil {
			t.Fatalf("GetConversation() error: %v", err)
		}
		if got == nil {
			t.Fatalf("expected conversation")
		}
		ids := transcriptIDs(got.Transcript)
		assertIDs(t, ids, []string{"t-anchor", "t-run-main", "t-wait-main", "t-done-main"})
	})

	t.Run("include transcript false", func(t *testing.T) {
		in := &agconv.ConversationInput{
			IncludeTranscript: false,
			Has:               &agconv.ConversationInputHas{IncludeTranscript: true},
		}
		got, err := svc.GetConversation(ctx, "c-main", in)
		if err != nil {
			t.Fatalf("GetConversation() error: %v", err)
		}
		if got == nil {
			t.Fatalf("expected conversation")
		}
		if len(got.Transcript) != 0 {
			t.Fatalf("expected empty transcript when includeTranscript=false, got %d turns", len(got.Transcript))
		}
	})

	t.Run("include transcript true", func(t *testing.T) {
		in := &agconv.ConversationInput{
			IncludeTranscript: true,
			Has:               &agconv.ConversationInputHas{IncludeTranscript: true},
		}
		got, err := svc.GetConversation(ctx, "c-main", in)
		if err != nil {
			t.Fatalf("GetConversation() error: %v", err)
		}
		if got == nil {
			t.Fatalf("expected conversation")
		}
		if len(got.Transcript) == 0 {
			t.Fatalf("expected transcript when includeTranscript=true")
		}
	})

	t.Run("include model call false", func(t *testing.T) {
		in := &agconv.ConversationInput{
			IncludeTranscript: true,
			IncludeModelCal:   false,
			Has: &agconv.ConversationInputHas{
				IncludeTranscript: true,
				IncludeModelCal:   true,
			},
		}
		got, err := svc.GetConversation(ctx, "c-main", in)
		if err != nil {
			t.Fatalf("GetConversation() error: %v", err)
		}
		msg := findMessage(got.Transcript, "m-main")
		if msg == nil {
			t.Fatalf("expected message m-main in transcript")
		}
		if msg.ModelCall != nil {
			t.Fatalf("expected nil model call when includeModelCall=false")
		}
	})

	t.Run("include model call true", func(t *testing.T) {
		in := &agconv.ConversationInput{
			IncludeTranscript: true,
			IncludeModelCal:   true,
			Has: &agconv.ConversationInputHas{
				IncludeTranscript: true,
				IncludeModelCal:   true,
			},
		}
		got, err := svc.GetConversation(ctx, "c-main", in)
		if err != nil {
			t.Fatalf("GetConversation() error: %v", err)
		}
		msg := findMessage(got.Transcript, "m-main")
		if msg == nil {
			t.Fatalf("expected message m-main in transcript")
		}
		if msg.ModelCall == nil {
			t.Fatalf("expected model call when includeModelCall=true")
		}
	})

	t.Run("include tool call false", func(t *testing.T) {
		in := &agconv.ConversationInput{
			IncludeTranscript: true,
			IncludeToolCall:   false,
			Has: &agconv.ConversationInputHas{
				IncludeTranscript: true,
				IncludeToolCall:   true,
			},
		}
		got, err := svc.GetConversation(ctx, "c-main", in)
		if err != nil {
			t.Fatalf("GetConversation() error: %v", err)
		}
		msg := findMessage(got.Transcript, "m-main")
		if msg == nil {
			t.Fatalf("expected message m-main in transcript")
		}
		if len(msg.ToolMessage) == 0 {
			t.Fatalf("expected grouped tool messages even when includeToolCall=false")
		}
		if hasAnyToolCall(msg) {
			t.Fatalf("expected nil tool call when includeToolCall=false")
		}
	})

	t.Run("include tool call true", func(t *testing.T) {
		in := &agconv.ConversationInput{
			IncludeTranscript: true,
			IncludeToolCall:   true,
			Has: &agconv.ConversationInputHas{
				IncludeTranscript: true,
				IncludeToolCall:   true,
			},
		}
		got, err := svc.GetConversation(ctx, "c-main", in)
		if err != nil {
			t.Fatalf("GetConversation() error: %v", err)
		}
		msg := findMessage(got.Transcript, "m-main")
		if msg == nil {
			t.Fatalf("expected message m-main in transcript")
		}
		if len(msg.ToolMessage) == 0 {
			t.Fatalf("expected grouped tool messages")
		}
		if !hasAnyToolCall(msg) {
			t.Fatalf("expected tool call when includeToolCall=true")
		}
		ids := toolMessageIDs(msg.ToolMessage)
		assertIDs(t, ids, []string{"m-main-tool-1"})
	})

	t.Run("orphan tool message is not attached to assistant parent", func(t *testing.T) {
		in := &agconv.ConversationInput{
			IncludeTranscript: true,
			IncludeToolCall:   true,
			Has: &agconv.ConversationInputHas{
				IncludeTranscript: true,
				IncludeToolCall:   true,
			},
		}
		got, err := svc.GetConversation(ctx, "c-main", in)
		if err != nil {
			t.Fatalf("GetConversation() error: %v", err)
		}
		msg := findMessage(got.Transcript, "m-main")
		if msg == nil {
			t.Fatalf("expected message m-main in transcript")
		}
		ids := toolMessageIDs(msg.ToolMessage)
		for _, id := range ids {
			if id == "m-orphan-tool" {
				t.Fatalf("unexpected orphan tool message attached to parent")
			}
		}
	})

	t.Run("json shape includes toolMessage and nested toolCall", func(t *testing.T) {
		in := &agconv.ConversationInput{
			IncludeTranscript: true,
			IncludeToolCall:   true,
			Has: &agconv.ConversationInputHas{
				IncludeTranscript: true,
				IncludeToolCall:   true,
			},
		}
		got, err := svc.GetConversation(ctx, "c-main", in)
		if err != nil {
			t.Fatalf("GetConversation() error: %v", err)
		}
		msg := findMessage(got.Transcript, "m-main")
		if msg == nil {
			t.Fatalf("expected message m-main in transcript")
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("Marshal() error: %v", err)
		}
		var doc map[string]interface{}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("Unmarshal() error: %v", err)
		}
		toolMessages, ok := doc["ToolMessage"].([]interface{})
		if !ok || len(toolMessages) == 0 {
			t.Fatalf("expected toolMessage array in json shape")
		}
		tm, ok := toolMessages[0].(map[string]interface{})
		if !ok {
			t.Fatalf("expected first toolMessage object")
		}
		if _, ok := tm["ToolCall"].(map[string]interface{}); !ok {
			t.Fatalf("expected nested toolCall object in json shape")
		}
	})
}

func TestDataService_MessageAndElicitation(t *testing.T) {
	svc := newSeededService(t, seedForMessageAndElicitation)
	ctx := context.Background()

	msg, err := svc.GetMessage(ctx, "m-main", &agmessage.MessageInput{
		Has: &agmessage.MessageInputHas{Id: true},
	})
	if err != nil {
		t.Fatalf("GetMessage() error: %v", err)
	}
	if msg == nil || msg.Id != "m-main" {
		t.Fatalf("expected m-main, got %#v", msg)
	}

	none, err := svc.GetMessage(ctx, "m-missing", nil)
	if err != nil {
		t.Fatalf("GetMessage() error: %v", err)
	}
	if none != nil {
		t.Fatalf("expected nil for missing message id")
	}

	elic, err := svc.GetMessageByElicitation(ctx, "c-main", "elic-1")
	if err != nil {
		t.Fatalf("GetMessageByElicitation() error: %v", err)
	}
	if elic == nil || elic.Id != "m-main" {
		t.Fatalf("expected elicitation message m-main, got %#v", elic)
	}

	elicNone, err := svc.GetMessageByElicitation(ctx, "c-main", "elic-missing")
	if err != nil {
		t.Fatalf("GetMessageByElicitation() error: %v", err)
	}
	if elicNone != nil {
		t.Fatalf("expected nil for missing elicitation")
	}
}

func TestDataService_RunPredicates(t *testing.T) {
	svc := newSeededService(t, seedForRunPredicates)
	ctx := context.Background()

	cases := []struct {
		name    string
		input   *agrun.RunRowsInput
		wantNil bool
	}{
		{name: "no extra filters"},
		{
			name: "turn id match",
			input: &agrun.RunRowsInput{
				TurnId: "t-run-main",
				Has:    &agrun.RunRowsInputHas{TurnId: true},
			},
		},
		{
			name: "turn id mismatch",
			input: &agrun.RunRowsInput{
				TurnId: "t-other-run",
				Has:    &agrun.RunRowsInputHas{TurnId: true},
			},
			wantNil: true,
		},
		{
			name: "conversation id match",
			input: &agrun.RunRowsInput{
				ConversationId: "c-main",
				Has:            &agrun.RunRowsInputHas{ConversationId: true},
			},
		},
		{
			name: "conversation id mismatch",
			input: &agrun.RunRowsInput{
				ConversationId: "c-other",
				Has:            &agrun.RunRowsInputHas{ConversationId: true},
			},
			wantNil: true,
		},
		{
			name: "schedule id match",
			input: &agrun.RunRowsInput{
				ScheduleId: "sch-1",
				Has:        &agrun.RunRowsInputHas{ScheduleId: true},
			},
		},
		{
			name: "schedule id mismatch",
			input: &agrun.RunRowsInput{
				ScheduleId: "sch-x",
				Has:        &agrun.RunRowsInputHas{ScheduleId: true},
			},
			wantNil: true,
		},
		{
			name: "worker id match",
			input: &agrun.RunRowsInput{
				WorkerId: "worker-1",
				Has:      &agrun.RunRowsInputHas{WorkerId: true},
			},
		},
		{
			name: "worker id mismatch",
			input: &agrun.RunRowsInput{
				WorkerId: "worker-x",
				Has:      &agrun.RunRowsInputHas{WorkerId: true},
			},
			wantNil: true,
		},
		{
			name: "status match",
			input: &agrun.RunRowsInput{
				RunStatus: "running",
				Has:       &agrun.RunRowsInputHas{RunStatus: true},
			},
		},
		{
			name: "status mismatch",
			input: &agrun.RunRowsInput{
				RunStatus: "failed",
				Has:       &agrun.RunRowsInputHas{RunStatus: true},
			},
			wantNil: true,
		},
		{
			name: "exclude statuses excludes row",
			input: &agrun.RunRowsInput{
				ExcludeStatuses: []string{"running", "queued"},
				Has:             &agrun.RunRowsInputHas{ExcludeStatuses: true},
			},
			wantNil: true,
		},
		{
			name: "exclude statuses allows row",
			input: &agrun.RunRowsInput{
				ExcludeStatuses: []string{"failed"},
				Has:             &agrun.RunRowsInputHas{ExcludeStatuses: true},
			},
		},
		{
			name: "combined positive filters",
			input: &agrun.RunRowsInput{
				TurnId:          "t-run-main",
				ConversationId:  "c-main",
				ScheduleId:      "sch-1",
				WorkerId:        "worker-1",
				RunStatus:       "running",
				ExcludeStatuses: []string{"failed", "queued"},
				Has: &agrun.RunRowsInputHas{
					TurnId:          true,
					ConversationId:  true,
					ScheduleId:      true,
					WorkerId:        true,
					RunStatus:       true,
					ExcludeStatuses: true,
				},
			},
		},
		{
			name: "combined negative mismatch status",
			input: &agrun.RunRowsInput{
				TurnId:         "t-run-main",
				ConversationId: "c-main",
				RunStatus:      "queued",
				Has: &agrun.RunRowsInputHas{
					TurnId:         true,
					ConversationId: true,
					RunStatus:      true,
				},
			},
			wantNil: true,
		},
		{
			name: "combined negative excluded by status list",
			input: &agrun.RunRowsInput{
				TurnId:          "t-run-main",
				ConversationId:  "c-main",
				RunStatus:       "running",
				ExcludeStatuses: []string{"running"},
				Has: &agrun.RunRowsInputHas{
					TurnId:          true,
					ConversationId:  true,
					RunStatus:       true,
					ExcludeStatuses: true,
				},
			},
			wantNil: true,
		},
		{
			name: "combined negative worker mismatch",
			input: &agrun.RunRowsInput{
				TurnId:         "t-run-main",
				ConversationId: "c-main",
				WorkerId:       "worker-missing",
				RunStatus:      "running",
				Has: &agrun.RunRowsInputHas{
					TurnId:         true,
					ConversationId: true,
					WorkerId:       true,
					RunStatus:      true,
				},
			},
			wantNil: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.GetRun(ctx, "run-1", tc.input)
			if err != nil {
				t.Fatalf("GetRun() error: %v", err)
			}
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil run, got id=%s", got.Id)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected run")
			}
			if got.Id != "run-1" {
				t.Fatalf("unexpected run id: %s", got.Id)
			}
		})
	}

	t.Run("query selector pagination for run rows", func(t *testing.T) {
		type testCase struct {
			name          string
			selector      *hstate.NamedQuerySelector
			expectNil     bool
			expectRunID   string
			expectedError string
		}
		cases := []testCase{
			{
				name: "limit one offset zero returns run",
				selector: &hstate.NamedQuerySelector{
					Name: "RunRows",
					QuerySelector: hstate.QuerySelector{
						Limit:  1,
						Offset: 0,
					},
				},
				expectRunID: "run-1",
			},
			{
				name: "limit one offset one returns nil",
				selector: &hstate.NamedQuerySelector{
					Name: "RunRows",
					QuerySelector: hstate.QuerySelector{
						Limit:  1,
						Offset: 1,
					},
				},
				expectNil: true,
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				opts := []Option{}
				if tc.selector != nil {
					opts = append(opts, WithQuerySelector(tc.selector))
				}
				got, err := svc.GetRun(ctx, "run-1", nil, opts...)
				if tc.expectedError != "" {
					if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
						t.Fatalf("expected error containing %q, got %v", tc.expectedError, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("GetRun() error: %v", err)
				}
				if tc.expectNil {
					if got != nil {
						t.Fatalf("expected nil run, got %#v", got)
					}
					return
				}
				if got == nil {
					t.Fatalf("expected run")
				}
				if tc.expectRunID != "" && got.Id != tc.expectRunID {
					t.Fatalf("unexpected run id: got=%s want=%s", got.Id, tc.expectRunID)
				}
			})
		}
	})
}

func TestDataService_ActiveAndStaleRuns(t *testing.T) {
	svc := newSeededService(t, seedForActiveAndStaleRuns)
	ctx := context.Background()

	active, err := svc.GetActiveRun(ctx, &agrunactive.ActiveRunsInput{
		TurnId: "t-run-main",
		Has:    &agrunactive.ActiveRunsInputHas{TurnId: true},
	})
	if err != nil {
		t.Fatalf("GetActiveRun() error: %v", err)
	}
	if active == nil || active.Id != "run-2" {
		t.Fatalf("expected run-2 as latest active run for t-run-main, got %#v", active)
	}

	activeNone, err := svc.GetActiveRun(ctx, &agrunactive.ActiveRunsInput{
		TurnId: "t-missing",
		Has:    &agrunactive.ActiveRunsInputHas{TurnId: true},
	})
	if err != nil {
		t.Fatalf("GetActiveRun() error: %v", err)
	}
	if activeNone != nil {
		t.Fatalf("expected nil for missing turn active run")
	}

	activeByConversation, err := svc.GetActiveRun(ctx, &agrunactive.ActiveRunsInput{
		ConversationId: "c-other",
		Has:            &agrunactive.ActiveRunsInputHas{ConversationId: true},
	})
	if err != nil {
		t.Fatalf("GetActiveRun() error: %v", err)
	}
	if activeByConversation == nil || activeByConversation.Id != "run-4" {
		t.Fatalf("expected run-4 for c-other, got %#v", activeByConversation)
	}

	stale, err := svc.ListStaleRuns(ctx, &agrunstale.StaleRunsInput{
		HeartbeatBefore: mustTime("2026-01-01T09:20:00Z"),
		WorkerHost:      "host-a",
		Has: &agrunstale.StaleRunsInputHas{
			HeartbeatBefore: true,
			WorkerHost:      true,
		},
	})
	if err != nil {
		t.Fatalf("ListStaleRuns() error: %v", err)
	}
	assertRunIDs(t, stale, []string{"run-1"})

	staleHostB, err := svc.ListStaleRuns(ctx, &agrunstale.StaleRunsInput{
		HeartbeatBefore: mustTime("2026-01-01T09:20:00Z"),
		WorkerHost:      "host-b",
		Has: &agrunstale.StaleRunsInputHas{
			HeartbeatBefore: true,
			WorkerHost:      true,
		},
	})
	if err != nil {
		t.Fatalf("ListStaleRuns() error: %v", err)
	}
	assertRunIDs(t, staleHostB, []string{"run-5"})

	staleNone, err := svc.ListStaleRuns(ctx, &agrunstale.StaleRunsInput{
		HeartbeatBefore: mustTime("2026-01-01T09:05:00Z"),
		WorkerHost:      "host-a",
		Has: &agrunstale.StaleRunsInputHas{
			HeartbeatBefore: true,
			WorkerHost:      true,
		},
	})
	if err != nil {
		t.Fatalf("ListStaleRuns() error: %v", err)
	}
	if len(staleNone) != 0 {
		t.Fatalf("expected no stale runs, got %d", len(staleNone))
	}
}

func TestDataService_TurnPredicates(t *testing.T) {
	svc := newSeededService(t, seedForTurnPredicates)
	ctx := context.Background()

	active, err := svc.GetActiveTurn(ctx, &agturnactive.ActiveTurnsInput{
		ConversationID: "c-main",
		Has:            &agturnactive.ActiveTurnsInputHas{ConversationID: true},
	})
	if err != nil {
		t.Fatalf("GetActiveTurn() error: %v", err)
	}
	if active == nil || active.Id != "t-wait-main" {
		t.Fatalf("expected t-wait-main, got %#v", active)
	}

	turnByID, err := svc.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{
		ID:             "t-run-main",
		ConversationID: "c-main",
		Has: &agturnbyid.TurnLookupInputHas{
			ID:             true,
			ConversationID: true,
		},
	})
	if err != nil {
		t.Fatalf("GetTurnByID() error: %v", err)
	}
	if turnByID == nil || turnByID.Id != "t-run-main" {
		t.Fatalf("expected t-run-main, got %#v", turnByID)
	}

	turnByIDNone, err := svc.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{
		ID:             "t-run-main",
		ConversationID: "c-other",
		Has: &agturnbyid.TurnLookupInputHas{
			ID:             true,
			ConversationID: true,
		},
	})
	if err != nil {
		t.Fatalf("GetTurnByID() error: %v", err)
	}
	if turnByIDNone != nil {
		t.Fatalf("expected nil turn for mismatched conversation filter")
	}

	nextQueued, err := svc.GetNextQueuedTurn(ctx, &agturnnext.QueuedTurnInput{
		ConversationID: "c-main",
		Has:            &agturnnext.QueuedTurnInputHas{ConversationID: true},
	})
	if err != nil {
		t.Fatalf("GetNextQueuedTurn() error: %v", err)
	}
	if nextQueued == nil || nextQueued.Id != "t-queued-main-1" {
		t.Fatalf("expected t-queued-main-1, got %#v", nextQueued)
	}

	listQueued, err := svc.ListQueuedTurns(ctx, &agturnlist.QueuedTurnsInput{
		ConversationID: "c-main",
		Has:            &agturnlist.QueuedTurnsInputHas{ConversationID: true},
	})
	if err != nil {
		t.Fatalf("ListQueuedTurns() error: %v", err)
	}
	assertTurnIDsInOrder(t, listQueued, []string{"t-queued-main-1", "t-anchor"})

	countQueued, err := svc.CountQueuedTurns(ctx, &agturncount.QueuedTotalInput{
		ConversationID: "c-main",
		Has:            &agturncount.QueuedTotalInputHas{ConversationID: true},
	})
	if err != nil {
		t.Fatalf("CountQueuedTurns() error: %v", err)
	}
	if countQueued != 2 {
		t.Fatalf("expected queued count 2, got %d", countQueued)
	}

	countQueuedMissing, err := svc.CountQueuedTurns(ctx, &agturncount.QueuedTotalInput{
		ConversationID: "c-missing",
		Has:            &agturncount.QueuedTotalInputHas{ConversationID: true},
	})
	if err != nil {
		t.Fatalf("CountQueuedTurns() error: %v", err)
	}
	if countQueuedMissing != 0 {
		t.Fatalf("expected queued count 0 for missing conversation, got %d", countQueuedMissing)
	}
}

func TestDataService_ToolCallAndPayloadPredicates(t *testing.T) {
	svc := newSeededService(t, seedForToolCallAndPayloadPredicates)
	ctx := context.Background()

	toolRows, err := svc.GetToolCallByOp(ctx, "op-byop", &agtoolcall.ToolCallRowsInput{
		ConversationId: "c-main",
		Has:            &agtoolcall.ToolCallRowsInputHas{ConversationId: true, OpId: true},
	})
	if err != nil {
		t.Fatalf("GetToolCallByOp() error: %v", err)
	}
	assertToolCallMessageIDs(t, toolRows, []string{"m-tool-main"})

	toolRowsOther, err := svc.GetToolCallByOp(ctx, "op-byop", &agtoolcall.ToolCallRowsInput{
		ConversationId: "c-other",
		Has:            &agtoolcall.ToolCallRowsInputHas{ConversationId: true, OpId: true},
	})
	if err != nil {
		t.Fatalf("GetToolCallByOp() error: %v", err)
	}
	assertToolCallMessageIDs(t, toolRowsOther, []string{"m-tool-other"})

	toolRowsMissing, err := svc.GetToolCallByOp(ctx, "op-missing", &agtoolcall.ToolCallRowsInput{
		ConversationId: "c-main",
		Has:            &agtoolcall.ToolCallRowsInputHas{ConversationId: true, OpId: true},
	})
	if err != nil {
		t.Fatalf("GetToolCallByOp() error: %v", err)
	}
	if len(toolRowsMissing) != 0 {
		t.Fatalf("expected no toolcall rows for missing op id, got %d", len(toolRowsMissing))
	}

	payloadCases := []struct {
		name    string
		input   *agpayload.PayloadRowsInput
		wantIDs []string
	}{
		{
			name: "tenant id",
			input: &agpayload.PayloadRowsInput{
				TenantID: "tenant-1",
				Has:      &agpayload.PayloadRowsInputHas{TenantID: true},
			},
			wantIDs: []string{"p1", "p2"},
		},
		{
			name: "single id",
			input: &agpayload.PayloadRowsInput{
				Id:  "p1",
				Has: &agpayload.PayloadRowsInputHas{Id: true},
			},
			wantIDs: []string{"p1"},
		},
		{
			name: "ids list",
			input: &agpayload.PayloadRowsInput{
				Ids: []string{"p1", "p3"},
				Has: &agpayload.PayloadRowsInputHas{Ids: true},
			},
			wantIDs: []string{"p1", "p3"},
		},
		{
			name: "kind",
			input: &agpayload.PayloadRowsInput{
				Kind: "request",
				Has:  &agpayload.PayloadRowsInputHas{Kind: true},
			},
			wantIDs: []string{"p1", "p3"},
		},
		{
			name: "digest",
			input: &agpayload.PayloadRowsInput{
				Digest: "dig-2",
				Has:    &agpayload.PayloadRowsInputHas{Digest: true},
			},
			wantIDs: []string{"p2"},
		},
		{
			name: "storage",
			input: &agpayload.PayloadRowsInput{
				Storage: "inline",
				Has:     &agpayload.PayloadRowsInputHas{Storage: true},
			},
			wantIDs: []string{"p1", "p3"},
		},
		{
			name: "mime type",
			input: &agpayload.PayloadRowsInput{
				MimeType: "text/plain",
				Has:      &agpayload.PayloadRowsInputHas{MimeType: true},
			},
			wantIDs: []string{"p1", "p3"},
		},
		{
			name: "since",
			input: &agpayload.PayloadRowsInput{
				Since: mustTime("2026-01-01T09:15:00Z"),
				Has:   &agpayload.PayloadRowsInputHas{Since: true},
			},
			wantIDs: []string{"p2", "p3"},
		},
		{
			name: "negative no rows",
			input: &agpayload.PayloadRowsInput{
				TenantID: "tenant-missing",
				Has:      &agpayload.PayloadRowsInputHas{TenantID: true},
			},
			wantIDs: []string{},
		},
	}

	for _, tc := range payloadCases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := svc.ListPayloadRows(ctx, tc.input)
			if err != nil {
				t.Fatalf("ListPayloadRows() error: %v", err)
			}
			got := make([]string, 0, len(rows))
			for _, r := range rows {
				got = append(got, r.Id)
			}
			assertIDs(t, got, tc.wantIDs)
		})
	}
}

func TestDataService_QuerySelectorPagination(t *testing.T) {
	svc := newSeededService(t, seedForQuerySelectorPagination)
	ctx := context.Background()

	t.Run("queued turns limit offset", func(t *testing.T) {
		rows, err := svc.ListQueuedTurns(
			ctx,
			&agturnlist.QueuedTurnsInput{
				ConversationID: "c-main",
				Has:            &agturnlist.QueuedTurnsInputHas{ConversationID: true},
			},
			WithQuerySelector(&hstate.NamedQuerySelector{
				Name: "QueuedTurns",
				QuerySelector: hstate.QuerySelector{
					Limit:  1,
					Offset: 1,
				},
			}),
		)
		if err != nil {
			t.Fatalf("ListQueuedTurns() error: %v", err)
		}
		assertTurnIDsInOrder(t, rows, []string{"t-anchor"})
	})

	t.Run("payload rows limit", func(t *testing.T) {
		rows, err := svc.ListPayloadRows(
			ctx,
			&agpayload.PayloadRowsInput{
				TenantID: "tenant-1",
				Has:      &agpayload.PayloadRowsInputHas{TenantID: true},
			},
			WithQuerySelector(&hstate.NamedQuerySelector{
				Name: "PayloadRows",
				QuerySelector: hstate.QuerySelector{
					Limit: 1,
				},
			}),
		)
		if err != nil {
			t.Fatalf("ListPayloadRows() error: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 payload row with selector limit, got %d", len(rows))
		}
	})

	t.Run("conversation nested transcript and message selectors", func(t *testing.T) {
		in := &agconv.ConversationInput{
			IncludeTranscript: true,
			IncludeToolCall:   true,
			Has: &agconv.ConversationInputHas{
				IncludeTranscript: true,
				IncludeToolCall:   true,
			},
		}
		got, err := svc.GetConversation(
			ctx,
			"c-main",
			in,
			WithQuerySelector(
				&hstate.NamedQuerySelector{Name: "Transcript", QuerySelector: hstate.QuerySelector{Limit: 1}},
				&hstate.NamedQuerySelector{Name: "Message", QuerySelector: hstate.QuerySelector{Limit: 1}},
				&hstate.NamedQuerySelector{Name: "ToolMessage", QuerySelector: hstate.QuerySelector{Limit: 1}},
			),
		)
		if err != nil {
			t.Fatalf("GetConversation() error: %v", err)
		}
		if got == nil {
			t.Fatalf("expected conversation")
		}
		if len(got.Transcript) != 1 {
			t.Fatalf("expected 1 transcript row with selector limit, got %d", len(got.Transcript))
		}
		if len(got.Transcript[0].Message) > 1 {
			t.Fatalf("expected at most 1 message row with selector limit, got %d", len(got.Transcript[0].Message))
		}
		if len(got.Transcript[0].Message) == 1 && len(got.Transcript[0].Message[0].ToolMessage) > 1 {
			t.Fatalf("expected at most 1 tool message row with selector limit, got %d", len(got.Transcript[0].Message[0].ToolMessage))
		}
	})
}

func TestDataService_PagedReads_DataDriven(t *testing.T) {
	svc := newSeededService(t, seedForPagedReads)
	ctx := context.Background()

	t.Run("conversation list", func(t *testing.T) {
		cases := []struct {
			name     string
			input    *agconvlist.ConversationRowsInput
			page     *PageInput
			wantIDs  []string
			wantMore bool
		}{
			{
				name:     "first page latest",
				input:    &agconvlist.ConversationRowsInput{Has: &agconvlist.ConversationRowsInputHas{}},
				page:     &PageInput{Limit: 1, Direction: DirectionLatest},
				wantIDs:  []string{"c-page-2"},
				wantMore: true,
			},
			{
				name: "filter by agent",
				input: &agconvlist.ConversationRowsInput{
					AgentId: "agent-1",
					Has:     &agconvlist.ConversationRowsInputHas{AgentId: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionLatest},
				wantIDs: []string{"c-page-2", "c-page-1"},
			},
			{
				name: "cursor before",
				input: &agconvlist.ConversationRowsInput{
					Has: &agconvlist.ConversationRowsInputHas{},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionBefore, Cursor: "c-page-2"},
				wantIDs: []string{"c-page-1"},
			},
			{
				name: "cursor after",
				input: &agconvlist.ConversationRowsInput{
					Has: &agconvlist.ConversationRowsInputHas{},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionAfter, Cursor: "c-page-1"},
				wantIDs: []string{"c-page-2"},
			},
			{
				name: "filter by status",
				input: &agconvlist.ConversationRowsInput{
					StatusFilter: "active",
					Has:          &agconvlist.ConversationRowsInputHas{StatusFilter: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionLatest},
				wantIDs: []string{"c-page-2", "c-page-1"},
			},
			{
				name: "filter by query",
				input: &agconvlist.ConversationRowsInput{
					Query: "c-page-1",
					Has:   &agconvlist.ConversationRowsInputHas{Query: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionLatest},
				wantIDs: []string{"c-page-1"},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				page, err := svc.ListConversations(ctx, tc.input, tc.page)
				if err != nil {
					t.Fatalf("ListConversations() error: %v", err)
				}
				if page == nil {
					t.Fatalf("expected page")
				}
				got := make([]string, 0, len(page.Rows))
				for _, row := range page.Rows {
					got = append(got, row.Id)
				}
				assertIDs(t, got, tc.wantIDs)
				if page.HasMore != tc.wantMore {
					t.Fatalf("unexpected hasMore: got=%v want=%v", page.HasMore, tc.wantMore)
				}
			})
		}
	})

	t.Run("message page", func(t *testing.T) {
		cases := []struct {
			name     string
			input    *agmessagelist.MessageRowsInput
			page     *PageInput
			wantIDs  []string
			wantMore bool
		}{
			{
				name: "turn scoped latest first page",
				input: &agmessagelist.MessageRowsInput{
					ConversationId: "c-page-1",
					TurnId:         "t-page-1",
					Has:            &agmessagelist.MessageRowsInputHas{ConversationId: true, TurnId: true},
				},
				page:     &PageInput{Limit: 2, Direction: DirectionLatest},
				wantIDs:  []string{"m-page-1-3", "m-page-1-2"},
				wantMore: true,
			},
			{
				name: "interim filter positive",
				input: &agmessagelist.MessageRowsInput{
					ConversationId: "c-page-1",
					TurnId:         "t-page-1",
					Interim:        1,
					Has:            &agmessagelist.MessageRowsInputHas{ConversationId: true, TurnId: true, Interim: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionLatest},
				wantIDs: []string{"m-page-1-3", "m-page-1-2"},
			},
			{
				name: "interim filter negative",
				input: &agmessagelist.MessageRowsInput{
					ConversationId: "c-page-1",
					TurnId:         "t-page-1",
					Interim:        2,
					Has:            &agmessagelist.MessageRowsInputHas{ConversationId: true, TurnId: true, Interim: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionLatest},
				wantIDs: []string{},
			},
			{
				name: "phase filter positive",
				input: &agmessagelist.MessageRowsInput{
					ConversationId: "c-page-1",
					TurnId:         "t-page-1",
					Phase:          "pending",
					Has:            &agmessagelist.MessageRowsInputHas{ConversationId: true, TurnId: true, Phase: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionLatest},
				wantIDs: []string{"m-page-1-2"},
			},
			{
				name: "phase filter negative",
				input: &agmessagelist.MessageRowsInput{
					ConversationId: "c-page-1",
					TurnId:         "t-page-1",
					Phase:          "streaming",
					Has:            &agmessagelist.MessageRowsInputHas{ConversationId: true, TurnId: true, Phase: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionLatest},
				wantIDs: []string{},
			},
			{
				name: "types filter positive",
				input: &agmessagelist.MessageRowsInput{
					ConversationId: "c-page-1",
					TurnId:         "t-page-1",
					Types:          []string{"text"},
					Has:            &agmessagelist.MessageRowsInputHas{ConversationId: true, TurnId: true, Types: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionLatest},
				wantIDs: []string{"m-page-1-3", "m-page-1-2", "m-page-1-1"},
			},
			{
				name: "types filter negative",
				input: &agmessagelist.MessageRowsInput{
					ConversationId: "c-page-1",
					TurnId:         "t-page-1",
					Types:          []string{"tool_op"},
					Has:            &agmessagelist.MessageRowsInputHas{ConversationId: true, TurnId: true, Types: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionLatest},
				wantIDs: []string{},
			},
			{
				name: "cursor before",
				input: &agmessagelist.MessageRowsInput{
					ConversationId: "c-page-1",
					Has:            &agmessagelist.MessageRowsInputHas{ConversationId: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionBefore, Cursor: "m-page-1-3"},
				wantIDs: []string{"m-page-1-2", "m-page-1-1"},
			},
			{
				name: "cursor after",
				input: &agmessagelist.MessageRowsInput{
					ConversationId: "c-page-1",
					Has:            &agmessagelist.MessageRowsInputHas{ConversationId: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionAfter, Cursor: "m-page-1-2"},
				wantIDs: []string{"m-page-1-3"},
			},
			{
				name: "latest ignores cursor",
				input: &agmessagelist.MessageRowsInput{
					ConversationId: "c-page-1",
					Has:            &agmessagelist.MessageRowsInputHas{ConversationId: true},
				},
				page:     &PageInput{Limit: 2, Direction: DirectionLatest, Cursor: "m-page-1-1"},
				wantIDs:  []string{"m-page-1-3", "m-page-1-2"},
				wantMore: true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				page, err := svc.GetMessagesPage(ctx, tc.input, tc.page)
				if err != nil {
					t.Fatalf("GetMessagesPage() error: %v", err)
				}
				if page == nil {
					t.Fatalf("expected page")
				}
				got := make([]string, 0, len(page.Rows))
				for _, row := range page.Rows {
					got = append(got, row.Id)
				}
				assertIDs(t, got, tc.wantIDs)
				if page.HasMore != tc.wantMore {
					t.Fatalf("unexpected hasMore: got=%v want=%v", page.HasMore, tc.wantMore)
				}
			})
		}
	})

	t.Run("turn page", func(t *testing.T) {
		cases := []struct {
			name    string
			input   *agturnlistall.TurnRowsInput
			page    *PageInput
			wantIDs []string
		}{
			{
				name: "conversation latest",
				input: &agturnlistall.TurnRowsInput{
					ConversationID: "c-page-1",
					Has:            &agturnlistall.TurnRowsInputHas{ConversationID: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionLatest},
				wantIDs: []string{"t-page-2", "t-page-1"},
			},
			{
				name: "status filter",
				input: &agturnlistall.TurnRowsInput{
					ConversationID: "c-page-1",
					Statuses:       []string{"queued"},
					Has:            &agturnlistall.TurnRowsInputHas{ConversationID: true, Statuses: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionLatest},
				wantIDs: []string{"t-page-2"},
			},
			{
				name: "cursor before",
				input: &agturnlistall.TurnRowsInput{
					ConversationID: "c-page-1",
					Has:            &agturnlistall.TurnRowsInputHas{ConversationID: true},
				},
				page:    &PageInput{Limit: 10, Direction: DirectionBefore, Cursor: "t-page-2"},
				wantIDs: []string{"t-page-1"},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				page, err := svc.GetTurnsPage(ctx, tc.input, tc.page)
				if err != nil {
					t.Fatalf("GetTurnsPage() error: %v", err)
				}
				if page == nil {
					t.Fatalf("expected page")
				}
				got := make([]string, 0, len(page.Rows))
				for _, row := range page.Rows {
					got = append(got, row.Id)
				}
				assertIDs(t, got, tc.wantIDs)
			})
		}
	})
}

func TestDataService_Patch_DataDriven(t *testing.T) {
	ctx := context.Background()

	t.Run("conversation patch", func(t *testing.T) {
		svc := newSeededService(t, seedForPatchBaseline)
		cases := []struct {
			name    string
			rows    []*agconvwrite.MutableConversationView
			wantErr bool
		}{
			{
				name: "insert",
				rows: []*agconvwrite.MutableConversationView{
					agconvwrite.NewMutableConversationView(
						agconvwrite.WithConversationID("c-patch"),
						agconvwrite.WithConversationStatus("active"),
					),
				},
			},
			{
				name: "update",
				rows: []*agconvwrite.MutableConversationView{
					agconvwrite.NewMutableConversationView(
						agconvwrite.WithConversationID("c-base"),
						agconvwrite.WithConversationSummary("updated-summary"),
					),
				},
			},
			{
				name: "missing id",
				rows: []*agconvwrite.MutableConversationView{
					agconvwrite.NewMutableConversationView(
						agconvwrite.WithConversationStatus("broken"),
					),
				},
				wantErr: true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.PatchConversations(ctx, tc.rows)
				if tc.wantErr {
					if err == nil {
						t.Fatalf("expected error")
					}
					return
				}
				if err != nil {
					t.Fatalf("PatchConversations() error: %v", err)
				}
			})
		}
		updated, err := svc.GetConversation(ctx, "c-base", nil)
		if err != nil {
			t.Fatalf("GetConversation() error: %v", err)
		}
		if updated == nil || updated.Summary == nil || *updated.Summary != "updated-summary" {
			t.Fatalf("expected updated summary, got %#v", updated)
		}
	})

	t.Run("turn patch", func(t *testing.T) {
		svc := newSeededService(t, seedForPatchBaseline)
		cases := []struct {
			name    string
			rows    []*agturnwrite.MutableTurnView
			wantErr bool
		}{
			{
				name: "insert",
				rows: []*agturnwrite.MutableTurnView{
					agturnwrite.NewMutableTurnView(
						agturnwrite.WithTurnID("t-patch"),
						agturnwrite.WithTurnConversationID("c-base"),
						agturnwrite.WithTurnStatus("queued"),
					),
				},
			},
			{
				name: "update",
				rows: []*agturnwrite.MutableTurnView{
					agturnwrite.NewMutableTurnView(
						agturnwrite.WithTurnID("t-base"),
						agturnwrite.WithTurnStatus("running"),
					),
				},
			},
			{
				name: "missing required status",
				rows: []*agturnwrite.MutableTurnView{
					agturnwrite.NewMutableTurnView(
						agturnwrite.WithTurnID("t-bad-status"),
						agturnwrite.WithTurnConversationID("c-base"),
					),
				},
				wantErr: true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.PatchTurns(ctx, tc.rows)
				if tc.wantErr {
					if err == nil {
						t.Fatalf("expected error")
					}
					return
				}
				if err != nil {
					t.Fatalf("PatchTurns() error: %v", err)
				}
			})
		}
	})

	t.Run("message patch", func(t *testing.T) {
		svc := newSeededService(t, seedForPatchBaseline)
		cases := []struct {
			name    string
			rows    []*agmessagewrite.MutableMessageView
			wantErr bool
		}{
			{
				name: "insert",
				rows: []*agmessagewrite.MutableMessageView{
					agmessagewrite.NewMutableMessageView(
						agmessagewrite.WithMessageID("m-patch"),
						agmessagewrite.WithMessageConversationID("c-base"),
						agmessagewrite.WithMessageTurnID("t-base"),
						agmessagewrite.WithMessageRole("assistant"),
						agmessagewrite.WithMessageType("text"),
						agmessagewrite.WithMessageContent("new"),
					),
				},
			},
			{
				name: "update",
				rows: []*agmessagewrite.MutableMessageView{
					agmessagewrite.NewMutableMessageView(
						agmessagewrite.WithMessageID("m-base"),
						agmessagewrite.WithMessageContent("updated"),
					),
				},
			},
			{
				name: "missing required role",
				rows: []*agmessagewrite.MutableMessageView{
					agmessagewrite.NewMutableMessageView(
						agmessagewrite.WithMessageID("m-bad-role"),
						agmessagewrite.WithMessageConversationID("c-base"),
						agmessagewrite.WithMessageType("text"),
						agmessagewrite.WithMessageContent("bad"),
					),
				},
				wantErr: true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.PatchMessages(ctx, tc.rows)
				if tc.wantErr {
					if err == nil {
						t.Fatalf("expected error")
					}
					return
				}
				if err != nil {
					t.Fatalf("PatchMessages() error: %v", err)
				}
			})
		}
		got, err := svc.GetMessage(ctx, "m-base", nil)
		if err != nil {
			t.Fatalf("GetMessage() error: %v", err)
		}
		if got == nil || got.Content == nil || *got.Content != "updated" {
			t.Fatalf("expected updated message content, got %#v", got)
		}
	})

	t.Run("payload patch", func(t *testing.T) {
		svc := newSeededService(t, seedForPatchBaseline)
		cases := []struct {
			name    string
			rows    []*agpayloadwrite.MutablePayloadView
			wantErr bool
		}{
			{
				name: "insert",
				rows: []*agpayloadwrite.MutablePayloadView{
					agpayloadwrite.NewMutablePayloadView(
						agpayloadwrite.WithPayloadID("p-patch"),
						agpayloadwrite.WithPayloadKind("request"),
						agpayloadwrite.WithPayloadMimeType("application/json"),
						agpayloadwrite.WithPayloadSizeBytes(2),
						agpayloadwrite.WithPayloadStorage("inline"),
						agpayloadwrite.WithPayloadInlineBody([]byte("{}")),
					),
				},
			},
			{
				name: "update",
				rows: []*agpayloadwrite.MutablePayloadView{
					func() *agpayloadwrite.MutablePayloadView {
						ret := agpayloadwrite.NewMutablePayloadView(
							agpayloadwrite.WithPayloadID("p-base"),
							agpayloadwrite.WithPayloadStorage("object"),
						)
						ret.SetURI("s3://bucket/item")
						return ret
					}(),
				},
			},
			{
				name: "missing required kind",
				rows: []*agpayloadwrite.MutablePayloadView{
					agpayloadwrite.NewMutablePayloadView(
						agpayloadwrite.WithPayloadID("p-bad"),
						agpayloadwrite.WithPayloadMimeType("text/plain"),
						agpayloadwrite.WithPayloadSizeBytes(1),
						agpayloadwrite.WithPayloadStorage("inline"),
					),
				},
				wantErr: true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.PatchPayloads(ctx, tc.rows)
				if tc.wantErr {
					if err == nil {
						t.Fatalf("expected error")
					}
					return
				}
				if err != nil {
					t.Fatalf("PatchPayloads() error: %v", err)
				}
			})
		}
		rows, err := svc.ListPayloadRows(ctx, &agpayload.PayloadRowsInput{Id: "p-base", Has: &agpayload.PayloadRowsInputHas{Id: true}})
		if err != nil {
			t.Fatalf("ListPayloadRows() error: %v", err)
		}
		if len(rows) != 1 || rows[0].Storage != "object" {
			t.Fatalf("expected updated payload storage, got %#v", rows)
		}
	})

	t.Run("model call patch", func(t *testing.T) {
		svc := newSeededService(t, seedForPatchBaseline)
		cases := []struct {
			name    string
			rows    []*agmodelcallwrite.MutableModelCallView
			wantErr bool
		}{
			{
				name: "insert",
				rows: []*agmodelcallwrite.MutableModelCallView{
					agmodelcallwrite.NewMutableModelCallView(
						agmodelcallwrite.WithModelCallMessageID("m-base"),
						agmodelcallwrite.WithModelCallTurnID("t-base"),
						agmodelcallwrite.WithModelCallProvider("openai"),
						agmodelcallwrite.WithModelCallModel("gpt-5-mini"),
						agmodelcallwrite.WithModelCallModelKind("chat"),
						agmodelcallwrite.WithModelCallStatus("completed"),
					),
				},
			},
			{
				name: "update",
				rows: []*agmodelcallwrite.MutableModelCallView{
					func() *agmodelcallwrite.MutableModelCallView {
						ret := agmodelcallwrite.NewMutableModelCallView(
							agmodelcallwrite.WithModelCallMessageID("m-base"),
						)
						ret.SetErrorMessage("warn")
						return ret
					}(),
				},
			},
			{
				name: "missing required model kind",
				rows: []*agmodelcallwrite.MutableModelCallView{
					agmodelcallwrite.NewMutableModelCallView(
						agmodelcallwrite.WithModelCallMessageID("m-bad-model"),
						agmodelcallwrite.WithModelCallProvider("openai"),
						agmodelcallwrite.WithModelCallModel("gpt"),
						agmodelcallwrite.WithModelCallStatus("completed"),
					),
				},
				wantErr: true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.PatchModelCalls(ctx, tc.rows)
				if tc.wantErr {
					if err == nil {
						t.Fatalf("expected error")
					}
					return
				}
				if err != nil {
					t.Fatalf("PatchModelCalls() error: %v", err)
				}
			})
		}

		conv := mustGetConversationWithModelCalls(t, ctx, svc, "c-base")
		msg := findMessage(conv.Transcript, "m-base")
		if msg == nil || msg.ModelCall == nil || msg.ModelCall.ErrorMessage == nil || *msg.ModelCall.ErrorMessage != "warn" {
			t.Fatalf("expected updated model call, got %#v", msg)
		}
	})

	t.Run("tool call patch", func(t *testing.T) {
		svc := newSeededService(t, seedForPatchBaseline)
		_, err := svc.PatchMessages(ctx, []*agmessagewrite.MutableMessageView{
			agmessagewrite.NewMutableMessageView(
				agmessagewrite.WithMessageID("m-tool"),
				agmessagewrite.WithMessageConversationID("c-base"),
				agmessagewrite.WithMessageTurnID("t-base"),
				agmessagewrite.WithMessageParentID("m-base"),
				agmessagewrite.WithMessageRole("assistant"),
				agmessagewrite.WithMessageType("tool_op"),
				agmessagewrite.WithMessageContent("tool op"),
			),
		})
		if err != nil {
			t.Fatalf("PatchMessages(seed tool message) error: %v", err)
		}

		cases := []struct {
			name    string
			rows    []*agtoolcallwrite.MutableToolCallView
			wantErr bool
		}{
			{
				name: "insert",
				rows: []*agtoolcallwrite.MutableToolCallView{
					agtoolcallwrite.NewMutableToolCallView(
						agtoolcallwrite.WithToolCallMessageID("m-tool"),
						agtoolcallwrite.WithToolCallTurnID("t-base"),
						agtoolcallwrite.WithToolCallOpID("op-1"),
						agtoolcallwrite.WithToolCallToolName("sql/query"),
						agtoolcallwrite.WithToolCallToolKind("mcp"),
						agtoolcallwrite.WithToolCallStatus("completed"),
					),
				},
			},
			{
				name: "update",
				rows: []*agtoolcallwrite.MutableToolCallView{
					agtoolcallwrite.NewMutableToolCallView(
						agtoolcallwrite.WithToolCallMessageID("m-tool"),
						agtoolcallwrite.WithToolCallErrorMessage("tool warning"),
					),
				},
			},
			{
				name: "missing required tool name",
				rows: []*agtoolcallwrite.MutableToolCallView{
					agtoolcallwrite.NewMutableToolCallView(
						agtoolcallwrite.WithToolCallMessageID("m-tool-bad"),
						agtoolcallwrite.WithToolCallOpID("op-x"),
						agtoolcallwrite.WithToolCallToolKind("mcp"),
						agtoolcallwrite.WithToolCallStatus("completed"),
					),
				},
				wantErr: true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.PatchToolCalls(ctx, tc.rows)
				if tc.wantErr {
					if err == nil {
						t.Fatalf("expected error")
					}
					return
				}
				if err != nil {
					t.Fatalf("PatchToolCalls() error: %v", err)
				}
			})
		}

		conv := mustGetConversationWithToolCalls(t, ctx, svc, "c-base")
		msg := findMessage(conv.Transcript, "m-base")
		if msg == nil || len(msg.ToolMessage) == 0 || msg.ToolMessage[0].ToolCall == nil || msg.ToolMessage[0].ToolCall.ErrorMessage == nil || *msg.ToolMessage[0].ToolCall.ErrorMessage != "tool warning" {
			t.Fatalf("expected updated tool call, got %#v", msg)
		}
	})
}

func TestDataService_CRUD_DataDriven(t *testing.T) {
	ctx := context.Background()
	type crudCase struct {
		name        string
		create      func(t *testing.T, svc Service)
		readCreated func(t *testing.T, svc Service)
		update      func(t *testing.T, svc Service)
		readUpdated func(t *testing.T, svc Service)
		remove      func(t *testing.T, svc Service)
		readRemoved func(t *testing.T, svc Service)
	}

	cases := []crudCase{
		{
			name: "conversation",
			create: func(t *testing.T, svc Service) {
				row := agconvwrite.NewMutableConversationView(
					agconvwrite.WithConversationID("c-crud"),
					agconvwrite.WithConversationStatus("active"),
					agconvwrite.WithConversationVisibility("private"),
				)
				row.SetCreatedByUserID("user-crud")
				if _, err := svc.PatchConversations(ctx, []*agconvwrite.MutableConversationView{row}); err != nil {
					t.Fatalf("PatchConversations(create) error: %v", err)
				}
			},
			readCreated: func(t *testing.T, svc Service) {
				created, err := svc.GetConversation(ctx, "c-crud", nil)
				if err != nil || created == nil {
					t.Fatalf("GetConversation(create) err=%v value=%#v", err, created)
				}
			},
			update: func(t *testing.T, svc Service) {
				if _, err := svc.PatchConversations(ctx, []*agconvwrite.MutableConversationView{
					agconvwrite.NewMutableConversationView(
						agconvwrite.WithConversationID("c-crud"),
						agconvwrite.WithConversationSummary("updated"),
					),
				}); err != nil {
					t.Fatalf("PatchConversations(update) error: %v", err)
				}
			},
			readUpdated: func(t *testing.T, svc Service) {
				updated, err := svc.GetConversation(ctx, "c-crud", nil)
				if err != nil || updated == nil || updated.Summary == nil || *updated.Summary != "updated" {
					t.Fatalf("GetConversation(update) err=%v value=%#v", err, updated)
				}
			},
			remove: func(t *testing.T, svc Service) {
				if err := svc.DeleteConversations(ctx, "c-crud"); err != nil {
					t.Fatalf("DeleteConversations() error: %v", err)
				}
			},
			readRemoved: func(t *testing.T, svc Service) {
				deleted, err := svc.GetConversation(ctx, "c-crud", nil)
				if err != nil {
					t.Fatalf("GetConversation(delete) error: %v", err)
				}
				if deleted != nil {
					t.Fatalf("expected nil after delete, got %#v", deleted)
				}
			},
		},
		{
			name: "message",
			create: func(t *testing.T, svc Service) {
				if _, err := svc.PatchMessages(ctx, []*agmessagewrite.MutableMessageView{
					agmessagewrite.NewMutableMessageView(
						agmessagewrite.WithMessageID("m-crud"),
						agmessagewrite.WithMessageConversationID("c-base"),
						agmessagewrite.WithMessageTurnID("t-base"),
						agmessagewrite.WithMessageRole("assistant"),
						agmessagewrite.WithMessageType("text"),
						agmessagewrite.WithMessageContent("created"),
					),
				}); err != nil {
					t.Fatalf("PatchMessages(create) error: %v", err)
				}
			},
			readCreated: func(t *testing.T, svc Service) {
				created, err := svc.GetMessage(ctx, "m-crud", nil)
				if err != nil || created == nil {
					t.Fatalf("GetMessage(create) err=%v value=%#v", err, created)
				}
			},
			update: func(t *testing.T, svc Service) {
				if _, err := svc.PatchMessages(ctx, []*agmessagewrite.MutableMessageView{
					agmessagewrite.NewMutableMessageView(
						agmessagewrite.WithMessageID("m-crud"),
						agmessagewrite.WithMessageContent("updated"),
					),
				}); err != nil {
					t.Fatalf("PatchMessages(update) error: %v", err)
				}
			},
			readUpdated: func(t *testing.T, svc Service) {
				updated, err := svc.GetMessage(ctx, "m-crud", nil)
				if err != nil || updated == nil || updated.Content == nil || *updated.Content != "updated" {
					t.Fatalf("GetMessage(update) err=%v value=%#v", err, updated)
				}
			},
			remove: func(t *testing.T, svc Service) {
				if err := svc.DeleteMessages(ctx, "m-crud"); err != nil {
					t.Fatalf("DeleteMessages() error: %v", err)
				}
			},
			readRemoved: func(t *testing.T, svc Service) {
				deleted, err := svc.GetMessage(ctx, "m-crud", nil)
				if err != nil {
					t.Fatalf("GetMessage(delete) error: %v", err)
				}
				if deleted != nil {
					t.Fatalf("expected nil after delete, got %#v", deleted)
				}
			},
		},
		{
			name: "turn",
			create: func(t *testing.T, svc Service) {
				if _, err := svc.PatchTurns(ctx, []*agturnwrite.MutableTurnView{
					agturnwrite.NewMutableTurnView(
						agturnwrite.WithTurnID("t-crud"),
						agturnwrite.WithTurnConversationID("c-base"),
						agturnwrite.WithTurnStatus("queued"),
					),
				}); err != nil {
					t.Fatalf("PatchTurns(create) error: %v", err)
				}
			},
			readCreated: func(t *testing.T, svc Service) {
				created, err := svc.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{ID: "t-crud", Has: &agturnbyid.TurnLookupInputHas{ID: true}})
				if err != nil || created == nil {
					t.Fatalf("GetTurnByID(create) err=%v value=%#v", err, created)
				}
			},
			update: func(t *testing.T, svc Service) {
				if _, err := svc.PatchTurns(ctx, []*agturnwrite.MutableTurnView{
					agturnwrite.NewMutableTurnView(
						agturnwrite.WithTurnID("t-crud"),
						agturnwrite.WithTurnStatus("running"),
					),
				}); err != nil {
					t.Fatalf("PatchTurns(update) error: %v", err)
				}
			},
			readUpdated: func(t *testing.T, svc Service) {
				updated, err := svc.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{ID: "t-crud", Has: &agturnbyid.TurnLookupInputHas{ID: true}})
				if err != nil || updated == nil || updated.Status != "running" {
					t.Fatalf("GetTurnByID(update) err=%v value=%#v", err, updated)
				}
			},
			remove: func(t *testing.T, svc Service) {
				if err := svc.DeleteTurns(ctx, "t-crud"); err != nil {
					t.Fatalf("DeleteTurns() error: %v", err)
				}
			},
			readRemoved: func(t *testing.T, svc Service) {
				deleted, err := svc.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{ID: "t-crud", Has: &agturnbyid.TurnLookupInputHas{ID: true}})
				if err != nil {
					t.Fatalf("GetTurnByID(delete) error: %v", err)
				}
				if deleted != nil {
					t.Fatalf("expected nil after delete, got %#v", deleted)
				}
			},
		},
		{
			name: "payload",
			create: func(t *testing.T, svc Service) {
				if _, err := svc.PatchPayloads(ctx, []*agpayloadwrite.MutablePayloadView{
					agpayloadwrite.NewMutablePayloadView(
						agpayloadwrite.WithPayloadID("p-crud"),
						agpayloadwrite.WithPayloadKind("request"),
						agpayloadwrite.WithPayloadMimeType("application/json"),
						agpayloadwrite.WithPayloadSizeBytes(2),
						agpayloadwrite.WithPayloadStorage("inline"),
						agpayloadwrite.WithPayloadInlineBody([]byte("{}")),
					),
				}); err != nil {
					t.Fatalf("PatchPayloads(create) error: %v", err)
				}
			},
			readCreated: func(t *testing.T, svc Service) {
				rows, err := svc.ListPayloadRows(ctx, &agpayload.PayloadRowsInput{Id: "p-crud", Has: &agpayload.PayloadRowsInputHas{Id: true}})
				if err != nil || len(rows) != 1 {
					t.Fatalf("ListPayloadRows(create) err=%v rows=%#v", err, rows)
				}
			},
			update: func(t *testing.T, svc Service) {
				if _, err := svc.PatchPayloads(ctx, []*agpayloadwrite.MutablePayloadView{
					agpayloadwrite.NewMutablePayloadView(
						agpayloadwrite.WithPayloadID("p-crud"),
						agpayloadwrite.WithPayloadMimeType("text/plain"),
						agpayloadwrite.WithPayloadSizeBytes(7),
						agpayloadwrite.WithPayloadInlineBody([]byte("updated")),
					),
				}); err != nil {
					t.Fatalf("PatchPayloads(update) error: %v", err)
				}
			},
			readUpdated: func(t *testing.T, svc Service) {
				rows, err := svc.ListPayloadRows(ctx, &agpayload.PayloadRowsInput{Id: "p-crud", Has: &agpayload.PayloadRowsInputHas{Id: true}})
				if err != nil || len(rows) != 1 || rows[0].MimeType != "text/plain" || rows[0].SizeBytes != 7 {
					t.Fatalf("ListPayloadRows(update) err=%v rows=%#v", err, rows)
				}
			},
			remove: func(t *testing.T, svc Service) {
				if err := svc.DeletePayloads(ctx, "p-crud"); err != nil {
					t.Fatalf("DeletePayloads() error: %v", err)
				}
			},
			readRemoved: func(t *testing.T, svc Service) {
				rows, err := svc.ListPayloadRows(ctx, &agpayload.PayloadRowsInput{Id: "p-crud", Has: &agpayload.PayloadRowsInputHas{Id: true}})
				if err != nil {
					t.Fatalf("ListPayloadRows(delete) error: %v", err)
				}
				if len(rows) != 0 {
					t.Fatalf("expected payload deleted, got %#v", rows)
				}
			},
		},
		{
			name: "model call",
			create: func(t *testing.T, svc Service) {
				if _, err := svc.PatchModelCalls(ctx, []*agmodelcallwrite.MutableModelCallView{
					agmodelcallwrite.NewMutableModelCallView(
						agmodelcallwrite.WithModelCallMessageID("m-base"),
						agmodelcallwrite.WithModelCallTurnID("t-base"),
						agmodelcallwrite.WithModelCallProvider("openai"),
						agmodelcallwrite.WithModelCallModel("gpt-5-mini"),
						agmodelcallwrite.WithModelCallModelKind("chat"),
						agmodelcallwrite.WithModelCallStatus("completed"),
					),
				}); err != nil {
					t.Fatalf("PatchModelCalls(create) error: %v", err)
				}
			},
			readCreated: func(t *testing.T, svc Service) {
				conv := mustGetConversationWithModelCalls(t, ctx, svc, "c-base")
				msg := findMessage(conv.Transcript, "m-base")
				if msg == nil || msg.ModelCall == nil || msg.ModelCall.Status != "completed" {
					t.Fatalf("expected model call created, got %#v", msg)
				}
			},
			update: func(t *testing.T, svc Service) {
				if _, err := svc.PatchModelCalls(ctx, []*agmodelcallwrite.MutableModelCallView{
					agmodelcallwrite.NewMutableModelCallView(
						agmodelcallwrite.WithModelCallMessageID("m-base"),
						agmodelcallwrite.WithModelCallStatus("failed"),
					),
				}); err != nil {
					t.Fatalf("PatchModelCalls(update) error: %v", err)
				}
			},
			readUpdated: func(t *testing.T, svc Service) {
				conv := mustGetConversationWithModelCalls(t, ctx, svc, "c-base")
				msg := findMessage(conv.Transcript, "m-base")
				if msg == nil || msg.ModelCall == nil || msg.ModelCall.Status != "failed" {
					t.Fatalf("expected model call updated, got %#v", msg)
				}
			},
			remove: func(t *testing.T, svc Service) {
				if err := svc.DeleteModelCalls(ctx, "m-base"); err != nil {
					t.Fatalf("DeleteModelCalls() error: %v", err)
				}
			},
			readRemoved: func(t *testing.T, svc Service) {
				conv := mustGetConversationWithModelCalls(t, ctx, svc, "c-base")
				msg := findMessage(conv.Transcript, "m-base")
				if msg == nil || msg.ModelCall != nil {
					t.Fatalf("expected model call deleted, got %#v", msg)
				}
			},
		},
		{
			name: "tool call",
			create: func(t *testing.T, svc Service) {
				if _, err := svc.PatchMessages(ctx, []*agmessagewrite.MutableMessageView{
					agmessagewrite.NewMutableMessageView(
						agmessagewrite.WithMessageID("m-tool-crud"),
						agmessagewrite.WithMessageConversationID("c-base"),
						agmessagewrite.WithMessageTurnID("t-base"),
						agmessagewrite.WithMessageParentID("m-base"),
						agmessagewrite.WithMessageRole("assistant"),
						agmessagewrite.WithMessageType("tool_op"),
						agmessagewrite.WithMessageContent("tool"),
					),
				}); err != nil {
					t.Fatalf("PatchMessages(tool seed) error: %v", err)
				}
				if _, err := svc.PatchToolCalls(ctx, []*agtoolcallwrite.MutableToolCallView{
					agtoolcallwrite.NewMutableToolCallView(
						agtoolcallwrite.WithToolCallMessageID("m-tool-crud"),
						agtoolcallwrite.WithToolCallTurnID("t-base"),
						agtoolcallwrite.WithToolCallOpID("op-crud"),
						agtoolcallwrite.WithToolCallToolName("sql/query"),
						agtoolcallwrite.WithToolCallToolKind("mcp"),
						agtoolcallwrite.WithToolCallStatus("completed"),
					),
				}); err != nil {
					t.Fatalf("PatchToolCalls(create) error: %v", err)
				}
			},
			readCreated: func(t *testing.T, svc Service) {
				conv := mustGetConversationWithToolCalls(t, ctx, svc, "c-base")
				msg := findMessage(conv.Transcript, "m-base")
				toolMsg := findToolMessage(msg, "m-tool-crud")
				if toolMsg == nil || toolMsg.ToolCall == nil || toolMsg.ToolCall.Status != "completed" {
					t.Fatalf("expected tool call created, got %#v", msg)
				}
			},
			update: func(t *testing.T, svc Service) {
				if _, err := svc.PatchToolCalls(ctx, []*agtoolcallwrite.MutableToolCallView{
					agtoolcallwrite.NewMutableToolCallView(
						agtoolcallwrite.WithToolCallMessageID("m-tool-crud"),
						agtoolcallwrite.WithToolCallStatus("failed"),
						agtoolcallwrite.WithToolCallErrorMessage("tool failed"),
					),
				}); err != nil {
					t.Fatalf("PatchToolCalls(update) error: %v", err)
				}
			},
			readUpdated: func(t *testing.T, svc Service) {
				conv := mustGetConversationWithToolCalls(t, ctx, svc, "c-base")
				msg := findMessage(conv.Transcript, "m-base")
				toolMsg := findToolMessage(msg, "m-tool-crud")
				if toolMsg == nil || toolMsg.ToolCall == nil || toolMsg.ToolCall.Status != "failed" || toolMsg.ToolCall.ErrorMessage == nil || *toolMsg.ToolCall.ErrorMessage != "tool failed" {
					t.Fatalf("expected tool call updated, got %#v", msg)
				}
			},
			remove: func(t *testing.T, svc Service) {
				if err := svc.DeleteToolCalls(ctx, "m-tool-crud"); err != nil {
					t.Fatalf("DeleteToolCalls() error: %v", err)
				}
			},
			readRemoved: func(t *testing.T, svc Service) {
				conv := mustGetConversationWithToolCalls(t, ctx, svc, "c-base")
				msg := findMessage(conv.Transcript, "m-base")
				toolMsg := findToolMessage(msg, "m-tool-crud")
				if toolMsg == nil || toolMsg.ToolCall != nil {
					t.Fatalf("expected tool call deleted, got %#v", msg)
				}
			},
		},
		{
			name: "run",
			create: func(t *testing.T, svc Service) {
				if _, err := svc.PatchRuns(ctx, []*agrunwrite.MutableRunView{
					agrunwrite.NewMutableRunView(
						agrunwrite.WithRunID("run-crud"),
						agrunwrite.WithRunTurnID("t-base"),
						agrunwrite.WithRunConversationID("c-base"),
						agrunwrite.WithRunStatus("running"),
						agrunwrite.WithRunIteration(1),
					),
				}); err != nil {
					t.Fatalf("PatchRuns(create) error: %v", err)
				}
			},
			readCreated: func(t *testing.T, svc Service) {
				created, err := svc.GetRun(ctx, "run-crud", nil)
				if err != nil || created == nil {
					t.Fatalf("GetRun(create) err=%v value=%#v", err, created)
				}
			},
			update: func(t *testing.T, svc Service) {
				if _, err := svc.PatchRuns(ctx, []*agrunwrite.MutableRunView{
					agrunwrite.NewMutableRunView(
						agrunwrite.WithRunID("run-crud"),
						agrunwrite.WithRunStatus("completed"),
						agrunwrite.WithRunIteration(2),
					),
				}); err != nil {
					t.Fatalf("PatchRuns(update) error: %v", err)
				}
			},
			readUpdated: func(t *testing.T, svc Service) {
				updated, err := svc.GetRun(ctx, "run-crud", nil)
				if err != nil || updated == nil || updated.Status != "completed" || updated.Iteration != 2 {
					t.Fatalf("GetRun(update) err=%v value=%#v", err, updated)
				}
			},
			remove: func(t *testing.T, svc Service) {
				if err := svc.DeleteRuns(ctx, "run-crud"); err != nil {
					t.Fatalf("DeleteRuns() error: %v", err)
				}
			},
			readRemoved: func(t *testing.T, svc Service) {
				deleted, err := svc.GetRun(ctx, "run-crud", nil)
				if err != nil {
					t.Fatalf("GetRun(delete) error: %v", err)
				}
				if deleted != nil {
					t.Fatalf("expected nil after delete, got %#v", deleted)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc := newSeededService(t, seedForPatchBaseline)
			tc.create(t, svc)
			tc.readCreated(t, svc)
			tc.update(t, svc)
			tc.readUpdated(t, svc)
			tc.remove(t, svc)
			tc.readRemoved(t, svc)
		})
	}
}

func TestDataService_ConversationPermissions(t *testing.T) {
	ctx := context.Background()
	svc := newSeededService(t, seedForConversationPermissions)

	page, err := svc.ListConversations(ctx, &agconvlist.ConversationRowsInput{Has: &agconvlist.ConversationRowsInputHas{}}, &PageInput{Limit: 20}, WithPrincipal("u1"))
	if err != nil {
		t.Fatalf("ListConversations(u1) error: %v", err)
	}
	got := make([]string, 0, len(page.Rows))
	for _, row := range page.Rows {
		got = append(got, row.Id)
	}
	assertIDs(t, got, []string{"c-private-u1", "c-public-u2"})

	_, err = svc.GetConversation(ctx, "c-private-u2", nil, WithPrincipal("u1"))
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}

	shareable, err := svc.GetConversation(ctx, "c-share-u2", nil, WithPrincipal("u1"))
	if err != nil || shareable == nil {
		t.Fatalf("shareable conversation should be accessible by id, err=%v value=%#v", err, shareable)
	}
	if shareable.Shareable == nil || *shareable.Shareable != 1 {
		t.Fatalf("expected shareable flag to be set, got %#v", shareable.Shareable)
	}

	allowed, err := svc.GetConversation(ctx, "c-private-u2", nil, WithAdminPrincipal("admin"))
	if err != nil || allowed == nil {
		t.Fatalf("admin should access conversation, err=%v value=%#v", err, allowed)
	}
}

func TestDataService_ReadPermissions_MessageTurnRun(t *testing.T) {
	ctx := context.Background()
	svc := newSeededService(t, seedForPermissionReadArtifacts)

	if _, err := svc.GetMessage(ctx, "m-u2", nil, WithPrincipal("u1")); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected message permission denied, got %v", err)
	}
	if _, err := svc.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{ID: "t-u2", Has: &agturnbyid.TurnLookupInputHas{ID: true}}, WithPrincipal("u1")); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected turn permission denied, got %v", err)
	}
	if _, err := svc.GetRun(ctx, "run-u2", nil, WithPrincipal("u1")); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected run permission denied, got %v", err)
	}

	msg, err := svc.GetMessage(ctx, "m-u2", nil, WithAdminPrincipal("admin"))
	if err != nil || msg == nil {
		t.Fatalf("admin should access message, err=%v value=%#v", err, msg)
	}
}

func TestDataService_RunStepsPage(t *testing.T) {
	ctx := context.Background()
	svc := newSeededService(t, seedForPermissionReadArtifacts)

	page, err := svc.GetRunStepsPage(ctx, &agrunsteps.RunStepsInput{RunID: "run-u2", Has: &agrunsteps.RunStepsInputHas{RunID: true}}, &PageInput{Limit: 10}, WithAdminPrincipal("admin"))
	if err != nil {
		t.Fatalf("GetRunStepsPage() error: %v", err)
	}
	if page == nil || len(page.Rows) == 0 {
		t.Fatalf("expected non-empty run steps page")
	}
	if page.Rows[0].RunId == nil || *page.Rows[0].RunId != "run-u2" {
		t.Fatalf("unexpected run id in steps: %#v", page.Rows[0])
	}
	if _, err := svc.GetRunStepsPage(ctx, &agrunsteps.RunStepsInput{RunID: "run-u2", Has: &agrunsteps.RunStepsInputHas{RunID: true}}, &PageInput{Limit: 10}, WithPrincipal("u1")); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected run steps permission denied, got %v", err)
	}
}

func TestPatchDeleteHandlerContracts(t *testing.T) {
	ctx := context.Background()
	svc := newSeededService(t, seedForPatchBaseline)
	dao := svc.Raw()

	t.Run("patch wrong method", func(t *testing.T) {
		in := &agmessagewrite.Input{Messages: []*agmessagewrite.MutableMessageView{
			agmessagewrite.NewMutableMessageView(
				agmessagewrite.WithMessageID("m-contract"),
				agmessagewrite.WithMessageConversationID("c-base"),
				agmessagewrite.WithMessageTurnID("t-base"),
				agmessagewrite.WithMessageRole("assistant"),
				agmessagewrite.WithMessageType("text"),
				agmessagewrite.WithMessageContent("x"),
			),
		}}
		out := &agmessagewrite.Output{}
		_, err := dao.Operate(ctx,
			datly.WithPath(contract.NewPath("GET", agmessagewrite.PathURI)),
			datly.WithInput(in),
			datly.WithOutput(out),
		)
		if err == nil {
			t.Fatalf("expected method/path mismatch error")
		}
	})

	t.Run("patch invalid body", func(t *testing.T) {
		in := &agmessagewrite.Input{Messages: []*agmessagewrite.MutableMessageView{
			agmessagewrite.NewMutableMessageView(
				agmessagewrite.WithMessageID("m-contract-invalid"),
				agmessagewrite.WithMessageConversationID("c-base"),
				agmessagewrite.WithMessageType("text"),
			),
		}}
		out := &agmessagewrite.Output{}
		_, err := dao.Operate(ctx,
			datly.WithPath(contract.NewPath("PATCH", agmessagewrite.PathURI)),
			datly.WithInput(in),
			datly.WithOutput(out),
		)
		if err == nil {
			t.Fatalf("expected validation error for invalid patch body")
		}
	})

	t.Run("delete wrong method", func(t *testing.T) {
		in := &agmessagewrite.DeleteInput{Rows: []*agmessagewrite.MutableMessageView{
			agmessagewrite.NewMutableMessageView(agmessagewrite.WithMessageID("m-base")),
		}}
		out := &agmessagewrite.DeleteOutput{}
		_, err := dao.Operate(ctx,
			datly.WithPath(contract.NewPath("GET", agmessagewrite.PathURI)),
			datly.WithInput(in),
			datly.WithOutput(out),
		)
		if err == nil {
			t.Fatalf("expected method mismatch for delete contract")
		}
	})
}

func TestMutableViews_SettersMarkHas(t *testing.T) {
	cases := []struct {
		name string
		dst  interface{}
	}{
		{name: "conversation", dst: agconvwrite.NewMutableConversationView()},
		{name: "message", dst: agmessagewrite.NewMutableMessageView()},
		{name: "turn", dst: agturnwrite.NewMutableTurnView()},
		{name: "model_call", dst: agmodelcallwrite.NewMutableModelCallView()},
		{name: "tool_call", dst: agtoolcallwrite.NewMutableToolCallView()},
		{name: "payload", dst: agpayloadwrite.NewMutablePayloadView()},
		{name: "run", dst: agrunwrite.NewMutableRunView()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertSettersMarkHas(t, tc.dst)
		})
	}
}

type seedFn func(t *testing.T, db *sql.DB)

func newSeededService(t *testing.T, seeds ...seedFn) Service {
	t.Helper()
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-core-data-service")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)
	for _, seed := range seeds {
		seed(t, db)
	}

	ctx := context.Background()
	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New() error: %v", err)
	}
	connector := view.NewConnector("agently", "sqlite", dbPath)
	if err = dao.AddConnectors(ctx, connector); err != nil {
		t.Fatalf("AddConnectors() error: %v", err)
	}
	if err = registerReadComponents(ctx, dao); err != nil {
		t.Fatalf("registerReadComponents() error: %v", err)
	}
	return NewService(dao)
}

func seedForConversationPredicates(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at, agent_id, conversation_parent_id, conversation_parent_turn_id, schedule_id, schedule_run_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"c-main", "2026-01-01T09:00:00Z", "agent-1", "parent-conv-1", "parent-turn-1", "sch-1", "sch-run-1"}},
		{SQL: `INSERT INTO conversation (id, created_at, agent_id) VALUES (?, ?, ?)`, Params: []interface{}{"c-other", "2026-01-01T09:00:00Z", "agent-2"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-queued-main-1", "c-main", "2026-01-01T09:00:00Z", 10, "queued"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-anchor", "c-main", "2026-01-01T09:05:00Z", 20, "queued"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status, run_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"t-run-main", "c-main", "2026-01-01T09:10:00Z", 30, "running", "run-1"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status, run_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"t-wait-main", "c-main", "2026-01-01T09:11:00Z", 31, "waiting_for_user", "run-2"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-done-main", "c-main", "2026-01-01T09:12:00Z", 40, "completed"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-other-queued", "c-other", "2026-01-01T09:03:00Z", 5, "queued"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status, run_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"t-other-run", "c-other", "2026-01-01T09:04:00Z", 6, "running", "run-4"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim, elicitation_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-main", "c-main", "t-run-main", "2026-01-01T09:10:00Z", "assistant", "text", "main", 1, "elic-1"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, parent_message_id, created_at, role, type, content, interim, tool_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-main-tool-1", "c-main", "t-run-main", "m-main", "2026-01-01T09:10:20Z", "assistant", "tool_op", "tool-main", 1, "sql/query"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim, tool_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-orphan-tool", "c-main", "t-run-main", "2026-01-01T09:10:30Z", "assistant", "tool_op", "orphan-tool", 1, "shell/exec"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-anchor", "c-main", "t-anchor", "2026-01-01T09:05:10Z", "user", "text", "anchor", 0}},
		{SQL: `INSERT INTO schedule (id, name, agent_ref, enabled, timezone) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"sch-1", "schedule-1", "agent-1", 1, "UTC"}},
		{SQL: `INSERT INTO call_payload (id, tenant_id, kind, mime_type, size_bytes, digest, storage, inline_body, compression, created_at, redacted) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"p2", "tenant-1", "response", "application/json", 11, "dig-2", "s3", "body-2", "none", "2026-01-01 09:20:00", 0}},
		{SQL: `INSERT INTO model_call (message_id, turn_id, provider, model, model_kind, status, started_at, completed_at, run_id, iteration) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-main", "t-run-main", "openai", "gpt", "chat", "completed", "2026-01-01T09:10:00Z", "2026-01-01T09:10:01Z", "run-1", 1}},
		{SQL: `INSERT INTO tool_call (message_id, turn_id, op_id, attempt, tool_name, tool_kind, status, started_at, completed_at, response_payload_id, run_id, iteration) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-main-tool-1", "t-run-main", "op-main", 1, "sql/query", "mcp", "completed", "2026-01-01T09:10:00Z", "2026-01-01T09:10:02Z", "p2", "run-1", 1}},
		{SQL: `INSERT INTO tool_call (message_id, turn_id, op_id, attempt, tool_name, tool_kind, status, started_at, completed_at, run_id, iteration) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-orphan-tool", "t-run-main", "op-orphan", 1, "shell/exec", "mcp", "completed", "2026-01-01T09:10:31Z", "2026-01-01T09:10:33Z", "run-1", 1}},
		{SQL: `INSERT INTO run (id, turn_id, schedule_id, conversation_id, status, worker_id, worker_host, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-1", "t-run-main", "sch-1", "c-main", "running", "worker-1", "host-a", "2026-01-01 09:10:00", "2026-01-01 09:15:00", 1, "interactive"}},
		{SQL: `INSERT INTO run (id, turn_id, schedule_id, conversation_id, status, worker_id, worker_host, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-2", "t-run-main", "sch-1", "c-main", "queued", "worker-2", "host-b", "2026-01-01 09:11:00", nil, 1, "interactive"}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForMessageAndElicitation(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-main", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"t-run-main", "c-main", "2026-01-01T09:10:00Z", "running"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim, elicitation_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-main", "c-main", "t-run-main", "2026-01-01T09:10:00Z", "assistant", "text", "main", 1, "elic-1"}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForRunPredicates(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-main", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-other", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"t-run-main", "c-main", "2026-01-01T09:10:00Z", "running"}},
		{SQL: `INSERT INTO schedule (id, name, agent_ref, enabled, timezone) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"sch-1", "schedule-1", "agent-1", 1, "UTC"}},
		{SQL: `INSERT INTO run (id, turn_id, schedule_id, conversation_id, status, worker_id, worker_host, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-1", "t-run-main", "sch-1", "c-main", "running", "worker-1", "host-a", "2026-01-01 09:10:00", "2026-01-01 09:15:00", 1, "interactive"}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForActiveAndStaleRuns(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-main", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-other", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"t-run-main", "c-main", "2026-01-01T09:10:00Z", "running"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"t-other-run", "c-other", "2026-01-01T09:04:00Z", "running"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"t-anchor", "c-main", "2026-01-01T09:05:00Z", "queued"}},
		{SQL: `INSERT INTO schedule (id, name, agent_ref, enabled, timezone) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"sch-1", "schedule-1", "agent-1", 1, "UTC"}},
		{SQL: `INSERT INTO run (id, turn_id, schedule_id, conversation_id, status, worker_id, worker_host, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-1", "t-run-main", "sch-1", "c-main", "running", "worker-1", "host-a", "2026-01-01 09:10:00", "2026-01-01 09:15:00", 1, "interactive"}},
		{SQL: `INSERT INTO run (id, turn_id, schedule_id, conversation_id, status, worker_id, worker_host, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-2", "t-run-main", "sch-1", "c-main", "queued", "worker-2", "host-b", "2026-01-01 09:11:00", nil, 1, "interactive"}},
		{SQL: `INSERT INTO run (id, turn_id, conversation_id, status, worker_id, worker_host, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-4", "t-other-run", "c-other", "running", "worker-3", "host-a", "2026-01-01 09:13:00", "2026-01-01 09:50:00", 1, "interactive"}},
		{SQL: `INSERT INTO run (id, turn_id, conversation_id, status, worker_id, worker_host, created_at, last_heartbeat_at, iteration, conversation_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-5", "t-anchor", "c-main", "running", "worker-4", "host-b", "2026-01-01 09:08:00", nil, 1, "interactive"}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForTurnPredicates(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-main", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-other", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-queued-main-1", "c-main", "2026-01-01T09:00:00Z", 10, "queued"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-anchor", "c-main", "2026-01-01T09:05:00Z", 20, "queued"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status, run_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"t-run-main", "c-main", "2026-01-01T09:10:00Z", 30, "running", "run-1"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status, run_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"t-wait-main", "c-main", "2026-01-01T09:11:00Z", 31, "waiting_for_user", "run-2"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-other-queued", "c-other", "2026-01-01T09:03:00Z", 5, "queued"}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForToolCallAndPayloadPredicates(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-main", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-other", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"t-run-main", "c-main", "2026-01-01T09:10:00Z", "running"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"t-other-run", "c-other", "2026-01-01T09:04:00Z", "running"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-tool-main", "c-main", "t-run-main", "2026-01-01T09:10:20Z", "assistant", "tool_op", "tool-main", 1}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-tool-other", "c-other", "t-other-run", "2026-01-01T09:04:20Z", "assistant", "tool_op", "tool-other", 1}},
		{SQL: `INSERT INTO call_payload (id, tenant_id, kind, mime_type, size_bytes, digest, storage, inline_body, compression, created_at, redacted) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"p1", "tenant-1", "request", "text/plain", 10, "dig-1", "inline", "body-1", "none", "2026-01-01 09:00:00", 0}},
		{SQL: `INSERT INTO call_payload (id, tenant_id, kind, mime_type, size_bytes, digest, storage, inline_body, compression, created_at, redacted) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"p2", "tenant-1", "response", "application/json", 11, "dig-2", "s3", "body-2", "none", "2026-01-01 09:20:00", 0}},
		{SQL: `INSERT INTO call_payload (id, tenant_id, kind, mime_type, size_bytes, digest, storage, inline_body, compression, created_at, redacted) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"p3", "tenant-2", "request", "text/plain", 12, "dig-3", "inline", "body-3", "none", "2026-01-01 09:30:00", 0}},
		{SQL: `INSERT INTO tool_call (message_id, turn_id, op_id, attempt, tool_name, tool_kind, status, started_at, completed_at, response_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-tool-main", "t-run-main", "op-byop", 1, "sql/query", "mcp", "completed", "2026-01-01T09:10:20Z", "2026-01-01T09:10:22Z", "p2"}},
		{SQL: `INSERT INTO tool_call (message_id, turn_id, op_id, attempt, tool_name, tool_kind, status, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-tool-other", "t-other-run", "op-byop", 1, "shell/exec", "mcp", "completed", "2026-01-01T09:04:20Z", "2026-01-01T09:04:22Z"}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForQuerySelectorPagination(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at, agent_id) VALUES (?, ?, ?)`, Params: []interface{}{"c-main", "2026-01-01T09:00:00Z", "agent-1"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-queued-main-1", "c-main", "2026-01-01T09:00:00Z", 10, "queued"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-anchor", "c-main", "2026-01-01T09:05:00Z", 20, "queued"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-run-main", "c-main", "2026-01-01T09:10:00Z", 30, "running"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-main", "c-main", "t-run-main", "2026-01-01T09:10:00Z", "assistant", "text", "main", 1}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, parent_message_id, created_at, role, type, content, interim, tool_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-main-tool-1", "c-main", "t-run-main", "m-main", "2026-01-01T09:10:20Z", "assistant", "tool_op", "tool-main", 1, "sql/query"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, parent_message_id, created_at, role, type, content, interim, tool_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-main-tool-2", "c-main", "t-run-main", "m-main", "2026-01-01T09:10:22Z", "assistant", "tool_op", "tool-main-2", 1, "sql/query"}},
		{SQL: `INSERT INTO call_payload (id, tenant_id, kind, mime_type, size_bytes, digest, storage, inline_body, compression, created_at, redacted) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"p1", "tenant-1", "request", "text/plain", 10, "dig-1", "inline", "body-1", "none", "2026-01-01 09:00:00", 0}},
		{SQL: `INSERT INTO call_payload (id, tenant_id, kind, mime_type, size_bytes, digest, storage, inline_body, compression, created_at, redacted) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"p2", "tenant-1", "response", "application/json", 11, "dig-2", "s3", "body-2", "none", "2026-01-01 09:20:00", 0}},
		{SQL: `INSERT INTO tool_call (message_id, turn_id, op_id, attempt, tool_name, tool_kind, status, started_at, completed_at, response_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-main-tool-1", "t-run-main", "op-main", 1, "sql/query", "mcp", "completed", "2026-01-01T09:10:20Z", "2026-01-01T09:10:22Z", "p2"}},
		{SQL: `INSERT INTO tool_call (message_id, turn_id, op_id, attempt, tool_name, tool_kind, status, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-main-tool-2", "t-run-main", "op-main-2", 1, "sql/query", "mcp", "completed", "2026-01-01T09:10:23Z", "2026-01-01T09:10:24Z"}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForPagedReads(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at, agent_id, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"c-page-1", "2026-01-01T09:00:00Z", "agent-1", "active"}},
		{SQL: `INSERT INTO conversation (id, created_at, agent_id, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"c-page-2", "2026-01-01T10:00:00Z", "agent-1", "active"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-page-1", "c-page-1", "2026-01-01T09:05:00Z", 10, "running"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-page-2", "c-page-1", "2026-01-01T09:10:00Z", 20, "queued"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-page-3", "c-page-2", "2026-01-01T10:05:00Z", 30, "running"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim, phase, iteration) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-page-1-1", "c-page-1", "t-page-1", "2026-01-01T09:05:00Z", "user", "text", "u1", 0, "final", 1}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim, phase, iteration) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-page-1-2", "c-page-1", "t-page-1", "2026-01-01T09:06:00Z", "assistant", "text", "a1", 1, "pending", 1}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim, phase, iteration) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-page-1-3", "c-page-1", "t-page-1", "2026-01-01T09:07:00Z", "assistant", "text", "a2", 1, "final", 2}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim, phase, iteration) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-page-2-1", "c-page-2", "t-page-3", "2026-01-01T10:05:00Z", "user", "text", "u2", 0, "final", 1}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForPatchBaseline(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at, status, visibility) VALUES (?, ?, ?, ?)`, Params: []interface{}{"c-base", "2026-01-01T09:00:00Z", "active", "private"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"t-base", "c-base", "2026-01-01T09:01:00Z", "queued"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-base", "c-base", "t-base", "2026-01-01T09:02:00Z", "assistant", "text", "seed", 0}},
		{SQL: `INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, inline_body, compression, redacted, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"p-base", "request", "application/json", 2, "inline", "{}", "none", 0, "2026-01-01T09:03:00Z"}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForConversationPermissions(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at, status, visibility, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"c-private-u1", "2026-01-01T09:00:00Z", "active", "private", "u1"}},
		{SQL: `INSERT INTO conversation (id, created_at, status, visibility, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"c-private-u2", "2026-01-01T09:01:00Z", "active", "private", "u2"}},
		{SQL: `INSERT INTO conversation (id, created_at, status, visibility, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"c-public-u2", "2026-01-01T09:02:00Z", "active", "public", "u2"}},
		{SQL: `INSERT INTO conversation (id, created_at, status, visibility, shareable, created_by_user_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"c-share-u2", "2026-01-01T09:03:00Z", "active", "private", 1, "u2"}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForPermissionReadArtifacts(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at, status, visibility, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"c-u1", "2026-01-01T09:00:00Z", "active", "private", "u1"}},
		{SQL: `INSERT INTO conversation (id, created_at, status, visibility, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"c-u2", "2026-01-01T09:01:00Z", "active", "private", "u2"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"t-u2", "c-u2", "2026-01-01T09:02:00Z", "running"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-u2", "c-u2", "t-u2", "2026-01-01T09:03:00Z", "assistant", "text", "u2-msg", 0}},
		{SQL: `INSERT INTO run (id, turn_id, conversation_id, conversation_kind, attempt, status, iteration, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-u2", "t-u2", "c-u2", "interactive", 1, "running", 1, "2026-01-01T09:04:00Z"}},
		{SQL: `INSERT INTO model_call (message_id, turn_id, provider, model, model_kind, status, run_id, iteration, started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-u2", "t-u2", "openai", "gpt-5-mini", "chat", "completed", "run-u2", 1, "2026-01-01T09:04:30Z"}},
	}
	dbtest.ExecAll(t, db, items)
}

func assertSettersMarkHas(t *testing.T, dst interface{}) {
	t.Helper()
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		t.Fatalf("expected non-nil pointer, got %T", dst)
	}
	typ := v.Type()
	for i := 0; i < typ.NumMethod(); i++ {
		method := typ.Method(i)
		if !strings.HasPrefix(method.Name, "Set") {
			continue
		}
		if method.Type.NumIn() != 2 {
			continue
		}
		arg, ok := setterArgValue(method.Type.In(1))
		if !ok {
			continue
		}
		method.Func.Call([]reflect.Value{v, arg})

		hasField := v.Elem().FieldByName("Has")
		if !hasField.IsValid() || hasField.IsNil() {
			t.Fatalf("%s: setter %s did not initialize Has", typ.String(), method.Name)
		}
		flagName := strings.TrimPrefix(method.Name, "Set")
		flag := hasField.Elem().FieldByName(flagName)
		if !flag.IsValid() || flag.Kind() != reflect.Bool {
			continue
		}
		if !flag.Bool() {
			t.Fatalf("%s: setter %s did not set Has.%s", typ.String(), method.Name, flagName)
		}
	}
}

func setterArgValue(t reflect.Type) (reflect.Value, bool) {
	switch t.Kind() {
	case reflect.String:
		return reflect.ValueOf("x"), true
	case reflect.Int:
		return reflect.ValueOf(1), true
	case reflect.Int64:
		return reflect.ValueOf(int64(1)), true
	case reflect.Float64:
		return reflect.ValueOf(1.0), true
	case reflect.Bool:
		return reflect.ValueOf(true), true
	case reflect.Struct:
		if t.PkgPath() == "time" && t.Name() == "Time" {
			return reflect.ValueOf(time.Now()), true
		}
	}
	return reflect.Value{}, false
}

func findMessage(turns []*agconv.TranscriptView, messageID string) *agconv.MessageView {
	for _, turn := range turns {
		for _, msg := range turn.Message {
			if msg.Id == messageID {
				return msg
			}
		}
	}
	return nil
}

func mustGetConversationWithModelCalls(t *testing.T, ctx context.Context, svc Service, id string) *agconv.ConversationView {
	t.Helper()
	conv, err := svc.GetConversation(ctx, id, &agconv.ConversationInput{
		IncludeTranscript: true,
		IncludeModelCal:   true,
		Has: &agconv.ConversationInputHas{
			IncludeTranscript: true,
			IncludeModelCal:   true,
		},
	})
	if err != nil {
		t.Fatalf("GetConversation(model) error: %v", err)
	}
	return conv
}

func mustGetConversationWithToolCalls(t *testing.T, ctx context.Context, svc Service, id string) *agconv.ConversationView {
	t.Helper()
	conv, err := svc.GetConversation(ctx, id, &agconv.ConversationInput{
		IncludeTranscript: true,
		IncludeToolCall:   true,
		Has: &agconv.ConversationInputHas{
			IncludeTranscript: true,
			IncludeToolCall:   true,
		},
	})
	if err != nil {
		t.Fatalf("GetConversation(tool) error: %v", err)
	}
	return conv
}

func findToolMessage(msg *agconv.MessageView, toolMessageID string) *agconv.ToolMessageView {
	if msg == nil {
		return nil
	}
	for _, toolMsg := range msg.ToolMessage {
		if toolMsg != nil && toolMsg.Id == toolMessageID {
			return toolMsg
		}
	}
	return nil
}

func hasAnyToolCall(msg *agconv.MessageView) bool {
	if msg == nil {
		return false
	}
	for _, toolMsg := range msg.ToolMessage {
		if toolMsg != nil && toolMsg.ToolCall != nil {
			return true
		}
	}
	return false
}

func toolMessageIDs(items []*agconv.ToolMessageView) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result = append(result, item.Id)
	}
	return result
}

func transcriptIDs(items []*agconv.TranscriptView) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Id)
	}
	return result
}

func assertRunIDs(t *testing.T, items []*agrunstale.StaleRunsView, want []string) {
	t.Helper()
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.Id)
	}
	assertIDs(t, got, want)
}

func assertTurnIDsInOrder(t *testing.T, items []*agturnlist.QueuedTurnsView, want []string) {
	t.Helper()
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.Id)
	}
	if len(got) != len(want) {
		t.Fatalf("unexpected result size: got=%v want=%v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("unexpected result order: got=%v want=%v", got, want)
		}
	}
}

func assertToolCallMessageIDs(t *testing.T, items []*agtoolcall.ToolCallRowsView, want []string) {
	t.Helper()
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.MessageId)
	}
	assertIDs(t, got, want)
}

func assertIDs(t *testing.T, got []string, want []string) {
	t.Helper()
	gotSorted := append([]string(nil), got...)
	wantSorted := append([]string(nil), want...)
	sort.Strings(gotSorted)
	sort.Strings(wantSorted)
	if len(gotSorted) != len(wantSorted) {
		t.Fatalf("unexpected result size: got=%v want=%v", gotSorted, wantSorted)
	}
	for i := range gotSorted {
		if gotSorted[i] != wantSorted[i] {
			t.Fatalf("unexpected result ids: got=%v want=%v", gotSorted, wantSorted)
		}
	}
}

func mustTime(value string) time.Time {
	tm, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return tm.UTC()
}
