package native_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	read "github.com/viant/agently-core/internal/datly/conversation/read"
	convwrite "github.com/viant/agently-core/internal/datly/conversation/write"
	gfread "github.com/viant/agently-core/internal/datly/generatedfile/read"
	gfwrite "github.com/viant/agently-core/internal/datly/generatedfile/write"
	msgread "github.com/viant/agently-core/internal/datly/message/read"
	msgwrite "github.com/viant/agently-core/internal/datly/message/write"
	modelwrite "github.com/viant/agently-core/internal/datly/modelcall/write"
	payloadwrite "github.com/viant/agently-core/internal/datly/payload/write"
	artifactread "github.com/viant/agently-core/internal/datly/reporting/artifact/read"
	artifactwrite "github.com/viant/agently-core/internal/datly/reporting/artifact/write"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
	jobwrite "github.com/viant/agently-core/internal/datly/reporting/job/write"
	agentruncube "github.com/viant/agently-core/internal/datly/run/cube"
	agentrunread "github.com/viant/agently-core/internal/datly/run/read"
	agentrunwrite "github.com/viant/agently-core/internal/datly/run/write"
	runstepsread "github.com/viant/agently-core/internal/datly/runsteps/read"
	schedulewrite "github.com/viant/agently-core/internal/datly/schedule/write"
	approvalread "github.com/viant/agently-core/internal/datly/toolapprovalqueue/read"
	approvalwrite "github.com/viant/agently-core/internal/datly/toolapprovalqueue/write"
	toolwrite "github.com/viant/agently-core/internal/datly/toolcall/write"
	turnread "github.com/viant/agently-core/internal/datly/turn/read"
	turnwrite "github.com/viant/agently-core/internal/datly/turn/write"
	agentrunstore "github.com/viant/agently-core/internal/store/agentrun"
	convstore "github.com/viant/agently-core/internal/store/conversation"
	conversationtree "github.com/viant/agently-core/internal/store/conversationtree"
	maintenance "github.com/viant/agently-core/internal/store/maintenancelease"
	queuereorder "github.com/viant/agently-core/internal/store/queuereorder"
	reportaudit "github.com/viant/agently-core/internal/store/reportaudit"
	adoption "github.com/viant/agently-core/internal/store/reporting/adoption"
	contextstore "github.com/viant/agently-core/internal/store/reporting/context"
	exportcomplete "github.com/viant/agently-core/internal/store/reporting/exportcomplete"
	exportsubmit "github.com/viant/agently-core/internal/store/reporting/exportsubmit"
	runstore "github.com/viant/agently-core/internal/store/reporting/run"
	sharedartifact "github.com/viant/agently-core/internal/store/reporting/sharedartifact"
	runstepsstore "github.com/viant/agently-core/internal/store/runsteps"
	schedulerlease "github.com/viant/agently-core/internal/store/schedulerlease"
	schedulerstore "github.com/viant/agently-core/internal/store/schedulerstore"

	authctx "github.com/viant/agently-core/internal/auth"
	datlypredicate "github.com/viant/agently-core/internal/datly/predicate"
	executionprotection "github.com/viant/agently-core/internal/tool/executionprotection"
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	toolprotection "github.com/viant/agently-core/protocol/tool/protection"
	goalsys "github.com/viant/agently-core/service/goal"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	xhandler "github.com/viant/xdatly/handler"
	"github.com/viant/xdatly/state"
)

type payloadRaceInvoker struct {
	next         dexec.ComponentInvoker
	beforeWriter func() error
}

func (i *payloadRaceInvoker) InvokeComponent(ctx context.Context, request dexec.ComponentRequest) (any, error) {
	if i.beforeWriter != nil && request.Target.Route.Method == "PATCH" && request.Target.Route.Path == "/v1/api/agently/payload" {
		before := i.beforeWriter
		i.beforeWriter = nil
		if err := before(); err != nil {
			return nil, err
		}
	}
	return i.next.InvokeComponent(ctx, request)
}

func TestWorkspaceRuntimeToolExecutionClaimRepository(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	server, err := native.New(ctx, native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repository := executionprotection.NewComponentRepository(server)
	record := executionprotection.ClaimRecord{
		ClaimKey: "linked-claim", RuleID: "rule-1", CanonicalToolName: "shell/exec",
		TurnID: "turn-1", SemanticHash: "hash-1", CreatedAt: now,
	}
	claimed, err := repository.Claim(ctx, record)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = repository.Claim(ctx, record)
	require.NoError(t, err)
	require.False(t, claimed)
	finished := now.Add(time.Minute)
	require.NoError(t, repository.Finish(ctx, record.ClaimKey, toolprotection.StateCompleted, finished))
	require.NoError(t, repository.Finish(ctx, "missing-claim", toolprotection.StateCompleted, finished))
	var state string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT state FROM tool_execution_claim WHERE claim_key=?", record.ClaimKey).Scan(&state))
	require.Equal(t, "completed", state)
	claimed, err = repository.Claim(ctx, record)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT state FROM tool_execution_claim WHERE claim_key=?", record.ClaimKey).Scan(&state))
	require.Equal(t, "completed", state, "a repeated claim must not reset a completed claim")
	concurrent := record
	concurrent.ClaimKey = "concurrent-claim"
	results := make(chan struct {
		claimed bool
		err     error
	}, 8)
	for i := 0; i < cap(results); i++ {
		go func() {
			claimed, err := repository.Claim(ctx, concurrent)
			results <- struct {
				claimed bool
				err     error
			}{claimed, err}
		}()
	}
	winners := 0
	for i := 0; i < cap(results); i++ {
		result := <-results
		require.NoError(t, result.err)
		if result.claimed {
			winners++
		}
	}
	require.Equal(t, 1, winners)
}

func TestWorkspaceRuntimeApprovalStore(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	server, err := native.New(ctx, native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, `INSERT INTO tool_approval_queue(id,user_id,tool_name,arguments,status,created_at) VALUES
		('a1','u1','shell/exec',X'7B7D','pending','2026-01-01 00:00:00'),
		('a2','u2','shell/exec',X'7B7D','approved','2026-01-02 00:00:00')`)
	require.NoError(t, err)
	store := &convstore.ApprovalStore{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u1"})
	query := &approvalread.ApprovalRowsInput{}
	query.SetUserId("u1")
	rows, err := store.List(owner, "rows", query, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "a1", rows[0].Id)
	selected, err := store.List(owner, "rows", nil, state.Selectors{&state.NamedSelector{Name: "queue_rows", Selector: state.Selector{Fields: []string{"id"}, Limit: 1, OrderBy: "id ASC"}}})
	require.NoError(t, err)
	require.Len(t, selected, 1)
	require.Equal(t, "a1", selected[0].Id)
	require.Empty(t, selected[0].ToolName)
	total, err := store.Count(owner, nil)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	patch := &approvalwrite.ToolApprovalQueue{}
	patch.SetId("a1")
	patch.SetStatus("approved")
	require.NoError(t, store.PatchTrusted(owner, patch))
	outcomes, err := store.List(owner, "outcome", query, nil)
	require.NoError(t, err)
	require.Len(t, outcomes, 1)
	require.Equal(t, "approved", outcomes[0].Status)
}

func TestWorkspaceRuntimeConversationTreeApprovalCandidates(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO conversation(id,created_by_user_id) VALUES('approval-graph','u1'),('approval-outside','u1');
		INSERT INTO turn(id,conversation_id,status) VALUES('approval-turn','approval-graph','succeeded');
		INSERT INTO message(id,conversation_id,role,type) VALUES('approval-message','approval-graph','user','text');
		INSERT INTO tool_approval_queue(id,user_id,conversation_id,tool_name,arguments,status,created_at) VALUES
		('approval-by-conversation','u1','approval-graph','tool',X'7B7D','pending','2026-01-01 00:00:00'),
		('approval-unrelated','u1','approval-outside','tool',X'7B7D','pending','2026-01-01 00:00:00');
		INSERT INTO tool_approval_queue(id,user_id,turn_id,tool_name,arguments,status,created_at) VALUES
		('approval-by-turn','u1','approval-turn','tool',X'7B7D','pending','2026-01-01 00:00:00');
		INSERT INTO tool_approval_queue(id,user_id,message_id,tool_name,arguments,status,created_at) VALUES
		('approval-by-message','u1','approval-message','tool',X'7B7D','pending','2026-01-01 00:00:00')`)
	require.NoError(t, err)
	discoverer := &conversationtree.Discoverer{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	graph, err := discoverer.DiscoverAuthorized(owner, "approval-graph")
	require.NoError(t, err)
	ids, err := discoverer.CollectApprovalIDs(owner, graph)
	require.NoError(t, err)
	require.Equal(t, []string{"approval-by-conversation", "approval-by-message", "approval-by-turn"}, ids)
	_, err = db.Exec(`CREATE TRIGGER reject_graph_approval BEFORE DELETE ON tool_approval_queue
		WHEN OLD.id='approval-by-turn' BEGIN SELECT RAISE(ABORT,'fixture approval delete rejection'); END`)
	require.NoError(t, err)
	store := &convstore.ApprovalStore{Invoker: server, OwnerID: authctx.EffectiveUserID}
	require.Error(t, store.DeleteTrusted(owner, ids...))
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM tool_approval_queue`).Scan(&count))
	require.Equal(t, 4, count, "late failure rolls back the earlier approval deletes")
	_, err = db.Exec(`DROP TRIGGER reject_graph_approval`)
	require.NoError(t, err)
	require.NoError(t, store.DeleteTrusted(owner, append(ids, "missing-approval")...))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM tool_approval_queue`).Scan(&count))
	require.Equal(t, 1, count)
	var remaining string
	require.NoError(t, db.QueryRow(`SELECT id FROM tool_approval_queue`).Scan(&remaining))
	require.Equal(t, "approval-unrelated", remaining)
}

func TestWorkspaceRuntimeGeneratedDeleteStores(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	server, err := native.New(ctx, native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, `INSERT INTO conversation(id) VALUES ('delete-c1'),('delete-c2')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO message(id,conversation_id,role,type,created_at) VALUES
		('delete-m1','delete-c1','user','text','2026-01-01 00:00:00'),
		('delete-m2','delete-c2','user','text','2026-01-01 00:00:00')`)
	require.NoError(t, err)
	messages := &convstore.MessageStore{Invoker: server}
	require.NoError(t, messages.DeleteTrusted(ctx, "delete-m1", "missing-message"))
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM message WHERE id='delete-m1'").Scan(&count))
	require.Zero(t, count)
	conversations := &convstore.Store{Invoker: server}
	require.NoError(t, conversations.DeleteTrusted(ctx, "delete-c1", "missing-conversation"))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversation WHERE id='delete-c1'").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversation WHERE id='delete-c2'").Scan(&count))
	require.Equal(t, 1, count)
	_, err = db.ExecContext(ctx, `INSERT INTO conversation(id) VALUES ('delete-c3'),('delete-c4')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE TRIGGER reject_delete_c4 BEFORE DELETE ON conversation
		WHEN OLD.id='delete-c4' BEGIN SELECT RAISE(ABORT,'fixture delete rejection'); END`)
	require.NoError(t, err)
	require.Error(t, conversations.DeleteTrusted(ctx, "delete-c3", "delete-c4"))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversation WHERE id IN ('delete-c3','delete-c4')").Scan(&count))
	require.Equal(t, 2, count, "the generated batch must roll back earlier deletes")
}

// The application's workspace provisioner and the stock linked runtime must
// open the same SQLite file and see generated goal mutations.
func TestWorkspaceRuntimeGoalRoundTrip(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	path := filepath.Join(workspaceRoot, "db", "agently-core.db")
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, "INSERT INTO conversation(id) VALUES (?)", "c1")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO goal(id,conversation_id,objective,status,created_at) VALUES ('g1','c1','native goal','active','2026-01-01 00:00:00')`)
	require.NoError(t, err)
	store := goalsys.NewStore(server)
	before, err := store.Current(ctx, "c1")
	require.NoError(t, err)
	require.NotNil(t, before)
	require.Equal(t, "native goal", before.Objective)
	require.NoError(t, store.RecordUsage(ctx, "g1", 42, 7))
	after, err := store.Current(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(42), after.TokensUsed)
	require.Equal(t, int64(7), after.TimeUsedSeconds)
}

