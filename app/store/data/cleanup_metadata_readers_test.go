package data

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	conversationmeta "github.com/viant/agently-core/internal/datly/conversation/cleanup/read"
	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	"github.com/viant/agently-core/internal/store/agentrun"
	"github.com/viant/agently-core/internal/store/conversation"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	xexec "github.com/viant/xdatly/exec"
	"github.com/viant/xdatly/response"
	"github.com/viant/xdatly/state"
)

var cleanupConversationColumns = []string{"id", "conversation_parent_id", "conversation_parent_turn_id", "scheduled", "schedule_id", "schedule_run_id", "schedule_kind", "created_at_raw", "activity_raw"}
var cleanupRunColumns = []string{"id", "status", "conversation_id", "turn_id", "schedule_id", "resumed_from_run_id", "conversation_kind", "lease_until_raw", "heartbeat_raw", "heartbeat_interval_sec", "activity_raw"}
var cleanupConversationProperties = []string{"Id", "ConversationParentId", "ConversationParentTurnId", "Scheduled", "ScheduleId", "ScheduleRunId", "ScheduleKind", "CreatedAtRaw", "ActivityRaw"}
var cleanupRunProperties = []string{"Id", "Status", "ConversationId", "TurnId", "ScheduleId", "ResumedFromRunId", "ConversationKind", "LeaseUntilRaw", "HeartbeatRaw", "HeartbeatIntervalSec", "ActivityRaw"}

func cleanupMetadataProperties(value any, properties []string) map[string]any {
	row := reflect.ValueOf(value).Elem()
	result := make(map[string]any, len(properties))
	for _, name := range properties {
		result[name] = row.FieldByName(name).Interface()
	}
	return result
}

func cleanupMetadataCalls(invoker dexec.ComponentInvoker, conversationID, runID string) (map[string]func(context.Context) (any, error), map[string][]string) {
	owner := func(context.Context) string { return "" }
	conversations := &conversation.Store{Invoker: invoker, OwnerID: owner}
	runs := &agentrun.Store{Invoker: invoker, OwnerID: owner}
	return map[string]func(context.Context) (any, error){
		"conversation-legacy": func(ctx context.Context) (any, error) {
			q := &convread.ConversationInput{}
			q.SetIds([]string{conversationID})
			r, e := conversations.GraphRows(ctx, q, cleanupConversationColumns)
			if e != nil {
				return nil, e
			}
			if len(r) != 1 {
				return nil, fmt.Errorf("conversation rows %d", len(r))
			}
			return cleanupMetadataProperties(r[0], cleanupConversationProperties), nil
		},
		"conversation-compact": func(ctx context.Context) (any, error) {
			q := &convread.ConversationInput{}
			q.SetIds([]string{conversationID})
			r, e := tree.ReadCleanupConversations(ctx, invoker, q, false)
			if e != nil {
				return nil, e
			}
			if len(r) != 1 {
				return nil, fmt.Errorf("conversation rows %d", len(r))
			}
			return cleanupMetadataProperties(r[0], cleanupConversationProperties), nil
		},
		"run-legacy": func(ctx context.Context) (any, error) {
			q := &runread.RunRowsInput{}
			q.SetIds([]string{runID})
			r, e := runs.ListTrusted(ctx, "rows", q, state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: cleanupRunColumns, Limit: 0}}})
			if e != nil {
				return nil, e
			}
			if len(r) != 1 {
				return nil, fmt.Errorf("run rows %d", len(r))
			}
			return cleanupMetadataProperties(r[0], cleanupRunProperties), nil
		},
		"run-compact": func(ctx context.Context) (any, error) {
			q := &runread.RunRowsInput{}
			q.SetIds([]string{runID})
			r, e := tree.ReadCleanupRuns(ctx, invoker, q, false)
			if e != nil {
				return nil, e
			}
			if len(r) != 1 {
				return nil, fmt.Errorf("run rows %d", len(r))
			}
			return cleanupMetadataProperties(r[0], cleanupRunProperties), nil
		},
	}, map[string][]string{"conversation": cleanupConversationColumns, "run": cleanupRunColumns}
}

