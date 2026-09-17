package data

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	agconvwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	agrunstale "github.com/viant/agently-core/pkg/agently/run/stale"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	agturnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
)

func TestDataService_RunRecoveryClaimMySQLSmoke(t *testing.T) {
	rawDSN := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if rawDSN == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	cfg, err := mysql.ParseDSN(rawDSN)
	if err != nil {
		t.Fatalf("parse MySQL DSN: %v", err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	dsn := cfg.FormatDSN()
	ctx := context.Background()
	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New: %v", err)
	}
	if err = dao.AddConnectors(ctx, view.NewConnector("agently", "mysql", dsn)); err != nil {
		t.Fatalf("add MySQL connector: %v", err)
	}
	if err = registerReadComponents(ctx, dao); err != nil {
		t.Fatalf("register Datly components: %v", err)
	}
	svc := NewService(dao)
	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	conversationID := "run-recovery-mysql-conv-" + suffix
	turnID := "run-recovery-mysql-turn-" + suffix
	owner := "run-recovery-owner-" + suffix
	now := time.Now().UTC().Truncate(time.Second)
	leaseUntil := now.Add(-time.Minute)
	t.Cleanup(func() {
		_ = svc.DeleteRuns(context.Background(), turnID)
		_ = svc.DeleteTurns(context.Background(), turnID)
		_ = svc.DeleteConversations(context.Background(), conversationID)
	})
	conversation := agconvwrite.NewMutableConversationView(agconvwrite.WithConversationID(conversationID), agconvwrite.WithConversationStatus("running"))
	conversation.SetCreatedAt(now.Add(-2 * time.Minute))
	if _, err = svc.PatchConversations(ctx, []*agconvwrite.MutableConversationView{conversation}); err != nil {
		t.Fatalf("patch MySQL conversation: %v", err)
	}
	turn := agturnwrite.NewMutableTurnView(agturnwrite.WithTurnID(turnID), agturnwrite.WithTurnConversationID(conversationID), agturnwrite.WithTurnStatus("running"), agturnwrite.WithTurnRunID(turnID))
	turn.SetCreatedAt(now.Add(-2 * time.Minute))
	if _, err = svc.PatchTurns(ctx, []*agturnwrite.MutableTurnView{turn}); err != nil {
		t.Fatalf("patch MySQL turn: %v", err)
	}
	run := agrunwrite.NewMutableRunView(agrunwrite.WithRunID(turnID), agrunwrite.WithRunTurnID(turnID), agrunwrite.WithRunConversationID(conversationID), agrunwrite.WithRunStatus("running"), agrunwrite.WithRunIteration(1))
	run.SetConversationKind("interactive")
	run.SetAttempt(1)
	run.SetLeaseOwner(owner)
	run.SetLeaseUntil(leaseUntil)
	run.SetLastHeartbeatAt(now.Add(-90 * time.Second))
	run.SetCreatedAt(now.Add(-2 * time.Minute))
	if _, err = svc.PatchRuns(ctx, []*agrunwrite.MutableRunView{run}); err != nil {
		t.Fatalf("patch MySQL run: %v", err)
	}
	rows, err := svc.ListStaleRuns(ctx, &agrunstale.StaleRunsInput{
		HeartbeatBefore: now.Add(-time.Minute), LeaseExpiredBefore: now, ActivityAfter: now.Add(-24 * time.Hour),
		ConversationKind: "interactive", RootInteractive: true,
		Has: &agrunstale.StaleRunsInputHas{HeartbeatBefore: true, LeaseExpiredBefore: true, ActivityAfter: true, ConversationKind: true, RootInteractive: true},
	})
	if err != nil {
		t.Fatalf("list MySQL recovery candidates: %v", err)
	}
	found := false
	for _, row := range rows {
		if row != nil && row.Id == turnID {
			found = true
		}
	}
	if !found {
		t.Fatalf("MySQL stale view did not return %s", turnID)
	}

	claimA := claimRow(turnID, owner, "pod-a-"+suffix, 1, now)
	if _, err = svc.PatchRuns(ctx, []*agrunwrite.MutableRunView{claimA}); err != nil {
		t.Fatalf("MySQL claim A: %v", err)
	}
	claimB := claimRow(turnID, owner, "pod-b-"+suffix, 1, now)
	if _, err = svc.PatchRuns(ctx, []*agrunwrite.MutableRunView{claimB}); err != nil {
		t.Fatalf("MySQL claim B: %v", err)
	}
	got, err := svc.GetRun(ctx, turnID, nil)
	if err != nil || got == nil {
		t.Fatalf("read MySQL claimed run: run=%v err=%v", got, err)
	}
	if got.LeaseOwner == nil || *got.LeaseOwner != *claimA.LeaseOwner || got.Attempt != 2 {
		t.Fatalf("MySQL one-winner claim failed: owner=%v attempt=%d", got.LeaseOwner, got.Attempt)
	}
	if got.LeaseUntil == nil || !got.LeaseUntil.UTC().Truncate(time.Second).Equal(claimA.LeaseUntil.UTC().Truncate(time.Second)) {
		t.Fatalf("MySQL UTC second lease mismatch: got=%v want=%v", got.LeaseUntil, claimA.LeaseUntil)
	}
}