func TestWorkspaceRuntimeConversationScope(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, "INSERT INTO conversation(id, visibility, created_by_user_id) VALUES ('private-c1','private','u1')")
	require.NoError(t, err)
	input := &read.ConversationInput{}
	input.SetId("private-c1")
	target := dexec.ComponentTarget{
		Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
		Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/conversation/{id}"},
	}
	invoke := func(callCtx context.Context) (*read.ConversationOutput, error) {
		value, err := server.InvokeComponent(callCtx, dexec.ComponentRequest{Target: target, Input: input})
		if err != nil {
			return nil, err
		}
		return value.(*read.ConversationOutput), nil
	}
	_, err = invoke(ctx)
	require.Error(t, err, "private access mode must be supplied by trusted Go scope")
	owner := authctx.WithUserInfo(native.WithAccess(ctx, native.Access{ListMode: true}), &authctx.UserInfo{Subject: "u1"})
	owned, err := invoke(owner)
	require.NoError(t, err)
	require.Len(t, owned.Data, 1)
	require.NoError(t, native.RequireVisibleConversation(authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u1"}), server, "private-c1"))
	other := authctx.WithUserInfo(native.WithAccess(ctx, native.Access{ListMode: true}), &authctx.UserInfo{Subject: "u2"})
	denied, err := invoke(other)
	require.NoError(t, err)
	require.Empty(t, denied.Data)
	require.ErrorIs(t, native.RequireVisibleConversation(authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u2"}), server, "private-c1"), native.ErrConversationNotVisible)
	require.ErrorIs(t, native.RequireVisibleConversation(ctx, server, "missing"), native.ErrConversationNotVisible)
}

func TestWorkspaceRuntimeConversationTreeDiscovery(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO conversation(id,created_by_user_id,status) VALUES('tree-root','u1','succeeded'),('tree-linked','u1','succeeded'),('tree-unrelated','u2','succeeded');
		INSERT INTO turn(id,conversation_id,status) VALUES('tree-turn','tree-root','succeeded');
		INSERT INTO conversation(id,created_by_user_id,status,conversation_parent_id) VALUES('tree-child','u1','succeeded','tree-root');
		INSERT INTO conversation(id,created_by_user_id,status,conversation_parent_id) VALUES('tree-grandchild','u1','succeeded','tree-child');
		INSERT INTO conversation(id,created_by_user_id,status,conversation_parent_id,conversation_parent_turn_id) VALUES('tree-turn-child','u1','succeeded','tree-root','tree-turn');
		INSERT INTO message(id,conversation_id,role,type,linked_conversation_id) VALUES('tree-message','tree-root','assistant','text','tree-linked'),('tree-cycle','tree-linked','assistant','text','tree-root')`)
	require.NoError(t, err)
	discoverer := &conversationtree.Discoverer{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	graph, err := discoverer.Discover(owner, "tree-root")
	require.NoError(t, err)
	require.Len(t, graph.Nodes, 5)
	require.Equal(t, 0, graph.Nodes["tree-root"].Depth)
	for _, id := range []string{"tree-child", "tree-turn-child", "tree-linked"} {
		require.Equal(t, 1, graph.Nodes[id].Depth)
		require.Equal(t, "u1", graph.Nodes[id].OwnerID)
	}
	require.Equal(t, 2, graph.Nodes["tree-grandchild"].Depth)
	require.Nil(t, graph.Nodes["tree-unrelated"])
	require.NoError(t, discoverer.ValidateInboundLinks(owner, graph))
	_, err = db.Exec(`INSERT INTO message(id,conversation_id,role,type,linked_conversation_id) VALUES('external-link','tree-unrelated','assistant','text','tree-root')`)
	require.NoError(t, err)
	require.ErrorIs(t, discoverer.ValidateInboundLinks(owner, graph), conversationtree.ErrGraphReferenced)
	_, err = db.Exec(`DELETE FROM message WHERE id='external-link';
		INSERT INTO conversation(id,created_by_user_id,status,conversation_parent_id,conversation_parent_turn_id) VALUES('late-turn-child','u2','succeeded','tree-root','tree-turn')`)
	require.NoError(t, err)
	require.ErrorIs(t, discoverer.ValidateInboundLinks(owner, graph), conversationtree.ErrGraphReferenced)
	_, err = db.Exec(`DELETE FROM conversation WHERE id='late-turn-child'`)
	require.NoError(t, err)
	_, err = discoverer.DiscoverAuthorized(owner, "tree-root")
	require.NoError(t, err)
	other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u2"})
	_, err = discoverer.DiscoverAuthorized(other, "tree-root")
	require.ErrorIs(t, err, conversationtree.ErrPermissionDenied)
	_, err = discoverer.DiscoverAuthorized(context.Background(), "tree-root")
	require.ErrorIs(t, err, conversationtree.ErrPermissionDenied)
	_, err = db.Exec("UPDATE conversation SET created_by_user_id='u2' WHERE id='tree-linked'")
	require.NoError(t, err)
	_, err = discoverer.DiscoverAuthorized(owner, "tree-root")
	require.ErrorIs(t, err, conversationtree.ErrPermissionDenied, "linked descendants must share the root owner")
	_, err = discoverer.Discover(owner, "missing-root")
	require.ErrorIs(t, err, conversationtree.ErrNotFound)
	tooMany := make([]string, conversationtree.MaxConversations+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("graph-%d", i)
	}
	_, err = discoverer.Discover(owner, tooMany...)
	require.ErrorIs(t, err, conversationtree.ErrTooLarge)
}

func TestWorkspaceRuntimeConversationTreeRunEvidence(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO conversation(id,created_by_user_id,status) VALUES('live-root','u1','succeeded');
		INSERT INTO schedule(id,name,agent_ref) VALUES('live-schedule','live','agent');
		INSERT INTO run(id,conversation_id,status,lease_until,last_heartbeat_at,heartbeat_interval_sec) VALUES('run-conv','live-root','running','2026-01-03 00:00:00','2025-01-01 00:00:00',5);
		INSERT INTO run(id,status) VALUES('run-turn','succeeded'),('run-model','succeeded'),('run-tool','succeeded');
		INSERT INTO turn(id,conversation_id,status,run_id) VALUES('live-turn','live-root','succeeded','run-turn');
		INSERT INTO message(id,conversation_id,turn_id,role,type) VALUES('live-model','live-root','live-turn','assistant','text'),('live-tool','live-root','live-turn','tool','tool_op');
		INSERT INTO model_call(message_id,turn_id,provider,model,model_kind,status,run_id) VALUES('live-model','live-turn','test','model','chat','succeeded','run-model');
		INSERT INTO tool_call(message_id,turn_id,op_id,tool_name,tool_kind,status,run_id) VALUES('live-tool','live-turn','op','tool','function','succeeded','run-tool')`)
	require.NoError(t, err)
	hasLegacy, err := server.HasTable(context.Background(), "agently", "schedule_run")
	require.NoError(t, err)
	require.False(t, hasLegacy)
	discoverer := &conversationtree.Discoverer{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	graph, err := discoverer.DiscoverAuthorized(owner, "live-root")
	require.NoError(t, err)
	evidence, err := discoverer.CollectRunEvidence(owner, graph)
	require.NoError(t, err)
	currentIDs := make([]string, 0, len(evidence.Current))
	for _, row := range evidence.Current {
		currentIDs = append(currentIDs, row.Id)
	}
	require.ElementsMatch(t, []string{"run-conv", "run-turn", "run-model", "run-tool"}, currentIDs)
	require.Empty(t, evidence.Legacy)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	require.ErrorIs(t, evidence.Validate(now), conversationtree.ErrConversationActive)
	_, err = db.Exec(`UPDATE run SET lease_until='2026-01-01 00:00:00' WHERE id='run-conv'`)
	require.NoError(t, err)
	evidence, err = discoverer.CollectRunEvidence(owner, graph)
	require.NoError(t, err)
	require.NoError(t, evidence.Validate(now))
	_, err = db.Exec(`UPDATE run SET last_heartbeat_at='2026-01-01 23:59:59' WHERE id='run-conv'`)
	require.NoError(t, err)
	evidence, err = discoverer.CollectRunEvidence(owner, graph)
	require.NoError(t, err)
	require.ErrorIs(t, evidence.Validate(now), conversationtree.ErrConversationActive, "fresh heartbeat blocks deletion")
	_, err = db.Exec(`UPDATE run SET last_heartbeat_at='2025-01-01 00:00:00' WHERE id='run-conv';
		CREATE TABLE schedule_run (
			id TEXT PRIMARY KEY, schedule_id TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME, status TEXT NOT NULL DEFAULT 'pending', error_message TEXT,
			lease_owner TEXT, lease_until DATETIME, precondition_ran_at DATETIME,
			precondition_passed INTEGER, precondition_result TEXT, conversation_id TEXT,
			conversation_kind TEXT DEFAULT 'scheduled', scheduled_for DATETIME,
			started_at DATETIME, completed_at DATETIME
		);
		INSERT INTO schedule_run(id,schedule_id,conversation_id,status,lease_until) VALUES('legacy-live','live-schedule','live-root','running','2026-01-03 00:00:00')`)
	require.NoError(t, err)
	hasLegacy, err = server.HasTable(context.Background(), "agently", "schedule_run")
	require.NoError(t, err)
	require.True(t, hasLegacy)
	evidence, err = discoverer.CollectRunEvidence(owner, graph)
	require.NoError(t, err)
	require.Len(t, evidence.Legacy, 1)
	require.Equal(t, "legacy-live", evidence.Legacy[0].Id)
	require.ErrorIs(t, evidence.Validate(now), conversationtree.ErrConversationActive, "legacy schedule-run lease remains live")
	_, err = db.Exec(`UPDATE schedule_run SET lease_until='2026-01-01 00:00:00' WHERE id='legacy-live'`)
	require.NoError(t, err)
	evidence, err = discoverer.CollectRunEvidence(owner, graph)
	require.NoError(t, err)
	require.NoError(t, evidence.Validate(now))
}

func TestWorkspaceRuntimeConversationTreeScheduleReferences(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO conversation(id,created_by_user_id,status) VALUES('schedule-root','u1','succeeded');
		INSERT INTO goal(id,conversation_id,objective,status) VALUES('goal-1','schedule-root','finish','active')`)
	require.NoError(t, err)
	discoverer := &conversationtree.Discoverer{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	graph, err := discoverer.DiscoverAuthorized(owner, "schedule-root")
	require.NoError(t, err)
	require.NoError(t, discoverer.ValidateInboundLinks(owner, graph))
	_, err = db.Exec(`INSERT INTO conversation(id,created_by_user_id,status) VALUES('external-goal-conversation','u2','succeeded');
		INSERT INTO turn(id,conversation_id,status,goal_id) VALUES('external-goal-turn','external-goal-conversation','succeeded','goal-1')`)
	require.NoError(t, err)
	require.ErrorIs(t, discoverer.ValidateInboundLinks(owner, graph), conversationtree.ErrGraphReferenced)
	_, err = db.Exec(`DELETE FROM turn WHERE id='external-goal-turn'; DELETE FROM conversation WHERE id='external-goal-conversation'`)
	require.NoError(t, err)
	require.NoError(t, discoverer.ValidateInboundLinks(owner, graph))
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	_, err = db.Exec(`INSERT INTO schedule(id,name,agent_ref,internal,conversation_id) VALUES('user-schedule','user','agent',0,'schedule-root')`)
	require.NoError(t, err)
	_, err = discoverer.ValidateScheduleReferences(owner, graph, now)
	require.ErrorIs(t, err, conversationtree.ErrScheduleReferenced)
	_, err = db.Exec(`DELETE FROM schedule WHERE id='user-schedule';
		INSERT INTO schedule(id,name,agent_ref,created_by_user_id,internal,conversation_id,goal_id,schedule_type)
		VALUES('goal-wakeup-goal-1','autonomous::goal-wakeup::goal-1','agent','u1',1,'schedule-root','goal-1','adhoc')`)
	require.NoError(t, err)
	allowed, err := discoverer.ValidateScheduleReferences(owner, graph, now)
	require.NoError(t, err)
	require.Equal(t, []string{"goal-wakeup-goal-1"}, allowed)
	_, err = db.Exec(`UPDATE schedule SET lease_until='2026-01-03 00:00:00' WHERE id='goal-wakeup-goal-1'`)
	require.NoError(t, err)
	_, err = discoverer.ValidateScheduleReferences(owner, graph, now)
	require.ErrorIs(t, err, conversationtree.ErrConversationActive)
	_, err = db.Exec(`UPDATE schedule SET lease_until=NULL,created_by_user_id='u2' WHERE id='goal-wakeup-goal-1'`)
	require.NoError(t, err)
	_, err = discoverer.ValidateScheduleReferences(owner, graph, now)
	require.ErrorIs(t, err, conversationtree.ErrPermissionDenied)
	_, err = db.Exec(`UPDATE schedule SET created_by_user_id='u1',name='wrong' WHERE id='goal-wakeup-goal-1'`)
	require.NoError(t, err)
	_, err = discoverer.ValidateScheduleReferences(owner, graph, now)
	require.ErrorIs(t, err, conversationtree.ErrScheduleReferenced)
}

func TestWorkspaceRuntimeConversationTreeReportReferences(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO conversation(id,created_by_user_id,status) VALUES('report-root','u1','succeeded'),('report-outside','u1','succeeded');
		INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,status,started_at,revision,ui_run_request_id)
		VALUES('report-1','u1','report-root','test','running','2026-01-01 00:00:00',1,'request-1');
		INSERT INTO conversation_report_context(owner_id,conversation_id,active_report_run_id,revision)
		VALUES('u1','report-root','report-1',1)`)
	require.NoError(t, err)
	discoverer := &conversationtree.Discoverer{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	graph, err := discoverer.DiscoverAuthorized(owner, "report-root")
	require.NoError(t, err)
	runIDs, err := discoverer.ValidateReportReferences(owner, graph)
	require.NoError(t, err)
	require.Equal(t, []string{"report-1"}, runIDs)
	_, err = db.Exec(`INSERT INTO conversation_report_context(owner_id,conversation_id,active_report_run_id,revision)
		VALUES('u1','report-outside','report-1',1)`)
	require.NoError(t, err)
	_, err = discoverer.ValidateReportReferences(owner, graph)
	require.ErrorIs(t, err, conversationtree.ErrGraphReferenced)
	_, err = db.Exec(`DELETE FROM conversation_report_context WHERE conversation_id='report-outside';
		INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,status,started_at,revision,ui_run_request_id)
		VALUES('foreign-report','u2','report-root','test','running','2026-01-01 00:00:00',1,'request-2')`)
	require.NoError(t, err)
	_, err = discoverer.ValidateReportReferences(owner, graph)
	require.ErrorIs(t, err, conversationtree.ErrPermissionDenied)
}

func TestWorkspaceRuntimeConversationTreeExportReferences(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO conversation(id,created_by_user_id,status) VALUES('export-root','u1','succeeded');
		INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,status,started_at,revision,ui_run_request_id)
		VALUES('export-report','u1','export-root','test','completed','2026-01-01 00:00:00',1,'export-request');
		INSERT INTO report_export_job(job_id,artifact_ref,owner_id,conversation_id,format,scope,status)
		VALUES('export-job','report://export','u1','export-root','pdf','draft','succeeded');
		INSERT INTO report_export_artifact(artifact_id,job_id,artifact_ref,owner_id,format,content_type)
		VALUES('export-artifact','export-job','report://export','u1','pdf','application/pdf')`)
	require.NoError(t, err)
	discoverer := &conversationtree.Discoverer{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	graph, err := discoverer.DiscoverAuthorized(owner, "export-root")
	require.NoError(t, err)
	references, err := discoverer.ValidateExportReferences(owner, graph)
	require.NoError(t, err)
	require.Equal(t, []string{"export-report"}, references.ReportRunIDs)
	require.Equal(t, []string{"export-job"}, references.JobIDs)
	require.Equal(t, []string{"export-artifact"}, references.ArtifactIDs)
	_, err = db.Exec(`INSERT INTO conversation(id,created_by_user_id,status) VALUES('export-other','u1','succeeded');
		INSERT INTO report_export_job(job_id,artifact_ref,owner_id,conversation_id,format,scope,status,report_run_id,report_run_revision,export_request_id)
		VALUES('job-by-run','report://other','u1','export-other','pdf','draft','succeeded','export-report',1,'other-request')`)
	require.NoError(t, err)
	references, err = discoverer.ValidateExportReferences(owner, graph)
	require.NoError(t, err)
	require.Equal(t, []string{"export-job", "job-by-run"}, references.JobIDs)
	_, err = db.Exec(`UPDATE report_export_job SET status='queued' WHERE job_id='export-job'`)
	require.NoError(t, err)
	_, err = discoverer.ValidateExportReferences(owner, graph)
	require.ErrorIs(t, err, conversationtree.ErrConversationActive)
	_, err = db.Exec(`UPDATE report_export_job SET status='succeeded' WHERE job_id='export-job';
		UPDATE report_export_artifact SET owner_id='u2' WHERE artifact_id='export-artifact'`)
	require.NoError(t, err)
	_, err = discoverer.ValidateExportReferences(owner, graph)
	require.ErrorIs(t, err, conversationtree.ErrPermissionDenied)
	_, err = db.Exec(`UPDATE report_export_artifact SET owner_id='u1' WHERE artifact_id='export-artifact';
		INSERT INTO report_export_job(job_id,artifact_ref,owner_id,conversation_id,format,scope,status)
		VALUES('foreign-job','report://foreign','u2','export-root','pdf','draft','succeeded')`)
	require.NoError(t, err)
	_, err = discoverer.ValidateExportReferences(owner, graph)
	require.ErrorIs(t, err, conversationtree.ErrPermissionDenied)
}

func TestWorkspaceRuntimeConversationTreeNonTerminalStatus(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO conversation(id,created_by_user_id,status) VALUES('status-root','u1','legacy_busy')`)
	require.NoError(t, err)
	discoverer := &conversationtree.Discoverer{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	readGraph := func() *conversationtree.Graph {
		graph, err := discoverer.DiscoverAuthorized(owner, "status-root")
		require.NoError(t, err)
		return graph
	}
	require.NoError(t, discoverer.ValidateNonTerminalStatuses(owner, readGraph()))
	_, err = db.Exec(`INSERT INTO turn(id,conversation_id,status) VALUES('status-turn','status-root','running')`)
	require.NoError(t, err)
	require.ErrorIs(t, discoverer.ValidateNonTerminalStatuses(owner, readGraph()), conversationtree.ErrNonTerminal)
	_, err = db.Exec(`UPDATE conversation SET status='running' WHERE id='status-root'`)
	require.NoError(t, err)
	require.NoError(t, discoverer.ValidateNonTerminalStatuses(owner, readGraph()))
	_, err = db.Exec(`UPDATE conversation SET status='succeeded' WHERE id='status-root'`)
	require.NoError(t, err)
	require.NoError(t, discoverer.ValidateNonTerminalStatuses(owner, readGraph()))
	_, err = db.Exec(`UPDATE conversation SET status=NULL WHERE id='status-root'`)
	require.NoError(t, err)
	require.NoError(t, discoverer.ValidateNonTerminalStatuses(owner, readGraph()))
	_, err = db.Exec(`DELETE FROM turn WHERE id='status-turn'; UPDATE conversation SET status='legacy_busy' WHERE id='status-root';
		INSERT INTO report_export_job(job_id,artifact_ref,owner_id,conversation_id,format,scope,status)
		VALUES('status-job','report://status','u1','status-root','pdf','draft','succeeded')`)
	require.NoError(t, err)
	require.ErrorIs(t, discoverer.ValidateNonTerminalStatuses(owner, readGraph()), conversationtree.ErrNonTerminal)
}

func TestWorkspaceRuntimeReportAuditImport(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	server, err := native.New(ctx, native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	store := &reportaudit.Store{Invoker: server}
	event := reportaudit.Event{ID: "import-1", Type: "report.saved", ArtifactRef: "report://one", ActorID: "u1", MetadataJSON: []byte(`{"source":"fs"}`)}
	require.Error(t, store.ImportIfAbsent(ctx, event), "missing trusted scope must fail")
	trusted := native.WithAccess(ctx, native.Access{Internal: true})
	require.NoError(t, store.ImportIfAbsent(trusted, event))
	require.NoError(t, store.ImportIfAbsent(trusted, event))
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM report_audit_event WHERE event_id=?", event.ID).Scan(&count))
	require.Equal(t, 1, count)
}

func TestWorkspaceRuntimeReportingTransactions(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	server, err := native.New(ctx, native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, `INSERT INTO conversation(id) VALUES ('c1');
INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,origin,status,started_at,completed_at,revision,ui_run_request_id,report_spec_json,report_fill_json,report_print_json,created_at,updated_at) VALUES
('adopt','u1',NULL,'test','manual','completed','2026-01-01 00:00:00','2026-01-02 00:00:00',2,'req-adopt',X'7B7D',NULL,NULL,'2026-01-01 00:00:00','2026-01-02 00:00:00'),
('export','u1','c1','test','manual','completed','2026-01-01 00:00:00','2026-01-02 00:00:00',2,'req-export',X'7B7D',X'7B7D',X'7B7D','2026-01-01 00:00:00','2026-01-02 00:00:00')`)
	require.NoError(t, err)
	owner := authctx.WithUserInfo(native.WithAccess(ctx, native.Access{Internal: false}), &authctx.UserInfo{Subject: "u1"})
	adoptTarget := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[adoption.Component]().PkgPath(), Name: "ReportAdoption"}, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/forge/reporting/adopt"}}
	completed := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	at := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	value, err := server.InvokeComponent(owner, dexec.ComponentRequest{Target: adoptTarget, Input: &adoption.Input{
		Run:                 &runstore.Record{ReportRunID: "adopt", OwnerID: "u1", ConversationID: "c1", Materializer: "test", Origin: "manual", Status: "completed", StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), CompletedAt: &completed, Revision: 3, UIRunRequestID: "req-adopt", ReportSpec: []byte(`{}`), AdoptionSource: "manual-adopt", ActorID: "u1", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: at},
		Context:             &contextstore.Record{OwnerID: "u1", ConversationID: "c1", ActiveReportRunID: "adopt", Revision: 1, ActivationSource: "manual", ActorID: "u1", UpdatedAt: at},
		ExpectedRunRevision: 2,
	}})
	require.NoError(t, err)
	require.Equal(t, int64(3), value.(*adoption.Output).RunRevision)
	submitTarget := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[exportsubmit.Component]().PkgPath(), Name: "RunExportSubmit"}, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/forge/reporting/export/submit"}}
	submit := &exportsubmit.Input{JobID: "job-1", OwnerID: "u1", ConversationID: "c1", ReportRunID: "export", ExportRequestID: "request-1", ArtifactRef: "report-run://export", Format: "pdf", Scope: "draft", Status: "queued", SubmittedAt: at}
	value, err = server.InvokeComponent(owner, dexec.ComponentRequest{Target: submitTarget, Input: submit})
	require.NoError(t, err)
	require.False(t, value.(*exportsubmit.Output).Replay)
	require.Equal(t, "job-1", value.(*exportsubmit.Output).Job.JobId)
	value, err = server.InvokeComponent(owner, dexec.ComponentRequest{Target: submitTarget, Input: submit})
	require.NoError(t, err)
	require.True(t, value.(*exportsubmit.Output).Replay)
	jobTarget := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[jobread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/job"}}
	lookup := &jobread.Input{}
	lookup.SetJobID("job-1")
	value, err = server.InvokeComponent(native.WithAccess(ctx, native.Access{Internal: true}), dexec.ComponentRequest{Target: jobTarget, Input: lookup})
	require.NoError(t, err)
	require.Len(t, value.(*jobread.Output).Data, 1)
	require.Equal(t, "u1", value.(*jobread.Output).Data[0].OwnerId)
	_, err = db.ExecContext(ctx, "UPDATE report_export_job SET status='running' WHERE job_id='job-1'")
	require.NoError(t, err)
	completeTarget := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[exportcomplete.Component]().PkgPath(), Name: "ExportComplete"}, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/forge/reporting/export/complete"}}
	completion := &exportcomplete.Input{JobID: "job-1", ArtifactID: "artifact-1", ContentType: "application/pdf", Data: []byte("%PDF"), ArtifactCreatedAt: at, CompletedAt: at.Add(time.Minute)}
	value, err = server.InvokeComponent(owner, dexec.ComponentRequest{Target: completeTarget, Input: completion})
	require.NoError(t, err)
	require.Equal(t, "succeeded", value.(*exportcomplete.Output).Job.Status)
	value, err = server.InvokeComponent(owner, dexec.ComponentRequest{Target: completeTarget, Input: completion})
	require.NoError(t, err)
	require.Equal(t, "succeeded", value.(*exportcomplete.Output).Job.Status)
}

