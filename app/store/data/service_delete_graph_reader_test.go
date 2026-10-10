package data

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	compact "github.com/viant/agently-core/internal/datly/conversation/graph/read"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
)

// Reuse existing behavioral regressions against both graph implementations.
// Each case creates its own temporary SQLite database; no application DB is used.
func TestDeleteGraphReaderBehaviorParity(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"tree_and_shared_payload", TestDeleteConversationTree_RemovesTreeArtifactsAndUnsharedPayloads},
		{"parent_turn", TestDeleteConversationTree_FollowsParentTurnRelationship},
		{"cycle", TestDeleteConversationTree_HandlesLinkedConversationCycle},
		{"linked_foreign_owner", TestDeleteConversationTree_RequiresOwnershipOfLinkedDescendant},
		{"null_and_empty_status", TestDeleteConversationTree_AllowsLegacyEmptyStatusWithoutLiveRun},
		{"live_descendant", TestDeleteConversationTree_BlocksLiveRunInDescendant},
		{"inbound_link", TestDeleteConversationTree_BlocksInboundLinkFromOutsideGraph},
		{"current_dependencies_and_saved_reports", TestDeleteConversationTree_DeletesCurrentDatabaseDependenciesAndRetainsSavedReports},
		{"rollback", TestDeleteConversationTree_RollsBackEarlierDeletesWhenLaterDeleteFails},
		{"schedule_cascade", TestDeleteScheduleCascade_RemovesScheduleConversationsAndRuns},
		{"scheduled_run", TestDeleteScheduledRun_DeletesGraphOrRunAndKeepsSchedule},
		{"retention_dry_run_execute", TestMaintainConversationTree_DryRunThenDeleteWholeInteractiveGraph},
		{"retention_recent_child", TestMaintainConversationTree_SkipsGraphWithRecentChild},
		{"retention_mixed_owners", TestMaintainConversationTree_DeletesMixedOwnerGraph},
		{"scheduled_retention", TestMaintainScheduledRun_DeleteIsFencedAndKeepsSchedule},
	}
	for _, mode := range []string{"legacy", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(tree.GraphReaderEnvironment, mode)
			for _, metadata := range []string{"legacy", "compact"} {
				t.Run("metadata_"+metadata, func(t *testing.T) {
					t.Setenv(tree.MetadataReaderEnvironment, metadata)
					for _, tc := range cases {
						t.Run(tc.name, tc.run)
					}
				})
			}
		})
	}
}

func TestDeleteGraphReaderFullTopologyParityWithoutPagination(t *testing.T) {
	svc := newSeededService(t, seedForConversationTreeDelete, func(t *testing.T, db *sql.DB) {
		_, err := db.Exec(`INSERT INTO conversation(id,created_at,created_by_user_id,status,conversation_parent_turn_id)
            VALUES('conv-parent-turn','2026-01-01T09:00:00.123456Z','u1',NULL,'turn-root')`)
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE message SET linked_conversation_id='conv-root' WHERE conversation_id='conv-linked'`)
		require.NoError(t, err)
		tx, err := db.Begin()
		require.NoError(t, err)
		defer tx.Rollback()
		for i := 0; i < 501; i++ {
			_, err := tx.Exec(`INSERT INTO conversation(id,created_at,created_by_user_id,status,conversation_parent_id)
                VALUES(?,'2026-01-01T09:00:00Z','u1','succeeded','conv-root')`, fmt.Sprintf("conv-wide-%03d", i))
			require.NoError(t, err)
		}
		require.NoError(t, tx.Commit())
	})
	var expected *tree.Graph
	for _, mode := range []string{"legacy", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(tree.GraphReaderEnvironment, mode)
			d := tree.NewSystemDiscoverer(svc.(*datlyService).native, nil)
			graph, err := d.Discover(context.Background(), "conv-root")
			require.NoError(t, err)
			require.Len(t, graph.Nodes, 505, "wide graph must not be paginated")
			require.Empty(t, graph.Nodes["conv-parent-turn"].Status)
			if expected == nil {
				expected = graph
			} else {
				require.Equal(t, expected, graph, "all graph IDs/owners/status/run/timestamp/depth must match")
			}
		})
	}
}

func TestDeleteGraphReaderInvalidConfigurationDoesNotMutate(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedForConversationTreeDelete)
	t.Setenv(tree.GraphReaderEnvironment, "invalid")
	err := svc.DeleteConversationTree(deleteTestContext(), "conv-root")
	require.ErrorContains(t, err, tree.GraphReaderEnvironment)
	assertStage1RowCount(t, db, "conversation", "id", "conv-root", 1)
	assertStage1RowCount(t, db, "message", "id", "msg-root", 1)
	// Reader selection is local to deletion; public reads still work.
	row, err := svc.GetConversation(deleteTestContext(), "conv-root", nil)
	require.NoError(t, err)
	require.NotNil(t, row)
}

func TestCompactGraphReaderRejectsMissingHostScope(t *testing.T) {
	svc := newSeededService(t, seedForConversationTreeDelete)
	input := &compact.Input{}
	input.SetIDs([]string{"conv-root"})
	_, err := svc.(*datlyService).native.InvokeComponent(context.Background(), dexec.ComponentRequest{
		Target: compactGraphTestTarget(),
		Input:  input,
	})
	require.Error(t, err, "private graph component must require the host capability")
	require.Regexp(t, "conversationtreescope|Trusted|authorized conversation graph scope", err.Error())
}

func TestCompactGraphReaderRuntimeRejectsUnboundedRead(t *testing.T) {
	svc := newSeededService(t, seedForConversationTreeDelete)
	for _, emptyIDs := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty_ids_%v", emptyIDs), func(t *testing.T) {
			input := &compact.Input{}
			if emptyIDs {
				input.SetIDs([]string{})
			}
			_, err := svc.(*datlyService).native.InvokeComponent(context.Background(), dexec.ComponentRequest{
				Target: compactGraphTestTarget(),
				Input:  input,
				Providers: []locator.Provider{provider.Named("conversationtreescope", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
					return true, name == "internal", nil
				})},
			})
			if emptyIDs {
				require.ErrorContains(t, err, "requires nonempty IDs")
			} else {
				require.ErrorContains(t, err, "requires a bounded predicate")
			}
		})
	}
}

func compactGraphTestTarget() dexec.ComponentTarget {
	return dexec.ComponentTarget{
		Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[compact.ReaderComponent]().PkgPath(), Name: "reader"},
		Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/conversation/graph"},
	}
}
