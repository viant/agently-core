package conversationtree

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	childdelete "github.com/viant/agently-core/internal/datly/conversation/children/delete"
	investigationread "github.com/viant/agently-core/internal/datly/investigation/read"
	investigationwrite "github.com/viant/agently-core/internal/datly/investigation/write"
	artifactread "github.com/viant/agently-core/internal/datly/reporting/artifact/read"
	artifactwrite "github.com/viant/agently-core/internal/datly/reporting/artifact/write"
	dexec "github.com/viant/datly/exec"
)

func TestChildDeletionBatchesNormalizeKeysAndStopOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		calls := 0
		m := &Mutator{OwnerID: func(context.Context) string { return "caller" }, Invoker: mutationTestInvoker(func(_ context.Context, request dexec.ComponentRequest) (any, error) {
			calls++
			input := request.Input.(*childdelete.Input)
			require.Equal(t, "message", input.Table)
			require.Len(t, input.IDs, map[int]int{1: 400, 2: 1}[calls])
			require.Equal(t, childdelete.Path, request.Target.Route.Path)
			require.Len(t, request.Providers, 2)
			if fail && calls == 2 {
				return nil, fmt.Errorf("second batch failed")
			}
			return &childdelete.Output{}, nil
		})}
		ids := []string{"", "key-000"}
		for i := 0; i < 401; i++ {
			ids = append(ids, fmt.Sprintf("key-%03d", i))
		}
		err := m.deleteChildren(context.Background(), "message", ids)
		if fail {
			require.ErrorContains(t, err, "second batch failed")
		} else {
			require.NoError(t, err)
		}
		require.Equal(t, 2, calls)
	}
}

type mutationTestInvoker func(context.Context, dexec.ComponentRequest) (any, error)

func (f mutationTestInvoker) InvokeComponent(ctx context.Context, request dexec.ComponentRequest) (any, error) {
	return f(ctx, request)
}

func TestArtifactWriterBatchesDoNotCrossOwnerOrExpectedJob(t *testing.T) {
	plan := &DeletePlan{Tables: map[string]bool{"report_export_artifact": true}}
	for i := 0; i < 401; i++ {
		plan.ReportArtifacts = append(plan.ReportArtifacts, &artifactread.Artifact{ArtifactId: fmt.Sprint(i), OwnerId: "a", JobId: "job-1"})
	}
	plan.ReportArtifacts = append(plan.ReportArtifacts, &artifactread.Artifact{ArtifactId: "different-job", OwnerId: "a", JobId: "job-2"}, &artifactread.Artifact{ArtifactId: "different-owner", OwnerId: "b", JobId: "job-1"})
	var batches []string
	m := &Mutator{OwnerID: func(context.Context) string { return "caller" }, Invoker: mutationTestInvoker(func(ctx context.Context, request dexec.ComponentRequest) (any, error) {
		input := request.Input.(*artifactwrite.Input)
		var owner string
		for _, p := range request.Providers {
			if p.Kind() == "visibility" {
				value, found, err := p.Locate(nil).Value(ctx, reflect.TypeFor[*string](), "subject")
				if err != nil || !found {
					t.Fatal("owner guard missing")
				}
				owner = *value.(*string)
			}
		}
		for _, row := range input.Artifacts {
			if row.OwnerId != owner || !row.ShouldDelete {
				t.Fatal("owner/delete guard changed")
			}
		}
		batches = append(batches, fmt.Sprintf("%s/%s/%d", owner, input.ExpectedJobID, len(input.Artifacts)))
		return &artifactwrite.Output{}, nil
	})}
	if err := m.reporting(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batches, []string{"a/job-1/400", "a/job-1/1", "a/job-2/1", "b/job-1/1"}) {
		t.Fatalf("batches=%v", batches)
	}
}

func TestInvestigationWriterGroupsByExpectedConversationForBothPolicies(t *testing.T) {
	for _, policy := range []InvestigationPolicy{InvestigationDelete, InvestigationRetainAndDetach} {
		a, b := "a", "b"
		plan := &DeletePlan{Tables: map[string]bool{"investigation": true}, Investigations: []*investigationread.Investigation{{Id: "1", ConversationId: &a}, {Id: "2", ConversationId: &b}, {Id: "3", ConversationId: &a}}}
		var batches []string
		m := &Mutator{OwnerID: func(context.Context) string { return "caller" }, Invoker: mutationTestInvoker(func(_ context.Context, request dexec.ComponentRequest) (any, error) {
			input := request.Input.(*investigationwrite.Input)
			batches = append(batches, fmt.Sprintf("%s/%d", input.ExpectedConversationID, len(input.Investigations)))
			for _, row := range input.Investigations {
				if policy == InvestigationDelete && !row.ShouldDelete {
					t.Fatal("missing deletion marker")
				}
				if policy == InvestigationRetainAndDetach && (row.ShouldDelete || row.ConversationId != nil || row.Has == nil || !row.Has.ConversationId) {
					t.Fatal("detach policy changed")
				}
			}
			return &investigationwrite.Output{}, nil
		})}
		if err := m.investigations(context.Background(), plan, policy); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(batches, []string{"a/2", "b/1"}) {
			t.Fatalf("batches=%v", batches)
		}
	}
}