func TestWorkspaceRuntimeQueueReorder(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	server, err := native.New(ctx, native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, `INSERT INTO conversation(id,visibility) VALUES('c1','public');
INSERT INTO turn(id,conversation_id,status,queue_seq) VALUES('t1','c1','queued',2),('t2','c1','queued',1);
INSERT INTO message(id,conversation_id,turn_id,role) VALUES('m1','c1','t1','user'),('m2','c1','t2','user');
INSERT INTO turn_queue(id,conversation_id,turn_id,message_id,queue_seq,status) VALUES('q1','c1','t1','m1',2,'queued'),('q2','c1','t2','m2',1,'queued')`)
	require.NoError(t, err)
	require.NoError(t, queuereorder.Move(ctx, server, "c1", "t1", "up"))
	var first, second int64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT queue_seq FROM turn WHERE id='t1'").Scan(&first))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT queue_seq FROM turn_queue WHERE id='q2'").Scan(&second))
	require.Equal(t, int64(1), first)
	require.Equal(t, int64(2), second)
}

func TestWorkspaceRuntimeSharedArtifact(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{SourceRoot: project, WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	store := &sharedartifact.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u1"})
	other := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u2"})
	record := &sharedartifact.Record{ArtifactID: "shared-1", ArtifactRef: "report://one", OwnerID: "u1", Kind: "saved", Lifecycle: "draft", Version: 1, ReportID: "report-1", Title: "Original", DocumentVersion: 1, Document: []byte(`{"kind":"report"}`), CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	require.NoError(t, store.Create(owner, record))
	got, err := store.Get(owner, record.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, record.Title, got.Title)
	_, err = store.Get(other, record.ArtifactID)
	require.ErrorIs(t, err, sharedartifact.ErrNotFound)
	record.Title = "Updated"
	require.NoError(t, store.Update(owner, record))
	rows, err := store.List(owner)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Updated", rows[0].Title)
	require.NoError(t, store.Delete(owner, record.ArtifactID))
	_, err = store.Get(owner, record.ArtifactID)
	require.ErrorIs(t, err, sharedartifact.ErrNotFound)
}

func TestWorkspaceRuntimeReportRunAndContextStores(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	server, err := native.New(ctx, native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, "INSERT INTO conversation(id) VALUES('c1')")
	require.NoError(t, err)
	owner := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u1"})
	other := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u2"})
	runs := &runstore.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}
	contexts := &contextstore.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	record := &runstore.Record{ReportRunID: "run-1", OwnerID: "u1", Materializer: "test", Origin: "manual", Status: "running", StartedAt: at, Revision: 1, UIRunRequestID: "request-1", ReportSpec: []byte(`{}`), CreatedAt: at, UpdatedAt: at}
	require.NoError(t, runs.Create(owner, record))
	got, err := runs.GetByRequestID(owner, "request-1")
	require.NoError(t, err)
	require.Equal(t, record.ReportRunID, got.ReportRunID)
	rootRun := reportrun.Record(*got)
	require.Equal(t, record.ReportRunID, rootRun.ReportRunID)
	_, err = runs.Get(other, record.ReportRunID)
	require.ErrorIs(t, err, runstore.ErrNotFound)
	record.Revision = 2
	record.Status = "completed"
	completed := at.Add(time.Hour)
	record.CompletedAt = &completed
	record.UpdatedAt = completed
	require.NoError(t, runs.UpdateCAS(owner, record, 1))
	require.ErrorIs(t, runs.UpdateCAS(owner, record, 1), runstore.ErrCASMismatch)
	pointer := &contextstore.Record{OwnerID: "u1", ConversationID: "c1", ActiveReportRunID: "run-1", Revision: 1, ActivationSource: "manual", ActorID: "u1", UpdatedAt: completed}
	require.NoError(t, contexts.PutCAS(owner, pointer, 0))
	readPointer, err := contexts.Get(owner, "c1")
	require.NoError(t, err)
	require.Equal(t, int64(1), readPointer.Revision)
	rootPointer := reportcontext.Record(*readPointer)
	require.Equal(t, pointer.ConversationID, rootPointer.ConversationID)
	pointer.Revision = 2
	pointer.ActivationSource = "refresh"
	require.NoError(t, contexts.PutCAS(owner, pointer, 1))
	require.ErrorIs(t, contexts.PutCAS(owner, pointer, 1), contextstore.ErrCASMismatch)
	_, err = contexts.Get(other, "c1")
	require.ErrorIs(t, err, contextstore.ErrNotFound)
}

func TestWorkspaceRuntimeReportJobWriterModes(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{SourceRoot: project, WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	owner := authctx.WithUserInfo(native.WithAccess(ctx, native.Access{Internal: false}), &authctx.UserInfo{Subject: "u1"})
	other := authctx.WithUserInfo(native.WithAccess(ctx, native.Access{Internal: false}), &authctx.UserInfo{Subject: "u2"})
	writerTarget := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[jobwrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/job"}}
	readerTarget := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[jobread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/job"}}
	write := func(callCtx context.Context, mode, expected string, row *jobwrite.Job) error {
		input := &jobwrite.Input{}
		input.SetMode(mode)
		if expected != "" {
			input.SetExpectedStatus(expected)
		}
		input.SetJobs([]*jobwrite.Job{row})
		_, err := server.InvokeComponent(callCtx, dexec.ComponentRequest{Target: writerTarget, Input: input})
		return err
	}
	readJob := func() *jobread.Job {
		input := &jobread.Input{}
		input.SetJobID("job-modes")
		value, err := server.InvokeComponent(owner, dexec.ComponentRequest{Target: readerTarget, Input: input})
		require.NoError(t, err)
		require.Len(t, value.(*jobread.Output).Data, 1)
		return value.(*jobread.Output).Data[0]
	}
	at := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	created := &jobwrite.Job{}
	created.SetJobId("job-modes")
	created.SetArtifactRef("report://one")
	created.SetOwnerId("u1")
	created.SetFormat("pdf")
	created.SetScope("draft")
	created.SetStatus("queued")
	created.SetSubmittedAt(at)
	require.Error(t, write(other, "create", "", created))
	require.NoError(t, write(owner, "create", "", created))
	updated := &jobwrite.Job{}
	updated.SetJobId("job-modes")
	updated.SetOwnerId("u1")
	updated.SetStatus("queued")
	updated.SetMetadataJson([]byte(`{"version":2}`))
	require.NoError(t, write(owner, "update", "", updated))
	require.JSONEq(t, `{"version":2}`, string(readJob().MetadataJson))
	claimed := &jobwrite.Job{}
	claimed.SetJobId("job-modes")
	claimed.SetOwnerId("u1")
	claimed.SetStatus("running")
	claimedAt := at.Add(time.Minute)
	claimed.SetStartedAt(&claimedAt)
	require.NoError(t, write(owner, "claim", "queued", claimed))
	require.Error(t, write(owner, "claim", "queued", claimed))
	failed := &jobwrite.Job{}
	failed.SetJobId("job-modes")
	failed.SetOwnerId("u1")
	failed.SetStatus("failed")
	message := "worker failure"
	failed.SetErrorText(&message)
	completedAt := at.Add(2 * time.Minute)
	failed.SetCompletedAt(&completedAt)
	require.NoError(t, write(owner, "fail", "running", failed))
	require.Equal(t, "failed", readJob().Status)
	artifactTarget := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[artifactwrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/artifact"}}
	artifact := &artifactwrite.Artifact{}
	artifact.SetArtifactId("artifact-modes")
	artifact.SetJobId("job-modes")
	artifact.SetArtifactRef("report://one")
	artifact.SetOwnerId("u1")
	artifact.SetFormat("pdf")
	artifact.SetContentType("application/pdf")
	artifact.SetInlineData([]byte("%PDF"))
	artifact.SetCreatedAt(completedAt)
	artifactInput := &artifactwrite.Input{}
	artifactInput.SetMode("create")
	artifactInput.SetArtifacts([]*artifactwrite.Artifact{artifact})
	_, err = server.InvokeComponent(other, dexec.ComponentRequest{Target: artifactTarget, Input: artifactInput})
	require.Error(t, err)
	_, err = server.InvokeComponent(owner, dexec.ComponentRequest{Target: artifactTarget, Input: artifactInput})
	require.NoError(t, err)
	lookup := &artifactread.Input{}
	lookup.SetArtifactID("artifact-modes")
	artifactReaderTarget := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[artifactread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/artifact"}}
	value, err := server.InvokeComponent(owner, dexec.ComponentRequest{Target: artifactReaderTarget, Input: lookup})
	require.NoError(t, err)
	require.Len(t, value.(*artifactread.Output).Data, 1)
	require.Equal(t, []byte("%PDF"), value.(*artifactread.Output).Data[0].InlineData)
}

func TestWorkspaceRuntimeMaintenanceLease(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	store := &maintenance.Store{Invoker: server}
	ctx := context.Background()
	first, err := store.Acquire(ctx, "cleanup", "worker-a", time.Hour)
	require.NoError(t, err)
	require.True(t, first.Acquired)
	require.NotEmpty(t, first.Lease.Token)
	blocked, err := store.Acquire(ctx, "cleanup", "worker-b", time.Hour)
	require.NoError(t, err)
	require.False(t, blocked.Acquired)
	require.Equal(t, "worker-a", blocked.Lease.OwnerID)
	require.Empty(t, blocked.Lease.Token)
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, "INSERT INTO maintenance_lease(lease_key,owner_id,lease_token,lease_until) VALUES('expired','old','old-token',?)", time.Now().UTC().Add(-8*24*time.Hour))
	require.NoError(t, err)
	deleted, err := store.DeleteExpired(ctx, first.Lease)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	stale := first.Lease
	stale.Token = "stale-token"
	_, err = store.DeleteExpired(ctx, stale)
	require.ErrorIs(t, err, maintenance.ErrLeaseLost)
	released, err := store.Release(ctx, first.Lease)
	require.NoError(t, err)
	require.True(t, released)
}

func TestWorkspaceRuntimeSchedulerLeases(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec("INSERT INTO schedule(id,name,agent_ref,enabled) VALUES('schedule-1','test','agent',1); INSERT INTO run(id,schedule_id,status) VALUES('run-1','schedule-1','pending')")
	require.NoError(t, err)
	store := &schedulerlease.Store{Invoker: server}
	ctx := native.WithAccess(context.Background(), native.Access{Internal: true, Mode: "rows"})
	until := time.Now().UTC().Add(time.Hour)
	claimed, err := store.TryClaimSchedule(ctx, "schedule-1", "worker-a", until)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = store.TryClaimSchedule(ctx, "schedule-1", "worker-b", until)
	require.NoError(t, err)
	require.False(t, claimed)
	released, err := store.ReleaseSchedule(ctx, "schedule-1", "worker-a")
	require.NoError(t, err)
	require.True(t, released)
	claimed, err = store.TryClaimRun(ctx, "run-1", "worker-a", until)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = store.TryClaimRun(ctx, "run-1", "worker-b", until)
	require.NoError(t, err)
	require.False(t, claimed)
	released, err = store.ReleaseRun(ctx, "run-1", "worker-a")
	require.NoError(t, err)
	require.True(t, released)
}

func TestWorkspaceRuntimeSchedulerStore(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	store := &schedulerstore.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u2"})
	u1 := "u1"
	row := &schedulewrite.Schedule{}
	row.SetId("private-schedule")
	row.SetName("private")
	row.SetAgentRef("agent")
	row.SetCreatedByUserId(&u1)
	row.SetVisibility("private")
	internalFlag := false
	row.SetInternal(&internalFlag)
	require.NoError(t, store.PatchTrusted(owner, row))
	owned, err := store.List(owner, row.Id, false)
	require.NoError(t, err)
	require.Len(t, owned, 1)
	require.Equal(t, "u1", *owned[0].CreatedByUserId)
	denied, err := store.List(other, row.Id, false)
	require.NoError(t, err)
	require.Empty(t, denied)
	internal, err := store.List(other, "", true)
	require.NoError(t, err)
	require.Len(t, internal, 1)
	newDescription := "updated"
	patch := &schedulewrite.Schedule{}
	patch.SetId(row.Id)
	patch.SetDescription(&newDescription)
	require.NoError(t, store.PatchTrusted(owner, patch))
	owned, err = store.List(owner, row.Id, false)
	require.NoError(t, err)
	require.Equal(t, "updated", *owned[0].Description)
	require.Equal(t, "private", owned[0].Name)
}

func TestWorkspaceRuntimeSchedulerRunLists(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO schedule(id,name,agent_ref,visibility,created_by_user_id,internal) VALUES
		('pub','pub','agent','public',NULL,0),('own','own','agent','private','u1',0),
		('other','other','agent','private','u2',0),('internal','internal','agent','public','u1',1);
		INSERT INTO run(id,schedule_id,status,started_at) VALUES
		('pub-old','pub','failed','2026-01-01 00:00:00'),
		('pub-new','pub','running','2026-01-02 00:00:00'),
		('own-run','own','running','2026-01-03 00:00:00'),
		('other-run','other','running','2026-01-04 00:00:00'),
		('internal-run','internal','running','2026-01-05 00:00:00')`)
	require.NoError(t, err)
	store := &agentrunstore.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	query := &agentrunread.RunRowsInput{}
	selector := state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{
		Fields: agentrunstore.SchedulerRunFields(), Limit: 1, Offset: 1,
	}}}
	rows, err := store.ListTrusted(owner, "schedulerList", query, selector)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "pub-new", rows[0].Id)
	require.Nil(t, rows[0].WorkerId)
	total, err := store.CountScheduler(owner, &agentruncube.RunReportInput{})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	total, err = store.CountScheduler(context.Background(), &agentruncube.RunReportInput{})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	due := &agentrunread.RunRowsInput{}
	due.SetScheduleId("internal")
	rows, err = store.ListTrusted(context.Background(), "schedulerDue", due, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "internal-run", rows[0].Id)
}

func TestWorkspaceRuntimeConversationStore(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	store := &convstore.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u2"})
	row := &convwrite.MutableConversationView{}
	row.SetId("private-conversation")
	u1, title := "u1", "First"
	row.SetCreatedByUserId(&u1)
	row.SetTitle(&title)
	require.NoError(t, store.PatchTrusted(owner, row))
	got, err := store.GetVisible(owner, row.Id, nil)
	require.NoError(t, err)
	require.Equal(t, "First", *got.Title)
	base, err := store.GetBaseInternal(owner, row.Id)
	require.NoError(t, err)
	require.Equal(t, row.Id, base.Id)
	require.Equal(t, "u1", *base.CreatedByUserId)
	require.Empty(t, base.Transcript)
	_, err = store.GetVisible(other, row.Id, nil)
	require.ErrorIs(t, err, convstore.ErrNotFound)
	rows, err := store.ListVisible(owner, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].Transcript)
	rows, err = store.ListVisible(other, nil)
	require.NoError(t, err)
	require.Empty(t, rows)
	updated := &convwrite.MutableConversationView{}
	updated.SetId(row.Id)
	changed := "Second"
	updated.SetTitle(&changed)
	require.NoError(t, store.PatchTrusted(owner, updated))
	got, err = store.GetVisible(owner, row.Id, nil)
	require.NoError(t, err)
	require.Equal(t, "Second", *got.Title)
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO turn(id,conversation_id,status,created_at) VALUES('turn-1','private-conversation','succeeded','2026-01-01 00:00:00');
INSERT INTO message(id,conversation_id,turn_id,role,type,content,status,created_at) VALUES('message-1','private-conversation','turn-1','assistant','text','done','succeeded','2026-01-01 00:00:00')`)
	require.NoError(t, err)
	rich, err := store.GetInternal(owner, row.Id, nil)
	require.NoError(t, err)
	require.Len(t, rich.Transcript, 1)
	require.Len(t, rich.Transcript[0].Message, 1)
	require.Equal(t, "done", *rich.Transcript[0].Message[0].Content)
	messages := &convstore.MessageStore{Invoker: server, OwnerID: authctx.EffectiveUserID}
	message, err := messages.Get(owner, "message-1", false, false)
	require.NoError(t, err)
	require.Equal(t, "done", *message.Content)
	patchMessage := &msgwrite.Message{}
	patchMessage.SetId("message-1")
	newContent := "updated"
	patchMessage.SetContent(&newContent)
	require.NoError(t, messages.PatchTrusted(owner, patchMessage))
	message, err = messages.Get(owner, "message-1", false, false)
	require.NoError(t, err)
	require.Equal(t, "updated", *message.Content)
	calls := &convstore.CallsStore{Invoker: server, OwnerID: authctx.EffectiveUserID}
	model := &modelwrite.ModelCall{}
	model.SetMessageId("message-1")
	model.SetProvider("test")
	model.SetModel("model")
	model.SetModelKind("chat")
	model.SetStatus("succeeded")
	require.NoError(t, calls.PatchModelTrusted(owner, model))
	tool := &toolwrite.ToolCall{}
	tool.SetMessageId("message-1")
	tool.SetOpId("op-1")
	tool.SetToolName("test/tool")
	tool.SetToolKind("local")
	tool.SetStatus("succeeded")
	trace := "trace-1"
	tool.SetTraceId(&trace)
	require.NoError(t, calls.PatchToolTrusted(owner, tool))
	lookupTrace, err := calls.TraceByOp(owner, row.Id, "op-1")
	require.NoError(t, err)
	require.Equal(t, trace, lookupTrace)
	lookupTrace, err = calls.TraceByOp(owner, "missing-conversation", "op-1")
	require.NoError(t, err)
	require.Empty(t, lookupTrace)
	_, err = db.Exec(`INSERT INTO message(id,conversation_id,turn_id,role,type,content,status,elicitation_id,created_at) VALUES('elicitation-message','private-conversation','turn-1','assistant','text','choose','pending','elicitation-1','2026-01-01 00:00:01')`)
	require.NoError(t, err)
	message, err = messages.ByElicitation(owner, "private-conversation", "elicitation-1")
	require.NoError(t, err)
	require.Equal(t, "elicitation-message", message.Id)
	_, err = db.Exec(`INSERT INTO message(id,conversation_id,turn_id,role,type,content,status,elicitation_id,parent_message_id,created_at) VALUES('child-elicitation','private-conversation','turn-1','assistant','text','nested','pending','elicitation-2','message-1','2026-01-01 00:00:02')`)
	require.NoError(t, err)
	message, err = messages.ByParentElicitation(owner, "message-1", "elicitation-2")
	require.NoError(t, err)
	require.Equal(t, "child-elicitation", message.Id)
	pageQuery := &msgread.MessagesInput{}
	pageQuery.SetConversationId(row.Id)
	pageSelector := state.Selectors{&state.NamedSelector{Name: "message_rows", Selector: state.Selector{
		Fields: convstore.BaseMessageFields(), OrderBy: "created_at DESC,id DESC", Limit: 2,
	}}}
	messageRows, err := messages.ListRows(owner, pageQuery, pageSelector)
	require.NoError(t, err)
	require.Len(t, messageRows, 2)
	require.Equal(t, "child-elicitation", messageRows[0].Id)
	require.Equal(t, "elicitation-message", messageRows[1].Id)
	require.Empty(t, messageRows[0].ModelCall)
	pageQuery.SetCursorBefore("child-elicitation")
	messageRows, err = messages.ListRows(owner, pageQuery, pageSelector)
	require.NoError(t, err)
	require.Len(t, messageRows, 2)
	require.Equal(t, "elicitation-message", messageRows[0].Id)
	require.Equal(t, "message-1", messageRows[1].Id)
	scheduled := &convwrite.MutableConversationView{}
	scheduled.SetId("scheduled-conversation")
	scheduled.SetCreatedByUserId(&u1)
	scheduleID := "schedule-1"
	scheduled.SetScheduleId(&scheduleID)
	require.NoError(t, store.PatchTrusted(owner, scheduled))
	_, err = db.Exec(`INSERT INTO message(id,conversation_id,turn_id,role,type,content,status,elicitation_id,linked_conversation_id,created_at) VALUES('linked-elicitation','private-conversation','turn-1','assistant','text','linked','pending','elicitation-3','scheduled-conversation','2026-01-01 00:00:03')`)
	require.NoError(t, err)
	message, err = messages.ByLinkedElicitation(owner, scheduled.Id, "elicitation-3")
	require.NoError(t, err)
	require.Equal(t, "linked-elicitation", message.Id)
	generatedFiles := &convstore.GeneratedFileStore{Invoker: server}
	fileRow := &gfwrite.GeneratedFile{}
	fileRow.SetId("file-1")
	fileRow.SetConversationId(row.Id)
	fileRow.SetProvider("test")
	fileRow.SetMode("input")
	fileRow.SetCopyMode("copy")
	require.NoError(t, generatedFiles.PatchTrusted(owner, fileRow))
	fileQuery := &gfread.Input{}
	fileQuery.SetConversationID(row.Id)
	files, err := generatedFiles.List(owner, fileQuery)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "ready", files[0].Status)
	filePatch := &gfwrite.GeneratedFile{}
	filePatch.SetId("file-1")
	filePatch.SetStatus("uploaded")
	require.NoError(t, generatedFiles.PatchTrusted(owner, filePatch))
	fileQuery.SetStatus("uploaded")
	files, err = generatedFiles.List(owner, fileQuery)
	require.NoError(t, err)
	require.Len(t, files, 1)
	fileQuery.SetStatus("ready")
	files, err = generatedFiles.List(owner, fileQuery)
	require.NoError(t, err)
	require.Empty(t, files)
	excludeScheduled := &read.ConversationInput{}
	excludeScheduled.SetExcludeScheduled(true)
	rows, err = store.ListVisible(owner, excludeScheduled)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	excludeScheduled.SetExcludeScheduled(false)
	rows, err = store.ListVisible(owner, excludeScheduled)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	hasSchedule := &read.ConversationInput{}
	hasSchedule.SetHasScheduleId(true)
	rows, err = store.ListVisible(owner, hasSchedule)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, scheduled.Id, rows[0].Id)
	hasSchedule.SetHasScheduleId(false)
	rows, err = store.ListVisible(owner, hasSchedule)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	includeChildren := &read.ConversationInput{}
	includeChildren.SetExcludeChildren(false)
	rows, err = store.ListVisible(owner, includeChildren)
	require.NoError(t, err)
	require.Len(t, rows, 2)
}

func TestWorkspaceRuntimeConversationListPage(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO conversation(id,visibility,created_by_user_id,created_at,last_activity,status) VALUES
		('list-new','private','u1','2026-01-01 00:00:00','2026-01-10 00:00:00','running'),
		('list-mid','public','u2','2026-01-03 00:00:00','2026-01-08 00:00:00','running'),
		('list-old','public','u2','2026-01-06 00:00:00',NULL,'succeeded');
		INSERT INTO turn(id,conversation_id,status,created_at) VALUES('list-turn','list-mid','failed','2026-01-09 00:00:00')`)
	require.NoError(t, err)
	store := &convstore.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	rows, err := store.ListPage(owner, nil, 2, false, true)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "list-new", rows[0].Id)
	require.Equal(t, "list-mid", rows[1].Id)
	require.Equal(t, "failed", *rows[1].Status)
	require.Equal(t, "error", rows[1].Stage)
	before := &read.ConversationInput{}
	before.SetCursorBefore("list-mid")
	rows, err = store.ListPage(owner, before, 2, false, true)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "list-old", rows[0].Id)
	after := &read.ConversationInput{}
	after.SetCursorAfter("list-mid")
	rows, err = store.ListPage(owner, after, 2, true, true)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "list-new", rows[0].Id)
	other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u2"})
	rows, err = store.ListPage(other, nil, 10, false, true)
	require.NoError(t, err)
	for _, row := range rows {
		require.NotEqual(t, "list-new", row.Id)
	}
	rows, err = store.ListPage(context.Background(), nil, 10, false, false)
	require.NoError(t, err)
	foundPrivate := false
	for _, row := range rows {
		foundPrivate = foundPrivate || row.Id == "list-new"
	}
	require.True(t, foundPrivate, "trusted unscoped list must preserve internal caller behavior")
}

func TestWorkspaceRuntimeTurnStore(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	conversation := &convstore.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}
	row := &convwrite.MutableConversationView{}
	row.SetId("turn-conversation")
	require.NoError(t, conversation.PatchTrusted(context.Background(), row))
	turns := &convstore.TurnStore{Invoker: server}
	for i, id := range []string{"turn-a", "turn-b"} {
		turn := &turnwrite.Turn{}
		turn.SetId(id)
		turn.SetConversationId(row.Id)
		turn.SetStatus("queued")
		sequence := int64(i + 1)
		turn.SetQueueSeq(&sequence)
		require.NoError(t, turns.PatchTrusted(context.Background(), turn))
	}
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var first, second int64
	require.NoError(t, db.QueryRow("SELECT queue_seq FROM turn WHERE id='turn-a'").Scan(&first))
	require.NoError(t, db.QueryRow("SELECT queue_seq FROM turn WHERE id='turn-b'").Scan(&second))
	require.NotEqual(t, first, second)
	patch := &turnwrite.Turn{}
	patch.SetId("turn-a")
	patch.SetStatus("succeeded")
	require.NoError(t, turns.PatchTrusted(context.Background(), patch))
	var status, conversationID string
	require.NoError(t, db.QueryRow("SELECT status,conversation_id FROM turn WHERE id='turn-a'").Scan(&status, &conversationID))
	require.Equal(t, "succeeded", status)
	require.Equal(t, row.Id, conversationID)
	turnQuery := &turnread.TurnRowsInput{}
	turnQuery.SetConversationID(row.Id)
	turnSelector := state.Selectors{&state.NamedSelector{Name: "TurnRows", Selector: state.Selector{Limit: 1}}}
	turnRows, err := turns.ListRows(context.Background(), turnQuery, turnSelector)
	require.NoError(t, err)
	require.Len(t, turnRows, 1)
	require.Equal(t, "turn-b", turnRows[0].Id)
	turnQuery.SetCursorBefore("turn-b")
	turnRows, err = turns.ListRows(context.Background(), turnQuery, turnSelector)
	require.NoError(t, err)
	require.Len(t, turnRows, 1)
	require.Equal(t, "turn-a", turnRows[0].Id)
}

func TestWorkspaceRuntimeRunStepsStore(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO conversation(id) VALUES('steps-conversation');
		INSERT INTO run(id,conversation_id,status,lease_owner) VALUES('steps-run','steps-conversation','running','owner-a');
		INSERT INTO message(id,conversation_id,role,type) VALUES('m1','steps-conversation','assistant','text'),('t1','steps-conversation','tool','tool_op'),('t2','steps-conversation','tool','tool_op');
		INSERT INTO model_call(message_id,provider,model,model_kind,status,run_id) VALUES('m1','test','model','chat','succeeded','steps-run');
		INSERT INTO tool_call(message_id,op_id,tool_name,tool_kind,status,run_id) VALUES('t1','op1','tool1','function','succeeded','steps-run'),('t2','op2','tool2','function','failed','steps-run')`)
	require.NoError(t, err)
	store := &runstepsstore.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}
	runQuery := &agentrunread.RunRowsInput{}
	runQuery.SetId("steps-run")
	runStore := &agentrunstore.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}
	runRow, err := runStore.GetTrusted(context.Background(), runQuery, nil)
	require.NoError(t, err)
	require.NotNil(t, runRow)
	require.Equal(t, "steps-run", runRow.Id)
	require.Equal(t, "steps-conversation", *runRow.ConversationId)
	activeQuery := &agentrunread.RunRowsInput{}
	activeQuery.SetConversationId("steps-conversation")
	activeRows, err := runStore.ListTrusted(context.Background(), "active", activeQuery, nil)
	require.NoError(t, err)
	require.Len(t, activeRows, 1)
	require.Equal(t, "steps-run", activeRows[0].Id)
	staleRows, err := runStore.ListTrusted(context.Background(), "stale", activeQuery, nil)
	require.NoError(t, err)
	require.Len(t, staleRows, 1)
	require.Equal(t, "steps-run", staleRows[0].Id)
	owner := "owner-a"
	patch := &agentrunwrite.MutableRunView{}
	patch.SetId("steps-run")
	patch.SetStatus("succeeded")
	patch.Condition = &datlypredicate.RunPatchCondition{LeaseOwner: &owner}
	patched, err := runStore.PatchTrusted(context.Background(), []*agentrunwrite.MutableRunView{patch})
	require.NoError(t, err)
	require.Len(t, patched, 1)
	require.NotNil(t, patched[0].Has)
	require.True(t, patched[0].Has.Status)
	staleOwner := "other-owner"
	stalePatch := &agentrunwrite.MutableRunView{}
	stalePatch.SetId("steps-run")
	stalePatch.SetStatus("failed")
	stalePatch.Condition = &datlypredicate.RunPatchCondition{LeaseOwner: &staleOwner}
	_, err = runStore.PatchTrusted(context.Background(), []*agentrunwrite.MutableRunView{stalePatch})
	require.NoError(t, err)
	var persistedStatus string
	require.NoError(t, db.QueryRow("SELECT status FROM run WHERE id='steps-run'").Scan(&persistedStatus))
	require.Equal(t, "succeeded", persistedStatus)
	query := &runstepsread.RunStepsInput{}
	query.SetRunID("steps-run")
	selector := state.Selectors{&state.NamedSelector{Name: "RunSteps", Selector: state.Selector{OrderBy: "message_id ASC", Limit: 2}}}
	rows, err := store.ListTrusted(context.Background(), query, selector)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "m1", rows[0].MessageId)
	require.Equal(t, "t1", rows[1].MessageId)
	query.SetCursorAfter("t1")
	rows, err = store.ListTrusted(context.Background(), query, selector)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "t2", rows[0].MessageId)
	newRun := &agentrunwrite.MutableRunView{}
	newRun.SetId("delete-run")
	newRunConversation := "steps-conversation"
	newRun.SetConversationId(&newRunConversation)
	_, err = runStore.PatchTrusted(context.Background(), []*agentrunwrite.MutableRunView{newRun})
	require.NoError(t, err)
	require.NoError(t, runStore.DeleteTrusted(context.Background(), "delete-run", "missing-run"))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE id='delete-run'").Scan(&count))
	require.Zero(t, count)
	_, err = db.Exec(`INSERT INTO run(id,status) VALUES('rollback-run-a','running'),('rollback-run-b','running');
		CREATE TRIGGER reject_rollback_run_b BEFORE DELETE ON run WHEN OLD.id='rollback-run-b' BEGIN SELECT RAISE(ABORT,'fixture run delete rejection'); END`)
	require.NoError(t, err)
	require.Error(t, runStore.DeleteTrusted(context.Background(), "rollback-run-a", "rollback-run-b"))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE id IN ('rollback-run-a','rollback-run-b')").Scan(&count))
	require.Equal(t, 2, count)
}

