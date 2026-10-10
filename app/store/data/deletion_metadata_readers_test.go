package data

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	message "github.com/viant/agently-core/internal/datly/message/read"
	model "github.com/viant/agently-core/internal/datly/modelcall/read"
	tool "github.com/viant/agently-core/internal/datly/toolcall/read"
	turnmeta "github.com/viant/agently-core/internal/datly/turn/cleanup/read"
	turn "github.com/viant/agently-core/internal/datly/turn/read"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	xexec "github.com/viant/xdatly/exec"
)

func TestDeletionMetadataInvalidConfigurationDoesNotMutate(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedForConversationTreeDelete)
	t.Setenv(tree.MetadataReaderEnvironment, "invalid")
	err := svc.DeleteConversationTree(deleteTestContext(), "conv-root")
	require.ErrorContains(t, err, tree.MetadataReaderEnvironment)
	assertStage1RowCount(t, db, "conversation", "id", "conv-root", 1)
	assertStage1RowCount(t, db, "message", "id", "msg-root", 1)
	row, err := svc.GetConversation(deleteTestContext(), "conv-root", nil)
	require.NoError(t, err)
	require.NotNil(t, row)
}

func TestDeletionMetadataReadersBoundedProjection(t *testing.T) {
	svc, _ := newSeededServiceWithDB(t, seedForConversationTreeDelete)
	invoker := svc.(*datlyService).native
	calls := map[string]func(context.Context) (any, error){
		"turn": func(ctx context.Context) (any, error) {
			q := &turn.TurnRowsInput{}
			q.SetConversationIDs([]string{"conv-root"})
			return tree.ReadCleanupTurns(ctx, invoker, q, false)
		},
		"message": func(ctx context.Context) (any, error) {
			q := &message.MessagesInput{}
			q.SetConversationIds([]string{"conv-root"})
			return tree.ReadCleanupMessages(ctx, invoker, q, false)
		},
		"modelcall": func(ctx context.Context) (any, error) {
			q := &model.ModelCallsInput{}
			q.SetTurnIds([]string{"turn-root"})
			return tree.ReadCleanupModels(ctx, invoker, q, false)
		},
		"toolcall": func(ctx context.Context) (any, error) {
			q := &tool.ToolCallsInput{}
			q.SetTurnIds([]string{"turn-root"})
			return tree.ReadCleanupTools(ctx, invoker, q, false)
		},
	}
	for kind, call := range calls {
		t.Run(kind, func(t *testing.T) {
			metric := xexec.New()
			rows, err := call(xexec.WithContext(context.Background(), metric))
			require.NoError(t, err)
			require.Greater(t, reflect.ValueOf(rows).Len(), 0)
			sample := cleanupMetadataSQL(t, metric)
			for _, forbidden := range []string{"t.*", "content", "checkpoint", "error_message", "security_context", "provider_payload", "request_json", "response_json"} {
				require.NotContains(t, strings.ToLower(sample.SQL), forbidden)
			}
		})
	}
	tq := &turn.TurnRowsInput{}
	tq.SetConversationIDs([]string{"conv-root"})
	tr, err := tree.ReadCleanupTurns(context.Background(), invoker, tq, false)
	require.NoError(t, err)
	require.Len(t, tr, 1)
	require.Equal(t, "run-root", *tr[0].RunId)
	mq := &message.MessagesInput{}
	mq.SetConversationIds([]string{"conv-root"})
	mr, err := tree.ReadCleanupMessages(context.Background(), invoker, mq, false)
	require.NoError(t, err)
	require.Len(t, mr, 4)
	var links, payloads []string
	for _, r := range mr {
		if r.LinkedConversationId != nil {
			links = append(links, *r.LinkedConversationId)
		}
		if r.AttachmentPayloadId != nil {
			payloads = append(payloads, *r.AttachmentPayloadId)
		}
	}
	require.Equal(t, []string{"conv-linked"}, links)
	require.ElementsMatch(t, []string{"payload-root", "payload-shared"}, payloads)
	empty := &turn.TurnRowsInput{}
	empty.SetConversationIDs(nil)
	metric := xexec.New()
	rows, err := tree.ReadCleanupTurns(xexec.WithContext(context.Background(), metric), invoker, empty, false)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Empty(t, metric.SnapshotForLogging().Metrics)
	_, err = tree.ReadCleanupTurns(context.Background(), invoker, &turn.TurnRowsInput{}, false)
	require.Error(t, err)
	untrusted := &turnmeta.Input{}
	untrusted.SetConversationIDs([]string{"conv-root"})
	_, err = invoker.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[turnmeta.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/turn/cleanup"}}, Input: untrusted})
	require.Error(t, err)
}

func TestDeletionMetadataReadersWideIDsNoPagination(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedForConversationTreeDelete)
	tx, err := db.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	ids := []string{}
	for i := 0; i < 501; i++ {
		id := fmt.Sprintf("metadata-turn-%03d", i)
		ids = append(ids, id)
		_, err = tx.Exec("INSERT INTO turn(id,conversation_id,status,queue_seq) VALUES(?,'conv-root','succeeded',?)", id, i+20)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	q := &turn.TurnRowsInput{}
	q.SetConversationIDs([]string{"conv-root"})
	rows, err := tree.ReadCleanupTurns(context.Background(), svc.(*datlyService).native, q, false)
	require.NoError(t, err)
	require.Len(t, rows, 502)
	// Independent bounded filters must not be combined or dropped.
	q.SetRetryOfIDs(ids)
	_, err = tree.ReadCleanupTurns(context.Background(), svc.(*datlyService).native, q, false)
	require.Error(t, err)
	q = &turn.TurnRowsInput{}
	q.SetRetryOfIDs(ids)
	metric := xexec.New()
	_, err = tree.ReadCleanupTurns(xexec.WithContext(context.Background(), metric), svc.(*datlyService).native, q, false)
	require.NoError(t, err)
	executions := 0
	for _, m := range metric.SnapshotForLogging().Metrics {
		if m != nil {
			for _, s := range m.Executions {
				executions++
				require.LessOrEqual(t, len(s.Args), 400)
			}
		}
	}
	require.Equal(t, 2, executions)
}