func TestCleanupMetadataReadersParityAndIsolation(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedForConversationTreeDelete)
	_, err := db.Exec(`UPDATE run SET lease_until='not-a-date',last_heartbeat_at=NULL,heartbeat_interval_sec=NULL WHERE id='run-root'`)
	require.NoError(t, err)
	invoker := svc.(*datlyService).native
	calls, _ := cleanupMetadataCalls(invoker, "conv-root", "run-root")
	for _, kind := range []string{"conversation", "run"} {
		old, err := calls[kind+"-legacy"](context.Background())
		require.NoError(t, err)
		fresh, err := calls[kind+"-compact"](context.Background())
		require.NoError(t, err)
		require.Equal(t, old, fresh, "raw nullable metadata must not be normalized or decoded")
		metric := xexec.New()
		_, err = calls[kind+"-compact"](xexec.WithContext(context.Background(), metric))
		require.NoError(t, err)
		sample := cleanupMetadataSQL(t, metric)
		for _, forbidden := range []string{"t.*", "c.*", "checkpoint_data", "security_context", "report_document_json", "content", "summary"} {
			require.NotContains(t, strings.ToLower(sample.SQL), forbidden)
		}
	}
	query := &convread.ConversationInput{}
	query.SetIds([]string{"conv-root"})
	query.SetStatusFilter("succeeded")
	_, err = tree.ReadCleanupConversations(context.Background(), invoker, query, false)
	require.Error(t, err, "unsupported predicates must not be silently dropped")
	untrusted := &conversationmeta.Input{}
	untrusted.SetIDs([]string{"conv-root"})
	_, err = invoker.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[conversationmeta.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/conversation/cleanup"}}, Input: untrusted})
	require.Error(t, err, "private metadata reader requires the internal host capability")
	_, err = db.Exec(`UPDATE run SET run_kind='agui_protocol' WHERE id='run-root'`)
	require.NoError(t, err)
	rq := &runread.RunRowsInput{}
	rq.SetIds([]string{"run-root"})
	rows, err := tree.ReadCleanupRuns(context.Background(), invoker, rq, false)
	require.NoError(t, err)
	require.Empty(t, rows, "protocol resources are not execution evidence")
}

func TestCleanupMetadataReadersDoNotPaginateWideGraphs(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedForConversationTreeDelete)
	tx, err := db.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	var ids []string
	for i := 0; i < 501; i++ {
		id := fmt.Sprintf("metadata-wide-%03d", i)
		ids = append(ids, id)
		_, err = tx.Exec(`INSERT INTO conversation(id,created_at,status,conversation_parent_id) VALUES(?,'2026-01-01T00:00:00Z','succeeded','conv-root')`, id)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	q := &convread.ConversationInput{}
	q.SetParentId("conv-root")
	rows, err := tree.ReadCleanupConversations(context.Background(), svc.(*datlyService).native, q, false)
	require.NoError(t, err)
	require.Len(t, rows, 502)
	q = &convread.ConversationInput{}
	q.SetIds(ids)
	metric := xexec.New()
	rows, err = tree.ReadCleanupConversations(xexec.WithContext(context.Background(), metric), svc.(*datlyService).native, q, false)
	require.NoError(t, err)
	require.Len(t, rows, 501)
	var executions int
	for _, m := range metric.SnapshotForLogging().Metrics {
		if m != nil {
			for _, s := range m.Executions {
				executions++
				require.LessOrEqual(t, len(s.Args), 400)
			}
		}
	}
	require.Equal(t, 2, executions, "501 identities require two bounded reads, not pagination")
}

func cleanupMetadataSQL(t *testing.T, ec *xexec.Context) *response.SQLExecution {
	t.Helper()
	var samples response.SQLExecutions
	for _, m := range ec.SnapshotForLogging().Metrics {
		if m != nil {
			samples = append(samples, m.Executions...)
		}
	}
	require.Len(t, samples, 1)
	return samples[0]
}