func TestWorkspaceRuntimePayloadStore(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	store := &convstore.PayloadStore{Invoker: server}
	body := bytes.Repeat([]byte("payload text "), 100)
	row := &payloadwrite.Payload{}
	row.SetId("payload-1")
	row.SetKind("request")
	row.SetMimeType("text/plain")
	row.SetSizeBytes(len(body))
	row.SetStorage("inline")
	row.SetInlineBody(&body)
	require.NoError(t, store.PatchTrusted(context.Background(), row))
	got, err := store.Get(context.Background(), row.Id)
	require.NoError(t, err)
	require.NotNil(t, got.InlineBody)
	require.Equal(t, bytes.TrimSpace(body), *got.InlineBody)
	require.Empty(t, got.Compression)
	uri := "payload://old"
	patch := &payloadwrite.Payload{}
	patch.SetId(row.Id)
	patch.SetUri(&uri)
	require.NoError(t, store.PatchTrusted(context.Background(), patch))
	patch = &payloadwrite.Payload{}
	patch.SetId(row.Id)
	patch.SetUri(nil)
	require.NoError(t, store.PatchTrusted(context.Background(), patch))
	got, err = store.Get(context.Background(), row.Id)
	require.NoError(t, err)
	require.Nil(t, got.Uri)
}

