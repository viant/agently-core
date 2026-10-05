package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	authctx "github.com/viant/agently-core/internal/auth"
	eventdelete "github.com/viant/agently-core/internal/datly/agui/cleanup/event/delete"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"reflect"
	"testing"
	"time"
)

func aguiDeleteFixture(t *testing.T) (Service, *sql.DB, *aguistore.ComponentStore) {
	t.Helper()
	service, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		_, err := db.Exec(`INSERT INTO conversation(id,status,created_by_user_id) VALUES('agui-root','completed','u1'),('agui-foreign','completed','u2');
 INSERT INTO conversation(id,status,created_by_user_id,conversation_parent_id) VALUES('agui-child','completed','u1','agui-root');
 INSERT INTO turn(id,conversation_id,status) VALUES('root-turn','agui-root','completed'),('child-turn','agui-child','completed'),('foreign-turn','agui-foreign','completed');
 INSERT INTO run(id,conversation_id,turn_id,status) VALUES('shared-external','agui-foreign','foreign-turn','completed');
 INSERT INTO call_payload(id,tenant_id,kind,mime_type,size_bytes,storage,inline_body,compression) VALUES('unrelated-payload','u2','request','application/json',2,'inline','{}','none');`)
		require.NoError(t, err)
	})
	return service, db, aguistore.New(service.(*datlyService).native)
}
func aguiDeleteSeedRun(t *testing.T, store *aguistore.ComponentStore, thread, principal string, eventCount int, terminal bool) *aguistore.Run {
	t.Helper()
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: principal})
	input, _ := json.Marshal(map[string]any{"threadId": thread, "runId": "shared-external", "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "operation": "capabilities"}}})
	record, _, err := store.Admit(ctx, aguistore.Admission{ThreadID: thread, RunID: "shared-external", Principal: principal, Input: input})
	require.NoError(t, err)
	record, err = store.Claim(ctx, principal, thread, record.RunID, record.Revision, "fixture-owner", time.Hour)
	require.NoError(t, err)
	if !terminal {
		return record
	}
	events := []json.RawMessage{}
	start, _ := json.Marshal(map[string]any{"type": "RUN_STARTED", "threadId": thread, "runId": record.RunID, "protocolVersion": "1.0"})
	events = append(events, start)
	for i := 0; i < eventCount; i++ {
		event, _ := json.Marshal(map[string]any{"type": "CUSTOM", "name": "fixture.counter", "value": i})
		events = append(events, event)
	}
	finish, _ := json.Marshal(map[string]any{"type": "RUN_FINISHED", "threadId": thread, "runId": record.RunID, "outcome": map[string]any{"type": "success"}})
	events = append(events, finish)
	record, err = store.Append(ctx, principal, thread, record.RunID, record.Revision, events, &aguistore.Change{LeaseOwner: "fixture-owner"})
	require.NoError(t, err)
	return record
}
func TestDeleteConversationTree_AGUIRecordsFollowThreadScopeNotRunID(t *testing.T) {
	service, db, store := aguiDeleteFixture(t)
	root := aguiDeleteSeedRun(t, store, "agui-root", "u1", 1030, true)
	child := aguiDeleteSeedRun(t, store, "agui-child", "u1", 1, true)
	foreign := aguiDeleteSeedRun(t, store, "agui-foreign", "u2", 1, true)
	journalRows, err := db.Query("SELECT p.id FROM call_payload p JOIN run r ON r.id=p.run_id WHERE r.conversation_id IN ('agui-root','agui-child') AND r.run_kind='agui' AND p.kind='agui.event'")
	require.NoError(t, err)
	journalIDs := []string{}
	for journalRows.Next() {
		var id string
		require.NoError(t, journalRows.Scan(&id))
		journalIDs = append(journalIDs, id)
	}
	require.NoError(t, journalRows.Err())
	require.NoError(t, journalRows.Close())
	require.Len(t, journalIDs, 1035)
	require.NoError(t, service.DeleteConversationTree(deleteTestContext(), "agui-root"))
	for _, id := range journalIDs {
		assertStage1RowCount(t, db, "call_payload", "id", id, 0)
	}
	assertStage1RowCount(t, db, "call_payload", "id", "unrelated-payload", 1)
	for _, thread := range []string{root.ThreadID, child.ThreadID} {
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE conversation_id=? AND run_kind='agui'", thread).Scan(&count))
		require.Zero(t, count)
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM call_payload p JOIN run r ON r.id=p.run_id WHERE r.conversation_id=? AND p.kind='agui.event'", thread).Scan(&count))
		require.Zero(t, count)
	}
	var leases int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE run_kind='agui'").Scan(&leases))
	require.Equal(t, 1, leases)
	assertStage1RowCount(t, db, "conversation", "id", "agui-root", 0)
	assertStage1RowCount(t, db, "conversation", "id", "agui-child", 0)
	assertStage1RowCount(t, db, "run", "id", "shared-external", 1)
	foreignCtx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u2"})
	saved, err := store.GetRun(foreignCtx, "u2", foreign.ThreadID, foreign.RunID)
	require.NoError(t, err)
	require.Equal(t, foreign.LastSequence, saved.LastSequence)
	events, err := store.Replay(foreignCtx, "u2", foreign.ThreadID, foreign.RunID, 0, 100)
	require.NoError(t, err)
	require.Len(t, events, 3)
}
func TestDeleteConversationTree_AGUIRollbackRestoresAllProtocolRecords(t *testing.T) {
	service, db, store := aguiDeleteFixture(t)
	root := aguiDeleteSeedRun(t, store, "agui-root", "u1", 1, true)
	aguiDeleteSeedRun(t, store, "agui-child", "u1", 1, true)
	var originalLeaseCount int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE run_kind='agui' AND protocol_lease_owner IS NOT NULL").Scan(&originalLeaseCount))
	_, err := db.Exec(`CREATE TRIGGER reject_agui_native_delete BEFORE DELETE ON conversation WHEN OLD.id='agui-root' BEGIN SELECT RAISE(ABORT,'late conversation deletion fixture'); END`)
	require.NoError(t, err)
	require.Error(t, service.DeleteConversationTree(deleteTestContext(), "agui-root"))
	for _, thread := range []string{"agui-root", "agui-child"} {
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE conversation_id=? AND run_kind='agui'", thread).Scan(&count))
		require.Equal(t, 1, count)
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM call_payload p JOIN run r ON r.id=p.run_id WHERE r.conversation_id=? AND p.kind='agui.event'", thread).Scan(&count))
		require.Equal(t, 3, count)
		assertStage1RowCount(t, db, "conversation", "id", thread, 1)
	}
	var leases int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE run_kind='agui' AND protocol_lease_owner IS NOT NULL").Scan(&leases))
	require.Equal(t, originalLeaseCount, leases)
	saved, err := store.GetRun(deleteTestContext(), "u1", root.ThreadID, root.RunID)
	require.NoError(t, err)
	require.Equal(t, root.LastSequence, saved.LastSequence)
}
func TestDeleteConversationTree_AGUIResourceObserverLeaseBlocksThenFences(t *testing.T) {
	service, db, store := aguiDeleteFixture(t)
	record := aguiDeleteSeedRun(t, store, "agui-root", "u1", 0, false)
	require.Empty(t, record.TurnID, "resource observer has no native execution turn")
	require.ErrorIs(t, service.DeleteConversationTree(deleteTestContext(), "agui-root"), ErrConversationActive)
	assertStage1RowCount(t, db, "run", "conversation_id", "agui-root", 1)
	_, err := db.Exec("UPDATE run SET protocol_lease_until=? WHERE run_kind='agui'", time.Now().Add(-time.Hour).UTC())
	require.NoError(t, err)
	require.NoError(t, service.DeleteConversationTree(deleteTestContext(), "agui-root"))
	_, err = store.Renew(deleteTestContext(), "u1", record.ThreadID, record.RunID, record.LeaseRevision, "fixture-owner", time.Minute)
	require.Error(t, err, "deleted lease must fence stale observer")
	_, err = store.Claim(deleteTestContext(), "u1", record.ThreadID, record.RunID, record.Revision, "new-worker", time.Minute)
	require.Error(t, err, "deleted admission cannot be recovered or claimed")
}

func TestDeleteConversationTree_AGUIAdmittedWithoutLeaseIsFenced(t *testing.T) {
	service, db, store := aguiDeleteFixture(t)
	input := json.RawMessage(`{"threadId":"agui-root","runId":"not-a-native-run","messages":[],"forwardedProps":{"agently":{"version":"1","operation":"capabilities"}}}`)
	record, _, err := store.Admit(deleteTestContext(), aguistore.Admission{ThreadID: "agui-root", RunID: "not-a-native-run", Principal: "u1", Input: input})
	require.NoError(t, err)
	require.Nil(t, record.LeaseUntil)
	require.NoError(t, service.DeleteConversationTree(deleteTestContext(), "agui-root"))
	assertStage1RowCount(t, db, "run", "conversation_id", "agui-root", 0)
	_, err = store.Claim(deleteTestContext(), "u1", record.ThreadID, record.RunID, record.Revision, "late-worker", time.Minute)
	require.Error(t, err)
}

func TestAGUIConversationCleanupWriterRejectsUnscopedAndUnmarkedDeletes(t *testing.T) {
	service, db, store := aguiDeleteFixture(t)
	root := aguiDeleteSeedRun(t, store, "agui-root", "u1", 1, true)
	foreign := aguiDeleteSeedRun(t, store, "agui-foreign", "u2", 1, true)
	var foreignKey string
	require.NoError(t, db.QueryRow("SELECT p.id FROM call_payload p JOIN run r ON r.id=p.run_id WHERE r.conversation_id='agui-foreign' AND r.run_kind='agui' AND p.kind='agui.event' ORDER BY p.sequence LIMIT 1").Scan(&foreignKey))
	target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[eventdelete.Input]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/ag-ui/cleanup/event/delete"}}
	row := &eventdelete.EventDelete{}
	row.SetEventKey(foreignKey)
	row.SetShouldDelete(true)
	input := &eventdelete.Input{}
	input.SetThreadIDs([]string{"agui-root"})
	var rootKey, foreignRunKey string
	require.NoError(t, db.QueryRow("SELECT id FROM run WHERE conversation_id=? AND run_kind='agui' AND protocol_run_id=?", root.ThreadID, root.RunID).Scan(&rootKey))
	require.NoError(t, db.QueryRow("SELECT id FROM run WHERE conversation_id=? AND run_kind='agui' AND protocol_run_id=?", foreign.ThreadID, foreign.RunID).Scan(&foreignRunKey))
	input.SetRunKeys([]string{rootKey})
	input.SetRows([]*eventdelete.EventDelete{row})
	_, err := service.(*datlyService).native.InvokeComponent(deleteTestContext(), dexec.ComponentRequest{Target: target, Input: input})
	require.Error(t, err, "private scope capability is required")
	scope := provider.Named("conversationtreescope", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		return true, name == "internal", nil
	})
	_, err = service.(*datlyService).native.InvokeComponent(deleteTestContext(), dexec.ComponentRequest{Target: target, Input: input, Providers: []locator.Provider{scope}})
	require.Error(t, err, "foreign event is outside authorized thread scope")
	input.SetThreadIDs([]string{"agui-foreign"})
	input.SetRunKeys([]string{foreignRunKey})
	row.SetShouldDelete(false)
	_, err = service.(*datlyService).native.InvokeComponent(deleteTestContext(), dexec.ComponentRequest{Target: target, Input: input, Providers: []locator.Provider{scope}})
	require.Error(t, err, "cleanup writer cannot perform updates or implicit deletion")
	for _, thread := range []string{"agui-root", "agui-foreign"} {
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM call_payload p JOIN run r ON r.id=p.run_id WHERE r.conversation_id=? AND r.run_kind='agui' AND p.kind='agui.event'", thread).Scan(&count))
		require.Equal(t, 3, count)
	}
}
