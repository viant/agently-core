package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	messagemodel "github.com/viant/agently-core/model/message"
	runmodel "github.com/viant/agently-core/model/run"
	turnmodel "github.com/viant/agently-core/model/turn"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/reactor"
)

type continuationStore struct {
	data.Service
	mu        sync.Mutex
	row       runmodel.RunRowsView
	claims    int
	loseClaim bool
}

func (s *continuationStore) GetRun(context.Context, string, *runmodel.RunRowsInput, ...data.Option) (*runmodel.RunRowsView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.row
	return &row, nil
}
func (s *continuationStore) PatchConversations(_ context.Context, rows []*conversationmodel.MutableConversationView) ([]*conversationmodel.MutableConversationView, error) {
	return rows, nil
}
func (s *continuationStore) GetNextQueuedTurn(context.Context, *turnmodel.QueuedTurnInput, ...data.Option) (*turnmodel.QueuedTurnView, error) {
	return nil, nil
}
func (s *continuationStore) GetMessagesPage(context.Context, *messagemodel.MessageRowsInput, *data.PageInput, ...data.Option) (*data.MessagePage, error) {
	return &data.MessagePage{}, nil
}
func (s *continuationStore) PatchRuns(_ context.Context, rows []*runmodel.MutableRunView) ([]*runmodel.MutableRunView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, patch := range rows {
		if cond := patch.Condition; cond != nil {
			if cond.Status != "" && s.row.Status != cond.Status {
				continue
			}
			if cond.Attempt != nil && s.row.Attempt != *cond.Attempt {
				continue
			}
			if cond.LeaseOwner != nil && (s.row.LeaseOwner == nil || *s.row.LeaseOwner != *cond.LeaseOwner) {
				continue
			}
			if cond.Status == "completed" {
				s.claims++
				if s.loseClaim {
					continue
				}
			}
		}
		if patch.Has.Status {
			s.row.Status = patch.Status
		}
		if patch.Has.LeaseOwner {
			s.row.LeaseOwner = patch.LeaseOwner
		}
		if patch.Has.LeaseUntil {
			s.row.LeaseUntil = patch.LeaseUntil
		}
		if patch.Has.Attempt {
			s.row.Attempt = *patch.Attempt
		}
		if patch.Has.Iteration {
			s.row.Iteration = *patch.Iteration
		}
	}
	return rows, nil
}

func continuationFixture(t *testing.T) (*Service, *QueryInput, *continuationStore, *singleResponseFinder) {
	t.Helper()
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user"})
	memory := convmem.New()
	conv := apiconv.NewConversation()
	conv.SetId("resume-conv")
	conv.SetVisibility("public")
	require.NoError(t, memory.PatchConversations(ctx, conv))
	turn := apiconv.NewTurn()
	turn.SetId("resume-turn")
	turn.SetConversationID("resume-conv")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, memory.PatchTurn(ctx, turn))
	conversationID, agentID, userID, owner := "resume-conv", "agent", "user", "old-owner"
	store := &continuationStore{row: runmodel.RunRowsView{Id: "resume-turn", Status: "completed", Iteration: 2, Attempt: 1, ConversationId: &conversationID, AgentId: &agentID, EffectiveUserId: &userID, LeaseOwner: &owner}}
	finder := &singleResponseFinder{content: "resumed"}
	llmSvc := core.New(finder, nil, memory)
	agent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "agent"}, ModelSelection: llm.ModelSelection{Model: "mock"}, Prompt: &binding.Prompt{Text: "continue"}}
	service := &Service{conversation: memory, dataService: store, llm: llmSvc, orchestrator: reactor.New(llmSvc, nil, memory, nil, nil), agentFinder: &allAgentFinder{items: []*agentmdl.Agent{agent}}, defaults: &config.Defaults{}}
	return service, &QueryInput{ConversationID: "resume-conv", UserId: "user"}, store, finder
}

func TestResumeContinuationRejectsWrongIdentityIterationAndLostClaim(t *testing.T) {
	for _, name := range []string{"identity", "iteration", "claim"} {
		t.Run(name, func(t *testing.T) {
			svc, input, store, finder := continuationFixture(t)
			iteration := 3
			switch name {
			case "identity":
				input.UserId = "other"
			case "iteration":
				iteration = 9
			case "claim":
				store.loseClaim = true
			}
			err := svc.ResumeContinuation(context.Background(), input, "resume-turn", iteration, &QueryOutput{})
			require.Error(t, err)
			require.EqualValues(t, 0, finder.calls.Load())
		})
	}
}

func TestResumeContinuationReusesTurnAndContinuesAtNextModelIteration(t *testing.T) {
	svc, input, store, finder := continuationFixture(t)
	output := &QueryOutput{}
	require.NoError(t, svc.ResumeContinuation(context.Background(), input, "resume-turn", 0, output))
	require.Equal(t, "resume-turn", output.TurnID)
	require.Equal(t, "resumed", output.Content)
	require.EqualValues(t, 1, finder.calls.Load())
	require.Equal(t, 1, store.claims)
	require.Equal(t, 3, store.row.Iteration)
	require.Equal(t, "succeeded", store.row.Status)
	conv, err := svc.conversation.GetConversation(context.Background(), "resume-conv", apiconv.WithIncludeTranscript(true))
	require.NoError(t, err)
	require.Len(t, conv.GetTranscript(), 1)
}

func TestContinuationClaimUsesRealDatlyWriterShapes(t *testing.T) {
	ctx := context.Background()
	native, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	conversation := conversationmodel.NewMutableConversationView(conversationmodel.WithConversationID("native-continuation"))
	_, err = native.PatchConversations(ctx, []*conversationmodel.MutableConversationView{conversation})
	require.NoError(t, err)
	turn := apiconv.NewTurn()
	turn.SetId("native-turn")
	turn.SetConversationID("native-continuation")
	turn.SetStatus("waiting_for_user")
	_, err = native.PatchTurns(ctx, []*turnmodel.MutableTurnView{turn})
	require.NoError(t, err)
	row := &runmodel.MutableRunView{}
	row.SetId("native-turn")
	row.SetTurnID("native-turn")
	row.SetConversationID("native-continuation")
	row.SetStatus("completed")
	row.SetAttempt(1)
	row.SetIteration(2)
	row.SetLeaseOwner("previous-owner")
	row.SetCompletedAt(time.Now())
	_, err = native.PatchRuns(ctx, []*runmodel.MutableRunView{row})
	require.NoError(t, err)
	observed, err := native.GetRun(ctx, "native-turn", nil)
	require.NoError(t, err)
	require.NotNil(t, observed)
	service := &Service{dataService: native}
	owner, err := service.claimContinuationRun(ctx, observed)
	require.NoError(t, err)
	reopened, err := native.GetRun(ctx, "native-turn", nil)
	require.NoError(t, err)
	require.Equal(t, "running", reopened.Status)
	require.Equal(t, owner, *reopened.LeaseOwner)
	require.Equal(t, 2, reopened.Attempt)
	require.Nil(t, reopened.CompletedAt)
	_, err = service.claimContinuationRun(ctx, observed)
	require.ErrorContains(t, err, "claim lost")
	after, err := native.GetRun(ctx, "native-turn", nil)
	require.NoError(t, err)
	require.Equal(t, owner, *after.LeaseOwner)
	require.Equal(t, 2, after.Attempt)
}
