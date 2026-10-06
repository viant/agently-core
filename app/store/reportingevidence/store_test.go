package reportingevidence_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	evidence "github.com/viant/agently-core/app/store/reportingevidence"
	authctx "github.com/viant/agently-core/internal/auth"
	convstore "github.com/viant/agently-core/internal/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	runmodel "github.com/viant/agently-core/model/run"
	turnmodel "github.com/viant/agently-core/model/turn"
)

func TestImmutableEvidenceSurvivesCheckpointsRestartAndLeaseChange(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	workspace := t.TempDir()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	svc := data.NewService(server)
	c := conversationmodel.NewMutableConversationView(conversationmodel.WithConversationID("evidence-conversation"), conversationmodel.WithConversationStatus("active"))
	c.SetCreatedByUserID("owner")
	_, err = svc.PatchConversations(ctx, []*conversationmodel.MutableConversationView{c})
	require.NoError(t, err)
	turn := &turnmodel.MutableTurnView{}
	turn.SetId("evidence-turn")
	turn.SetConversationID(c.Id)
	turn.SetStatus("running")
	_, err = svc.PatchTurns(ctx, []*turnmodel.MutableTurnView{turn})
	require.NoError(t, err)
	r := &runmodel.MutableRunView{}
	r.SetId(turn.Id)
	r.SetTurnID(turn.Id)
	r.SetConversationID(c.Id)
	r.SetStatus("running")
	r.SetEffectiveUserID("owner")
	r.SetLeaseOwner("worker")
	r.SetLeaseUntil(time.Now().Add(time.Minute))
	r.SetCheckpointData(`{"ordinary":1}`)
	_, err = svc.PatchRuns(ctx, []*runmodel.MutableRunView{r})
	require.NoError(t, err)
	scope := evidence.Scope{OwnerID: "owner", ConversationID: c.Id, TurnID: turn.Id, RunID: r.Id}
	docs := evidence.New(server)
	body := json.RawMessage(`{"version":1,"selected":7366798}`)
	require.NoError(t, docs.Save(ctx, scope, evidence.Admission, "", "worker", body))
	require.NoError(t, docs.Save(ctx, scope, evidence.Admission, "", "worker", body))
	require.Error(t, docs.Save(ctx, scope, evidence.Admission, "", "worker", json.RawMessage(`{"version":1,"selected":7}`)))
	before, err := svc.GetRun(ctx, r.Id, nil)
	require.NoError(t, err)
	require.Equal(t, `{"ordinary":1}`, *before.CheckpointData)
	// Independent checkpoint writes and document reads touch different rows; neither
	// can overwrite the other's value even under the same native execution lease.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 12; i++ {
			p := &runmodel.MutableRunView{}
			p.SetId(r.Id)
			p.SetCheckpointData(fmt.Sprintf(`{"ordinary":%d}`, i))
			_, e := svc.PatchRuns(ctx, []*runmodel.MutableRunView{p})
			if e != nil {
				errs <- e
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 12; i++ {
			if e := docs.Save(ctx, scope, evidence.Plan, fmt.Sprintf("concurrent-%d", i), "worker", body); e != nil {
				errs <- e
				return
			}
			b, e := docs.Load(ctx, scope, evidence.Admission, "")
			if e != nil {
				errs <- e
				return
			}
			if !json.Valid(b) {
				errs <- fmt.Errorf("invalid body")
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	loaded, err := docs.Load(ctx, scope, evidence.Admission, "")
	require.NoError(t, err)
	require.JSONEq(t, string(body), string(loaded))
	after, err := svc.GetRun(ctx, r.Id, nil)
	require.NoError(t, err)
	require.Equal(t, `{"ordinary":11}`, *after.CheckpointData)
	foreign := scope
	foreign.OwnerID = "foreign"
	_, err = docs.Load(ctx, foreign, evidence.Admission, "")
	require.Error(t, err)
	wrongTurn := scope
	wrongTurn.TurnID = "other"
	_, err = docs.Load(ctx, wrongTurn, evidence.Admission, "")
	require.Error(t, err)
	require.NoError(t, server.Shutdown(ctx))
	restarted, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer restarted.Shutdown(ctx)
	docs = evidence.New(restarted)
	loaded, err = docs.Load(ctx, scope, evidence.Admission, "")
	require.NoError(t, err)
	require.JSONEq(t, string(body), string(loaded))
	svc = data.NewService(restarted)
	lease := &runmodel.MutableRunView{}
	lease.SetId(r.Id)
	lease.SetLeaseOwner("replacement")
	_, err = svc.PatchRuns(ctx, []*runmodel.MutableRunView{lease})
	require.NoError(t, err)
	require.Error(t, docs.Save(ctx, scope, evidence.Plan, "plan", "worker", body))
	require.NoError(t, docs.Save(ctx, scope, evidence.Plan, "plan", "replacement", body))
	terminal := &runmodel.MutableRunView{}
	terminal.SetId(r.Id)
	terminal.SetStatus("completed")
	_, err = svc.PatchRuns(ctx, []*runmodel.MutableRunView{terminal})
	require.NoError(t, err)
	require.Error(t, docs.Save(ctx, scope, evidence.Plan, "late", "replacement", body))
	_, err = docs.Load(ctx, scope, evidence.Plan, "plan")
	require.NoError(t, err)
	payloads := &convstore.PayloadStore{Invoker: restarted}
	admissionID := evidence.ID(scope, evidence.Admission, "")
	require.NoError(t, payloads.DeleteUnreferencedTrusted(ctx, admissionID))
	retained, err := payloads.Get(ctx, admissionID)
	require.NoError(t, err)
	require.NotNil(t, retained)
	require.NoError(t, svc.DeleteRuns(ctx, r.Id))
	removed, err := payloads.Get(ctx, admissionID)
	require.NoError(t, err)
	require.Nil(t, removed, "provisioned native connections must cascade run-owned payloads")
}
