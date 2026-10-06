package forecastbinding

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/evidence"
)

type rolloutStore struct {
	*lifecycleStore
	createdAt        time.Time
	readError        error
	admissionError   error
	admissionPresent bool
	ownedReads       int
}

func (s *rolloutStore) LoadAdmission(ctx context.Context, scope Scope) (*Admission, error) {
	if s.admissionError != nil {
		return nil, s.admissionError
	}
	if !s.admissionPresent {
		return nil, nil
	}
	return s.lifecycleStore.LoadAdmission(ctx, scope)
}
func (s *rolloutStore) OwnedRunCreatedAt(context.Context, Scope) (time.Time, error) {
	s.ownedReads++
	return s.createdAt, s.readError
}
func TestRolloutResumeRequiresOwnedServerTimestampStrictlyBeforePinnedCutoff(t *testing.T) {
	cutoff := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	a := actualAdmission(t)
	for _, tc := range []struct {
		name    string
		at      time.Time
		err     error
		allowed bool
	}{
		{"before", cutoff.Add(-time.Nanosecond), nil, true},
		{"equal", cutoff, nil, false}, {"after", cutoff.Add(time.Nanosecond), nil, false},
		{"unknown", time.Time{}, nil, false}, {"foreign", cutoff.Add(-time.Hour), errors.New("foreign owner"), false},
		{"storageFailure", cutoff.Add(-time.Hour), errors.New("storage unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &rolloutStore{lifecycleStore: &lifecycleStore{controllerStore: &controllerStore{receiptStore: &receiptStore{}, projected: map[string]json.RawMessage{}}}, createdAt: tc.at, readError: tc.err}
			factory, err := NewFactory(ProjectionPolicyProducer{}, store, "UTC", WithRolloutCutoff(cutoff))
			require.NoError(t, err)
			turn := evidence.Turn{ConversationID: a.ConversationID, TurnID: a.TurnID, LeaseOwner: func() string { return "current-lease" }}
			ctx := controllerContext(a.Scope)
			restored, err := factory.Restore(ctx, turn)
			if tc.allowed {
				require.NoError(t, err)
				require.Nil(t, evidence.ToolsFromContext(restored))
			} else {
				require.Error(t, err)
			}
			require.Zero(t, store.saves, "resume never recaptures admission")
			// Restart keeps the configured cutoff; a new zone does not change eligibility.
			restarted, err := NewFactory(ProjectionPolicyProducer{}, store, "Asia/Tokyo", WithRolloutCutoff(cutoff))
			require.NoError(t, err)
			_, err = restarted.Restore(ctx, turn)
			require.Equal(t, tc.allowed, err == nil)
		})
	}
}
func TestRolloutExistingOrCorruptAdmissionNeverFallsBack(t *testing.T) {
	a := actualAdmission(t)
	store := &rolloutStore{lifecycleStore: &lifecycleStore{controllerStore: &controllerStore{receiptStore: &receiptStore{admission: a}, projected: map[string]json.RawMessage{}}}, admissionPresent: true, createdAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	factory, err := NewFactory(ProjectionPolicyProducer{}, store, "UTC", WithRolloutCutoff(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	require.NoError(t, err)
	turn := evidence.Turn{ConversationID: a.ConversationID, TurnID: a.TurnID, LeaseOwner: func() string { return "lease" }}
	ctx := controllerContext(a.Scope)
	restored, err := factory.Restore(ctx, turn)
	require.NoError(t, err)
	require.NotNil(t, evidence.ToolsFromContext(restored))
	require.Zero(t, store.ownedReads)
	store.admission.OwnerID = "foreign"
	_, err = factory.Restore(ctx, turn)
	require.Error(t, err)
	require.Zero(t, store.ownedReads)
	store.admissionPresent = false
	store.admissionError = errors.New("read failure")
	_, err = factory.Restore(ctx, turn)
	require.Error(t, err)
	require.Zero(t, store.ownedReads)
}
