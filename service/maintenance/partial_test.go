package maintenance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	coredata "github.com/viant/agently-core/app/store/data"
)

type partialCleanupData struct {
	recordingConversationCleanupData
}

func (*partialCleanupData) MaintainConversationTree(context.Context, coredata.ConversationMaintenanceRequest) (*coredata.ConversationMaintenanceResult, error) {
	return &coredata.ConversationMaintenanceResult{Eligible: true, Partial: true, CommittedBatches: 2, AffectedRows: 401}, errors.New("later unit failed")
}
func (*partialCleanupData) MaintainScheduledRun(context.Context, coredata.ScheduledRunMaintenanceRequest) (*coredata.ScheduledRunMaintenanceResult, error) {
	return &coredata.ScheduledRunMaintenanceResult{Eligible: true, Partial: true, CommittedBatches: 2, AffectedRows: 401}, errors.New("later unit failed")
}

func TestCleanupPoliciesPreservePartialResultsAlongsideErrors(t *testing.T) {
	d := &partialCleanupData{}
	policies := []conversationCleanupPolicy{
		newInteractiveConversationCleanupPolicy(d, time.Hour, true),
		newScheduledRunCleanupPolicy(d, time.Hour, true),
		newScheduledConversationFallbackPolicy(d, time.Hour, true),
	}
	for _, policy := range policies {
		t.Run(policy.Name(), func(t *testing.T) {
			outcome, err := policy.ProcessCandidate(context.Background(), conversationCleanupCandidate{RootID: "root"})
			require.ErrorContains(t, err, "later unit failed")
			require.True(t, outcome.Partial)
			require.True(t, outcome.Mutated)
			require.False(t, outcome.Deleted)
			require.Equal(t, 2, outcome.CommittedBatches)
			require.EqualValues(t, 401, outcome.AffectedRows)
		})
	}
}

type committedCleanupData struct {
	recordingConversationCleanupData
}

func (*committedCleanupData) MaintainConversationTree(context.Context, coredata.ConversationMaintenanceRequest) (*coredata.ConversationMaintenanceResult, error) {
	return &coredata.ConversationMaintenanceResult{Eligible: true, CommittedBatches: 1, AffectedRows: 400}, nil
}
func (*committedCleanupData) MaintainScheduledRun(context.Context, coredata.ScheduledRunMaintenanceRequest) (*coredata.ScheduledRunMaintenanceResult, error) {
	return &coredata.ScheduledRunMaintenanceResult{Eligible: true, CommittedBatches: 1, AffectedRows: 400}, nil
}

func TestCleanupPoliciesCountCommittedWorkWhenAnotherWorkerFinishesTree(t *testing.T) {
	d := &committedCleanupData{}
	policies := []conversationCleanupPolicy{
		newInteractiveConversationCleanupPolicy(d, time.Hour, true),
		newScheduledRunCleanupPolicy(d, time.Hour, true),
		newScheduledConversationFallbackPolicy(d, time.Hour, true),
	}
	for _, policy := range policies {
		t.Run(policy.Name(), func(t *testing.T) {
			outcome, err := policy.ProcessCandidate(context.Background(), conversationCleanupCandidate{RootID: "root"})
			require.NoError(t, err)
			require.True(t, outcome.Mutated)
			require.False(t, outcome.Deleted)
			require.False(t, outcome.Partial)
			require.Equal(t, 1, outcome.CommittedBatches)
			require.EqualValues(t, 400, outcome.AffectedRows)
		})
	}
}

type partialCleanupPolicy struct {
	recordingConversationCleanupPolicy
	err error
}

func (p *partialCleanupPolicy) ProcessCandidate(context.Context, conversationCleanupCandidate) (conversationCleanupOutcome, error) {
	return conversationCleanupOutcome{Eligible: true, Partial: true, CommittedBatches: 2, AffectedRows: 401}, p.err
}

func TestCleanupWorkerCountsPartialProgressOnFailure(t *testing.T) {
	for _, failure := range []error{errors.New("later unit failed"), coredata.ErrMaintenanceLeaseLost} {
		p := &partialCleanupPolicy{recordingConversationCleanupPolicy: recordingConversationCleanupPolicy{pages: [][]conversationCleanupCandidate{{{RootID: "root", ActivityAt: time.Now()}}}}, err: failure}
		result, err := runConversationCleanupPolicy(context.Background(), p, 1, false)
		if errors.Is(failure, coredata.ErrMaintenanceLeaseLost) {
			require.ErrorIs(t, err, failure)
		} else {
			require.NoError(t, err, "ordinary candidate errors do not abort the pass")
		}
		require.Equal(t, 1, result.Failed)
		require.Equal(t, 1, result.Partial)
		require.Equal(t, 1, result.Eligible)
		require.Equal(t, 1, result.Mutated)
		require.Zero(t, result.Deleted)
		require.Equal(t, 2, result.CommittedBatches)
		require.EqualValues(t, 401, result.AffectedRows)
		var total Result
		total.add(result)
		total.add(result)
		require.Equal(t, 2, total.Partial)
		require.Equal(t, 4, total.CommittedBatches)
		require.EqualValues(t, 802, total.AffectedRows)
	}
}