func TestWorkspaceRuntimePayloadRetention(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO conversation(id) VALUES('payload-conversation'),('outside-conversation');
		INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage) VALUES
		('payload-delete','request','application/json',2,'inline'),
		('payload-shared','request','application/json',2,'inline');
		INSERT INTO message(id,conversation_id,role,type,attachment_payload_id)
		VALUES('payload-message','payload-conversation','user','text','payload-shared'),
		('outside-message','outside-conversation','user','text','payload-shared')`)
	require.NoError(t, err)
	store := &convstore.PayloadStore{Invoker: server}
	require.NoError(t, store.DeleteUnreferencedTrusted(context.Background(), "payload-delete", "payload-shared", "missing-payload"))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM call_payload WHERE id='payload-delete'").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM call_payload WHERE id='payload-shared'").Scan(&count))
	require.Equal(t, 1, count)
	_, err = db.Exec(`DELETE FROM message WHERE id='payload-message'`)
	require.NoError(t, err)
	require.NoError(t, store.DeleteUnreferencedTrusted(context.Background(), "payload-shared"))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM call_payload WHERE id='payload-shared'").Scan(&count))
	require.Equal(t, 1, count, "an outside conversation still references the payload")
	_, err = db.Exec(`DELETE FROM message WHERE id='outside-message'`)
	require.NoError(t, err)
	require.NoError(t, store.DeleteUnreferencedTrusted(context.Background(), "payload-shared"))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM call_payload WHERE id='payload-shared'").Scan(&count))
	require.Zero(t, count)
}

func TestWorkspaceRuntimePayloadRetentionEveryReference(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ids := []string{"p-attach", "p-elicitation", "p-model-request", "p-model-response", "p-provider-request", "p-provider-response", "p-stream", "p-tool-request", "p-tool-response", "p-generated"}
	_, err = db.Exec(`INSERT INTO conversation(id,created_by_user_id) VALUES('payload-refs','u1')`)
	require.NoError(t, err)
	for _, id := range ids {
		_, err = db.Exec(`INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage) VALUES(?,?,?,?,?)`, id, "request", "application/json", 2, "inline")
		require.NoError(t, err)
	}
	_, err = db.Exec(`INSERT INTO message(id,conversation_id,role,type,attachment_payload_id) VALUES('m-attach','payload-refs','user','text','p-attach');
		INSERT INTO message(id,conversation_id,role,type,elicitation_payload_id) VALUES('m-elicitation','payload-refs','user','text','p-elicitation');
		INSERT INTO message(id,conversation_id,role,type) VALUES('m-model','payload-refs','assistant','text'),('m-tool','payload-refs','tool','tool_op');
		INSERT INTO model_call(message_id,provider,model,model_kind,status,request_payload_id,response_payload_id,provider_request_payload_id,provider_response_payload_id,stream_payload_id)
		VALUES('m-model','test','model','chat','succeeded','p-model-request','p-model-response','p-provider-request','p-provider-response','p-stream');
		INSERT INTO tool_call(message_id,op_id,tool_name,tool_kind,status,request_payload_id,response_payload_id)
		VALUES('m-tool','op','tool','function','succeeded','p-tool-request','p-tool-response');
		INSERT INTO generated_file(id,conversation_id,provider,mode,copy_mode,payload_id)
		VALUES('gf-ref','payload-refs','test','input','copy','p-generated')`)
	require.NoError(t, err)
	discoverer := &conversationtree.Discoverer{Invoker: server, OwnerID: authctx.EffectiveUserID}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	graph, err := discoverer.DiscoverAuthorized(owner, "payload-refs")
	require.NoError(t, err)
	candidates, err := discoverer.CollectPayloadIDs(owner, graph)
	require.NoError(t, err)
	require.ElementsMatch(t, ids, candidates)
	store := &convstore.PayloadStore{Invoker: server}
	require.NoError(t, store.DeleteUnreferencedTrusted(context.Background(), ids...))
	for _, id := range ids {
		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM call_payload WHERE id=?`, id).Scan(&count))
		require.Equal(t, 1, count, id)
	}
	_, err = db.Exec(`DELETE FROM generated_file WHERE id='gf-ref';
		DELETE FROM model_call WHERE message_id='m-model';
		DELETE FROM tool_call WHERE message_id='m-tool';
		DELETE FROM message WHERE conversation_id='payload-refs'`)
	require.NoError(t, err)
	require.NoError(t, store.DeleteUnreferencedTrusted(context.Background(), ids...))
	for _, id := range ids {
		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM call_payload WHERE id=?`, id).Scan(&count))
		require.Zero(t, count, id)
	}
}

func TestWorkspaceRuntimePayloadRetentionLateReference(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..", "..")
	workspaceRoot := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO conversation(id) VALUES('late-payload-conversation');
		INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage) VALUES('late-payload','request','application/json',2,'inline')`)
	require.NoError(t, err)
	interceptor := &payloadRaceInvoker{next: server, beforeWriter: func() error {
		_, err := db.Exec(`INSERT INTO message(id,conversation_id,role,type,attachment_payload_id)
			VALUES('late-payload-message','late-payload-conversation','user','text','late-payload')`)
		return err
	}}
	store := &convstore.PayloadStore{Invoker: interceptor}
	err = store.DeleteUnreferencedTrusted(context.Background(), "late-payload")
	var conflict *xhandler.Conflict
	require.ErrorAs(t, err, &conflict)
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM call_payload WHERE id='late-payload'").Scan(&count))
	require.Equal(t, 1, count)
}
